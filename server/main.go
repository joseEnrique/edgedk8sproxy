package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	// Load configuration - OPTIMIZED: Much faster default timeouts
	config := &Config{
		Port:         getEnv("PORT", "8080"),
		TCPPort:      getEnv("TCP_PORT", "8081"),
		Domain:       getEnv("DOMAIN", "localhost"),
		MaxClients:   getEnvAsInt("MAX_CLIENTS", 1000),                             // Increased from 100 to 1000
		ReadTimeout:  time.Duration(getEnvAsInt("READ_TIMEOUT", 5)) * time.Second,  // Reduced from 120s to 5s
		WriteTimeout: time.Duration(getEnvAsInt("WRITE_TIMEOUT", 5)) * time.Second, // Reduced from 30s to 5s
	}

	// Create server
	server := NewServer(config)

	// Setup HTTP routes (admin/API + subdomain routing)
	http.HandleFunc("/", server.HandleRoot) // Subdomain routing
	http.HandleFunc("/clients", server.HandleListClients)
	http.HandleFunc("/status", server.HandleStatus)

	// Start HTTP server for admin/API + subdomain routing
	log.Printf("🚀 Starting HTTP server on port %s", config.Port)
	log.Printf("📊 Status endpoint: http://localhost:%s/status", config.Port)
	log.Printf("👥 Clients endpoint: http://localhost:%s/clients", config.Port)
	log.Printf("🌐 Subdomain routing: client1.%s, client2.%s, etc.", config.Domain, config.Domain)
	log.Printf("🔌 Max Clients: %d", config.MaxClients)

	// Start TCP tunnel server in background
	go server.StartTCPTunnelServer()

	// Start HTTP server
	if err := http.ListenAndServe(":"+config.Port, nil); err != nil {
		log.Fatalf("❌ HTTP server failed: %v", err)
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
