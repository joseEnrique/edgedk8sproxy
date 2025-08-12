package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// HandleRoot handles the root endpoint and proxy requests
func (s *Server) HandleRoot(w http.ResponseWriter, r *http.Request) {
	// Check if this is a kubectl request FIRST (before subdomain check)
	// kubectl sends Upgrade: websocket and Sec-Websocket-Protocol: v5.channel.k8s.io
	if r.Header.Get("Upgrade") == "websocket" &&
		r.Header.Get("Sec-Websocket-Protocol") == "v5.channel.k8s.io" {
		log.Printf("🔌 kubectl request detected (port-forward/exec)")

		// Find the first available client for kubectl requests
		s.mutex.RLock()
		var client *Client
		for _, c := range s.clients {
			client = c
			break
		}
		s.mutex.RUnlock()

		if client == nil {
			log.Printf("❌ No clients available for kubectl request")
			http.Error(w, "No clients available", http.StatusServiceUnavailable)
			return
		}

		log.Printf("✅ Using client %s for kubectl request", client.ID)
		s.HandleKubectlWebSocket(w, r, client)
		return
	}

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
		http.Error(w, "Client not found", http.StatusNotFound)
		return
	}

	// Check if this is a TCP tunnel request (kubectl port-forward)
	// kubectl sends Upgrade: SPDY/3.1 and X-Stream-Protocol-Version: portforward.k8s.io
	if r.Header.Get("Upgrade") == "tcp" ||
		(r.Header.Get("Upgrade") == "SPDY/3.1" && r.Header.Get("X-Stream-Protocol-Version") == "portforward.k8s.io") {
		s.HandleTCPTunnel(w, r, client)
		return
	}

	// ALL other requests (including curl to port-forward) go through WebSocket
	// This ensures everything works through WebSockets as requested
	log.Printf("🌐 All HTTP requests go through WebSocket for client: %s", client.ID)
	s.ProxyRequest(w, r, client)
}

// HandleTCPTunnel handles TCP tunnel requests (for kubectl port-forward)
func (s *Server) HandleTCPTunnel(w http.ResponseWriter, r *http.Request, client *Client) {
	log.Printf("🔌 TCP tunnel request for %s", client.ID)

	// Get target from header, query parameter, or extract from kubectl path
	target := r.Header.Get("X-Target")
	if target == "" {
		target = r.URL.Query().Get("target")
	}

	// If still no target, try to extract from kubectl port-forward path
	// kubectl path format: /api/v1/namespaces/{namespace}/pods/{pod}/portforward
	if target == "" {
		path := r.URL.Path
		log.Printf("🔍 Extracting target from path: %s", path)

		// Extract namespace and pod from path
		// Example: /api/v1/namespaces/default/pods/my-pod/portforward
		parts := strings.Split(path, "/")
		if len(parts) >= 6 && parts[1] == "api" && parts[2] == "v1" && parts[3] == "namespaces" {
			namespace := parts[4]
			pod := parts[6]
			// Use the actual pod name without port for kubectl port-forward
			target = fmt.Sprintf("%s.%s", pod, namespace)
			log.Printf("🎯 Extracted target: %s", target)
		}
	}

	if target == "" {
		// For kubectl port-forward, use a default target
		target = "kubernetes.default.svc.cluster.local"
		log.Printf("⚠️ No target specified, using default: %s", target)
	}

	log.Printf("🎯 Final target: %s", target)

	// For kubectl port-forward, we need to handle SPDY/3.1 upgrade
	// kubectl doesn't use WebSocket, it uses SPDY protocol
	if r.Header.Get("Upgrade") == "SPDY/3.1" {
		log.Printf("🔄 Handling SPDY/3.1 upgrade for kubectl port-forward")

		// Send tunnel request to client
		tunnelMsg := Message{
			Type: "tcp_tunnel",
			Payload: TCPTunnelRequest{
				Target: target,
			},
		}

		data, err := json.Marshal(tunnelMsg)
		if err != nil {
			log.Printf("❌ Failed to marshal tunnel request: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Send to client via WebSocket
		if err := client.Conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("❌ Failed to send tunnel request to client: %v", err)
			http.Error(w, "Failed to establish tunnel", http.StatusInternalServerError)
			return
		}

		// For kubectl, we need to respond with 101 Switching Protocols
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Upgrade", "SPDY/3.1")
		w.WriteHeader(http.StatusSwitchingProtocols)

		log.Printf("✅ SPDY upgrade response sent")

		// Handle SPDY streaming directly between kubectl and user
		s.handleSPDYStream(w, r, client, target)
		return
	}

	// For regular WebSocket TCP tunnel
	wsConn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("❌ Failed to upgrade to WebSocket for TCP tunnel: %v", err)
		return
	}
	defer wsConn.Close()

	log.Printf("✅ WebSocket upgraded for TCP tunnel")

	// Handle TCP tunnel streaming
	s.handleTCPStream(wsConn, client, target)
}

// handleTCPStream handles the actual TCP streaming
func (s *Server) handleTCPStream(wsConn *websocket.Conn, client *Client, target string) {
	log.Printf("📡 Starting TCP stream for %s to target %s", client.ID, target)

	// Create a channel to coordinate shutdown
	done := make(chan bool)
	defer close(done)

	// Start goroutine to handle WebSocket to TCP streaming (kubectl -> client)
	go func() {
		defer func() {
			wsConn.Close()
			log.Printf("📤 WebSocket to TCP stream ended for %s", client.ID)
		}()

		for {
			select {
			case <-done:
				return
			default:
				// Read binary message from WebSocket (data from kubectl)
				_, message, err := wsConn.ReadMessage()
				if err != nil {
					if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
						log.Printf("❌ WebSocket read error (kubectl): %v", err)
					}
					return
				}

				// Forward data to client (kubectl -> local service)
				if err := client.Conn.WriteMessage(websocket.BinaryMessage, message); err != nil {
					log.Printf("❌ Failed to forward data to client: %v", err)
					return
				}

				log.Printf("📤 Forwarded %d bytes from kubectl to client", len(message))
			}
		}
	}()

	// Start goroutine to handle TCP to WebSocket streaming (client -> kubectl)
	go func() {
		defer func() {
			wsConn.Close()
			log.Printf("📥 TCP to WebSocket stream ended for %s", client.ID)
		}()

		// Listen for binary messages from client (data from local service)
		for {
			select {
			case <-done:
				return
			default:
				// Read message from client WebSocket
				messageType, message, err := client.Conn.ReadMessage()
				if err != nil {
					if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
						log.Printf("❌ Client WebSocket read error: %v", err)
					}
					return
				}

				// Only forward binary messages (TCP data)
				if messageType == websocket.BinaryMessage {
					// Forward data to kubectl
					if err := wsConn.WriteMessage(websocket.BinaryMessage, message); err != nil {
						log.Printf("❌ Failed to forward data to kubectl: %v", err)
						return
					}

					log.Printf("📥 Forwarded %d bytes from client to kubectl", len(message))
				} else {
					// Handle text messages (control messages)
					log.Printf("📨 Received text message from client: %s", string(message))
				}
			}
		}
	}()

	// Keep the connection alive and handle cleanup
	select {
	case <-done:
		log.Printf("📡 TCP stream ended for %s", client.ID)
	}
}

// handleSPDYStream handles SPDY streaming for kubectl port-forward
// This function handles the direct tunnel between kubectl and the user
func (s *Server) handleSPDYStream(w http.ResponseWriter, r *http.Request, client *Client, target string) {
	// Get the underlying connection BEFORE sending any response
	hj, ok := w.(http.Hijacker)
	if !ok {
		log.Printf("❌ ResponseWriter is not a Hijacker")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	conn, bufrw, err := hj.Hijack()
	if err != nil {
		log.Printf("❌ Failed to hijack connection: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	// Send SPDY upgrade response
	bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	bufrw.WriteString("Connection: Upgrade\r\n")
	bufrw.WriteString("Upgrade: SPDY/3.1\r\n")
	bufrw.WriteString("\r\n")
	bufrw.Flush()

	log.Printf("✅ SPDY connection established")

	// Create a channel to coordinate shutdown
	done := make(chan bool)
	defer close(done)

	// Start goroutine to handle SPDY to user streaming (kubectl -> user)
	go func() {
		defer func() {
			log.Printf("📤 SPDY to user stream ended for %s", client.ID)
		}()

		buffer := make([]byte, 4096)
		for {
			select {
			case <-done:
				return
			default:
				// Read from SPDY connection (kubectl)
				n, err := conn.Read(buffer)
				if err != nil {
					if err.Error() != "EOF" {
						log.Printf("❌ SPDY read error: %v", err)
					}
					return
				}

				if n > 0 {
					// Forward data to client via WebSocket (binary message)
					// The client will handle sending it back to the user
					log.Printf("📤 Sending %d bytes from kubectl to client", n)
					if err := client.writeMessage(websocket.BinaryMessage, buffer[:n]); err != nil {
						log.Printf("❌ Failed to forward data to client: %v", err)
						return
					}

					log.Printf("📤 Successfully forwarded %d bytes from kubectl to client", n)
				}
			}
		}
	}()

	// Start goroutine to handle client to SPDY streaming (client -> kubectl)
	go func() {
		defer func() {
			conn.Close()
			log.Printf("📥 Client to SPDY stream ended for %s", client.ID)
		}()

		// Create a channel to receive binary messages from the client
		binaryChan := make(chan []byte, 100)

		// Register this channel with the client for binary message routing
		client.registerBinaryHandler(binaryChan)
		defer client.unregisterBinaryHandler(binaryChan)

		log.Printf("📡 Waiting for binary messages from client for kubectl forwarding")

		for {
			select {
			case <-done:
				return
			case message := <-binaryChan:
				// Forward data to SPDY connection (kubectl)
				log.Printf("📥 Sending %d bytes from client to kubectl", len(message))
				if _, err := conn.Write(message); err != nil {
					log.Printf("❌ Failed to forward data to kubectl: %v", err)
					return
				}

				log.Printf("📥 Successfully sent %d bytes from client to kubectl", len(message))
			}
		}
	}()

	// Keep the connection alive
	select {
	case <-done:
		log.Printf("📡 SPDY stream ended for %s", client.ID)
	}
}

// ProxyRequest forwards an HTTP request to a client and waits for response
func (s *Server) ProxyRequest(w http.ResponseWriter, r *http.Request, client *Client) {
	log.Printf("🌐 Proxying %s %s for %s", r.Method, r.URL.Path, client.ID)

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("❌ Failed to read request body: %v", err)
		http.Error(w, "Failed to read request", http.StatusBadRequest)
		return
	}

	// Convert headers to map with Forward- header transformation
	headers := make(map[string]string)
	for key, values := range r.Header {
		//fmt.Println(key, values)

		// Transform any Forward- headers by removing the prefix
		if strings.HasPrefix(key, "Forward-") {
			// Remove "Forward-" prefix and convert to lowercase
			transformedKey := strings.TrimPrefix(key, "Forward-")
			headers[transformedKey] = values[0]
			log.Printf("🔄 Forward header: %s → %s", key, transformedKey)
		} else {
			headers[key] = values[0] // Take first value
		}
	}
	//headers["Authorization"] = "Bearer eyJhbGciOiJSUzI1NiIsImtpZCI6InNqcmVlbjByQ2lJVTJuOWVBNS1BbzNoY0tIS0gzSGdVZWFlUGR4RVhKWW8ifQ.eyJhdWQiOlsiaHR0cHM6Ly9rdWJlcm5ldGVzLmRlZmF1bHQuc3ZjLmNsdXN0ZXIubG9jYWwiLCJrM3MiXSwiZXhwIjoxNzU0NjQ0OTgyLCJpYXQiOjE3NTQ2NDEzODIsImlzcyI6Imh0dHBzOi8va3ViZXJuZXRlcy5kZWZhdWx0LnN2Yy5jbHVzdGVyLmxvY2FsIiwianRpIjoiODkxOWQxMTUtMjMwZi00NDQ1LTlmYmMtMzU4MDQ5YzdkNzE4Iiwia3ViZXJuZXRlcy5pbyI6eyJuYW1lc3BhY2UiOiJrdWJlLXN5c3RlbSIsInNlcnZpY2VhY2NvdW50Ijp7Im5hbWUiOiJwcm94eS1zYSIsInVpZCI6IjMyYzRjOTU4LWIzZWUtNGQwYi1iY2IwLTMyZmQ1MjhhMWIwZSJ9fSwibmJmIjoxNzU0NjQxMzgyLCJzdWIiOiJzeXN0ZW06c2VydmljZWFjY291bnQ6a3ViZS1zeXN0ZW06cHJveHktc2EifQ.BBjqgwBCt1OvFlF_joW7y99vG4a9I_yG_dfS9IaEwvGptykwsgBtwH037jXnfBbMC7VfgIfyEfZrMBF0ZqTFieQjDChVYKp84kuCaIali6H6rQubQ2IJFmAAfnGA28zaYorjTitdfYm-DjlPazl7BPgbF970eayTEW0zvuAbEsuPZImXXGO0WAvCkjS6fDc95M8E-jcRuyREiDHPAAlb-H0I8O-rjWWjcejtXCoyphpR9UYHlY7v-VNNekPiywS0fHAW8_8UY-AtMiKNCHmYR1Lz6mAxUNE4mIHjCNM9nNN02T1dgj8oQ9yW7L5ewKFFoXraijcWvOYX0S9zOr7mrw"
	headers["Authorization"] = "Bearer eyJhbGciOiJSUzI1NiIsImtpZCI6InNqcmVlbjByQ2lJVTJuOWVBNS1BbzNoY0tIS0gzSGdVZWFlUGR4RVhKWW8ifQ.eyJhdWQiOlsiaHR0cHM6Ly9rdWJlcm5ldGVzLmRlZmF1bHQuc3ZjLmNsdXN0ZXIubG9jYWwiLCJrM3MiXSwiZXhwIjoxNzU0OTkyODczLCJpYXQiOjE3NTQ5ODkyNzMsImlzcyI6Imh0dHBzOi8va3ViZXJuZXRlcy5kZWZhdWx0LnN2Yy5jbHVzdGVyLmxvY2FsIiwianRpIjoiN2Y0NGNkOGMtOWIxZS00ZDhmLThhOGEtYWZkYjYxNzcwMjcyIiwia3ViZXJuZXRlcy5pbyI6eyJuYW1lc3BhY2UiOiJrdWJlLXN5c3RlbSIsInNlcnZpY2VhY2NvdW50Ijp7Im5hbWUiOiJwcm94eS1zYSIsInVpZCI6IjMyYzRjOTU4LWIzZWUtNGQwYi1iY2IwLTMyZmQ1MjhhMWIwZSJ9fSwibmJmIjoxNzU0OTg5MjczLCJzdWIiOiJzeXN0ZW06c2VydmljZWFjY291bnQ6a3ViZS1zeXN0ZW06cHJveHktc2EifQ.bWZS0n-l9e0CDTkOL_3sbxwGALIxNYRq4ZkKRP-TnXt77q0FHHG_ZlPfHn6jD5PIgVeMRgcNVHSbzRGCXvragDA2Gnnw1HCTL3Qy0_x_8gcKw5QdHcESo-KVlmBkXawghr1oRcWmdSH7qQqXr_B7R9LXp1Ath1EF6YUKD5Fgf2Byr9iEm919lL2Ua8Aw7NsUFb-bEFp_oi0pZt-M4TEt1ue5sv8YpeUYQa9O9KXozpNoJ7F5MV4SAB2OOD72prCWpWX8eHFGmsNBkibUUGrTN-xZABEfnR-HEZMVo58P_NjvG61pEnMvEcUcakJ2Y1KOdh0sapjD9BjnJdqjguDzXA"
	// Generate unique request ID
	requestID := fmt.Sprintf("%d", time.Now().UnixNano())
	// Create proxy request message
	proxyMsg := Message{
		Type: "proxy_request",
		Payload: ProxyRequest{
			RequestID: requestID,
			Method:    r.Method,
			Path:      r.URL.Path,
			Headers:   headers,
			Body:      string(body),
		},
	}

	// Create response channel
	responseChan := make(chan ProxyResponse, 1)

	// Register response channel
	s.mutex.Lock()
	if client.ResponseChannels == nil {
		client.ResponseChannels = make(map[string]chan ProxyResponse)
	}
	client.ResponseChannels[requestID] = responseChan
	s.mutex.Unlock()

	// Cleanup function
	defer func() {
		s.mutex.Lock()
		delete(client.ResponseChannels, requestID)
		s.mutex.Unlock()
	}()

	// Send to client
	data, err := json.Marshal(proxyMsg)
	if err != nil {
		log.Printf("❌ Failed to marshal proxy message: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Send with configurable timeout
	client.Conn.SetWriteDeadline(time.Now().Add(s.config.WriteTimeout))
	if err := client.Conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("❌ Failed to send proxy message: %v", err)
		http.Error(w, "Failed to forward request", http.StatusBadGateway)
		return
	}

	// Wait for response with timeout
	select {
	case response := <-responseChan:
		// Write response headers
		for key, value := range response.Headers {
			w.Header().Set(key, value)
		}

		// Write status code
		w.WriteHeader(response.StatusCode)

		// Write body
		w.Write([]byte(response.Body))

		log.Printf("✅ Proxy response sent: %d bytes", len(response.Body))

	case <-time.After(30 * time.Second):
		log.Printf("❌ Proxy request timeout for %s", client.ID)
		http.Error(w, "Request timeout", http.StatusGatewayTimeout)
		return
	}

	// Update client stats
	s.mutex.Lock()
	client.Requests++
	client.BytesIn += int64(len(body))
	client.BytesOut += int64(len(data))
	s.mutex.Unlock()
}

// HandleListClients returns a list of connected clients
func (s *Server) HandleListClients(w http.ResponseWriter, r *http.Request) {
	// Check authentication if configured
	if s.config.Username != "" && s.config.Password != "" {
		user, pass, ok := r.BasicAuth()
		if !ok || user != s.config.Username || pass != s.config.Password {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	s.mutex.RLock()
	clientList := make([]map[string]interface{}, 0, len(s.clients))
	for _, client := range s.clients {
		clientList = append(clientList, map[string]interface{}{
			"id":        client.ID,
			"url":       client.URL,
			"connected": client.Connected,
			"last_seen": client.LastSeen,
			"requests":  client.Requests,
			"bytes_in":  client.BytesIn,
			"bytes_out": client.BytesOut,
		})
	}
	s.mutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"clients": clientList,
		"count":   len(clientList),
		"stats":   s.GetClientStats(),
	})
}

// HandleKubectlWebSocket handles kubectl WebSocket requests (port-forward, exec, etc.)
func (s *Server) HandleKubectlWebSocket(w http.ResponseWriter, r *http.Request, client *Client) {
	log.Printf("🔌 Handling kubectl WebSocket request for client: %s", client.ID)

	// Upgrade to WebSocket
	wsConn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("❌ Failed to upgrade to WebSocket: %v", err)
		return
	}
	defer wsConn.Close()

	log.Printf("✅ WebSocket upgraded for kubectl request")

	// Send tunnel request to client via WebSocket
	tunnelMsg := Message{
		Type: "tcp_tunnel",
		Payload: TCPTunnelRequest{
			Target: "nginx.default", // Default target, can be made configurable
		},
	}

	data, err := json.Marshal(tunnelMsg)
	if err != nil {
		log.Printf("❌ Failed to marshal tunnel request: %v", err)
		return
	}

	// Send to client via WebSocket
	if err := client.writeMessage(websocket.TextMessage, data); err != nil {
		log.Printf("❌ Failed to send tunnel request to client: %v", err)
		return
	}

	log.Printf("📨 Tunnel request sent to client %s", client.ID)

	// Handle WebSocket communication between kubectl and client
	s.handleKubectlWebSocketStream(wsConn, client)
}

// handleKubectlWebSocketStream handles the WebSocket stream between kubectl and client
func (s *Server) handleKubectlWebSocketStream(wsConn *websocket.Conn, client *Client) {
	log.Printf("📡 Starting kubectl WebSocket stream for client: %s", client.ID)

	// Create a channel to coordinate shutdown
	done := make(chan bool)
	defer close(done)

	// Start kubectl to client data flow
	go func() {
		defer func() {
			log.Printf("📤 kubectl to client stream ended")
		}()

		for {
			select {
			case <-done:
				return
			default:
				// Read from kubectl WebSocket
				messageType, message, err := wsConn.ReadMessage()
				if err != nil {
					if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
						log.Printf("❌ kubectl WebSocket read error: %v", err)
					}
					return
				}

				if messageType == websocket.BinaryMessage {
					log.Printf("📤 Received %d bytes from kubectl", len(message))

					// Forward to client via WebSocket
					if err := client.writeMessage(websocket.BinaryMessage, message); err != nil {
						log.Printf("❌ Failed to forward data to client: %v", err)
						return
					}

					log.Printf("📤 Forwarded %d bytes from kubectl to client", len(message))
				}
			}
		}
	}()

	// Keep connection alive
	select {
	case <-done:
		return
	}
}

// HandleStatus returns the server status page
func (s *Server) HandleStatus(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	clientCount := len(s.clients)
	clientList := make([]map[string]string, 0, len(s.clients))
	for _, client := range s.clients {
		clientList = append(clientList, map[string]string{
			"id":  client.ID,
			"url": client.URL,
		})
	}
	s.mutex.RUnlock()

	html := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<head>
    <title>Reverse Proxy Server</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 40px; background: #f5f5f5; }
        .container { max-width: 800px; margin: 0 auto; background: white; padding: 30px; border-radius: 10px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); }
        .status { padding: 20px; background: #e8f4f8; border-radius: 5px; margin: 20px 0; }
        .connected { color: #28a745; font-weight: bold; }
        .disconnected { color: #dc3545; }
        .client { margin: 10px 0; padding: 15px; background: #f8f9fa; border-radius: 5px; border-left: 4px solid #007bff; }
        .stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 20px; margin: 20px 0; }
        .stat-card { background: #f8f9fa; padding: 15px; border-radius: 5px; text-align: center; }
        .stat-number { font-size: 2em; font-weight: bold; color: #007bff; }
        .stat-label { color: #6c757d; font-size: 0.9em; }
    </style>
</head>
<body>
    <div class="container">
        <h1> Reverse Proxy Server</h1>
        <div class="status">
            <h2>Status: <span class="connected">Running</span></h2>
            <p>Connected clients: <strong>%d</strong></p>
            <p><a href="/clients">View Clients (JSON)</a></p>
        </div>
        
        <div class="stats">
            <div class="stat-card">
                <div class="stat-number">%d</div>
                <div class="stat-label">Connected Clients</div>
            </div>
            <div class="stat-card">
                <div class="stat-number">%d</div>
                <div class="stat-label">Max Clients</div>
            </div>
            <div class="stat-card">
                <div class="stat-number">%s</div>
                <div class="stat-label">Domain</div>
            </div>
        </div>
        
        <h3>Connected Clients:</h3>
        <div class="clients">`, clientCount, clientCount, s.config.MaxClients, s.config.Domain)

	for _, client := range clientList {
		html += fmt.Sprintf(`
            <div class="client">
                <strong>%s</strong> - <a href="%s" target="_blank">%s</a>
            </div>`, client["id"], client["url"], client["url"])
	}

	html += `
        </div>
    </div>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html")
	_, err := w.Write([]byte(html))
	if err != nil {
		return
	}
}
