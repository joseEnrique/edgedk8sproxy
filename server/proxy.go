package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
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

// handleTCPConnection handles incoming TCP connections and determines if they are clients or users
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
		log.Printf("🔌 Client connection detected")
		s.handleTCPClientConnection(conn, string(data))
		return
	}
	log.Printf("📡 User traffic detected, forwarding to client")
	s.handleUserTraffic(conn, data)
}

// handleUserTraffic handles user traffic (SSH, HTTP, kubectl, etc.) and forwards it to a client
func (s *Server) handleUserTraffic(conn net.Conn, initialData []byte) {
	log.Printf("📡 Handling user traffic (%d bytes)", len(initialData))
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
	}

	clientID := "client1"

	s.mutex.RLock()
	client, exists := s.clients[clientID]
	s.mutex.RUnlock()

	if !exists {
		log.Printf("❌ Client %s not found", clientID)
		_ = conn.Close()
		return
	}

	log.Printf("✅ Routing user traffic to client: %s", client.ID)

	// Send initial data to client first
	if len(initialData) > 0 {
		log.Printf("📤 Sending initial %d bytes to client", len(initialData))
		if _, err := client.TCPConn.Write(initialData); err != nil {
			log.Printf("❌ Failed to send initial data to client: %v", err)
			_ = conn.Close()
			return
		}
		log.Printf("✅ Initial data sent to client")
	}

	// Start a new user session for this connection (concurrent)
	go s.forwardUserToClient(conn, client)
}

// forwardUserToClient handles bidirectional forwarding between user connection and client
func (s *Server) forwardUserToClient(userConn net.Conn, client *Client) {
	defer userConn.Close()

	log.Printf("📡 Starting direct TCP forwarding for client %s", client.ID)

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
	client.tunnelMutex.Lock()
	if client.userConnections == nil {
		client.userConnections = make(map[string]chan []byte)
	}
	userID := fmt.Sprintf("%s-%d", userConn.RemoteAddr().String(), time.Now().UnixNano())
	client.userConnections[userID] = responseChan
	client.tunnelMutex.Unlock()

	// Clean up when this function exits
	defer func() {
		client.tunnelMutex.Lock()
		delete(client.userConnections, userID)
		client.tunnelMutex.Unlock()
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
					client.writerMu.Lock()
					if _, err := client.TCPConn.Write(buffer[:n]); err != nil {
						client.writerMu.Unlock()
						if isConnectionClosed(err) {
							log.Printf("📤 Tunnel connection closed during write")
							closeDone()
							return
						}
						log.Printf("❌ Failed to write to tunnel: %v", err)
						closeDone()
						return
					}
					client.writerMu.Unlock()
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
	log.Printf("📡 User session ended for client %s (tunnel remains persistent)", client.ID)
}

// isConnectionClosed checks if an error indicates a closed connection
func isConnectionClosed(err error) bool {
	if err == nil {
		return false
	}
	es := err.Error()
	return es == "EOF" || strings.Contains(es, "use of closed network connection") || strings.Contains(es, "connection reset by peer") || strings.Contains(es, "broken pipe")
}

// handleTCPClientConnection handles a TCP connection from a client
func (s *Server) handleTCPClientConnection(conn net.Conn, clientID string) {
	log.Printf("🔌 Client %s connected via TCP tunnel", clientID)
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	client := &Client{
		ID:              clientID,
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
	s.clients[clientID] = client
	s.mutex.Unlock()

	log.Printf("✅ Created new TCP client: %s", clientID)

	// Start the central tunnel reader that distributes responses
	go s.tunnelReader(client)

	// Start TCP tunnel handling
	s.handleTCPTunnel(conn, clientID)
}

// handleTCPTunnel handles the TCP tunnel between server and client
func (s *Server) handleTCPTunnel(conn net.Conn, clientID string) {
	log.Printf("🔌 Starting PERSISTENT TCP tunnel for client: %s", clientID)

	// Get client reference
	s.mutex.RLock()
	client, exists := s.clients[clientID]
	s.mutex.RUnlock()

	if !exists {
		log.Printf("❌ Client %s not found during tunnel handling", clientID)
		return
	}

	// Mark tunnel as active
	client.tunnelMutex.Lock()
	client.isActive = true
	client.tunnelMutex.Unlock()

	log.Printf("✅ TCP tunnel established successfully for client: %s", clientID)

	// PERSISTENT TUNNEL: This connection NEVER closes
	// Only close if the client sends FIN TCP or server shuts down
	// But we don't read from it here - tunnelReader handles all data

	// Just wait for the connection to be closed by client
	// We can detect this by checking if the connection is still alive
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Check if connection is still alive without reading data
			if err := conn.SetReadDeadline(time.Now().Add(1 * time.Millisecond)); err != nil {
				log.Printf("🔌 Tunnel connection lost for client %s", clientID)
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
				log.Printf("🔌 Tunnel connection lost for client %s: %v", clientID, err)
				goto cleanup
			}

			// Reset deadline
			conn.SetReadDeadline(time.Time{})

		case <-client.done:
			log.Printf("🔌 Client %s requested tunnel closure", clientID)
			goto cleanup
		}
	}

cleanup:
	// Clean up when tunnel is closed by client
	log.Printf("🔌 TCP tunnel ended for client: %s", clientID)
	client.tunnelMutex.Lock()
	client.isActive = false
	client.tunnelMutex.Unlock()

	s.mutex.Lock()
	delete(s.clients, clientID)
	s.mutex.Unlock()

	conn.Close()
}

// tunnelReader is the central reader that distributes responses to user connections
func (s *Server) tunnelReader(client *Client) {
	defer func() {
		log.Printf("🔌 Tunnel reader ended for client %s", client.ID)
	}()

	buffer := make([]byte, 32*1024)
	for {
		n, err := client.TCPConn.Read(buffer)
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
			client.tunnelMutex.RLock()
			for userID, responseQueue := range client.userConnections {
				select {
				case responseQueue <- data:
					// Response queued successfully
				default:
					// Queue full, drop response
					log.Printf("⚠️ Response queue full for user %s, dropping response", userID)
				}
			}
			client.tunnelMutex.RUnlock()
		}
	}
}

// processTCPData processes data received from TCP client
func (s *Server) processTCPData(client *Client, data []byte) {
	log.Printf("📦 Processing %d bytes from TCP client %s", len(data), client.ID)

	// The client is sending data from the target back to the server
	// This data should be forwarded to the user connection, not echoed back
	// We don't need to process it, just log it for debugging

	log.Printf("📥 Received %d bytes from client %s (target response)", len(data), client.ID)

	// Note: This data should be automatically forwarded to the user connection
	// by the bidirectional forwarding logic in forwardUserToClient
	// We don't need to do anything here
}

// HandleRoot handles the root endpoint and routes requests based on subdomain
func (s *Server) HandleRoot(w http.ResponseWriter, r *http.Request) {
	// Extract subdomain from host
	host := r.Host
	subdomain := ""

	if idx := strings.Index(host, "."); idx > 0 {
		subdomain = host[:idx]
	}

	// If no subdomain, show status page
	if subdomain == "" {
		s.HandleStatus(w, r)
		return
	}

	// Find client by subdomain
	client := s.GetClientBySubdomain(subdomain)
	if client == nil {
		http.Error(w, fmt.Sprintf("Client '%s' not found", subdomain), http.StatusNotFound)
		return
	}

	log.Printf("🌐 Routing request for subdomain '%s' to client '%s'", subdomain, client.ID)

	// Forward the request to the client via TCP tunnel
	s.forwardHTTPRequestToClient(w, r, client)
}

// GetClientBySubdomain finds a client by subdomain
func (s *Server) GetClientBySubdomain(subdomain string) *Client {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// Look for exact match first
	if client, exists := s.clients[subdomain]; exists {
		return client
	}

	// If no exact match, look for clients that start with the subdomain
	for clientID, client := range s.clients {
		if strings.HasPrefix(clientID, subdomain) {
			return client
		}
	}

	return nil
}

// forwardHTTPRequestToClient forwards HTTP requests to a specific client via TCP tunnel
func (s *Server) forwardHTTPRequestToClient(w http.ResponseWriter, r *http.Request, client *Client) {
	log.Printf("📡 Forwarding HTTP request to client %s: %s %s", client.ID, r.Method, r.URL.Path)

	// Check if client is active
	client.tunnelMutex.RLock()
	if !client.isActive {
		client.tunnelMutex.RUnlock()
		http.Error(w, "Client not active", http.StatusServiceUnavailable)
		return
	}
	client.tunnelMutex.RUnlock()

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

	// Send via TCP tunnel to client
	if _, err := client.TCPConn.Write([]byte(httpMsg)); err != nil {
		log.Printf("❌ Failed to send HTTP request to client: %v", err)
		http.Error(w, "Failed to forward request", http.StatusBadGateway)
		return
	}

	// For now, just send a simple response
	// In a real implementation, you'd wait for the client's response
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf("Request forwarded to client %s via TCP tunnel", client.ID)))

	log.Printf("✅ HTTP request forwarded to client %s", client.ID)
}

// getActiveConnections returns the number of active user connections for a client
func (s *Server) getActiveConnections(client *Client) int {
	client.tunnelMutex.RLock()
	defer client.tunnelMutex.RUnlock()
	return len(client.userConnections)
}

// getTotalActiveConnections returns the total number of active connections across all clients
func (s *Server) getTotalActiveConnections() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	total := 0
	for _, client := range s.clients {
		total += s.getActiveConnections(client)
	}
	return total
}

// HandleListClients returns a list of connected clients
func (s *Server) HandleListClients(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	clientList := make([]map[string]interface{}, 0, len(s.clients))
	for _, client := range s.clients {
		activeConnections := s.getActiveConnections(client)
		clientList = append(clientList, map[string]interface{}{
			"id":                 client.ID,
			"connected":          client.Connected,
			"last_seen":          client.LastSeen,
			"requests":           client.Requests,
			"bytes_in":           client.BytesIn,
			"bytes_out":          client.BytesOut,
			"active":             client.isActive,
			"active_connections": activeConnections,
			"subdomain":          client.ID + "." + s.config.Domain, // Add subdomain info
		})
	}
	totalConnections := s.getTotalActiveConnections()
	s.mutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"clients":           clientList,
		"count":             len(clientList),
		"total_connections": totalConnections,
	})
}

// HandleStatus returns the server status page
func (s *Server) HandleStatus(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	clientCount := len(s.clients)
	activeClients := 0
	totalConnections := s.getTotalActiveConnections()

	// Count active clients
	for _, client := range s.clients {
		if client.isActive {
			activeClients++
		}
	}

	// Get detailed client info for the table
	clientDetails := make([]map[string]interface{}, 0, len(s.clients))
	for _, client := range s.clients {
		activeConnections := s.getActiveConnections(client)
		clientDetails = append(clientDetails, map[string]interface{}{
			"id":                 client.ID,
			"active":             client.isActive,
			"connected":          client.Connected.Format("15:04:05"),
			"active_connections": activeConnections,
			"subdomain":          client.ID + "." + s.config.Domain,
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
        .client-table { width: 100%%; border-collapse: collapse; margin: 20px 0; }
        .client-table th, .client-table td { padding: 12px; text-align: left; border-bottom: 1px solid #ddd; }
        .client-table th { background-color: #f8f9fa; font-weight: bold; }
        .status-active { color: #28a745; font-weight: bold; }
        .status-inactive { color: #dc3545; font-weight: bold; }
        .connections-badge { background: #007bff; color: white; padding: 4px 8px; border-radius: 12px; font-size: 0.8em; }
        .client { margin: 10px 0; padding: 15px; background: #f8f9fa; border-radius: 5px; border-left: 4px solid #007bff; }
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
            <p><a href="/clients">View Clients (JSON)</a></p>
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
        <table class="client-table">
            <thead>
                <tr>
                    <th>Client ID</th>
                    <th>Status</th>
                    <th>Connected At</th>
                    <th>Active Connections</th>
                    <th>Subdomain</th>
                </tr>
            </thead>
            <tbody>`, clientCount, activeClients, totalConnections, s.config.TCPPort, s.config.Port)

	// Add client rows
	for _, client := range clientDetails {
		statusClass := "status-inactive"
		statusText := "Inactive"
		if client["active"].(bool) {
			statusClass = "status-active"
			statusText = "Active"
		}

		connectionsText := ""
		if client["active_connections"].(int) > 0 {
			connectionsText = fmt.Sprintf(`<span class="connections-badge">%d</span>`, client["active_connections"].(int))
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
			client["id"].(string),
			statusClass,
			statusText,
			client["connected"].(string),
			connectionsText,
			client["subdomain"].(string))
	}

	html += `
            </tbody>
        </table>
        
        <h3>🌐 Subdomain Routing:</h3>
        <div class="subdomain">
            <strong>How to use:</strong><br>
            &bull; <code>client1.` + s.config.Domain + `</code> &rarr; Routes to client1<br>
            &bull; <code>client2.` + s.config.Domain + `</code> &rarr; Routes to client2<br>
            &bull; <code>` + s.config.Domain + `</code> &rarr; Shows this status page
        </div>
        
        <h3>💡 Example Usage:</h3>
        <div class="client">
            <strong>SSH to client1:</strong> <code>ssh -p ` + s.config.TCPPort + ` client1.` + s.config.Domain + `</code><br>
            <strong>HTTP to client2:</strong> <code>curl http://client2.` + s.config.Domain + `:` + s.config.Port + `/api</code><br>
            <strong>Multiple curls:</strong> <code>for i in {1..100}; do curl http://client1.` + s.config.Domain + `:` + s.config.Port + `/ & done</code>
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
