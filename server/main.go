package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func main() {
	// Load configuration (H2-only tunnel; TCP_PORT used for user entry)
	config := &Config{
		Port:        getEnv("PORT", "8080"),
		TCPPort:     getEnv("TCP_PORT", "8081"),
		Domain:      getEnv("DOMAIN", "localhost"),
		MaxClients:  getEnvAsInt("MAX_CLIENTS", 1000),
		H2Port:      getEnv("H2_PORT", "8443"),
		TLSCertFile: getEnv("TLS_CERT_FILE", ""),
		TLSKeyFile:  getEnv("TLS_KEY_FILE", ""),
		TLSClientCA: getEnv("TLS_CLIENT_CA_FILE", ""),
	}

	// DOMAIN_MAP syntax: "quique.es=agent1,foo.bar=agent2"; matches any subdomain suffix
	if dm := getEnv("DOMAIN_MAP", ""); dm != "" {
		config.DomainMap = parseDomainMap(dm)
		log.Printf("🔧 Domain map loaded: %v", config.DomainMap)
	}

	// Create server
	server := NewServer(config)

	// Setup HTTP routes (admin/API + subdomain routing)
	http.HandleFunc("/", server.HandleRoot) // Subdomain routing
	http.HandleFunc("/agents", server.HandleListAgents)
	http.HandleFunc("/status", server.HandleStatus)

	// Start HTTP server for admin/API + subdomain routing
	log.Printf("🚀 Starting HTTP server on port %s", config.Port)
	log.Printf("📊 Status endpoint: http://localhost:%s/status", config.Port)
	log.Printf("👥 Agents endpoint: http://localhost:%s/agents", config.Port)
	log.Printf("🌐 Subdomain routing: agent1.%s, agent2.%s, etc.", config.Domain, config.Domain)
	log.Printf("🔌 Max Agents: %d", config.MaxClients)

	// Start TCP user listener (accepts user connections) and HTTP/2 mTLS server (accepts agent streams)
	go server.StartTCPTunnelServer()
	// Start H2 TLS server (non-blocking). If misconfigured, it will log and return.
	go server.StartH2TLSServer()

	// Start admin HTTP server (blocking)
	if err := http.ListenAndServe(":"+config.Port, nil); err != nil {
		log.Printf("❌ HTTP server failed: %v", err)
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

// parseDomainMap parses comma-separated domain=agent pairs
func parseDomainMap(s string) map[string]string {
	m := make(map[string]string)
	items := strings.Split(s, ",")
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			continue
		}
		domain := strings.ToLower(strings.TrimSpace(parts[0]))
		agent := strings.TrimSpace(parts[1])
		if domain != "" && agent != "" {
			m[domain] = agent
		}
	}
	return m
}
