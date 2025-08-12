package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	// Load configuration
	config := &Config{
		Port:           getEnv("PORT", "8080"),
		Domain:         getEnv("DOMAIN", "localhost"),
		MaxClients:     getEnvAsInt("MAX_CLIENTS", 100),
		ChunkSize:      getEnvAsInt("CHUNK_SIZE", 8192),
		MaxMessageSize: int64(getEnvAsInt("MAX_MESSAGE_SIZE", 10*1024*1024)), // 10MB default
		ReadTimeout:    time.Duration(getEnvAsInt("READ_TIMEOUT", 120)) * time.Second,
		WriteTimeout:   time.Duration(getEnvAsInt("WRITE_TIMEOUT", 30)) * time.Second,
		Username:       getEnv("USERNAME", ""),
		Password:       getEnv("PASSWORD", ""),
	}

	// Create server
	server := NewServer(config)

	// Setup routes
	http.HandleFunc("/", server.HandleRoot)
	http.HandleFunc("/ws", server.HandleWebSocket)
	http.HandleFunc("/clients", server.HandleListClients)
	http.HandleFunc("/status", server.HandleStatus)
	http.HandleFunc("/test", server.HandleTest)              // Simple test endpoint
	http.HandleFunc("/kubectl", server.HandleKubectlRequest) // kubectl connection requests
	http.HandleFunc("/tunnel", server.HandleTunnelData)      // tunnel data from client

	// Start server
	log.Printf("🚀 Starting reverse proxy server on port %s", config.Port)
	log.Printf("📡 WebSocket endpoint: ws://localhost:%s/ws", config.Port)
	log.Printf("🔌 kubectl endpoint: POST /kubectl")
	log.Printf("🌐 Domain: %s", config.Domain)
	log.Printf("👥 Max clients: %d", config.MaxClients)
	log.Printf("🔌 kubectl requests will be detected and handled via WebSocket")

	if err := http.ListenAndServe(":"+config.Port, nil); err != nil {
		log.Fatalf("❌ Server failed: %v", err)
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
