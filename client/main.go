package main

import (
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	// Load configuration
	config := &Config{
		//ServerURL: getEnv("SERVER_URL", "wss://multiplexer.azure.byoc.quix.ai/ws"),
		ServerURL:      getEnv("SERVER_URL", "ws://localhost:8080/ws"),
		ClientID:       getEnv("CLIENT_ID", "client1"),
		LocalHost:      getEnv("LOCAL_HOST", "192.168.1.2"),
		LocalPort:      getEnvAsInt("LOCAL_PORT", 6443),
		LocalSchema:    getEnv("LOCAL_SCHEMA", "https"), // Empty means auto-detect
		ChunkSize:      getEnvAsInt("CHUNK_SIZE", 8192),
		MaxMessageSize: int64(getEnvAsInt("MAX_MESSAGE_SIZE", 10*1024*1024)), // 10MB default
		ReadTimeout:    time.Duration(getEnvAsInt("READ_TIMEOUT", 120)) * time.Second,
		WriteTimeout:   time.Duration(getEnvAsInt("WRITE_TIMEOUT", 30)) * time.Second,
		ReconnectDelay: time.Duration(getEnvAsInt("RECONNECT_DELAY", 5)) * time.Second,
		MaxRetries:     getEnvAsInt("MAX_RETRIES", -1),    // -1 means infinite retries
		SkipVerify:     getEnvAsBool("SKIP_VERIFY", true), // Skip SSL verification by default
	}

	// Create client
	client := NewClient(config)

	// Handle shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Printf("🛑 Shutdown signal received")
		client.Stop()
	}()

	// Run client
	if err := client.Run(); err != nil {
		log.Fatalf("❌ Client failed: %v", err)
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

func getEnvAsBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}
