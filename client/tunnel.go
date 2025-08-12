package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Run starts the client tunnel with automatic reconnection
func (c *Client) Run() error {
	retryCount := 0

	for {
		select {
		case <-c.done:
			log.Printf("🛑 Client stopping")
			return nil
		default:
			if err := c.runConnection(); err != nil {
				retryCount++

				// Check if we've exceeded max retries
				if c.config.MaxRetries > 0 && retryCount > c.config.MaxRetries {
					log.Printf("❌ Max retries exceeded (%d), stopping client", c.config.MaxRetries)
					return fmt.Errorf("max retries exceeded: %v", err)
				}

				// Use fixed delay (no exponential backoff)
				delay := c.config.ReconnectDelay

				log.Printf("❌ Connection failed: %v", err)
				log.Printf("🔄 Attempting to reconnect in %v (attempt %d)...", delay, retryCount)
				time.Sleep(delay)
				continue
			}

			// Reset retry count on successful connection
			retryCount = 0
		}
	}
}

// runConnection handles a single connection session
func (c *Client) runConnection() error {
	log.Printf("🔌 Connecting to %s as %s", c.config.ServerURL, c.config.ClientID)

	// Connect to server
	if err := c.connect(); err != nil {
		return fmt.Errorf("failed to connect: %v", err)
	}

	// Send initial registration
	if err := c.sendMessage("register", map[string]string{
		"client_id": c.config.ClientID,
		"timestamp": time.Now().Format(time.RFC3339),
	}); err != nil {
		log.Printf("❌ Failed to send register message: %v", err)
	}

	// Start ping ticker
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Message loop
	for {
		select {
		case <-c.done:
			log.Printf("🛑 Client stopping")
			return nil
		case <-ticker.C:
			// Send ping
			if err := c.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second)); err != nil {
				log.Printf("❌ Ping failed: %v", err)
				return fmt.Errorf("ping failed: %v", err)
			}
			// Send heartbeat
			if err := c.sendMessage("heartbeat", map[string]string{
				"timestamp": time.Now().Format(time.RFC3339),
			}); err != nil {
				log.Printf("❌ Failed to send heartbeat: %v", err)
				return fmt.Errorf("heartbeat failed: %v", err)
			}
		default:
			// Set read deadline with configurable timeout
			c.conn.SetReadDeadline(time.Now().Add(c.config.ReadTimeout))

			// Read message
			log.Printf("🔍 Waiting for message from server...")
			messageType, message, err := c.conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("❌ WebSocket disconnected: %v", err)
				} else {
					log.Printf("✅ WebSocket disconnected normally")
				}

				// Check if it's a timeout error
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					log.Printf("⏰ Read timeout - server may be busy or connection slow")
				}

				log.Printf("🔄 Connection error, will attempt to reconnect: %v", err)
				return fmt.Errorf("read message failed: %v", err)
			}

			log.Printf("📨 Received message type: %d, size: %d bytes", messageType, len(message))

			// Handle message based on type - prioritize text messages (HTTP) over binary (TCP)
			if messageType == websocket.TextMessage {
				// Handle JSON text messages (HTTP requests, etc.) with priority
				if c.isTCPTunnelActive() {
					log.Printf("📨 Processing HTTP request (TCP tunnel active)...")
				} else {
					log.Printf("📨 Processing HTTP request...")
				}
				// Process HTTP requests synchronously to ensure proper handling
				if err := c.handleMessage(message); err != nil {
					log.Printf("❌ Failed to handle message: %v", err)
					// Don't return here, continue processing other messages
				}
			} else if messageType == websocket.BinaryMessage {
				// Handle binary messages (TCP tunnel data) in background
				// This prevents blocking HTTP requests
				// Use a buffered channel to avoid blocking
				log.Printf("📦 Received binary message: %d bytes", len(message))
				go func() {
					select {
					case <-time.After(100 * time.Millisecond):
						// Timeout to prevent blocking
						log.Printf("⚠️ Binary message processing timeout")
					default:
						// Forward to registered handlers
						c.forwardBinaryMessage(message)

						// If TCP tunnel is active (kubectl mode), echo the message back to server
						// This completes the kubectl port-forward loop
						if c.isTCPTunnelActive() {
							log.Printf("📤 Echoing %d bytes back to server (kubectl loop)", len(message))
							if err := c.writeMessage(websocket.BinaryMessage, message); err != nil {
								log.Printf("❌ Failed to echo binary message back to server: %v", err)
							} else {
								log.Printf("📤 Successfully echoed %d bytes back to server", len(message))
							}
						}
					}
				}()
			} else {
				log.Printf("⚠️ Unknown message type: %d", messageType)
			}
		}
	}
}

// connect establishes WebSocket connection to server
func (c *Client) connect() error {
	// Add client ID to URL
	url := fmt.Sprintf("%s?id=%s", c.config.ServerURL, c.config.ClientID)

	// Close existing connection if any
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return fmt.Errorf("failed to connect: %v", err)
	}

	c.conn = conn
	log.Printf("✅ Connected successfully")

	// Set up ping/pong
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Set connection limits
	conn.SetReadLimit(c.config.MaxMessageSize) // Configurable max message size

	return nil
}

// sendMessage sends a message to the server
func (c *Client) sendMessage(msgType string, payload interface{}) error {
	msg := Message{
		Type:    msgType,
		Payload: payload,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	return c.writeMessage(websocket.TextMessage, data)
}

// handleMessage processes incoming messages
func (c *Client) handleMessage(data []byte) error {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return fmt.Errorf("failed to unmarshal message: %v", err)
	}

	log.Printf("📨 Received message: %s", msg.Type)

	switch msg.Type {
	case "proxy_request":
		return c.handleProxyRequest(msg.Payload)
	case "tcp_tunnel":
		return c.handleTCPTunnel(msg.Payload)
	case "tunnel_request":
		return c.handleTunnelRequest(msg.Payload)
	case "kubectl_connection":
		return c.handleKubectlConnection(msg.Payload)
	case "tunnel_ready":
		return c.handleTunnelReady(msg.Payload)
	default:
		log.Printf("⚠️ Unknown message type: %s", msg.Type)
		return nil
	}
}

// Stop stops the client
func (c *Client) Stop() {
	close(c.done)
	if c.conn != nil {
		c.conn.Close()
	}
}

// handleTunnelRequest handles tunnel requests from the server
func (c *Client) handleTunnelRequest(payload interface{}) error {
	// Convert payload to tunnel request
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %v", err)
	}

	var tunnelReq struct {
		TunnelID  string `json:"tunnel_id"`
		Data      string `json:"data"`
		Direction string `json:"direction"`
	}

	if err := json.Unmarshal(payloadBytes, &tunnelReq); err != nil {
		return fmt.Errorf("failed to unmarshal tunnel request: %v", err)
	}

	log.Printf("📡 Received tunnel request: %s -> %s (%d bytes)", tunnelReq.Direction, tunnelReq.TunnelID, len(tunnelReq.Data))

	// Convert string data back to bytes
	data := []byte(tunnelReq.Data)

	// Check if this looks like HTTP text or binary data
	if c.isHTTPText(data) {
		// This looks like HTTP text, process it normally
		requestStr := string(data)
		log.Printf("📡 HTTP Request: %s", requestStr[:min(len(requestStr), 200)])

		// Extract target from tunnel ID (e.g., "tunnel-client1-8081-1234567890" -> "nginx")
		target := "nginx" // Default target for now
		if strings.Contains(tunnelReq.TunnelID, "client1") {
			target = "nginx.default"
		}

		// Connect to the actual pod nginx
		if err := c.processKubectlRequest(target, data); err != nil {
			log.Printf("❌ Failed to process kubectl request: %v", err)
			// Send error response back to kubectl
			c.sendErrorResponseToKubectl(data)
		}
	} else {
		// This is binary data, handle it differently
		log.Printf("📡 Binary data received from kubectl (%d bytes)", len(data))

		// Extract target from tunnel ID
		target := "nginx.default" // Default target for now

		// For binary data, we need to process it according to kubectl's protocol
		if err := c.processBinaryKubectlData(target, data); err != nil {
			log.Printf("❌ Failed to process binary kubectl data: %v", err)
		}
	}

	return nil
}

// handleKubectlConnection handles kubectl connection requests from the server
func (c *Client) handleKubectlConnection(payload interface{}) error {
	// Convert payload to kubectl connection
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %v", err)
	}

	var kubectlConn struct {
		TunnelID string `json:"tunnel_id"`
		Port     int    `json:"port"`
		Action   string `json:"action"`
	}

	if err := json.Unmarshal(payloadBytes, &kubectlConn); err != nil {
		return fmt.Errorf("failed to unmarshal kubectl connection: %v", err)
	}

	log.Printf("🔌 Received kubectl connection request: %s on port %d", kubectlConn.Action, kubectlConn.Port)

	// Send success response
	if err := c.sendTunnelResponse(kubectlConn.TunnelID, true, ""); err != nil {
		return fmt.Errorf("failed to send tunnel response: %v", err)
	}

	log.Printf("✅ Tunnel response sent, waiting for server confirmation...")

	// Don't start kubectl stream yet - wait for server to confirm tunnel is active
	// The server will send a tunnel_response message when the tunnel is ready

	return nil
}

// sendTunnelResponse sends a tunnel response to the server
func (c *Client) sendTunnelResponse(tunnelID string, success bool, errorMsg string) error {
	response := map[string]interface{}{
		"type": "tunnel_response",
		"payload": map[string]interface{}{
			"tunnel_id": tunnelID,
			"success":   success,
			"error":     errorMsg,
		},
	}

	data, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to marshal tunnel response: %v", err)
	}

	return c.writeMessage(websocket.TextMessage, data)
}

// handleTunnelReady handles tunnel ready confirmation from the server
func (c *Client) handleTunnelReady(payload interface{}) error {
	// Convert payload to tunnel ready
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %v", err)
	}

	var tunnelReady struct {
		TunnelID string `json:"tunnel_id"`
		Status   string `json:"status"`
		Message  string `json:"message"`
	}

	if err := json.Unmarshal(payloadBytes, &tunnelReady); err != nil {
		return fmt.Errorf("failed to unmarshal tunnel ready: %v", err)
	}

	log.Printf("🚀 Tunnel %s is ready: %s", tunnelReady.TunnelID, tunnelReady.Message)

	// Now start the kubectl stream since the tunnel is confirmed active
	c.setTCPTunnelActive(true)
	go func() {
		defer c.setTCPTunnelActive(false)
		if err := c.handleKubectlStream("nginx.default"); err != nil {
			log.Printf("❌ kubectl stream error: %v", err)
		}
	}()

	log.Printf("✅ kubectl stream started for tunnel %s", tunnelReady.TunnelID)
	return nil
}
