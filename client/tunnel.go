package main

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// Run starts the TCP tunnel client
func (c *Client) Run() error {
	log.Printf("🚀 Starting TCP tunnel client")
	log.Printf("🔌 Connecting to server: %s:%s", c.config.ServerHost, c.config.ServerPort)
	log.Printf("🆔 Client ID: %s", c.config.ClientID)
	log.Printf("🎯 Forwarding all traffic to: %s:%d", c.config.ForwardHost, c.config.ForwardPort)

	// OPTIMIZED: Much faster reconnection delays
	reconnectDelay := 1 * time.Second
	maxReconnectDelay := 10 * time.Second

	for {
		select {
		case <-c.done:
			return nil
		default:
			if err := c.connect(); err != nil {
				log.Printf("❌ Connection failed: %v", err)
				log.Printf("🔄 Retrying in %v...", reconnectDelay)
				time.Sleep(reconnectDelay)
				reconnectDelay *= 2
				if reconnectDelay > maxReconnectDelay {
					reconnectDelay = maxReconnectDelay
				}
				continue
			}
			reconnectDelay = 1 * time.Second
			if err := c.startTunnel(); err != nil {
				log.Printf("❌ Tunnel failed: %v", err)
				_ = c.conn.Close()
				log.Printf("🔄 Reconnecting...")
				continue
			}
		}
	}
}

// connect establishes TCP connection to the server
func (c *Client) connect() error {
	// Use a Dialer with TCP keepalive to keep the tunnel open reliably
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	conn, err := dialer.Dial("tcp", c.config.ServerHost+":"+c.config.ServerPort)
	if err != nil {
		return err
	}
	// Configure TCP options for low latency and robustness
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(30 * time.Second)
		_ = tcp.SetLinger(0)
		_ = tcp.SetReadBuffer(64 * 1024)
		_ = tcp.SetWriteBuffer(64 * 1024)
	}
	c.conn = conn
	log.Printf("✅ Connected to server %s:%s", c.config.ServerHost, c.config.ServerPort)
	return nil
}

// startTunnel starts the TCP tunnel
func (c *Client) startTunnel() error {
	log.Printf("📡 Starting TCP tunnel")
	if _, err := c.conn.Write([]byte(c.config.ClientID)); err != nil {
		return err
	}
	log.Printf("✅ Client identification sent: %s", c.config.ClientID)
	c.mutex.Lock()
	c.active = true
	c.mutex.Unlock()
	return c.handleTunnel()
}

// handleTunnel handles the TCP tunnel communication
func (c *Client) handleTunnel() error {
	log.Printf("📡 TCP tunnel active")

	// Create a channel to coordinate shutdown
	done := make(chan bool)
	var closeOnce sync.Once

	// Safe close function that only closes once
	closeDone := func() {
		closeOnce.Do(func() {
			close(done)
		})
	}

	defer func() {
		closeDone()
		c.mutex.Lock()
		c.active = false
		c.mutex.Unlock()
		log.Printf("📡 TCP tunnel ended")
	}()

	// Start reading from server with optimized buffer
	go func() {
		defer func() {
			log.Printf("📤 Read stream ended")
		}()

		buffer := make([]byte, 32*1024)
		for {
			select {
			case <-done:
				return
			default:
				// NO TIMEOUTS - wait for FIN TCP from server
				n, err := c.conn.Read(buffer)
				if err != nil {
					if err.Error() != "EOF" {
						log.Printf("❌ Read error: %v", err)
					}
					// Signal that tunnel is broken and needs reconnection
					closeDone()
					return
				}

				if n > 0 {
					data := make([]byte, n)
					copy(data, buffer[:n])

					log.Printf("📤 Received %d bytes from server", n)

					// Forward ALL data to the target immediately
					c.forwardToTarget(data)
				}
			}
		}
	}()

	// Wait for tunnel to be closed by server (FIN TCP) or client shutdown
	select {
	case <-done:
		// Tunnel was broken, return error to trigger reconnection
		return fmt.Errorf("tunnel connection lost")
	case <-c.done:
		// Client was stopped intentionally
		return nil
	}
}

// forwardToTarget forwards all data to the target host
func (c *Client) forwardToTarget(data []byte) {
	log.Printf("📤 Forwarding %d bytes to target %s:%d", len(data), c.config.ForwardHost, c.config.ForwardPort)

	// Get or create connection to target
	targetConn, err := c.getOrCreateTargetConnection()
	if err != nil {
		log.Printf("❌ Failed to get target connection: %v", err)
		// Send error response back to server
		errorResp := fmt.Sprintf("Forward Error: %v", err)
		c.conn.Write([]byte(errorResp))
		return
	}

	log.Printf("🔌 Using target connection: %s", targetConn.RemoteAddr().String())

	// Forward data to target - NO DELAYS
	if _, err := targetConn.Write(data); err != nil {
		log.Printf("❌ Failed to forward data to target: %v", err)
		// Close dead connection and try to create new one
		_ = targetConn.Close()
		c.forwardMutex.Lock()
		c.forwardConn = nil
		c.forwardMutex.Unlock()
		return
	}

	log.Printf("✅ Successfully forwarded %d bytes to target", len(data))

	// Start reading response from target in a separate goroutine (only if not already running)
	c.forwardMutex.Lock()
	if !c.targetReaderActive {
		c.targetReaderActive = true
		go c.readTargetResponse(targetConn)
	}
	c.forwardMutex.Unlock()
}

// getOrCreateTargetConnection gets or creates a connection to the target
func (c *Client) getOrCreateTargetConnection() (net.Conn, error) {
	c.forwardMutex.Lock()
	defer c.forwardMutex.Unlock()

	// Check if target connection exists and is active
	if c.forwardConn != nil {
		// Quick test if connection is still alive
		if err := c.quickTestConnection(c.forwardConn); err == nil {
			return c.forwardConn, nil
		}
		// Connection is dead, close it
		_ = c.forwardConn.Close()
		c.forwardConn = nil
		c.targetReaderActive = false
	}

	// Connect to the target with timeout
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	conn, err := dialer.Dial("tcp", fmt.Sprintf("%s:%d", c.config.ForwardHost, c.config.ForwardPort))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to target %s:%d: %v", c.config.ForwardHost, c.config.ForwardPort, err)
	}

	// Set TCP options for better performance
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(30 * time.Second)
		_ = tcpConn.SetLinger(0)
		// Increase buffer sizes
		_ = tcpConn.SetReadBuffer(64 * 1024)
		_ = tcpConn.SetWriteBuffer(64 * 1024)
	}

	// Successfully connected to target
	c.forwardConn = conn
	c.targetReaderActive = false // Will be set to true when we start reading
	log.Printf("✅ Created new connection to target %s:%d", c.config.ForwardHost, c.config.ForwardPort)
	return conn, nil
}

// quickTestConnection tests if a connection is still alive with minimal overhead
func (c *Client) quickTestConnection(conn net.Conn) error {
	// Set a very short timeout for testing
	conn.SetDeadline(time.Now().Add(50 * time.Millisecond))

	// Try to read 1 byte (this will fail if connection is dead)
	buf := make([]byte, 1)
	_, err := conn.Read(buf)

	// Reset deadline
	conn.SetDeadline(time.Time{})

	return err
}

// readTargetResponse reads response from target and forwards to tunnel
func (c *Client) readTargetResponse(targetConn net.Conn) {
	defer func() {
		log.Printf("📤 Target response reader ended")
		c.forwardMutex.Lock()
		c.targetReaderActive = false
		c.forwardMutex.Unlock()
	}()

	buffer := make([]byte, 32*1024)
	for {
		// NO TIMEOUTS - just read directly, wait for FIN TCP
		n, err := targetConn.Read(buffer)
		if err != nil {
			if err.Error() == "EOF" {
				log.Printf("📤 Target connection closed (EOF)")
			} else {
				log.Printf("❌ Target read error: %v", err)
			}
			return
		}

		if n > 0 {
			data := make([]byte, n)
			copy(data, buffer[:n])

			log.Printf("📤 Received %d bytes from target, forwarding to tunnel", n)

			// Forward target response back to server through tunnel - NO DELAYS
			if _, err := c.conn.Write(data); err != nil {
				log.Printf("❌ Failed to forward target response to tunnel: %v", err)
				return
			}

			log.Printf("✅ Successfully forwarded %d bytes from target to server", n)
		}
	}
}

// Stop stops the client
func (c *Client) Stop() {
	log.Printf("🛑 Stopping client")
	close(c.done)

	c.mutex.Lock()
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.active = false
	c.mutex.Unlock()

	// Close target connection
	c.forwardMutex.Lock()
	if c.forwardConn != nil {
		log.Printf("🔌 Closing target connection")
		_ = c.forwardConn.Close()
		c.forwardConn = nil
	}
	c.forwardMutex.Unlock()

	log.Printf("✅ Client stopped")
}
