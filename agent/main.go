package main

import (
	"log"
	// _ "net/http/pprof"
	"os"
	"strconv"
)

func main() {
	// Start pprof server for local profiling
	// go func() {
	// 	addr := getEnv("PPROF_ADDR", "127.0.0.1:6060")
	// 	log.Printf("🧩 pprof listening on %s", addr)
	// 	if err := http.ListenAndServe(addr, nil); err != nil {
	// 		log.Printf("⚠️ pprof server error: %v", err)
	// 	}
	// }()
	config := &Config{
		ServerHost: getEnv("SERVER_HOST", "localhost"),
		H2Port:     getEnv("H2_PORT", "80"),
		AgentID:    getEnv("AGENT_ID", "test"),

		// Single forwarding target - all TCP traffic goes here
		ForwardHost: getEnv("FORWARD_HOST", "localhost"),
		ForwardPort: getEnvAsInt("FORWARD_PORT", 3000),

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
