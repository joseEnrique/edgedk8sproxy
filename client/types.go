package main

import (
	"crypto/tls"
	"crypto/x509"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Config holds client configuration
type Config struct {
	ServerURL      string
	ClientID       string
	LocalHost      string
	LocalPort      int
	LocalSchema    string
	ChunkSize      int
	MaxMessageSize int64
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	ReconnectDelay time.Duration
	MaxRetries     int
	SkipVerify     bool // Skip SSL verification for local services
	// SSL Certificate configuration
	CACertFile     string // CA certificate file path
	ClientCertFile string // Client certificate file path
	ClientKeyFile  string // Client private key file path
}

// Client represents the tunnel client
type Client struct {
	config     *Config
	conn       *websocket.Conn
	done       chan bool
	httpClient *http.Client
	// Binary message handling for TCP tunnels
	binaryHandlers []chan []byte
	binaryMutex    sync.RWMutex

	// TCP tunnel state
	tcpTunnelActive bool
	tcpMutex        sync.RWMutex
	writeMutex      sync.Mutex // For synchronizing WebSocket writes
}

// NewClient creates a new client instance
func NewClient(config *Config) *Client {
	// Create TLS config
	tlsConfig := &tls.Config{}

	// Configure SSL verification skip if enabled
	if config.SkipVerify {
		tlsConfig.InsecureSkipVerify = true
	}

	// Load client certificates if provided
	if config.ClientCertFile != "" && config.ClientKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(config.ClientCertFile, config.ClientKeyFile)
		if err != nil {
			log.Printf("⚠️ Warning: Failed to load client certificates: %v", err)
		} else {
			tlsConfig.Certificates = []tls.Certificate{cert}
			log.Printf("✅ Loaded client certificate: %s", config.ClientCertFile)
		}
	}

	// Load CA certificate if provided
	if config.CACertFile != "" {
		caCert, err := os.ReadFile(config.CACertFile)
		if err != nil {
			log.Printf("⚠️ Warning: Failed to load CA certificate: %v", err)
		} else {
			caCertPool := x509.NewCertPool()
			if caCertPool.AppendCertsFromPEM(caCert) {
				tlsConfig.RootCAs = caCertPool
				log.Printf("✅ Loaded CA certificate: %s", config.CACertFile)
			} else {
				log.Printf("⚠️ Warning: Failed to parse CA certificate: %s", config.CACertFile)
			}
		}
	}

	// Create HTTP client with TLS configuration
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	return &Client{
		config:     config,
		done:       make(chan bool),
		httpClient: httpClient,
	}
}

// registerBinaryHandler registers a channel to receive binary messages
func (c *Client) registerBinaryHandler(ch chan []byte) {
	c.binaryMutex.Lock()
	defer c.binaryMutex.Unlock()
	c.binaryHandlers = append(c.binaryHandlers, ch)
}

// unregisterBinaryHandler removes a channel from binary message handlers
func (c *Client) unregisterBinaryHandler(ch chan []byte) {
	c.binaryMutex.Lock()
	defer c.binaryMutex.Unlock()

	for i, handler := range c.binaryHandlers {
		if handler == ch {
			c.binaryHandlers = append(c.binaryHandlers[:i], c.binaryHandlers[i+1:]...)
			break
		}
	}
}

// forwardBinaryMessage forwards a binary message to all registered handlers
func (c *Client) forwardBinaryMessage(data []byte) {
	c.binaryMutex.RLock()
	defer c.binaryMutex.RUnlock()

	// Only log occasionally to avoid spam
	if len(data) > 1000 {
		log.Printf("📦 Forwarding large binary message: %d bytes", len(data))
	}

	// Use non-blocking send to avoid blocking the main message loop
	// Limit the number of handlers to prevent blocking
	handlersProcessed := 0
	maxHandlers := 5 // Limit to prevent blocking

	for _, handler := range c.binaryHandlers {
		if handlersProcessed >= maxHandlers {
			break
		}

		select {
		case handler <- data:
			// Message sent successfully
			handlersProcessed++
		default:
			// Channel is full, skip this handler silently
			// This prevents blocking when TCP tunnel is very active
		}
	}
}

// setTCPTunnelActive sets the TCP tunnel state
func (c *Client) setTCPTunnelActive(active bool) {
	c.tcpMutex.Lock()
	defer c.tcpMutex.Unlock()
	c.tcpTunnelActive = active
	if active {
		log.Printf("🔌 TCP tunnel activated - HTTP requests will continue to work")
	} else {
		log.Printf("🔌 TCP tunnel deactivated")
	}
}

// isTCPTunnelActive checks if TCP tunnel is active
func (c *Client) isTCPTunnelActive() bool {
	c.tcpMutex.RLock()
	defer c.tcpMutex.RUnlock()
	return c.tcpTunnelActive
}

// writeMessage writes a message to the WebSocket connection in a thread-safe manner
func (c *Client) writeMessage(messageType int, data []byte) error {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()
	return c.conn.WriteMessage(messageType, data)
}
