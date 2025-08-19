package main

import (
	"net"
	"sync"
	"time"
)

// Config holds the client configuration
type Config struct {
	// Server connection settings
	ServerHost string
	ServerPort string
	ClientID   string

	// Timeouts
	ReadTimeout   time.Duration
	WriteTimeout  time.Duration
	TargetTimeout time.Duration // New field for target connection timeout

	// Forwarding configuration - single port
	ForwardHost string
	ForwardPort int
}

// Client represents the TCP tunnel client
type Client struct {
	config *Config
	conn   net.Conn
	done   chan bool
	active bool
	mutex  sync.RWMutex

	// Single forwarding connection
	forwardConn        net.Conn
	forwardMutex       sync.RWMutex
	targetReaderActive bool // Track if target reader is already running
}

// NewClient creates a new client instance
func NewClient(config *Config) *Client {
	return &Client{
		config: config,
		done:   make(chan bool),
		active: false,
	}
}
