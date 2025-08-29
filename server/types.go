package main

import (
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// Forward declarations for multiplexed tunnel
type ServerTCPConnection struct {
	ID       uint32
	UserConn net.Conn
	toAgent  chan []byte
	done     chan struct{}
}

type MultiplexedManager struct {
	agentID     string
	toAgent     http.ResponseWriter
	fromAgent   io.Reader
	connections map[uint32]*ServerTCPConnection
	mutex       sync.RWMutex
	nextConnID  uint32
	done        chan struct{}
	// Non-blocking write system
	writeQueue chan []byte
}

// Config holds server configuration
type Config struct {
	Port        string
	TCPPort     string // still used for user TCP entry (8081)
	Domain      string
	MaxClients  int
	H2Port      string
	TLSCertFile string
	TLSKeyFile  string
	TLSClientCA string
	DomainMap   map[string]string
}

// UserSession represents a single user connection session multiplexed over the tunnel
type UserSession struct {
	ID   uint64
	Conn net.Conn
	Done chan struct{}
}

// Agent represents a connected agent via TCP tunnel
type Agent struct {
	ID        string    `json:"id"`
	TCPConn   net.Conn  `json:"-"`
	Connected time.Time `json:"connected"`
	LastSeen  time.Time `json:"last_seen"`
	Requests  int64     `json:"requests"`
	BytesIn   int64     `json:"bytes_in"`
	BytesOut  int64     `json:"bytes_out"`

	// TCP user path state (for classic TCP tunnel, kept for admin/status)
	tunnelMutex sync.RWMutex
	isActive    bool
	done        chan bool // Channel to signal tunnel closure

	// Multiplexing state
	userConnections map[string]chan []byte
	writerMu        sync.Mutex // serialize writes to TCPConn

	// HTTP/2 stream pool (H2 mode)
	h2Streams chan *H2Stream

	// Multiplexed tunnel manager (ALWAYS-ON mode)
	multiplexedManager *MultiplexedManager
}

// H2Stream represents one HTTP/2 stream endpoints
type H2Stream struct {
	id          string
	writeStream io.WriteCloser // server -> agent (response body)
	readStream  io.ReadCloser  // agent -> server (request body)
	done        chan struct{}
	closeOnce   sync.Once
}

// Close signals the H2 stream to stop exactly once
func (h *H2Stream) Close() {
	if h == nil {
		return
	}
	h.closeOnce.Do(func() {
		close(h.done)
		if h.writeStream != nil {
			h.writeStream.Close()
		}
		if h.readStream != nil {
			h.readStream.Close()
		}
	})
}

// Server represents the TCP tunnel server
type Server struct {
	config *Config
	agents map[string]*Agent
	mutex  sync.RWMutex
}

// NewServer creates a new server instance
func NewServer(config *Config) *Server {
	return &Server{
		config: config,
		agents: make(map[string]*Agent),
	}
}
