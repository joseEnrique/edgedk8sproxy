package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// HandleWebSocket handles WebSocket connections from clients
func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("id")
	if clientID == "" {
		http.Error(w, "Client ID required", http.StatusBadRequest)
		return
	}

	log.Printf("🔌 Client connecting: %s", clientID)

	// Check if we have room for more clients
	s.mutex.RLock()
	if len(s.clients) >= s.config.MaxClients {
		s.mutex.RUnlock()
		http.Error(w, "Server at maximum capacity", http.StatusServiceUnavailable)
		return
	}
	s.mutex.RUnlock()

	// Set headers for better WebSocket compatibility with modern proxies
	w.Header().Set("Sec-WebSocket-Protocol", "kubernetes-streaming")
	w.Header().Set("X-Streaming-Protocol", "websocket")

	// Upgrade to WebSocket with optimized settings for Kubernetes v1.31+
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("❌ WebSocket upgrade failed: %v", err)
		return
	}

	// Set connection limits and timeouts optimized for Kubernetes streaming
	conn.SetReadLimit(s.config.MaxMessageSize)
	conn.SetReadDeadline(time.Now().Add(120 * time.Second)) // Increased for kubectl operations

	// Create client
	client := &Client{
		ID:        clientID,
		Conn:      conn,
		URL:       fmt.Sprintf("http://%s.%s", clientID, s.config.Domain),
		Connected: time.Now(),
		LastSeen:  time.Now(),
	}

	// Register client
	s.mutex.Lock()
	s.clients[clientID] = client
	s.mutex.Unlock()

	log.Printf("✅ Client registered: %s (WebSocket streaming ready for Kubernetes v1.31+)", clientID)

	// Ensure cleanup on exit
	defer func() {
		s.mutex.Lock()
		delete(s.clients, clientID)
		s.mutex.Unlock()
		log.Printf("❌ Client unregistered: %s", clientID)
		conn.Close()
	}()

	// Set up ping/pong with Kubernetes-optimized timing
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(120 * time.Second)) // Increased timeout
		return nil
	})

	// Start ping ticker with Kubernetes-friendly interval
	ticker := time.NewTicker(25 * time.Second) // Slightly more frequent for better proxy compatibility
	defer ticker.Stop()

	// Connection activity timeout - close connection if no activity for too long
	activityTimeout := time.NewTimer(300 * time.Second) // 5 minutes of inactivity
	defer activityTimeout.Stop()

	// Message loop
	for {
		select {
		case <-ticker.C:
			// Send ping with extended timeout for kubectl operations
			if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(15*time.Second)); err != nil {
				log.Printf("❌ Ping failed for %s: %v", clientID, err)
				return
			}
			// Update last seen
			s.mutex.Lock()
			client.LastSeen = time.Now()
			s.mutex.Unlock()
		case <-activityTimeout.C:
			// Connection inactive for too long
			log.Printf("⏰ Connection timeout for client %s - no activity for 5 minutes", clientID)
			return
		default:
			// Set read deadline with Kubernetes-optimized timeout
			conn.SetReadDeadline(time.Now().Add(s.config.ReadTimeout))

			// Read message with timeout
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("❌ Client disconnected: %s - %v", clientID, err)
				} else {
					log.Printf("✅ Client disconnected normally: %s", clientID)
				}

				// Check if it's a timeout error
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					log.Printf("⏰ Read timeout for client %s - client may be busy processing large response", clientID)
					// Don't return on timeout, just continue the loop
					continue
				}

				return
			}

			// Reset activity timeout when we receive a message
			activityTimeout.Reset(300 * time.Second)

			// Handle different message types
			if messageType == websocket.TextMessage {
				// Handle JSON messages (control messages)
				var msg Message
				if err := json.Unmarshal(message, &msg); err != nil {
					log.Printf("❌ Invalid JSON message from %s: %v", clientID, err)
					continue
				}

				// Update last seen
				s.mutex.Lock()
				client.LastSeen = time.Now()
				s.mutex.Unlock()

				log.Printf("📨 Message from %s: %s", clientID, msg.Type)

				// Handle different message types
				switch msg.Type {
				case "proxy_response":
					s.handleProxyResponse(client, msg.Payload)
				case "tcp_tunnel_response":
					s.handleTCPTunnelResponse(client, msg.Payload)
				case "register":
					log.Printf("✅ Client %s registered", clientID)
				case "heartbeat":
					log.Printf("💓 Heartbeat from %s", clientID)
				case "kubernetes_streaming_ready":
					log.Printf("🚀 Kubernetes streaming protocol ready for client %s", clientID)
				case "tunnel_data":
					s.handleTunnelData(client, msg.Payload)
				case "tunnel_response":
					s.handleTunnelResponse(client, msg.Payload)
				case "tunnel_request":
					s.handleTunnelRequest(client, msg.Payload)
				default:
					log.Printf("⚠️ Unknown message type from %s: %s", clientID, msg.Type)
				}
			} else if messageType == websocket.BinaryMessage {
				// Handle binary messages (streaming data) - optimized for Kubernetes v1.31+
				log.Printf("📦 Received %d bytes of binary data from %s (Kubernetes streaming)", len(message), clientID)

				// First, try to find an active kubectl tunnel
				s.mutex.RLock()
				var activeKubectlTunnel *Tunnel
				for tunnelID, tunnel := range client.Tunnels {
					log.Printf("🔍 Checking kubectl tunnel %s: Active=%v, KubectlConn=%v", tunnelID, tunnel.Active, tunnel.KubectlConn != nil)
					if tunnel.Active && tunnel.KubectlConn != nil {
						activeKubectlTunnel = tunnel
						log.Printf("✅ Found active kubectl tunnel %s for client %s", tunnelID, clientID)
						break
					}
				}
				s.mutex.RUnlock()

				if activeKubectlTunnel != nil {
					// Forward data directly to kubectl via kubectl tunnel
					_, err := activeKubectlTunnel.KubectlConn.Write(message)
					if err != nil {
						log.Printf("❌ Failed to forward data to kubectl in tunnel %s: %v", activeKubectlTunnel.ID, err)
					} else {
						log.Printf("📦 Forwarded %d bytes from client to kubectl in tunnel %s", len(message), activeKubectlTunnel.ID)
					}
				} else {
					// No kubectl tunnel found, check if client has TCP tunnel active
					// TCP tunnels use binary handlers instead of direct connections
					log.Printf("🔍 No kubectl tunnel found, checking for TCP tunnel via binary handlers")

					// Use registered binary handlers (for TCP tunnels)
					client.forwardBinaryMessage(message)
					log.Printf("📦 Forwarded %d bytes via binary handlers (TCP tunnel mode)", len(message))
				}
			}
		}
	}
}

// GetClientBySubdomain returns a client by subdomain
func (s *Server) GetClientBySubdomain(subdomain string) *Client {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.clients[subdomain]
}

// GetClientStats returns statistics about all clients
func (s *Server) GetClientStats() map[string]interface{} {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	stats := make(map[string]interface{})
	stats["total_clients"] = len(s.clients)
	stats["max_clients"] = s.config.MaxClients

	var totalRequests, totalBytesIn, totalBytesOut int64
	for _, client := range s.clients {
		totalRequests += client.Requests
		totalBytesIn += client.BytesIn
		totalBytesOut += client.BytesOut
	}

	stats["total_requests"] = totalRequests
	stats["total_bytes_in"] = totalBytesIn
	stats["total_bytes_out"] = totalBytesOut

	return stats
}

// handleProxyResponse handles proxy responses from clients
func (s *Server) handleProxyResponse(client *Client, payload interface{}) {
	// Convert payload to ProxyResponse
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		log.Printf("❌ Failed to marshal proxy response payload: %v", err)
		return
	}

	var proxyResp ProxyResponse
	if err := json.Unmarshal(payloadBytes, &proxyResp); err != nil {
		log.Printf("❌ Failed to unmarshal proxy response: %v", err)
		return
	}

	// Find the response channel
	s.mutex.RLock()
	responseChan, exists := client.ResponseChannels[proxyResp.RequestID]
	s.mutex.RUnlock()

	if !exists {
		log.Printf("⚠️ No response channel found for request ID: %s", proxyResp.RequestID)
		return
	}

	// Send response to channel
	select {
	case responseChan <- proxyResp:
		log.Printf("✅ Proxy response forwarded for request: %s", proxyResp.RequestID)
	default:
		log.Printf("❌ Response channel full for request: %s", proxyResp.RequestID)
	}
}

// handleTCPTunnelResponse handles TCP tunnel responses from clients
func (s *Server) handleTCPTunnelResponse(client *Client, payload interface{}) {
	// Convert payload to TCPTunnelResponse
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		log.Printf("❌ Failed to marshal TCP tunnel response payload: %v", err)
		return
	}

	var tunnelResp TCPTunnelResponse
	if err := json.Unmarshal(payloadBytes, &tunnelResp); err != nil {
		log.Printf("❌ Failed to unmarshal TCP tunnel response: %v", err)
		return
	}

	if tunnelResp.Success {
		log.Printf("✅ TCP tunnel established successfully for client: %s", client.ID)
	} else {
		log.Printf("❌ TCP tunnel failed for client %s: %s", client.ID, tunnelResp.Error)
	}
}

// HandleTest provides a simple test endpoint
func (s *Server) HandleTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := map[string]interface{}{
		"status":                "ok",
		"message":               "Server is running",
		"timestamp":             time.Now().Format(time.RFC3339),
		"kubernetes_compatible": true,
		"protocol":              "websocket",
		"version":               "v1.31+",
	}

	json.NewEncoder(w).Encode(response)
}

// StartKubectlProxy starts listening for kubectl connections on port 8081
func (s *Server) StartKubectlProxy() {
	// This function is now deprecated - kubectl opens local ports
	// The server should connect TO kubectl's local port, not listen
	log.Printf("📡 Server will connect to kubectl's local port when needed")
}

// ConnectToKubectlPort connects to a kubectl port-forward on the client's machine
func (s *Server) ConnectToKubectlPort(clientID string, localPort int) error {
	client := s.GetClientBySubdomain(clientID)
	if client == nil {
		return fmt.Errorf("client %s not found", clientID)
	}

	// Connect to kubectl's local port
	kubectlAddr := fmt.Sprintf("localhost:%d", localPort)
	kubectlConn, err := net.Dial("tcp", kubectlAddr)
	if err != nil {
		return fmt.Errorf("failed to connect to kubectl port %d: %v", localPort, err)
	}

	log.Printf("🔌 Connected to kubectl port %d for client %s", localPort, clientID)

	// Create a unique tunnel ID
	tunnelID := fmt.Sprintf("tunnel-%s-%d-%d", clientID, localPort, time.Now().Unix())

	// Store the kubectl connection
	s.mutex.Lock()
	if client.Tunnels == nil {
		client.Tunnels = make(map[string]*Tunnel)
	}
	client.Tunnels[tunnelID] = &Tunnel{
		ID:          tunnelID,
		KubectlConn: kubectlConn,
		ClientID:    clientID,
		LocalPort:   localPort,
		Created:     time.Now(),
		Active:      true,
	}
	s.mutex.Unlock()

	// Send connection request to client via WebSocket
	connectionMsg := map[string]interface{}{
		"type": "kubectl_connection",
		"payload": map[string]interface{}{
			"tunnel_id": tunnelID,
			"port":      localPort,
			"action":    "establish_tunnel",
		},
	}

	msgBytes, err := json.Marshal(connectionMsg)
	if err != nil {
		kubectlConn.Close()
		return fmt.Errorf("failed to marshal connection message: %v", err)
	}

	// Send message to client
	if err := client.writeMessage(websocket.TextMessage, msgBytes); err != nil {
		kubectlConn.Close()
		return fmt.Errorf("failed to send connection message to client: %v", err)
	}

	log.Printf("📨 Sent kubectl connection request to client %s for port %d (tunnel: %s)", clientID, localPort, tunnelID)

	// Don't start tunneling yet - wait for client confirmation
	// The tunnel will be started when the client sends tunnel_response
	log.Printf("⏳ Waiting for client confirmation before starting tunnel %s", tunnelID)

	return nil
}

// handleTunnelData handles tunnel data from client
func (s *Server) handleTunnelData(client *Client, payload interface{}) {
	// Convert payload to tunnel data
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		log.Printf("❌ Failed to marshal tunnel data payload: %v", err)
		return
	}

	var tunnelData struct {
		TunnelID  string `json:"tunnel_id"`
		Data      []byte `json:"data"`
		Direction string `json:"direction"`
	}

	if err := json.Unmarshal(payloadBytes, &tunnelData); err != nil {
		log.Printf("❌ Failed to unmarshal tunnel data: %v", err)
		return
	}

	// Find the tunnel
	s.mutex.RLock()
	tunnel, exists := client.Tunnels[tunnelData.TunnelID]
	s.mutex.RUnlock()

	if !exists {
		log.Printf("⚠️ Tunnel %s not found for client %s", tunnelData.TunnelID, client.ID)
		return
	}

	// Forward data to kubectl
	if tunnelData.Direction == "client_to_kubectl" {
		_, err := tunnel.KubectlConn.Write(tunnelData.Data)
		if err != nil {
			log.Printf("❌ Failed to write to kubectl in tunnel %s: %v", tunnelData.TunnelID, err)
			return
		}
		log.Printf("📦 Forwarded %d bytes from client to kubectl in tunnel %s", len(tunnelData.Data), tunnelData.TunnelID)
	}
}

// handleTunnelResponse handles tunnel responses from client
func (s *Server) handleTunnelResponse(client *Client, payload interface{}) {
	// Convert payload to tunnel response
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		log.Printf("❌ Failed to marshal tunnel response payload: %v", err)
		return
	}

	var tunnelResp struct {
		TunnelID string `json:"tunnel_id"`
		Success  bool   `json:"success"`
		Error    string `json:"error,omitempty"`
	}

	if err := json.Unmarshal(payloadBytes, &tunnelResp); err != nil {
		log.Printf("❌ Failed to unmarshal tunnel response: %v", err)
		return
	}

	if tunnelResp.Success {
		log.Printf("✅ Tunnel %s confirmed by client %s", tunnelResp.TunnelID, client.ID)

		// Find the tunnel and start it
		s.mutex.RLock()
		tunnel, exists := client.Tunnels[tunnelResp.TunnelID]
		s.mutex.RUnlock()

		if !exists {
			log.Printf("❌ Tunnel %s not found for client %s", tunnelResp.TunnelID, client.ID)
			return
		}

		// Start the tunnel now that the client is ready
		log.Printf("🚀 Starting tunnel %s for client %s", tunnelResp.TunnelID, client.ID)
		go s.startTunneling(tunnelResp.TunnelID, client.ID, tunnel.KubectlConn)

		// Send confirmation to client that tunnel is ready
		confirmationMsg := map[string]interface{}{
			"type": "tunnel_ready",
			"payload": map[string]interface{}{
				"tunnel_id": tunnelResp.TunnelID,
				"status":    "active",
				"message":   "Tunnel is ready, you can start processing kubectl data",
			},
		}

		msgBytes, err := json.Marshal(confirmationMsg)
		if err != nil {
			log.Printf("❌ Failed to marshal tunnel confirmation: %v", err)
			return
		}

		if err := client.writeMessage(websocket.TextMessage, msgBytes); err != nil {
			log.Printf("❌ Failed to send tunnel confirmation to client: %v", err)
			return
		}

		log.Printf("📨 Sent tunnel confirmation to client %s for tunnel %s", client.ID, tunnelResp.TunnelID)
	} else {
		log.Printf("❌ Tunnel %s failed for client %s: %s", tunnelResp.TunnelID, client.ID, tunnelResp.Error)
		// Close the tunnel on failure
		s.mutex.Lock()
		if tunnel, exists := client.Tunnels[tunnelResp.TunnelID]; exists {
			tunnel.Active = false
			tunnel.KubectlConn.Close()
			delete(client.Tunnels, tunnelResp.TunnelID)
		}
		s.mutex.Unlock()
	}
}

// startTunneling starts the bidirectional data flow between kubectl and client
func (s *Server) startTunneling(tunnelID, clientID string, kubectlConn net.Conn) {
	defer func() {
		kubectlConn.Close()
		// Remove tunnel from client
		s.mutex.Lock()
		if client := s.GetClientBySubdomain(clientID); client != nil {
			delete(client.Tunnels, tunnelID)
		}
		s.mutex.Unlock()
		log.Printf("🔌 Tunnel %s closed for client %s", tunnelID, clientID)
	}()

	log.Printf("🚀 Starting tunnel %s for client %s", tunnelID, clientID)

	// Verify tunnel is still active before starting
	s.mutex.RLock()
	client := s.GetClientBySubdomain(clientID)
	var tunnel *Tunnel
	if client != nil {
		tunnel = client.Tunnels[tunnelID]
	}
	s.mutex.RUnlock()

	if tunnel == nil || !tunnel.Active {
		log.Printf("❌ Tunnel %s is no longer active, aborting start", tunnelID)
		return
	}

	log.Printf("✅ Tunnel %s verified active, starting data flow", tunnelID)

	// Create channels for data flow
	kubectlToClient := make(chan []byte, 100)
	clientToKubectl := make(chan []byte, 100)

	// Start kubectl to client data flow (kubectl sends request)
	go func() {
		defer close(kubectlToClient)
		buffer := make([]byte, 4096)
		for {
			n, err := kubectlConn.Read(buffer)
			if err != nil {
				if err != io.EOF {
					log.Printf("❌ Error reading from kubectl in tunnel %s: %v", tunnelID, err)
				}
				return
			}
			if n > 0 {
				data := make([]byte, n)
				copy(data, buffer[:n])
				select {
				case kubectlToClient <- data:
					log.Printf("📦 kubectl → client: %d bytes in tunnel %s", n, tunnelID)
				default:
					log.Printf("⚠️ Client buffer full in tunnel %s, dropping data", tunnelID)
				}
			}
		}
	}()

	// Start client to kubectl data flow (client sends response)
	go func() {
		defer close(clientToKubectl)
		for data := range clientToKubectl {
			_, err := kubectlConn.Write(data)
			if err != nil {
				log.Printf("❌ Error writing to kubectl in tunnel %s: %v", tunnelID, err)
				return
			}
			log.Printf("📦 client → kubectl: %d bytes in tunnel %s", len(data), tunnelID)
		}
	}()

	// Main tunnel loop - handle data flow
	for {
		select {
		case data, ok := <-kubectlToClient:
			if !ok {
				log.Printf("🔌 kubectl to client channel closed in tunnel %s", tunnelID)
				return
			}
			// Send kubectl request to client via WebSocket
			if client := s.GetClientBySubdomain(clientID); client != nil {
				// Send tunnel data with metadata
				tunnelMsg := map[string]interface{}{
					"type": "tunnel_request",
					"payload": map[string]interface{}{
						"tunnel_id": tunnelID,
						"data":      string(data), // Convert to string for JSON
						"direction": "kubectl_to_client",
					},
				}
				msgBytes, err := json.Marshal(tunnelMsg)
				if err != nil {
					log.Printf("❌ Failed to marshal tunnel message: %v", err)
					continue
				}
				if err := client.writeMessage(websocket.TextMessage, msgBytes); err != nil {
					log.Printf("❌ Failed to send tunnel request to client: %v", err)
					return
				}
				log.Printf("📦 Sent kubectl request (%d bytes) to client in tunnel %s", len(data), tunnelID)
			}

		case data, ok := <-clientToKubectl:
			if !ok {
				log.Printf("🔌 client to kubectl channel closed in tunnel %s", tunnelID)
				return
			}
			// Data already sent to kubectl in the goroutine above
			_ = data // Use data to avoid unused variable warning

		case <-time.After(30 * time.Second):
			// Check if tunnel is still active
			s.mutex.RLock()
			client := s.GetClientBySubdomain(clientID)
			var tunnel *Tunnel
			if client != nil {
				tunnel = client.Tunnels[tunnelID]
			}
			s.mutex.RUnlock()

			if tunnel == nil || !tunnel.Active {
				log.Printf("🔌 Tunnel %s is no longer active, closing", tunnelID)
				return
			}
		}
	}
}

// HandleKubectlRequest handles HTTP requests to establish kubectl connections
func (s *Server) HandleKubectlRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var req struct {
		ClientID string `json:"client_id"`
		Port     int    `json:"port"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.ClientID == "" || req.Port == 0 {
		http.Error(w, "client_id and port are required", http.StatusBadRequest)
		return
	}

	log.Printf("🔌 kubectl connection request: client=%s, port=%d", req.ClientID, req.Port)

	// Try to establish connection
	if err := s.ConnectToKubectlPort(req.ClientID, req.Port); err != nil {
		log.Printf("❌ Failed to connect to kubectl port: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Send success response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": fmt.Sprintf("kubectl connection established for client %s on port %d", req.ClientID, req.Port),
	})
}

// HandleTunnelData handles data sent back from client to kubectl
func (s *Server) HandleTunnelData(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var req struct {
		TunnelID string `json:"tunnel_id"`
		Data     string `json:"data"` // Response data from client
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.TunnelID == "" || req.Data == "" {
		http.Error(w, "tunnel_id and data are required", http.StatusBadRequest)
		return
	}

	log.Printf("📦 Tunnel response from client: tunnel=%s, data_size=%d", req.TunnelID, len(req.Data))

	// Find the tunnel and send response data to kubectl
	if err := s.sendResponseToKubectl(req.TunnelID, req.Data); err != nil {
		log.Printf("❌ Failed to send response to kubectl: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Send success response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": fmt.Sprintf("Response sent to kubectl in tunnel %s", req.TunnelID),
	})
}

// sendResponseToKubectl sends response data from client back to kubectl
func (s *Server) sendResponseToKubectl(tunnelID, data string) error {
	// Find the tunnel
	s.mutex.RLock()
	var targetTunnel *Tunnel

	for _, client := range s.clients {
		if tunnel, exists := client.Tunnels[tunnelID]; exists {
			targetTunnel = tunnel
			break
		}
	}
	s.mutex.RUnlock()

	if targetTunnel == nil {
		return fmt.Errorf("tunnel %s not found", tunnelID)
	}

	// Convert string data back to bytes
	dataBytes := []byte(data)

	// Send response data to kubectl
	_, err := targetTunnel.KubectlConn.Write(dataBytes)
	if err != nil {
		return fmt.Errorf("failed to write response to kubectl: %v", err)
	}

	log.Printf("📦 Sent response (%d bytes) to kubectl in tunnel %s", len(dataBytes), tunnelID)
	return nil
}

// handleTunnelRequest handles tunnel requests from client
func (s *Server) handleTunnelRequest(client *Client, payload interface{}) {
	// Convert payload to tunnel request
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		log.Printf("❌ Failed to marshal tunnel request payload: %v", err)
		return
	}

	var tunnelReq struct {
		TunnelID  string `json:"tunnel_id"`
		Data      string `json:"data"`
		Direction string `json:"direction"`
	}

	if err := json.Unmarshal(payloadBytes, &tunnelReq); err != nil {
		log.Printf("❌ Failed to unmarshal tunnel request: %v", err)
		return
	}

	log.Printf("📦 Tunnel request from client %s: tunnel=%s, direction=%s, bytes=%d",
		client.ID, tunnelReq.TunnelID, tunnelReq.Direction, len(tunnelReq.Data))

	// For now, just log the request
	// In a real implementation, this would handle client-initiated tunnel requests
	log.Printf("📡 Client %s requested tunnel operation: %s", client.ID, tunnelReq.Direction)
}

// handleKubectlConnection handles a single kubectl connection
func (s *Server) handleKubectlConnection(kubectlConn net.Conn) {
	defer kubectlConn.Close()

	remoteAddr := kubectlConn.RemoteAddr().String()
	log.Printf("🔌 kubectl connection from %s", remoteAddr)

	// For now, we'll just log the connection and close it
	// TODO: Implement actual tunneling through WebSocket
	log.Printf("📡 kubectl connection established from %s", remoteAddr)

	// Send a simple response to kubectl
	kubectlConn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\nkubectl proxy connected"))

	log.Printf("✅ kubectl connection from %s completed", remoteAddr)
}
