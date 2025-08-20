package main

// Config holds the agent configuration
type Config struct {
	// Server H2 endpoint
	ServerHost string
	H2Port     string
	AgentID    string

	// Forwarding configuration - single port
	ForwardHost string
	ForwardPort int

	// H2 mTLS config
	TLSCertFile string
	TLSKeyFile  string
	TLSServerCA string
}

// Agent represents the H2 tunnel agent
type Agent struct {
	config *Config
	done   chan bool
}

// NewAgent creates a new agent instance
func NewAgent(config *Config) *Agent {
	return &Agent{
		config: config,
		done:   make(chan bool),
	}
}
