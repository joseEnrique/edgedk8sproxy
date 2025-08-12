package main

import (
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Config holds server configuration
type Config struct {
	Port           string
	Domain         string
	MaxClients     int
	ChunkSize      int
	MaxMessageSize int64
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	Username       string
	Password       string
}

// Client represents a connected client
type Client struct {
	ID               string                        `json:"id"`
	Conn             *websocket.Conn               `json:"-"`
	URL              string                        `json:"url"`
	Connected        time.Time                     `json:"connected"`
	LastSeen         time.Time                     `json:"last_seen"`
	Requests         int64                         `json:"requests"`
	BytesIn          int64                         `json:"bytes_in"`
	BytesOut         int64                         `json:"bytes_out"`
	ResponseChannels map[string]chan ProxyResponse `json:"-"`
	// Binary message handling for TCP tunnels
	binaryHandlers []chan []byte
	binaryMutex    sync.RWMutex
	writeMutex     sync.Mutex // For synchronizing WebSocket writes
	// Kubernetes streaming specific fields
	KubernetesStreamingReady bool   `json:"kubernetes_streaming_ready"`
	StreamingProtocol        string `json:"streaming_protocol"`
	// Tunnel management
	Tunnels map[string]*Tunnel `json:"-"`
}

// Tunnel represents a kubectl tunnel connection
type Tunnel struct {
	ID          string    `json:"id"`
	ClientID    string    `json:"client_id"`
	LocalPort   int       `json:"local_port"`
	Created     time.Time `json:"created"`
	Active      bool      `json:"active"`
	KubectlConn net.Conn  `json:"-"`
}

// Server represents the reverse proxy server
type Server struct {
	config   *Config
	clients  map[string]*Client
	mutex    sync.RWMutex
	upgrader websocket.Upgrader
}

// NewServer creates a new server instance
func NewServer(config *Config) *Server {
	return &Server{
		config:  config,
		clients: make(map[string]*Client),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
			// Enable compression for better performance with Kubernetes streaming
			EnableCompression: true,
			// Set buffer sizes optimized for kubectl operations
			ReadBufferSize:  64 * 1024, // 64KB read buffer
			WriteBufferSize: 64 * 1024, // 64KB write buffer
			// Set subprotocol for Kubernetes streaming compatibility
			Subprotocols: []string{"kubernetes-streaming"},
		},
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

// writeMessage writes a message to the WebSocket connection in a thread-safe manner
func (c *Client) writeMessage(messageType int, data []byte) error {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()
	return c.Conn.WriteMessage(messageType, data)
}
