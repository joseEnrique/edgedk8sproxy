package main

import (
	"net"
	"sync"
	"time"
)

// Config holds server configuration
type Config struct {
	Port         string
	TCPPort      string
	Domain       string
	MaxClients   int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// UserSession represents a single user connection session multiplexed over the tunnel
type UserSession struct {
	ID   uint64
	Conn net.Conn
	Done chan struct{}
}

// Client represents a connected client via TCP tunnel
type Client struct {
	ID        string    `json:"id"`
	TCPConn   net.Conn  `json:"-"`
	Connected time.Time `json:"connected"`
	LastSeen  time.Time `json:"last_seen"`
	Requests  int64     `json:"requests"`
	BytesIn   int64     `json:"bytes_in"`
	BytesOut  int64     `json:"bytes_out"`

	// TCP tunnel specific fields
	tunnelMutex sync.RWMutex
	isActive    bool
	done        chan bool // Channel to signal tunnel closure

	// Multiplexing state
	userConnections map[string]chan []byte
	writerMu        sync.Mutex // serialize writes to TCPConn
}

// Server represents the TCP tunnel server
type Server struct {
	config  *Config
	clients map[string]*Client
	mutex   sync.RWMutex

	// Session ID generator
	nextSessionID uint64
}

// NewServer creates a new server instance
func NewServer(config *Config) *Server {
	return &Server{
		config:  config,
		clients: make(map[string]*Client),
	}
}
