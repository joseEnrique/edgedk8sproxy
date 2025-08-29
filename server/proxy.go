package main

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
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

// StartTCPTunnelServer starts the TCP tunnel server for client connections
func (s *Server) StartTCPTunnelServer() {
	listener, err := net.Listen("tcp", ":"+s.config.TCPPort)
	if err != nil {
		log.Printf("❌ Failed to start TCP tunnel server on port %s: %v", s.config.TCPPort, err)
		return
	}
	defer listener.Close()

	log.Printf("🔌 TCP tunnel server listening on port %s", s.config.TCPPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("❌ Failed to accept TCP connection: %v", err)
			continue
		}

		log.Printf("🔌 TCP connection accepted from %s", conn.RemoteAddr())

		go s.handleTCPConnection(conn)
	}
}

// StartH2TLSServer starts an HTTP/2 TLS server requiring client certs (mTLS)
func (s *Server) StartH2TLSServer() {
	if s.config.TLSCertFile == "" || s.config.TLSKeyFile == "" || s.config.TLSClientCA == "" {
		log.Printf("❌ H2 TLS config missing (TLS_CERT_FILE/TLS_KEY_FILE/TLS_CLIENT_CA_FILE)")
		return
	}

	// Load server cert
	cert, err := tls.LoadX509KeyPair(s.config.TLSCertFile, s.config.TLSKeyFile)
	if err != nil {
		log.Printf("❌ Failed to load server cert/key: %v", err)
		return
	}

	// Load client CA
	caCert, err := os.ReadFile(s.config.TLSClientCA)
	if err != nil {
		log.Printf("❌ Failed to read client CA: %v", err)
		return
	}
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caCert)

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2", "http/1.1"},
	}

	srv := &http.Server{
		Addr:      ":" + s.config.H2Port,
		TLSConfig: tlsCfg,
		Handler:   http.HandlerFunc(s.handleH2Tunnel),
	}
	// Configure HTTP/2 parameters
	http2.ConfigureServer(srv, &http2.Server{
		MaxConcurrentStreams: 1024,
		ReadIdleTimeout:      30 * time.Second,
		PingTimeout:          15 * time.Second,
	})

	log.Printf("🔐 Starting HTTP/2 mTLS tunnel server on %s", s.config.H2Port)
	if err := srv.ListenAndServeTLS("", ""); err != nil {
		log.Printf("❌ H2 TLS server failed: %v", err)
	}
}

// handleH2Tunnel handles HTTP/2 streams as tunnel carriers
func (s *Server) handleH2Tunnel(w http.ResponseWriter, r *http.Request) {
	// Expect path /tunnel and method POST for streams
	if r.Method != http.MethodPost || r.URL.Path != "/tunnel" {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
		return
	}
	agentID := r.Header.Get("X-Agent-ID")
	if agentID == "" {
		agentID = "agent1"
	}

	// ALWAYS use multiplexed mode
	s.handleMultiplexedTunnel(w, r, agentID)
}

func (s *Server) forwardUserToAgentH2(userConn net.Conn, agent *Agent, initialData []byte) {
	defer userConn.Close()

	// Wait for an available stream with timeout
	var h2 *H2Stream
	select {
	case h2 = <-agent.h2Streams:
		log.Printf("✅ Got H2 stream %s for user %s", h2.id, userConn.RemoteAddr())
	case <-time.After(10 * time.Second):
		log.Printf("❌ Timeout waiting for H2 stream for user %s", userConn.RemoteAddr())
		return
	}

	done := make(chan struct{})
	var once sync.Once
	closeDone := func() {
		once.Do(func() {
			close(done)
			h2.Close()
		})
	}
	defer closeDone()

	// Send initial data first if available
	if len(initialData) > 0 {
		log.Printf("📤 Sending initial data (%d bytes) to agent via H2 stream %s", len(initialData), h2.id)
		if _, err := h2.writeStream.Write(initialData); err != nil {
			log.Printf("❌ Failed to write initial data to H2 stream: %v", err)
			return
		}
	}

	var wg sync.WaitGroup

	// agent->user: read from h2.readStream (agent sends data via HTTP request body)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			log.Printf("📥 Agent->User stream ended for %s", userConn.RemoteAddr())
		}()

		buffer := make([]byte, 32*1024)
		for {
			select {
			case <-done:
				return
			default:
				n, err := h2.readStream.Read(buffer)
				if n > 0 {
					if _, writeErr := userConn.Write(buffer[:n]); writeErr != nil {
						log.Printf("⚠️ Error writing to user connection: %v", writeErr)
						closeDone()
						return
					}
				}
				if err != nil {
					if err == io.EOF {
						closeDone()
						return
					}
					log.Printf("⚠️ Error reading from H2 readStream: %v", err)
					closeDone()
					return
				}
			}
		}
	}()

	// user->agent: write to h2.writeStream (server sends data via HTTP response body)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			log.Printf("📤 User->Agent stream ended for %s", userConn.RemoteAddr())
		}()

		buffer := make([]byte, 32*1024)
		for {
			select {
			case <-done:
				return
			default:
				// Set read timeout for user connection
				userConn.SetReadDeadline(time.Now().Add(30 * time.Second))

				n, err := userConn.Read(buffer)
				if n > 0 {
					if _, writeErr := h2.writeStream.Write(buffer[:n]); writeErr != nil {
						log.Printf("⚠️ Error writing to H2 writeStream: %v", writeErr)
						closeDone()
						return
					}
				}
				if err != nil {
					if err == io.EOF {
						closeDone()
						return
					}
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						// Timeout is normal, continue
						continue
					}
					log.Printf("⚠️ Error reading from user connection: %v", err)
					closeDone()
					return
				}
			}
		}
	}()

	// Wait for either direction to finish
	wg.Wait()
	log.Printf("🔄 H2 bridge completed for user %s via stream %s", userConn.RemoteAddr(), h2.id)
}

// handleTCPConnection handles incoming TCP connections and determines if they are agents or users
func (s *Server) handleTCPConnection(conn net.Conn) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(30 * time.Second)
		_ = tcpConn.SetLinger(0)
	}

	conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))

	buffer := make([]byte, 1024)
	n, err := conn.Read(buffer)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			log.Printf("📡 Fast classification: treating as user traffic")
			conn.SetReadDeadline(time.Time{})
			s.handleUserTraffic(conn, buffer[:n])
			return
		}
		log.Printf("❌ Failed to read from connection: %v", err)
		return
	}
	conn.SetReadDeadline(time.Time{})

	data := buffer[:n]
	if n > 0 && n < 50 && !strings.Contains(string(data), "SSH-") && !strings.Contains(string(data), "HTTP") {
		log.Printf("🔌 Agent connection detected")
		s.handleTCPAgentConnection(conn, string(data))
		return
	}
	log.Printf("📡 User traffic detected, forwarding to agent")
	s.handleUserTraffic(conn, data)
}

// handleUserTraffic handles user traffic (SSH, HTTP, kubectl, etc.) and forwards it to an agent
func (s *Server) handleUserTraffic(conn net.Conn, initialData []byte) {
	log.Printf("📡 Handling user traffic (%d bytes)", len(initialData))
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
	}

	agent := s.selectAgentForInitialData(initialData)
	if agent == nil {
		log.Printf("❌ No matching agent for initial data; rejecting")
		_ = conn.Close()
		return
	}

	log.Printf("✅ Routing user traffic to MULTIPLEXED agent: %s", agent.ID)

	// ALWAYS use multiplexed tunnel
	go s.forwardUserToMuxAgent(conn, agent, initialData)
}

// forwardUserToMuxAgent forwards user connection via multiplexed tunnel
func (s *Server) forwardUserToMuxAgent(userConn net.Conn, agent *Agent, initialData []byte) {
	defer userConn.Close()

	// Check if agent has THE ONLY multiplexed manager
	if agent.multiplexedManager == nil {
		log.Printf("❌ Agent %s has no multiplexed manager", agent.ID)
		return
	}

	mux := agent.multiplexedManager

	// Create new multiplexed connection
	connID := s.createMuxConnection(mux, userConn)

	// Send initial data as first frame if any
	if len(initialData) > 0 {
		payload := compressData(initialData)
		// Send frame to agent: [conn_id(4)] + [length(4)] + [data]
		frame := make([]byte, 8+len(payload))

		// Write connection ID (big-endian)
		frame[0] = byte(connID >> 24)
		frame[1] = byte(connID >> 16)
		frame[2] = byte(connID >> 8)
		frame[3] = byte(connID)

		// Write data length (big-endian)
		dataLen := uint32(len(payload))
		frame[4] = byte(dataLen >> 24)
		frame[5] = byte(dataLen >> 16)
		frame[6] = byte(dataLen >> 8)
		frame[7] = byte(dataLen)

		// Copy data
		copy(frame[8:], payload)

		// Send frame to agent via non-blocking queue
		select {
		case mux.writeQueue <- frame:
			// Frame queued successfully
		case <-time.After(100 * time.Millisecond):
			log.Printf("⚠️ Write queue full for initial frame conn %d", connID)
			s.closeMuxConnection(mux, connID)
			return
		case <-mux.done:
			s.closeMuxConnection(mux, connID)
			return
		}
	}

	log.Printf("🔗 User %s connected via multiplexed connection %d", userConn.RemoteAddr(), connID)

	// The readFromUserToAgent goroutine handles the rest of the data transfer
	// Wait for connection to close
	<-mux.done
}

// forwardUserToAgent handles bidirectional forwarding between user connection and agent
func (s *Server) forwardUserToAgent(userConn net.Conn, agent *Agent) {
	defer userConn.Close()

	log.Printf("📡 Starting direct TCP forwarding for agent %s", agent.ID)

	// Create a done channel for this specific user session
	done := make(chan struct{})
	var closeOnce sync.Once
	closeDone := func() {
		closeOnce.Do(func() {
			close(done)
		})
	}

	// Create a response channel for this user connection
	responseChan := make(chan []byte, 100)

	// Register this user connection to receive responses
	agent.tunnelMutex.Lock()
	if agent.userConnections == nil {
		agent.userConnections = make(map[string]chan []byte)
	}
	userID := fmt.Sprintf("%s-%d", userConn.RemoteAddr().String(), time.Now().UnixNano())
	agent.userConnections[userID] = responseChan
	agent.tunnelMutex.Unlock()

	// Clean up when this function exits
	defer func() {
		agent.tunnelMutex.Lock()
		delete(agent.userConnections, userID)
		agent.tunnelMutex.Unlock()
		close(responseChan)
	}()

	// User to Tunnel - only writing, no reading
	go func() {
		defer func() {
			log.Printf("📤 User to Tunnel stream ended")
		}()

		buffer := make([]byte, 32*1024)
		for {
			select {
			case <-done:
				return
			default:
				n, err := userConn.Read(buffer)
				if err != nil {
					if isConnectionClosed(err) {
						log.Printf("📤 User connection closed gracefully")
						closeDone()
						return
					}
					log.Printf("❌ User read error: %v", err)
					closeDone()
					return
				}

				if n > 0 {
					// Write to tunnel with mutex to prevent conflicts
					agent.writerMu.Lock()
					if _, err := agent.TCPConn.Write(buffer[:n]); err != nil {
						agent.writerMu.Unlock()
						if isConnectionClosed(err) {
							log.Printf("📤 Tunnel connection closed during write")
							closeDone()
							return
						}
						log.Printf("❌ Failed to write to tunnel: %v", err)
						closeDone()
						return
					}
					agent.writerMu.Unlock()
				}
			}
		}
	}()

	// Response reader from channel (no reading from tunnel)
	go func() {
		defer func() {
			log.Printf("📥 Response reader ended")
		}()

		for {
			select {
			case <-done:
				return
			case response, ok := <-responseChan:
				if !ok {
					return
				}
				// Write response to user connection
				if _, err := userConn.Write(response); err != nil {
					if isConnectionClosed(err) {
						log.Printf("📥 User connection closed")
						closeDone()
						return
					}
					log.Printf("❌ Failed to write to user: %v", err)
					closeDone()
					return
				}
			}
		}
	}()

	// Wait for this user session to end (tunnel stays open FOREVER)
	<-done
	log.Printf("📡 User session ended for agent %s (tunnel remains persistent)", agent.ID)
}

// isConnectionClosed checks if an error indicates a closed connection
func isConnectionClosed(err error) bool {
	if err == nil {
		return false
	}
	es := err.Error()
	return es == "EOF" || strings.Contains(es, "use of closed network connection") || strings.Contains(es, "connection reset by peer") || strings.Contains(es, "broken pipe")
}

// handleTCPClientConnection handles a TCP connection from a agent
func (s *Server) handleTCPAgentConnection(conn net.Conn, agentID string) {
	log.Printf("🔌 Client %s connected via TCP tunnel", agentID)
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	agent := &Agent{
		ID:              agentID,
		TCPConn:         conn,
		Connected:       time.Now(),
		LastSeen:        time.Now(),
		Requests:        0,
		BytesIn:         0,
		BytesOut:        0,
		userConnections: make(map[string]chan []byte), // Initialize map for user connections
		done:            make(chan bool),              // Initialize done channel
	}

	s.mutex.Lock()
	s.agents[agentID] = agent
	s.mutex.Unlock()

	log.Printf("✅ Created new TCP agent: %s", agentID)

	// Start the central tunnel reader that distributes responses
	go s.tunnelReader(agent)

	// Start TCP tunnel handling
	s.handleTCPTunnel(conn, agentID)
}

// handleTCPTunnel handles the TCP tunnel between server and agent
func (s *Server) handleTCPTunnel(conn net.Conn, agentID string) {
	log.Printf("🔌 Starting PERSISTENT TCP tunnel for agent: %s", agentID)

	// Get agent reference
	s.mutex.RLock()
	agent, exists := s.agents[agentID]
	s.mutex.RUnlock()

	if !exists {
		log.Printf("❌ Client %s not found during tunnel handling", agentID)
		return
	}

	// Mark tunnel as active
	agent.tunnelMutex.Lock()
	agent.isActive = true
	agent.tunnelMutex.Unlock()

	log.Printf("✅ TCP tunnel established successfully for agent: %s", agentID)

	// PERSISTENT TUNNEL: This connection NEVER closes
	// Only close if the agent sends FIN TCP or server shuts down
	// But we don't read from it here - tunnelReader handles all data

	// Just wait for the connection to be closed by agent
	// We can detect this by checking if the connection is still alive
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Check if connection is still alive without reading data
			if err := conn.SetReadDeadline(time.Now().Add(1 * time.Millisecond)); err != nil {
				log.Printf("🔌 Tunnel connection lost for agent %s", agentID)
				goto cleanup
			}

			// Try to read 1 byte to check if connection is alive
			buf := make([]byte, 1)
			if _, err := conn.Read(buf); err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// Timeout is normal, connection is alive
					conn.SetReadDeadline(time.Time{}) // Reset deadline
					continue
				}
				// Connection is dead
				log.Printf("🔌 Tunnel connection lost for agent %s: %v", agentID, err)
				goto cleanup
			}

			// Reset deadline
			conn.SetReadDeadline(time.Time{})

		case <-agent.done:
			log.Printf("🔌 Client %s requested tunnel closure", agentID)
			goto cleanup
		}
	}

cleanup:
	// Clean up when tunnel is closed by agent
	log.Printf("🔌 TCP tunnel ended for agent: %s", agentID)
	agent.tunnelMutex.Lock()
	agent.isActive = false
	agent.tunnelMutex.Unlock()

	s.mutex.Lock()
	delete(s.agents, agentID)
	s.mutex.Unlock()

	conn.Close()
}

// tunnelReader is the central reader that distributes responses to user connections
func (s *Server) tunnelReader(agent *Agent) {
	defer func() {
		log.Printf("🔌 Tunnel reader ended for agent %s", agent.ID)
	}()

	buffer := make([]byte, 32*1024)
	for {
		n, err := agent.TCPConn.Read(buffer)
		if err != nil {
			if isConnectionClosed(err) {
				log.Printf("🔌 Tunnel connection closed")
				return
			}
			// Don't treat timeouts as fatal errors - just continue
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				log.Printf("⚠️ Tunnel read timeout, continuing...")
				continue
			}
			log.Printf("❌ Tunnel read error: %v", err)
			return
		}

		if n > 0 {
			data := buffer[:n]

			// This is raw response data, send to all user connections
			agent.tunnelMutex.RLock()
			for userID, responseQueue := range agent.userConnections {
				select {
				case responseQueue <- data:
					// Response queued successfully
				default:
					// Queue full, drop response
					log.Printf("⚠️ Response queue full for user %s, dropping response", userID)
				}
			}
			agent.tunnelMutex.RUnlock()
		}
	}
}

// HandleRoot handles the root endpoint and routes requests based on subdomain
func (s *Server) HandleRoot(w http.ResponseWriter, r *http.Request) {
	// Extract host
	host := r.Host
	// Try domain suffix mapping first (matches any subdomain)
	if s.config != nil && len(s.config.DomainMap) > 0 {
		lower := strings.ToLower(host)
		// strip port if present
		if colon := strings.IndexByte(lower, ':'); colon != -1 {
			lower = lower[:colon]
		}
		if mapped := s.lookupAgentByDomainSuffix(lower); mapped != nil {
			log.Printf("🌐 Routing via domain map '%s' -> agent '%s'", host, mapped.ID)
			s.forwardHTTPRequestToAgent(w, r, mapped)
			return
		}
	}

	// Fallback: subdomain-based routing
	subdomain := ""
	if idx := strings.Index(host, "."); idx > 0 {
		subdomain = host[:idx]
	}

	// If no subdomain, show status page
	if subdomain == "" {
		s.HandleStatus(w, r)
		return
	}

	// Find agent by subdomain
	agent := s.GetAgentBySubdomain(subdomain)
	if agent == nil {
		http.Error(w, fmt.Sprintf("Client '%s' not found", subdomain), http.StatusNotFound)
		return
	}

	log.Printf("🌐 Routing request for subdomain '%s' to agent '%s'", subdomain, agent.ID)

	// Forward the request to the agent via TCP tunnel
	s.forwardHTTPRequestToAgent(w, r, agent)
}

// GetClientBySubdomain finds a agent by subdomain
func (s *Server) GetAgentBySubdomain(subdomain string) *Agent {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Look for exact match first
	if agent, exists := s.agents[subdomain]; exists {
		return agent
	}

	// If no exact match, look for agents that start with the subdomain
	for agentID, agent := range s.agents {
		if strings.HasPrefix(agentID, subdomain) {
			return agent
		}
	}

	return nil
}

// forwardHTTPRequestToClient forwards HTTP requests to a specific agent via TCP tunnel
func (s *Server) forwardHTTPRequestToAgent(w http.ResponseWriter, r *http.Request, agent *Agent) {
	log.Printf("📡 Forwarding HTTP request to agent %s: %s %s", agent.ID, r.Method, r.URL.Path)

	// Check if agent is active
	agent.tunnelMutex.RLock()
	if !agent.isActive {
		agent.tunnelMutex.RUnlock()
		http.Error(w, "Client not active", http.StatusServiceUnavailable)
		return
	}
	agent.tunnelMutex.RUnlock()

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("❌ Failed to read request body: %v", err)
		http.Error(w, "Failed to read request", http.StatusBadRequest)
		return
	}

	// Create HTTP request message to send via TCP tunnel
	httpMsg := fmt.Sprintf("%s %s HTTP/1.1\r\n", r.Method, r.URL.Path)

	// Add headers
	for key, values := range r.Header {
		for _, value := range values {
			httpMsg += fmt.Sprintf("%s: %s\r\n", key, value)
		}
	}

	// Add body
	httpMsg += "\r\n" + string(body)

	// Send via TCP tunnel to agent
	if _, err := agent.TCPConn.Write([]byte(httpMsg)); err != nil {
		log.Printf("❌ Failed to send HTTP request to agent: %v", err)
		http.Error(w, "Failed to forward request", http.StatusBadGateway)
		return
	}

	// For now, just send a simple response
	// In a real implementation, you'd wait for the agent's response
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf("Request forwarded to agent %s via TCP tunnel", agent.ID)))

	log.Printf("✅ HTTP request forwarded to agent %s", agent.ID)
}

// getActiveConnections returns the number of active user connections for a agent
func (s *Server) getActiveConnections(agent *Agent) int {
	agent.tunnelMutex.RLock()
	defer agent.tunnelMutex.RUnlock()
	return len(agent.userConnections)
}

// getTotalActiveConnections returns the total number of active connections across all agents
func (s *Server) getTotalActiveConnections() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	total := 0
	for _, agent := range s.agents {
		total += s.getActiveConnections(agent)
	}
	return total
}

// HandleListClients returns a list of connected agents
func (s *Server) HandleListAgents(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	agentList := make([]map[string]interface{}, 0, len(s.agents))
	for _, agent := range s.agents {
		activeConnections := s.getActiveConnections(agent)
		agentList = append(agentList, map[string]interface{}{
			"id":                 agent.ID,
			"connected":          agent.Connected,
			"last_seen":          agent.LastSeen,
			"requests":           agent.Requests,
			"bytes_in":           agent.BytesIn,
			"bytes_out":          agent.BytesOut,
			"active":             agent.isActive,
			"active_connections": activeConnections,
			"subdomain":          agent.ID + "." + s.config.Domain, // Add subdomain info
		})
	}
	totalConnections := s.getTotalActiveConnections()
	s.mutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"agents":            agentList,
		"count":             len(agentList),
		"total_connections": totalConnections,
	})
}

// HandleStatus returns the server status page
func (s *Server) HandleStatus(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	agentCount := len(s.agents)
	activeClients := 0
	totalConnections := s.getTotalActiveConnections()

	// Count active agents
	for _, agent := range s.agents {
		if agent.isActive {
			activeClients++
		}
	}

	// Get detailed agent info for the table
	agentDetails := make([]map[string]interface{}, 0, len(s.agents))
	for _, agent := range s.agents {
		activeConnections := s.getActiveConnections(agent)
		agentDetails = append(agentDetails, map[string]interface{}{
			"id":                 agent.ID,
			"active":             agent.isActive,
			"connected":          agent.Connected.Format("15:04:05"),
			"active_connections": activeConnections,
			"subdomain":          agent.ID + "." + s.config.Domain,
		})
	}
	s.mutex.RUnlock()

	html := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>TCP Tunnel Server</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; background: #f5f5f5; }
        .container { max-width: 1200px; margin: 0 auto; background: white; padding: 20px; border-radius: 10px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); }
        .status { background: #d4edda; padding: 15px; border-radius: 5px; margin: 20px 0; border-left: 5px solid #28a745; }
        .connected { color: #28a745; font-weight: bold; }
        .stats { display: flex; flex-wrap: wrap; gap: 20px; margin: 20px 0; }
        .stat-card { background: #f8f9fa; padding: 20px; border-radius: 8px; min-width: 150px; text-align: center; }
        .stat-number { font-size: 2em; font-weight: bold; color: #007bff; }
        .stat-label { color: #6c757d; font-size: 0.9em; }
        .subdomain { background: #e3f2fd; padding: 10px; border-radius: 5px; margin: 10px 0; }
        .agent-table { width: 100%%; border-collapse: collapse; margin: 20px 0; }
        .agent-table th, .agent-table td { padding: 12px; text-align: left; border-bottom: 1px solid #ddd; }
        .agent-table th { background-color: #f8f9fa; font-weight: bold; }
        .status-active { color: #28a745; font-weight: bold; }
        .status-inactive { color: #dc3545; font-weight: bold; }
        .connections-badge { background: #007bff; color: white; padding: 4px 8px; border-radius: 12px; font-size: 0.8em; }
        .agent { margin: 10px 0; padding: 15px; background: #f8f9fa; border-radius: 5px; border-left: 4px solid #007bff; }
    </style>
    <script>
        function refreshPage() {
            location.reload();
        }
        // Auto-refresh every 5 seconds
        setInterval(refreshPage, 5000);
    </script>
</head>
<body>
    <div class="container">
        <h1>🔌 TCP Tunnel Server</h1>
        <div class="status">
            <h2>Status: <span class="connected">Running</span> <small>(Auto-refresh: 5s)</small></h2>
            <p><a href="/agents">View Clients (JSON)</a></p>
        </div>
        
        <div class="stats">
            <div class="stat-card">
                <div class="stat-number">%d</div>
                <div class="stat-label">Total Clients</div>
            </div>
            <div class="stat-card">
                <div class="stat-number">%d</div>
                <div class="stat-label">Active Clients</div>
            </div>
            <div class="stat-card">
                <div class="stat-number">%d</div>
                <div class="stat-label">Active Connections</div>
            </div>
            <div class="stat-card">
                <div class="stat-number">%s</div>
                <div class="stat-label">TCP Port</div>
            </div>
            <div class="stat-card">
                <div class="stat-number">%s</div>
                <div class="stat-label">HTTP Port</div>
            </div>
        </div>
        
        <h3>📊 Connected Clients:</h3>
        <table class="agent-table">
            <thead>
                <tr>
                    <th>Client ID</th>
                    <th>Status</th>
                    <th>Connected At</th>
                    <th>Active Connections</th>
                    <th>Subdomain</th>
                </tr>
            </thead>
            <tbody>`, agentCount, activeClients, totalConnections, s.config.TCPPort, s.config.Port)

	// Add agent rows
	for _, agent := range agentDetails {
		statusClass := "status-inactive"
		statusText := "Inactive"
		if agent["active"].(bool) {
			statusClass = "status-active"
			statusText = "Active"
		}

		connectionsText := ""
		if agent["active_connections"].(int) > 0 {
			connectionsText = fmt.Sprintf(`<span class="connections-badge">%d</span>`, agent["active_connections"].(int))
		} else {
			connectionsText = "0"
		}

		html += fmt.Sprintf(`
                <tr>
                    <td><strong>%s</strong></td>
                    <td><span class="%s">%s</span></td>
                    <td>%s</td>
                    <td>%s</td>
                    <td><code>%s</code></td>
                </tr>`,
			agent["id"].(string),
			statusClass,
			statusText,
			agent["connected"].(string),
			connectionsText,
			agent["subdomain"].(string))
	}

	html += `
            </tbody>
        </table>
        
        <h3>🌐 Subdomain Routing:</h3>
        <div class="subdomain">
            <strong>How to use:</strong><br>
            &bull; <code>agent1.` + s.config.Domain + `</code> &rarr; Routes to agent1<br>
            &bull; <code>agent2.` + s.config.Domain + `</code> &rarr; Routes to agent2<br>
            &bull; <code>` + s.config.Domain + `</code> &rarr; Shows this status page
        </div>
        
        <h3>💡 Example Usage:</h3>
        <div class="agent">
            <strong>SSH to agent1:</strong> <code>ssh -p ` + s.config.TCPPort + ` agent1.` + s.config.Domain + `</code><br>
            <strong>HTTP to agent2:</strong> <code>curl http://agent2.` + s.config.Domain + `:` + s.config.Port + `/api</code><br>
            <strong>Multiple curls:</strong> <code>for i in {1..100}; do curl http://agent1.` + s.config.Domain + `:` + s.config.Port + `/ & done</code>
        </div>
    </div>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := w.Write([]byte(html))
	if err != nil {
		return
	}
}

// extractSubdomain returns the label before the first dot
func extractSubdomain(host string) string {
	if idx := strings.IndexByte(host, '.'); idx > 0 {
		return host[:idx]
	}
	return host
}

// tryExtractHTTPHost tries to extract Host header from an HTTP request
func tryExtractHTTPHost(initial []byte) (string, bool) {
	data := string(initial)
	if !strings.Contains(data, "HTTP/") {
		return "", false
	}
	lower := strings.ToLower(data)
	pos := strings.Index(lower, "\nhost:")
	if pos == -1 {
		if strings.HasPrefix(lower, "host:") {
			pos = 0
		} else {
			return "", false
		}
	}
	line := data[pos:]
	if len(line) > 0 && line[0] == '\n' {
		line = line[1:]
	}
	if nl := strings.IndexByte(line, '\n'); nl != -1 {
		line = line[:nl]
	}
	parts := strings.SplitN(line, ":", 2)
	if len(parts) < 2 {
		return "", false
	}
	hostPort := strings.TrimSpace(parts[1])
	if sp := strings.IndexByte(hostPort, ' '); sp != -1 {
		hostPort = hostPort[:sp]
	}
	if colon := strings.IndexByte(hostPort, ':'); colon != -1 {
		hostPort = hostPort[:colon]
	}
	if hostPort == "" {
		return "", false
	}
	return hostPort, true
}

// tryExtractTLSSNI parses a TLS ClientHello to extract SNI without consuming beyond provided bytes
func tryExtractTLSSNI(b []byte) (string, bool) {
	if len(b) < 5 {
		return "", false
	}
	// TLS record header
	if b[0] != 0x16 { // Handshake
		return "", false
	}
	// version b[1:3], length b[3:5]
	recLen := int(b[3])<<8 | int(b[4])
	if 5+recLen > len(b) {
		// allow partial
		recLen = len(b) - 5
	}
	p := 5
	if p+4 > 5+recLen {
		return "", false
	}
	// Handshake: type, len(3)
	if b[p] != 0x01 { // ClientHello
		return "", false
	}
	hLen := int(b[p+1])<<16 | int(b[p+2])<<8 | int(b[p+3])
	p += 4
	end := p + hLen
	if end > len(b) {
		end = len(b)
	}
	// agent_version(2) + random(32)
	p += 2 + 32
	if p >= end || p+1 > end {
		return "", false
	}
	// session id
	sidLen := int(b[p])
	p += 1 + sidLen
	if p+2 > end {
		return "", false
	}
	// cipher suites
	csLen := int(b[p])<<8 | int(b[p+1])
	p += 2 + csLen
	if p >= end || p+1 > end {
		return "", false
	}
	// compression methods
	cmLen := int(b[p])
	p += 1 + cmLen
	if p+2 > end {
		return "", false
	}
	// extensions
	extLen := int(b[p])<<8 | int(b[p+1])
	p += 2
	extEnd := p + extLen
	if extEnd > end {
		extEnd = end
	}
	for p+4 <= extEnd {
		extType := int(b[p])<<8 | int(b[p+1])
		extL := int(b[p+2])<<8 | int(b[p+3])
		p += 4
		if p+extL > extEnd {
			break
		}
		if extType == 0x0000 { // server_name
			q := p
			if q+2 > p+extL {
				break
			}
			listLen := int(b[q])<<8 | int(b[q+1])
			q += 2
			listEnd := q + listLen
			if listEnd > p+extL {
				listEnd = p + extL
			}
			for q+3 <= listEnd {
				nType := b[q]
				nLen := int(b[q+1])<<8 | int(b[q+2])
				q += 3
				if nType != 0x00 {
					break
				}
				if q+nLen > listEnd {
					break
				}
				host := string(b[q : q+nLen])
				return host, true
			}
		}
		p += extL
	}
	return "", false
}

// selectClientForInitialData chooses a agent based on the initial bytes (HTTP Host or TLS SNI). If none found and exactly 1 agent exists, returns it; otherwise nil.
func (s *Server) selectAgentForInitialData(initial []byte) *Agent {
	nameAttempted := false
	// Try HTTP Host
	if host, ok := tryExtractHTTPHost(initial); ok {
		nameAttempted = true
		// Domain map first
		if s.config != nil && len(s.config.DomainMap) > 0 {
			if mapped := s.lookupAgentByDomainSuffix(strings.ToLower(host)); mapped != nil {
				return mapped
			}
		}
		sub := extractSubdomain(host)
		log.Printf("🔍 Extracted host: %s, subdomain: %s", host, sub)
		s.mutex.RLock()
		c := s.agents[sub]
		log.Printf("🔍 Available agents: %v", func() []string {
			var keys []string
			for k := range s.agents {
				keys = append(keys, k)
			}
			return keys
		}())
		s.mutex.RUnlock()
		if c != nil {
			log.Printf("✅ Found agent: %s", c.ID)
			return c
		}
		log.Printf("❌ No agent found for subdomain: %s", sub)
	}
	// Try TLS SNI
	if host, ok := tryExtractTLSSNI(initial); ok {
		nameAttempted = true
		// Domain map first
		if s.config != nil && len(s.config.DomainMap) > 0 {
			if mapped := s.lookupAgentByDomainSuffix(strings.ToLower(host)); mapped != nil {
				return mapped
			}
		}
		sub := extractSubdomain(host)
		s.mutex.RLock()
		c := s.agents[sub]
		s.mutex.RUnlock()
		if c != nil {
			return c
		}
	}
	// If a name was presented but didn't match, do not fallback
	if nameAttempted {
		log.Printf("❌ Name was attempted but no agent found, not falling back")
		return nil
	}
	// Fallback only if a single agent exists and no name was presented
	s.mutex.RLock()
	log.Printf("🔍 No name attempted, checking fallback. Found %d agents", len(s.agents))
	if len(s.agents) == 1 {
		for _, c := range s.agents {
			log.Printf("✅ Using single available agent as fallback: %s", c.ID)
			s.mutex.RUnlock()
			return c
		}
	}
	s.mutex.RUnlock()
	log.Printf("❌ No suitable agent found for fallback")
	return nil
}

// handleMultiplexedTunnel handles THE ONLY multiplexed stream for the agent
func (s *Server) handleMultiplexedTunnel(w http.ResponseWriter, r *http.Request, agentID string) {
	ultraPersistent := r.Header.Get("X-Ultra-Persistent")
	log.Printf("🔗 Starting THE ONLY ultra-persistent multiplexed tunnel for agent %s (ultra-persistent: %s)", agentID, ultraPersistent)

	// Set headers for streaming
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	// Flush headers immediately
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Create multiplexed connection manager
	muxManager := &MultiplexedManager{
		agentID:     agentID,
		toAgent:     w,
		fromAgent:   r.Body,
		connections: make(map[uint32]*ServerTCPConnection),
		mutex:       sync.RWMutex{},
		nextConnID:  1,
		done:        make(chan struct{}),
		writeQueue:  make(chan []byte, 1000), // Buffered for high concurrency
	}

	// Register this agent as using multiplexed mode
	s.mutex.Lock()
	agent, ok := s.agents[agentID]
	if !ok {
		agent = &Agent{
			ID:                 agentID,
			Connected:          time.Now(),
			LastSeen:           time.Now(),
			isActive:           true,
			userConnections:    make(map[string]chan []byte),
			h2Streams:          make(chan *H2Stream, 32),
			done:               make(chan bool),
			multiplexedManager: muxManager,
		}
		s.agents[agentID] = agent
	} else {
		agent.isActive = true
		agent.LastSeen = time.Now()
		agent.multiplexedManager = muxManager
	}
	s.mutex.Unlock()

	var wg sync.WaitGroup

	// Start frame handler for incoming data from agent
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.handleIncomingMuxFrames(muxManager)
	}()

	// Start non-blocking write worker
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.handleWriteQueue(muxManager)
	}()

	// Start TCP listener redirect - route new connections to multiplexed tunnel
	log.Printf("✅ Multiplexed tunnel ready for agent %s", agentID)

	// Wait for context cancellation or connection close
	<-r.Context().Done()
	close(muxManager.done)
	close(muxManager.writeQueue) // Close write queue to stop worker
	wg.Wait()

	// CRITICAL: Clean up all TCP connections and manager when stream closes
	// First, close all TCP connections WITHOUT holding server mutex to avoid deadlock
	log.Printf("🧹 Cleaning up multiplexed manager for agent %s", agentID)
	muxManager.mutex.Lock()
	connectionsToClose := make([]*ServerTCPConnection, 0, len(muxManager.connections))
	for connID, tcpConn := range muxManager.connections {
		log.Printf("🔌 Closing TCP connection %d for agent %s", connID, agentID)
		connectionsToClose = append(connectionsToClose, tcpConn)
	}
	// Clear connections map
	muxManager.connections = make(map[uint32]*ServerTCPConnection)
	muxManager.mutex.Unlock()

	// Close connections outside of any lock
	for _, tcpConn := range connectionsToClose {
		if tcpConn.UserConn != nil {
			tcpConn.UserConn.Close()
		}
		close(tcpConn.done)
	}

	// Now safely update agent
	s.mutex.Lock()
	if agent, ok := s.agents[agentID]; ok {
		if agent.multiplexedManager == muxManager {
			agent.multiplexedManager = nil
		}
	}
	s.mutex.Unlock()

	log.Printf("🔌 Multiplexed tunnel closed for agent %s", agentID)
}

// handleWriteQueue processes writes to agent in background to avoid blocking
func (s *Server) handleWriteQueue(mux *MultiplexedManager) {
	log.Printf("🚀 Starting non-blocking write queue for agent %s", mux.agentID)

	for {
		select {
		case <-mux.done:
			log.Printf("💀 Write queue stopping for agent %s", mux.agentID)
			return
		case frame := <-mux.writeQueue:
			// Write frame with timeout
			done := make(chan error, 1)
			go func() {
				_, err := mux.toAgent.Write(frame)
				done <- err
			}()

			select {
			case err := <-done:
				if err != nil {
					log.Printf("⚠️ Write queue error for agent %s: %v", mux.agentID, err)
					// Continue processing other frames
				} else {
					// Flush immediately after successful write
					if flusher, ok := mux.toAgent.(http.Flusher); ok {
						flusher.Flush()
					}
				}
			case <-time.After(1 * time.Second):
				log.Printf("⚠️ Write queue timeout for agent %s", mux.agentID)
				// Continue processing other frames
			case <-mux.done:
				return
			}
		}
	}
}

// Structures moved to types.go to avoid duplication

// handleIncomingMuxFrames processes frames from agent and routes to TCP connections
func (s *Server) handleIncomingMuxFrames(mux *MultiplexedManager) {
	for {
		select {
		case <-mux.done:
			return
		default:
			// Read frame header (8 bytes: 4 bytes conn_id + 4 bytes length)
			header := make([]byte, 8)
			if _, err := io.ReadFull(mux.fromAgent, header); err != nil {
				if err != io.EOF {
					log.Printf("⚠️ Error reading mux frame header: %v", err)
				}
				return
			}

			connID := uint32(header[0])<<24 | uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
			length := uint32(header[4])<<24 | uint32(header[5])<<16 | uint32(header[6])<<8 | uint32(header[7])

			if length == 0 {
				if connID == 0 {
					// Heartbeat frame - just log and ignore
					log.Printf("🫀 Received heartbeat from agent %s - stream is alive", mux.agentID)
					continue
				} else {
					// Control frame (connection close)
					s.closeMuxConnection(mux, connID)
					continue
				}
			}

			if length > 64*1024 {
				log.Printf("⚠️ Mux frame too large: %d bytes", length)
				return
			}

			// Read frame data
			data := make([]byte, length)
			if _, err := io.ReadFull(mux.fromAgent, data); err != nil {
				log.Printf("⚠️ Error reading mux frame data: %v", err)
				return
			}

			// Route to existing connection
			s.routeToUserConnection(mux, connID, data)
		}
	}
}

// routeToUserConnection sends data to specific user TCP connection
func (s *Server) routeToUserConnection(mux *MultiplexedManager, connID uint32, data []byte) {
	mux.mutex.RLock()
	serverConn, exists := mux.connections[connID]
	mux.mutex.RUnlock()

	if !exists {
		log.Printf("⚠️ Mux connection %d not found", connID)
		return
	}

	// Decompress and write data to user connection
	plain := decompressData(data)
	if _, err := serverConn.UserConn.Write(plain); err != nil {
		log.Printf("⚠️ Error writing to user conn %d: %v", connID, err)
		s.closeMuxConnection(mux, connID)
	}
}

// closeMuxConnection closes a multiplexed TCP connection
func (s *Server) closeMuxConnection(mux *MultiplexedManager, connID uint32) {
	mux.mutex.Lock()
	serverConn, exists := mux.connections[connID]
	if exists {
		delete(mux.connections, connID)
	}
	mux.mutex.Unlock()

	if exists {
		close(serverConn.done)
		serverConn.UserConn.Close()
		log.Printf("🔌 Mux connection %d closed", connID)
	}
}

// createMuxConnection creates a new multiplexed connection for user traffic
func (s *Server) createMuxConnection(mux *MultiplexedManager, userConn net.Conn) uint32 {
	// Use atomic operation for connection ID to avoid contention
	connID := atomic.AddUint32(&mux.nextConnID, 1)

	serverConn := &ServerTCPConnection{
		ID:       connID,
		UserConn: userConn,
		toAgent:  make(chan []byte, 100),
		done:     make(chan struct{}),
	}

	// Minimize lock time - only for map write
	mux.mutex.Lock()
	mux.connections[connID] = serverConn
	mux.mutex.Unlock()

	log.Printf("🔗 Created mux connection %d for user %s", connID, userConn.RemoteAddr())

	// Start goroutine to read from user and send to agent
	go s.readFromUserToAgent(mux, serverConn)

	return connID
}

// readFromUserToAgent reads data from user connection and sends as frames to agent
func (s *Server) readFromUserToAgent(mux *MultiplexedManager, serverConn *ServerTCPConnection) {
	defer s.closeMuxConnection(mux, serverConn.ID)

	buffer := make([]byte, 64*1024)
	for {
		select {
		case <-serverConn.done:
			return
		case <-mux.done:
			return
		default:
			serverConn.UserConn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
			n, err := serverConn.UserConn.Read(buffer)
			if n > 0 {
				// Compress and send frame to agent: [conn_id(4)] + [length(4)] + [data]
				payload := compressData(buffer[:n])
				frame := make([]byte, 8+len(payload))

				// Write connection ID (big-endian)
				frame[0] = byte(serverConn.ID >> 24)
				frame[1] = byte(serverConn.ID >> 16)
				frame[2] = byte(serverConn.ID >> 8)
				frame[3] = byte(serverConn.ID)

				// Write data length (big-endian)
				dataLen := uint32(len(payload))
				frame[4] = byte(dataLen >> 24)
				frame[5] = byte(dataLen >> 16)
				frame[6] = byte(dataLen >> 8)
				frame[7] = byte(dataLen)

				// Copy data
				copy(frame[8:], payload)

				// Send frame to agent via non-blocking queue
				select {
				case mux.writeQueue <- frame:
					// Frame queued successfully
				case <-time.After(50 * time.Millisecond):
					log.Printf("⚠️ Write queue full for conn %d", serverConn.ID)
					return
				case <-mux.done:
					return
				case <-serverConn.done:
					return
				}
			}
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				if err != io.EOF {
					log.Printf("⚠️ Error reading from user conn %d: %v", serverConn.ID, err)
				}
				return
			}
		}
	}
}

// compressData gzips the given data unconditionally and prefixes with 'G”Z'
func compressData(data []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write(data)
	_ = zw.Close()
	comp := buf.Bytes()
	out := make([]byte, 0, len(comp)+2)
	out = append(out, 'G', 'Z')
	out = append(out, comp...)
	return out
}

// decompressData gunzips data if prefixed with 'G”Z', otherwise returns as-is
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

// lookupAgentByDomainSuffix finds agent by matching host against configured domain suffixes
func (s *Server) lookupAgentByDomainSuffix(host string) *Agent {
	if s.config == nil || len(s.config.DomainMap) == 0 {
		return nil
	}
	// Match the longest suffix first
	bestLen := -1
	bestAgentID := ""
	for suffix, agentID := range s.config.DomainMap {
		if strings.HasSuffix(host, suffix) {
			if len(suffix) > bestLen {
				bestLen = len(suffix)
				bestAgentID = agentID
			}
		}
	}
	if bestAgentID == "" {
		return nil
	}
	// Resolve agent by ID
	s.mutex.RLock()
	a := s.agents[bestAgentID]
	s.mutex.RUnlock()
	return a
}
