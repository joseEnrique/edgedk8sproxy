package main

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

// Pools for compression buffers/writers to reduce allocations
var agentGzipWriterPool = sync.Pool{New: func() interface{} {
	zw, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return zw
}}

var agentBytesBufferPool = sync.Pool{New: func() interface{} { return new(bytes.Buffer) }}

// Compression threshold in bytes; payloads smaller than this are sent uncompressed
var compressThreshold = 32768

// Adaptive batching thresholds (configurable via env)
var agentBatchMaxBytes = 64 * 1024
var agentBatchMaxFrames = 8
var agentBatchMaxDelay = 1 * time.Millisecond

func init() {
	if v := os.Getenv("COMPRESS_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			compressThreshold = n
		}
	}
	if v := os.Getenv("AGENT_BATCH_MAX_BYTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			agentBatchMaxBytes = n
		}
	}
	if v := os.Getenv("AGENT_BATCH_MAX_FRAMES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			agentBatchMaxFrames = n
		}
	}
	if v := os.Getenv("AGENT_BATCH_MAX_DELAY_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			agentBatchMaxDelay = time.Duration(n) * time.Millisecond
		}
	}
}

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
		ReadIdleTimeout:  60 * time.Second,
		PingTimeout:      5 * time.Second,
		WriteByteTimeout: 2 * time.Second,
		MaxReadFrameSize: 32768,
		IdleConnTimeout:  60 * time.Second,
	}
	httpClient := &http.Client{Transport: h2tr}

	baseURL := fmt.Sprintf("https://%s:%s", a.config.ServerHost, a.config.H2Port)
	log.Printf("🔐 H2 mTLS enabled. Ready to tunnel TCP traffic via %s/tunnel", baseURL)

	// Start ALWAYS-MULTIPLEXED single stream
	log.Printf("🚀 Starting ALWAYS-MULTIPLEXED single stream tunnel")
	go a.runAlwaysMultiplexed(httpClient, baseURL)

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
			log.Printf("🚀 Attempting connection to server (failures: %d)", consecutiveFailures)
			err := a.createUltraPersistentStream(httpClient, baseURL)
			if err != nil {
				consecutiveFailures++
				retryDelay := a.calculateRetryDelay(consecutiveFailures, err)
				if strings.Contains(err.Error(), "EOF") {
					log.Printf("❌ Connection failed with EOF (attempt %d): %v", consecutiveFailures, err)
					log.Printf("🔍 This likely means the server closed the connection")
				} else {
					log.Printf("❌ Connection failed (attempt %d): %v", consecutiveFailures, err)
				}
				log.Printf("🔄 Auto-reconnecting in %v...", retryDelay)
				if consecutiveFailures%10 == 0 {
					log.Printf("🔧 Recreating HTTP client after %d failures", consecutiveFailures)
					httpClient = a.createOptimizedHTTPClient()
				}
				select {
				case <-time.After(retryDelay):
					continue
				case <-a.done:
					return
				}
			}
			if consecutiveFailures > 0 {
				log.Printf("✅ Reconnected successfully after %d failures", consecutiveFailures)
			}
			consecutiveFailures = 0
			log.Printf("🔄 Stream ended normally, reconnecting immediately...")
		}
	}
}

// calculateRetryDelay calcula el tiempo de espera basado en el tipo de error y número de fallos
func (a *Agent) calculateRetryDelay(failures int, err error) time.Duration {
	errStr := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "connect: connection refused"):
		switch {
		case failures <= 3:
			return 1 * time.Second
		case failures <= 10:
			return 5 * time.Second
		default:
			return 15 * time.Second
		}
	case strings.Contains(errStr, "server closed connection") || strings.Contains(errStr, "unexpected eof") || strings.Contains(errStr, "connection closed"):
		switch {
		case failures <= 5:
			return 2 * time.Second
		case failures <= 15:
			return 5 * time.Second
		default:
			return 10 * time.Second
		}
	case strings.Contains(errStr, "no such host") || strings.Contains(errStr, "dns"):
		switch {
		case failures <= 2:
			return 5 * time.Second
		default:
			return 30 * time.Second
		}
	case strings.Contains(errStr, "timeout") || strings.Contains(errStr, "i/o timeout"):
		switch {
		case failures <= 5:
			return 2 * time.Second
		case failures <= 15:
			return 10 * time.Second
		default:
			return 30 * time.Second
		}
	case strings.Contains(errStr, "network is unreachable") || strings.Contains(errStr, "no route to host"):
		switch {
		case failures <= 3:
			return 10 * time.Second
		default:
			return 60 * time.Second
		}
	case strings.Contains(errStr, "certificate") || strings.Contains(errStr, "tls"):
		switch {
		case failures <= 2:
			return 5 * time.Second
		case failures <= 5:
			return 30 * time.Second
		default:
			return 120 * time.Second
		}
	default:
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
	cert, err := tls.LoadX509KeyPair(a.config.TLSCertFile, a.config.TLSKeyFile)
	if err != nil {
		log.Printf("⚠️ Error loading client cert during reconnect: %v", err)
		return nil
	}
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
	transport := &http2.Transport{
		TLSClientConfig:    tlsCfg,
		AllowHTTP:          false,
		DisableCompression: false,
		DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
			conn, err := tls.DialWithDialer(dialer, network, addr, cfg)
			if err != nil {
				return nil, err
			}
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
	return &http.Client{Transport: transport, Timeout: 120 * time.Second}
}

// createUltraPersistentStream creates THE ONLY multiplexed stream with maximum persistence
func (a *Agent) createUltraPersistentStream(httpClient *http.Client, baseURL string) error {
	pr, pw := io.Pipe()
	req, err := http.NewRequest("POST", baseURL+"/tunnel", pr)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-Agent-ID", a.config.AgentID)
	req.Header.Set("X-Tunnel-Mode", "multiplexed")
	req.Header.Set("X-Ultra-Persistent", "true")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := httpClient.Do(req)
	if err != nil {
		_ = pw.Close()
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = pw.Close()
		return fmt.Errorf("server rejected connection (status %d)", resp.StatusCode)
	}

	connManager := &AgentConnectionManager{connections: make(map[uint32]*AgentTCPConnection), mutex: sync.RWMutex{}, nextConnID: 1, outgoing: make(chan OutgoingChunk, 1024)}
	done := make(chan struct{})
	connectionLost := make(chan error, 1)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() { defer wg.Done(); a.handleIncomingFrames(resp.Body, connManager, done, connectionLost) }()
	wg.Add(1)
	go func() { defer wg.Done(); a.handleOutgoingFrames(pw, connManager, done, connectionLost) }()
	wg.Add(1)
	go func() { defer wg.Done(); a.ultraKeepAlive(pw, done) }()

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

// ultraKeepAlive mantiene el stream activo enviando heartbeats cada 30 segundos
func (a *Agent) ultraKeepAlive(writer io.Writer, done <-chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	heartbeat := make([]byte, 8)
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if _, err := writer.Write(heartbeat); err != nil {
				return
			}
		}
	}
}

// AgentConnectionManager manages multiple TCP connections within single stream
type AgentConnectionManager struct {
	connections map[uint32]*AgentTCPConnection
	mutex       sync.RWMutex
	nextConnID  uint32
	outgoing    chan OutgoingChunk
}

// OutgoingChunk represents a payload destined to the server for a given connID
type OutgoingChunk struct {
	connID uint32
	data   []byte
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
			header := make([]byte, 8)
			if _, err := io.ReadFull(reader, header); err != nil {
				select {
				case connectionLost <- err:
				default:
				}
				return
			}
			connID := uint32(header[0])<<24 | uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
			length := uint32(header[4])<<24 | uint32(header[5])<<16 | uint32(header[6])<<8 | uint32(header[7])
			if length == 0 {
				a.handleConnectionClose(connManager, connID)
				continue
			}
			if length > 256*1024 {
				log.Printf("⚠️ Frame too large: %d bytes (limit 256KB)", length)
				return
			}
			data := getBuffer(int(length))
			if _, err := io.ReadFull(reader, data[:length]); err != nil {
				putBuffer(data)
				return
			}
			if connID == 0 {
				log.Printf("⚠️ Received frame with connID 0 from server")
				putBuffer(data)
			} else {
				plain := decompressData(data[:length])
				putBuffer(data)
				a.routeToConnection(connManager, connID, plain)
			}
		}
	}
}

// handleOutgoingFrames sends data from TCP connections as frames to server
func (a *Agent) handleOutgoingFrames(writer io.Writer, connManager *AgentConnectionManager, done <-chan struct{}, connectionLost chan<- error) {
	batchBuf := agentBytesBufferPool.Get().(*bytes.Buffer)
	batchBuf.Reset()
	defer agentBytesBufferPool.Put(batchBuf)

	framesInBatch := 0
	bytesInBatch := 0
	var flushTimer *time.Timer
	var flushC <-chan time.Time

	flush := func() error {
		if bytesInBatch == 0 {
			return nil
		}
		if _, err := writer.Write(batchBuf.Bytes()); err != nil {
			return err
		}
		batchBuf.Reset()
		framesInBatch = 0
		bytesInBatch = 0
		if flushTimer != nil {
			if !flushTimer.Stop() {
				select {
				case <-flushTimer.C:
				default:
				}
			}
			flushC = nil
		}
		return nil
	}

	for {
		select {
		case <-done:
			_ = flush()
			return
		case oc := <-connManager.outgoing:
			payload := compressData(oc.data)
			frameSize := 8 + len(payload)

			header := getBuffer(8)
			header[0] = byte(oc.connID >> 24)
			header[1] = byte(oc.connID >> 16)
			header[2] = byte(oc.connID >> 8)
			header[3] = byte(oc.connID)
			dataLen := uint32(len(payload))
			header[4] = byte(dataLen >> 24)
			header[5] = byte(dataLen >> 16)
			header[6] = byte(dataLen >> 8)
			header[7] = byte(dataLen)
			_, _ = batchBuf.Write(header[:8])
			putBuffer(header)
			_, _ = batchBuf.Write(payload)
			putBuffer(oc.data)

			framesInBatch++
			bytesInBatch += frameSize

			if framesInBatch >= agentBatchMaxFrames || bytesInBatch >= agentBatchMaxBytes {
				if err := flush(); err != nil {
					select {
					case connectionLost <- err:
					default:
					}
					return
				}
				continue
			}
			if flushC == nil {
				flushTimer = time.NewTimer(agentBatchMaxDelay)
				flushC = flushTimer.C
			}
		case <-flushC:
			if err := flush(); err != nil {
				select {
				case connectionLost <- err:
				default:
				}
				return
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
		tcpConn = a.createNewConnection(connManager, connID)
		if tcpConn == nil {
			return
		}
	}
	if _, err := tcpConn.Conn.Write(data); err != nil {
		log.Printf("⚠️ Error writing to target conn %d: %v", connID, err)
		a.closeConnection(connManager, connID)
	}
}

// createNewConnection creates a new TCP connection to target
func (a *Agent) createNewConnection(connManager *AgentConnectionManager, connID uint32) *AgentTCPConnection {
	targetConn, err := net.Dial("tcp", net.JoinHostPort(a.config.ForwardHost, fmt.Sprintf("%d", a.config.ForwardPort)))
	if err != nil {
		log.Printf("⚠️ Failed to connect to target for conn %d: %v", connID, err)
		return nil
	}
	tcpConn := &AgentTCPConnection{ID: connID, Conn: targetConn, ToServer: make(chan []byte, 100), fromTarget: make(chan []byte, 100), done: make(chan struct{})}
	connManager.mutex.Lock()
	connManager.connections[connID] = tcpConn
	connManager.mutex.Unlock()
	log.Printf("🔗 New multiplexed connection %d created", connID)
	go a.readFromTarget(connManager, tcpConn)
	return tcpConn
}

func (a *Agent) handleConnectionClose(connManager *AgentConnectionManager, connID uint32) {
	a.closeConnection(connManager, connID)
}

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

func (a *Agent) readFromTarget(connManager *AgentConnectionManager, tcpConn *AgentTCPConnection) {
	buffer := make([]byte, 64*1024)
	for {
		select {
		case <-tcpConn.done:
			return
		default:
			tcpConn.Conn.SetReadDeadline(time.Now().Add(5 * time.Millisecond))
			n, err := tcpConn.Conn.Read(buffer)
			if n > 0 {
				chunk := getBuffer(n)
				copy(chunk, buffer[:n])
				select {
				case connManager.outgoing <- OutgoingChunk{connID: tcpConn.ID, data: chunk}:
				case <-tcpConn.done:
					putBuffer(chunk)
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

// Helpers for mandatory compression in tunnel frames
func compressData(data []byte) []byte {
	// Skip compression for small payloads
	if len(data) < compressThreshold {
		return data
	}
	buf := agentBytesBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	zw := agentGzipWriterPool.Get().(*gzip.Writer)
	zw.Reset(buf)
	_, _ = zw.Write(data)
	_ = zw.Close()
	comp := buf.Bytes()
	out := make([]byte, 0, len(comp)+2)
	out = append(out, 'G', 'Z')
	out = append(out, comp...)
	// Return resources to pools, but avoid retaining huge buffers
	capLimit := 64 * 1024
	if buf.Cap() <= capLimit {
		buf.Reset()
		agentBytesBufferPool.Put(buf)
	} // else: let GC reclaim a too-large buffer
	zw.Reset(io.Discard)
	agentGzipWriterPool.Put(zw)
	return out
}

func decompressData(data []byte) []byte {
	if len(data) >= 2 && data[0] == 'G' && data[1] == 'Z' {
		r, err := gzip.NewReader(bytes.NewReader(data[2:]))
		if err != nil {
			return data
		}
		defer r.Close()
		plain, err := io.ReadAll(r)
		if err != nil {
			return data
		}
		return plain
	}
	return data
}
