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
	config := &Config{
		ServerHost:   getEnv("SERVER_HOST", "localhost"),
		ServerPort:   getEnv("SERVER_PORT", "8081"),
		ClientID:     getEnv("CLIENT_ID", "client1"),
		ReadTimeout:  time.Duration(getEnvAsInt("READ_TIMEOUT", 5)) * time.Second,  // Reduced from 120s to 5s
		WriteTimeout: time.Duration(getEnvAsInt("WRITE_TIMEOUT", 5)) * time.Second, // Reduced from 30s to 5s

		TargetTimeout: time.Duration(getEnvAsInt("TARGET_TIMEOUT", 30)) * time.Second, // New timeout for target

		// Single forwarding target - all TCP traffic goes here
		ForwardHost: getEnv("FORWARD_HOST", "192.168.1.2"),
		ForwardPort: getEnvAsInt("FORWARD_PORT", 80),
	}

	log.Printf("🎯 Forwarding all traffic to %s:%d", config.ForwardHost, config.ForwardPort)

	client := NewClient(config)
	defer client.Stop()

	// Handle shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Printf("🛑 Shutdown signal received")
	}()

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
