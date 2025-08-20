package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
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

	// Start H2 TLS server (blocking)
	server.StartH2TLSServer()
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
