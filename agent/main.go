package main

import (
	"log"
	"os"
	"strconv"
)

func main() {
	config := &Config{
		ServerHost: getEnv("SERVER_HOST", "51.44.245.239"),
		H2Port:     getEnv("H2_PORT", "8443"),
		AgentID:    getEnv("AGENT_ID", "agent1"),

		// Single forwarding target - all TCP traffic goes here
		ForwardHost: getEnv("FORWARD_HOST", "192.168.1.2"),
		ForwardPort: getEnvAsInt("FORWARD_PORT", 6443),

		// H2 mTLS
		TLSCertFile: getEnv("TLS_CERT_FILE", ""),
		TLSKeyFile:  getEnv("TLS_KEY_FILE", ""),
		TLSServerCA: getEnv("TLS_SERVER_CA_FILE", ""),
	}

	log.Printf("🎯 Forwarding all traffic to %s:%d", config.ForwardHost, config.ForwardPort)

	agent := NewAgent(config)
	defer agent.Stop()

	if err := agent.Run(); err != nil {
		log.Fatalf("❌ Agent failed: %v", err)
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
