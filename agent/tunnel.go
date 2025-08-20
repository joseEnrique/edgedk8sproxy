package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"
)

// Global counters for connection tracking
var (
	activeStreams int64
	totalStreams  int64
)

// Buffer pool to reduce memory allocations and GC pressure
var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 64*1024) // Pre-allocate 64KB buffers
	},
}

// getBuffer gets a buffer from pool
func getBuffer(minSize int) []byte {
	buf := bufferPool.Get().([]byte)
	if cap(buf) < minSize {
		// If buffer is too small, create a new one
		return make([]byte, minSize)
	}
	return buf[:minSize]
}

// putBuffer returns buffer to pool
func putBuffer(buf []byte) {
	if cap(buf) == 64*1024 {
		bufferPool.Put(buf)
	}
}

// Run starts the H2 tunnel agent
func (a *Agent) Run() error {
	log.Printf("🚀 Starting TCP-over-HTTP/2 tunnel agent")
	log.Printf("🔐 H2 endpoint: %s:%s", a.config.ServerHost, a.config.H2Port)
	log.Printf("🎯 Tunneling ALL TCP traffic to: %s:%d", a.config.ForwardHost, a.config.ForwardPort)
	return a.runH2()
}

// runH2 starts HTTP/2 mTLS tunnel with a pool of streams
func (a *Agent) runH2() error {
	// Load client cert
	cert, err := tls.LoadX509KeyPair(a.config.TLSCertFile, a.config.TLSKeyFile)
	if err != nil {
		return fmt.Errorf("load client cert: %w", err)
	}
	// Load server CA
	caPem, err := os.ReadFile(a.config.TLSServerCA)
	if err != nil {
		return fmt.Errorf("read server CA: %w", err)
	}
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPem)

	tlsCfg := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		RootCAs:            caPool,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: false,
		NextProtos:         []string{"h2"},
	}
	// Create HTTP/2 transport optimized for low latency
	h2tr := &http2.Transport{
		TLSClientConfig:  tlsCfg,
		ReadIdleTimeout:  60 * time.Second, // Keep connections alive longer
		PingTimeout:      5 * time.Second,  // Faster ping detection
		WriteByteTimeout: 2 * time.Second,  // Faster write timeout
		MaxReadFrameSize: 32768,            // Larger frames for better throughput
		IdleConnTimeout:  60 * time.Second, // Keep connections alive longer
	}
	httpClient := &http.Client{Transport: h2tr}

	baseURL := fmt.Sprintf("https://%s:%s", a.config.ServerHost, a.config.H2Port)
	log.Printf("🔐 H2 mTLS enabled. Ready to tunnel TCP traffic via %s/tunnel", baseURL)

	// Start ALWAYS-MULTIPLEXED single stream
	log.Printf("🚀 Starting ALWAYS-MULTIPLEXED single stream tunnel")
	go a.runAlwaysMultiplexed(httpClient, baseURL)

	// Start statistics reporter
	go a.runStatsReporter()

	<-a.done
	return nil
}

// runAlwaysMultiplexed mantiene EL ÚNICO stream multiplexado SIEMPRE activo con reconexión inteligente
func (a *Agent) runAlwaysMultiplexed(httpClient *http.Client, baseURL string) {
	log.Printf("🔗 Maintaining SINGLE ULTRA-PERSISTENT multiplexed stream with AUTO-RECONNECT")

	consecutiveFailures := 0

	for {
		select {
		case <-a.done:
			log.Printf("🛑 Ultra-persistent stream shutting down")
			return
		default:
			// Intentar crear/recrear el stream
			log.Printf("🚀 Attempting connection to server (failures: %d)", consecutiveFailures)
			err := a.createUltraPersistentStream(httpClient, baseURL)

			if err != nil {
				consecutiveFailures++
				retryDelay := a.calculateRetryDelay(consecutiveFailures, err)

				// Log with more detail for EOF debugging
				if strings.Contains(err.Error(), "EOF") {
					log.Printf("❌ Connection failed with EOF (attempt %d): %v", consecutiveFailures, err)
					log.Printf("🔍 This likely means the server closed the connection")
				} else {
					log.Printf("❌ Connection failed (attempt %d): %v", consecutiveFailures, err)
				}
				log.Printf("🔄 Auto-reconnecting in %v...", retryDelay)

				// Check if we should recreate HTTP client for DNS/connection issues
				if consecutiveFailures%10 == 0 {
					log.Printf("🔧 Recreating HTTP client after %d failures", consecutiveFailures)
					httpClient = a.createOptimizedHTTPClient()
				}

				// Sleep with cancellation support
				select {
				case <-time.After(retryDelay):
					continue
				case <-a.done:
					return
				}
			}

			// Connection successful
			if consecutiveFailures > 0 {
				log.Printf("✅ Reconnected successfully after %d failures", consecutiveFailures)
			}
			consecutiveFailures = 0

			// If we reach here, the stream ended normally, reconnect immediately
			log.Printf("🔄 Stream ended normally, reconnecting immediately...")
		}
	}
}

// calculateRetryDelay calcula el tiempo de espera basado en el tipo de error y número de fallos
func (a *Agent) calculateRetryDelay(failures int, err error) time.Duration {
	errStr := strings.ToLower(err.Error())

	// Análisis del tipo de error para ajustar la estrategia
	switch {
	case strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "connect: connection refused"):
		// Server down - backoff rápido inicialmente, luego más lento
		switch {
		case failures <= 3:
			return 1 * time.Second
		case failures <= 10:
			return 5 * time.Second
		default:
			return 15 * time.Second
		}

	case strings.Contains(errStr, "server closed connection") || strings.Contains(errStr, "unexpected eof") || strings.Contains(errStr, "connection closed"):
		// Server closed connection - puede ser restart o overflow, reconectar rápido
		switch {
		case failures <= 5:
			return 2 * time.Second
		case failures <= 15:
			return 5 * time.Second
		default:
			return 10 * time.Second
		}

	case strings.Contains(errStr, "no such host") || strings.Contains(errStr, "dns"):
		// DNS issues - esperar más tiempo para que se resuelva
		switch {
		case failures <= 2:
			return 5 * time.Second
		default:
			return 30 * time.Second
		}

	case strings.Contains(errStr, "timeout") || strings.Contains(errStr, "i/o timeout"):
		// Network timeout - backoff progresivo moderado
		switch {
		case failures <= 5:
			return 2 * time.Second
		case failures <= 15:
			return 10 * time.Second
		default:
			return 30 * time.Second
		}

	case strings.Contains(errStr, "network is unreachable") || strings.Contains(errStr, "no route to host"):
		// Network infrastructure issues - espera más larga
		switch {
		case failures <= 3:
			return 10 * time.Second
		default:
			return 60 * time.Second
		}

	case strings.Contains(errStr, "certificate") || strings.Contains(errStr, "tls"):
		// TLS/Certificate issues - backoff más lento ya que puede requerir intervención
		switch {
		case failures <= 2:
			return 5 * time.Second
		case failures <= 5:
			return 30 * time.Second
		default:
			return 120 * time.Second
		}

	default:
		// Error desconocido - backoff estándar
		switch {
		case failures <= 3:
			return 1 * time.Second
		case failures <= 10:
			return 5 * time.Second
		case failures <= 20:
			return 15 * time.Second
		default:
			return 60 * time.Second
		}
	}
}

// createOptimizedHTTPClient crea un nuevo cliente HTTP optimizado para reconexión
func (a *Agent) createOptimizedHTTPClient() *http.Client {
	// Load client cert
	cert, err := tls.LoadX509KeyPair(a.config.TLSCertFile, a.config.TLSKeyFile)
	if err != nil {
		log.Printf("⚠️ Error loading client cert during reconnect: %v", err)
		return nil
	}

	// Load server CA
	caPem, err := os.ReadFile(a.config.TLSServerCA)
	if err != nil {
		log.Printf("⚠️ Error loading server CA during reconnect: %v", err)
		return nil
	}
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPem)

	tlsCfg := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		RootCAs:            caPool,
		ServerName:         a.config.ServerHost,
		InsecureSkipVerify: false,
	}

	// Create HTTP/2 transport with reconnection-friendly settings
	transport := &http2.Transport{
		TLSClientConfig: tlsCfg,
		// Allow multiple HTTP/2 connections for better reliability
		AllowHTTP: false,
		// Disable connection pooling to always create fresh connections
		DisableCompression: false,
		DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
			dialer := &net.Dialer{
				Timeout:   15 * time.Second, // Longer timeout for reliability
				KeepAlive: 30 * time.Second,
			}
			conn, err := tls.DialWithDialer(dialer, network, addr, cfg)
			if err != nil {
				return nil, err
			}
			// Set TCP socket options for better error detection
			if netConn := conn.NetConn(); netConn != nil {
				if tcpConn, ok := netConn.(*net.TCPConn); ok {
					tcpConn.SetKeepAlive(true)
					tcpConn.SetKeepAlivePeriod(30 * time.Second)
					tcpConn.SetNoDelay(true)
				}
			}
			return conn, nil
		},
	}

	return &http.Client{
		Transport: transport,
		Timeout:   120 * time.Second, // Longer timeout for persistent connections
	}
}

// createUltraPersistentStream creates THE ONLY multiplexed stream with maximum persistence
func (a *Agent) createUltraPersistentStream(httpClient *http.Client, baseURL string) error {
	streamID := atomic.AddInt64(&totalStreams, 1)
	// Create pipe for bidirectional communication
	pr, pw := io.Pipe()

	// Create HTTP/2 request for the multiplexed stream
	req, err := http.NewRequest("POST", baseURL+"/tunnel", pr)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Agent-ID", a.config.AgentID)
	req.Header.Set("X-Tunnel-Mode", "multiplexed")
	req.Header.Set("X-Ultra-Persistent", "true")
	req.Header.Set("Cache-Control", "no-cache")

	log.Printf("✅ PERSISTENT multiplexed stream #%d established for agent %s", streamID, a.config.AgentID)

	// Start the HTTP/2 request with detailed error handling
	resp, err := httpClient.Do(req)
	if err != nil {
		_ = pw.Close()
		// Classify error for better reconnection strategy
		errStr := err.Error()
		if strings.Contains(errStr, "connection refused") {
			return fmt.Errorf("server unavailable: %w", err)
		} else if strings.Contains(errStr, "timeout") {
			return fmt.Errorf("connection timeout: %w", err)
		} else if strings.Contains(errStr, "no such host") {
			return fmt.Errorf("dns resolution failed: %w", err)
		} else if strings.Contains(errStr, "unexpected EOF") {
			return fmt.Errorf("server closed connection: %w", err)
		} else if strings.Contains(errStr, "EOF") {
			return fmt.Errorf("connection closed: %w", err)
		}
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_ = pw.Close()
		return fmt.Errorf("server rejected connection (status %d)", resp.StatusCode)
	}

	// Connection manager for multiple TCP connections
	connManager := &AgentConnectionManager{
		connections: make(map[uint32]*AgentTCPConnection),
		mutex:       sync.RWMutex{},
		nextConnID:  1,
	}

	done := make(chan struct{})
	connectionLost := make(chan error, 1) // Signal when connection is lost
	var wg sync.WaitGroup

	// Goroutine 1: Read frames from server and dispatch to connections
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.handleIncomingFrames(resp.Body, connManager, done, connectionLost)
	}()

	// Goroutine 2: Read from connections and send frames to server
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.handleOutgoingFrames(pw, connManager, done, connectionLost)
	}()

	// Goroutine 3: Ultra-aggressive keepalive to maintain stream
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.ultraKeepAlive(pw, done)
	}()

	// Wait for connection loss or manual stop
	select {
	case err := <-connectionLost:
		log.Printf("🔌 Connection lost, triggering reconnection: %v", err)
		close(done)
		wg.Wait()
		return fmt.Errorf("connection lost: %w", err)
	case <-done:
		wg.Wait()
		return nil
	}
}

// ultraKeepAlive mantiene el stream activo enviando heartbeats cada 30 segundos (reduced frequency)
func (a *Agent) ultraKeepAlive(writer io.Writer, done <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second) // Reduced from 10s to 30s for lower CPU
	defer ticker.Stop()

	// Pre-allocate heartbeat buffer to avoid repeated allocations
	heartbeat := make([]byte, 8) // [conn_id=0][length=0] - all zeros

	log.Printf("🫀 Starting lightweight keepalive for THE ONLY stream (30s interval)")

	for {
		select {
		case <-done:
			return // Removed log to reduce I/O
		case <-ticker.C:
			if _, err := writer.Write(heartbeat); err != nil {
				return // Fail silently to reduce I/O
			}
			// Removed heartbeat success log to reduce I/O overhead
		}
	}
}

// AgentConnectionManager manages multiple TCP connections within single stream
type AgentConnectionManager struct {
	connections map[uint32]*AgentTCPConnection
	mutex       sync.RWMutex
	nextConnID  uint32
}

// AgentTCPConnection represents a single TCP connection on agent side
type AgentTCPConnection struct {
	ID         uint32
	Conn       net.Conn
	ToServer   chan []byte
	fromTarget chan []byte
	done       chan struct{}
}

// handleIncomingFrames processes frames from server and routes to TCP connections
func (a *Agent) handleIncomingFrames(reader io.Reader, connManager *AgentConnectionManager, done <-chan struct{}, connectionLost chan<- error) {
	for {
		select {
		case <-done:
			return
		default:
			// Read frame header (8 bytes: 4 bytes conn_id + 4 bytes length)
			header := make([]byte, 8)
			if _, err := io.ReadFull(reader, header); err != nil {
				// Connection lost - signal for reconnection
				if err == io.EOF {
					log.Printf("🔌 Server closed connection (EOF)")
				} else if strings.Contains(err.Error(), "connection reset") {
					log.Printf("🔌 Connection reset by server")
				} else if strings.Contains(err.Error(), "broken pipe") {
					log.Printf("🔌 Connection broken")
				} else {
					log.Printf("🔌 Connection error: %v", err)
				}
				// Signal connection loss to trigger reconnection
				select {
				case connectionLost <- err:
				default:
				}
				return
			}

			connID := uint32(header[0])<<24 | uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
			length := uint32(header[4])<<24 | uint32(header[5])<<16 | uint32(header[6])<<8 | uint32(header[7])

			if length == 0 {
				// Control frame (connection close)
				a.handleConnectionClose(connManager, connID)
				continue
			}

			if length > 64*1024 {
				log.Printf("⚠️ Frame too large: %d bytes", length)
				return
			}

			// Use buffer pool to reduce allocations
			data := getBuffer(int(length))
			defer putBuffer(data)

			if _, err := io.ReadFull(reader, data[:length]); err != nil {
				// Reduced logging for performance
				return
			}

			// Handle new connection or route to existing
			if connID == 0 {
				// This shouldn't happen in this direction (server sends to existing connections)
				log.Printf("⚠️ Received frame with connID 0 from server")
			} else {
				// Route to existing connection, or create new one if needed
				a.routeToConnection(connManager, connID, data)
			}
		}
	}
}

// handleOutgoingFrames sends data from TCP connections as frames to server
func (a *Agent) handleOutgoingFrames(writer io.Writer, connManager *AgentConnectionManager, done <-chan struct{}, connectionLost chan<- error) {
	ticker := time.NewTicker(50 * time.Millisecond) // Reduced frequency from 10ms to 50ms
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			// Use pre-allocated slice to reduce allocations
			connManager.mutex.RLock()
			connectionCount := len(connManager.connections)
			if connectionCount == 0 {
				connManager.mutex.RUnlock()
				continue
			}
			connections := make([]*AgentTCPConnection, 0, connectionCount)
			for _, conn := range connManager.connections {
				connections = append(connections, conn)
			}
			connManager.mutex.RUnlock()

			// Collect data from active connections only
			for _, tcpConn := range connections {
				select {
				case data := <-tcpConn.fromTarget:
					// Use buffer pool for frame creation
					frameSize := 8 + len(data)
					frame := getBuffer(frameSize)

					// Write connection ID (big-endian)
					frame[0] = byte(tcpConn.ID >> 24)
					frame[1] = byte(tcpConn.ID >> 16)
					frame[2] = byte(tcpConn.ID >> 8)
					frame[3] = byte(tcpConn.ID)

					// Write data length (big-endian)
					dataLen := uint32(len(data))
					frame[4] = byte(dataLen >> 24)
					frame[5] = byte(dataLen >> 16)
					frame[6] = byte(dataLen >> 8)
					frame[7] = byte(dataLen)

					// Copy data
					copy(frame[8:], data)

					// Send frame to server
					if _, err := writer.Write(frame[:frameSize]); err != nil {
						putBuffer(frame) // Return buffer to pool
						// Connection lost - log for debugging and signal for reconnection
						if strings.Contains(err.Error(), "broken pipe") ||
							strings.Contains(err.Error(), "connection reset") ||
							err == io.EOF {
							log.Printf("🔌 Lost connection to server while sending frame")
						}
						// Signal connection loss to trigger reconnection
						select {
						case connectionLost <- err:
						default:
						}
						return
					}
					putBuffer(frame) // Return buffer to pool after use
				default:
					// No data available from this connection
				}
			}
		}
	}
}

// routeToConnection sends data to specific TCP connection, creates if needed
func (a *Agent) routeToConnection(connManager *AgentConnectionManager, connID uint32, data []byte) {
	connManager.mutex.RLock()
	tcpConn, exists := connManager.connections[connID]
	connManager.mutex.RUnlock()

	if !exists {
		// Create new connection to target
		tcpConn = a.createNewConnection(connManager, connID)
		if tcpConn == nil {
			return
		}
	}

	// Write data to target connection
	if _, err := tcpConn.Conn.Write(data); err != nil {
		log.Printf("⚠️ Error writing to target conn %d: %v", connID, err)
		a.closeConnection(connManager, connID)
	}
}

// createNewConnection creates a new TCP connection to target
func (a *Agent) createNewConnection(connManager *AgentConnectionManager, connID uint32) *AgentTCPConnection {
	// Connect to target
	targetConn, err := net.Dial("tcp", net.JoinHostPort(a.config.ForwardHost, fmt.Sprintf("%d", a.config.ForwardPort)))
	if err != nil {
		log.Printf("⚠️ Failed to connect to target for conn %d: %v", connID, err)
		return nil
	}

	// Create TCP connection object
	tcpConn := &AgentTCPConnection{
		ID:         connID,
		Conn:       targetConn,
		ToServer:   make(chan []byte, 100),
		fromTarget: make(chan []byte, 100),
		done:       make(chan struct{}),
	}

	connManager.mutex.Lock()
	connManager.connections[connID] = tcpConn
	connManager.mutex.Unlock()

	log.Printf("🔗 New multiplexed connection %d created", connID)

	// Start goroutines for this connection
	go a.readFromTarget(tcpConn)

	return tcpConn
}

// handleConnectionClose closes a TCP connection
func (a *Agent) handleConnectionClose(connManager *AgentConnectionManager, connID uint32) {
	a.closeConnection(connManager, connID)
}

// closeConnection closes and removes a TCP connection
func (a *Agent) closeConnection(connManager *AgentConnectionManager, connID uint32) {
	connManager.mutex.Lock()
	tcpConn, exists := connManager.connections[connID]
	if exists {
		delete(connManager.connections, connID)
	}
	connManager.mutex.Unlock()

	if exists {
		close(tcpConn.done)
		tcpConn.Conn.Close()
		log.Printf("🔌 Multiplexed connection %d closed", connID)
	}
}

// readFromTarget reads data from target and queues for server
func (a *Agent) readFromTarget(tcpConn *AgentTCPConnection) {
	buffer := make([]byte, 32*1024)
	for {
		select {
		case <-tcpConn.done:
			return
		default:
			tcpConn.Conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, err := tcpConn.Conn.Read(buffer)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buffer[:n])
				select {
				case tcpConn.fromTarget <- data:
				case <-tcpConn.done:
					return
				}
			}
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				return
			}
		}
	}
}

// MULTIPLEXED MODE - All legacy pool functions removed
// Now using single stream with internal connection multiplexing

// runStatsReporter logs periodic statistics about stream usage
func (a *Agent) runStatsReporter() {
	ticker := time.NewTicker(30 * time.Second) // Less frequent reporting
	defer ticker.Stop()

	for {
		select {
		case <-a.done:
			return
		case <-ticker.C:
			active := atomic.LoadInt64(&activeStreams)
			total := atomic.LoadInt64(&totalStreams)
			log.Printf("📊 Stream stats: Active=%d, Total created=%d", active, total)
		}
	}
}

// openH2StreamBridge opens one HTTP/2 stream and bridges server<->target
func (a *Agent) openH2StreamBridge(httpClient *http.Client, baseURL string) error {
	streamID := atomic.AddInt64(&totalStreams, 1)
	activeCount := atomic.AddInt64(&activeStreams, 1)

	defer func() {
		atomic.AddInt64(&activeStreams, -1)
	}()

	// Only log every 10th stream to reduce verbosity
	if streamID%10 == 1 || activeCount <= 5 {
		log.Printf("🔗 Opening H2 stream #%d (active: %d)", streamID, activeCount)
	}

	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/tunnel", pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Agent-ID", a.config.AgentID)
	req.Header.Set("Cache-Control", "no-cache")

	// Start HTTP request
	resp, err := httpClient.Do(req)
	if err != nil {
		_ = pw.Close()
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_ = pw.Close()
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Minimal logging for maximum performance - only log first few streams
	if activeCount <= 3 {
		log.Printf("✅ H2 stream #%d ready for instant TCP traffic", streamID)
	}

	// Open a dedicated TCP connection to the target for this H2 stream - OPTIMIZED FOR SPEED
	dialer := &net.Dialer{
		Timeout:   2 * time.Second, // Ultra-fast connection timeout for local targets
		KeepAlive: 5 * time.Second, // Very frequent keepalives
	}
	targetConn, err := dialer.Dial("tcp", net.JoinHostPort(a.config.ForwardHost, fmt.Sprintf("%d", a.config.ForwardPort)))
	if err != nil {
		_ = pw.Close()
		return fmt.Errorf("failed to connect to target %s:%d: %w", a.config.ForwardHost, a.config.ForwardPort, err)
	}

	// Configure TCP connection for optimal performance and low latency
	if tcpConn, ok := targetConn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true) // Disable Nagle for low latency
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(10 * time.Second) // More frequent keepalives
		_ = tcpConn.SetLinger(0)                         // Close immediately for faster cleanup
		_ = tcpConn.SetReadBuffer(256 * 1024)            // Larger buffers for better throughput
		_ = tcpConn.SetWriteBuffer(256 * 1024)           // Larger buffers for better throughput
	}

	// Skip individual connection logs for maximum performance

	// Ensure both sides are closed when done with aggressive cleanup
	done := make(chan struct{})
	var once sync.Once
	closeDone := func() {
		once.Do(func() {
			close(done)

			// Aggressively close all connections
			if tcpConn, ok := targetConn.(*net.TCPConn); ok {
				// Force immediate close without lingering
				tcpConn.SetLinger(0)
				tcpConn.Close()
			} else {
				targetConn.Close()
			}

			// Close pipes with explicit error to trigger immediate cleanup
			pw.CloseWithError(io.ErrClosedPipe)

			log.Printf("🔄 H2 stream #%d bridge flushed and closed for agent %s (remaining active: %d)", streamID, a.config.AgentID, atomic.LoadInt64(&activeStreams)-1)
		})
	}
	defer closeDone()

	var wg sync.WaitGroup

	// Start connection monitor to detect TCP close events
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.monitorConnectionHealth(targetConn, done, closeDone)
	}()

	// Server→Target: copy TCP data from HTTP/2 stream to target
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			log.Printf("📥 TCP Server→Target stream ended for agent %s", a.config.AgentID)
		}()

		buffer := make([]byte, 64*1024) // Larger buffer for better throughput

		for {
			select {
			case <-done:
				return
			default:
				n, err := resp.Body.Read(buffer)
				if n > 0 {
					// Data received from server, forward immediately
					if _, writeErr := targetConn.Write(buffer[:n]); writeErr != nil {
						if isConnectionClosed(writeErr) {
							log.Printf("📡 TCP target connection closed gracefully during write - flushing stream")
						} else {
							log.Printf("⚠️ Error writing TCP data to target connection: %v", writeErr)
						}
						closeDone()
						return
					}
				}
				if err != nil {
					if err == io.EOF {
						log.Printf("📡 TCP stream from server ended normally - flushing stream")
						closeDone()
						return
					}
					if isConnectionClosed(err) {
						log.Printf("📡 TCP stream from server closed gracefully - flushing stream")
						closeDone()
						return
					}

					log.Printf("⚠️ Error reading TCP data from server stream: %v", err)
					closeDone()
					return
				}
			}
		}
	}()

	// Target→Server: copy TCP data from target back to HTTP/2 stream
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			log.Printf("📤 TCP Target→Server stream ended for agent %s", a.config.AgentID)
		}()

		buffer := make([]byte, 64*1024) // Larger buffer for better throughput
		lastActivity := time.Now()
		idleTimeout := 2 * time.Second // Ultra-short timeout for instant HTTP cleanup
		hasReceivedData := false

		for {
			select {
			case <-done:
				return
			default:
				// Use a short read timeout to detect both data and inactivity
				if err := targetConn.SetReadDeadline(time.Now().Add(1 * time.Second)); err != nil {
					log.Printf("⚠️ Error setting read deadline on target: %v", err)
					closeDone()
					return
				}

				n, err := targetConn.Read(buffer)
				if n > 0 {
					// Data received, update activity and forward immediately
					lastActivity = time.Now()
					hasReceivedData = true
					if _, writeErr := pw.Write(buffer[:n]); writeErr != nil {
						if isConnectionClosed(writeErr) {
							log.Printf("📡 TCP stream to server closed gracefully during write")
						} else {
							log.Printf("⚠️ Error writing TCP data to server stream: %v", writeErr)
						}
						closeDone()
						return
					}
				}

				if err != nil {
					if err == io.EOF {
						log.Printf("📡 TCP target connection closed normally - flushing stream")
						closeDone()
						return
					}
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						// Only apply idle timeout after we've received some data
						// This prevents premature closure during initial connection
						if hasReceivedData && time.Since(lastActivity) > idleTimeout {
							log.Printf("📡 TCP target connection idle for %v - closing stream", idleTimeout)
							closeDone()
							return
						}
						// Short timeout is expected, continue polling
						continue
					}
					if isConnectionClosed(err) {
						log.Printf("📡 TCP target connection closed gracefully - flushing stream")
						closeDone()
						return
					}
					log.Printf("⚠️ Error reading TCP data from target connection: %v", err)
					closeDone()
					return
				}
			}
		}
	}()

	// Wait for both directions to finish
	wg.Wait()
	return nil
}

// monitorConnectionHealth actively monitors the TCP connection for closure
func (a *Agent) monitorConnectionHealth(conn net.Conn, done <-chan struct{}, closeDone func()) {
	// Use TCP-specific keepalive monitoring if available
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		a.monitorTCPConnection(tcpConn, done, closeDone)
	} else {
		// Fallback for non-TCP connections
		a.monitorGenericConnection(conn, done, closeDone)
	}
}

// monitorTCPConnection monitors a TCP connection using keepalive probes
func (a *Agent) monitorTCPConnection(conn *net.TCPConn, done <-chan struct{}, closeDone func()) {
	// Set aggressive keepalive settings for faster detection
	conn.SetKeepAlive(true)
	conn.SetKeepAlivePeriod(30 * time.Second) // Less frequent keepalives for lower CPU

	ticker := time.NewTicker(10 * time.Second) // Reduced frequency for lower CPU
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			// Try a zero-byte write to test connection
			if err := conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
				log.Printf("📡 TCP health check: connection is closed")
				closeDone()
				return
			}

			// Reset deadline
			conn.SetWriteDeadline(time.Time{})
		}
	}
}

// monitorGenericConnection monitors a generic connection
func (a *Agent) monitorGenericConnection(conn net.Conn, done <-chan struct{}, closeDone func()) {
	ticker := time.NewTicker(15 * time.Second) // Much less frequent for lower CPU
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			// Simple deadline test
			if err := conn.SetReadDeadline(time.Now().Add(1 * time.Millisecond)); err != nil {
				log.Printf("📡 Generic health check: connection is closed")
				closeDone()
				return
			}
			conn.SetReadDeadline(time.Time{})
		}
	}
}

// isConnectionClosed checks if an error indicates a closed connection
func isConnectionClosed(err error) bool {
	if err == nil {
		return false
	}
	es := err.Error()
	return es == "EOF" ||
		strings.Contains(es, "use of closed network connection") ||
		strings.Contains(es, "connection reset by peer") ||
		strings.Contains(es, "broken pipe") ||
		strings.Contains(es, "connection refused") ||
		strings.Contains(es, "io: read/write on closed pipe")
}

// Stop stops the agent
func (a *Agent) Stop() {
	close(a.done)
}
