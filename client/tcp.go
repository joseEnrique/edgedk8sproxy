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

// handleTCPTunnel handles TCP tunnel requests from the server
func (c *Client) handleTCPTunnel(payload interface{}) error {
	// Convert payload to TCPTunnelRequest
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %v", err)
	}

	var tunnelReq TCPTunnelRequest
	if err := json.Unmarshal(payloadBytes, &tunnelReq); err != nil {
		return fmt.Errorf("failed to unmarshal tunnel request: %v", err)
	}

	log.Printf("🔌 TCP tunnel request for target: %s", tunnelReq.Target)

	// Check if this is a kubectl port-forward request
	// kubectl port-forward targets don't have a port number
	if !strings.Contains(tunnelReq.Target, ":") {
		// This is a kubectl port-forward - no need to connect locally
		log.Printf("🔌 kubectl port-forward detected for target: %s", tunnelReq.Target)
		log.Printf("📡 kubectl will handle the actual connection to the pod")

		// Send success response immediately
		if err := c.sendTCPTunnelResponse(true, ""); err != nil {
			return err
		}

		// Start kubectl stream in a separate goroutine
		c.setTCPTunnelActive(true)
		go func() {
			defer c.setTCPTunnelActive(false)
			if err := c.handleKubectlStream(tunnelReq.Target); err != nil {
				log.Printf("❌ kubectl stream error: %v", err)
			}
		}()

		return nil
	}

	// Regular TCP tunnel - connect to the configured Kubernetes API
	localAddr := fmt.Sprintf("%s:%d", c.config.LocalHost, c.config.LocalPort)
	log.Printf("🔌 Connecting to Kubernetes API: %s", localAddr)
	log.Printf("🎯 Target requested by server: %s (will be handled by kubectl)", tunnelReq.Target)

	conn, err := net.Dial("tcp", localAddr)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to connect to target %s: %v", localAddr, err)
		log.Printf("❌ %s", errMsg)
		return c.sendTCPTunnelResponse(false, errMsg)
	}
	defer conn.Close()

	log.Printf("✅ TCP tunnel established to %s", localAddr)

	// Send success response
	if err := c.sendTCPTunnelResponse(true, ""); err != nil {
		return err
	}

	// Start TCP streaming in a separate goroutine to avoid blocking HTTP requests
	c.setTCPTunnelActive(true)
	go func() {
		defer c.setTCPTunnelActive(false)
		if err := c.handleTCPStream(conn, tunnelReq.Target); err != nil {
			log.Printf("❌ TCP stream error: %v", err)
		}
	}()

	return nil
}

// handleTCPStream handles the actual TCP streaming between local service and server
func (c *Client) handleTCPStream(tcpConn net.Conn, target string) error {
	log.Printf("📡 Starting TCP stream for target: %s", target)

	// Create a channel to coordinate shutdown
	done := make(chan bool)
	defer close(done)

	// Start goroutine to handle TCP to WebSocket streaming (local service -> server)
	go func() {
		defer func() {
			tcpConn.Close()
			log.Printf("📤 TCP to WebSocket stream ended for target: %s", target)
		}()

		buffer := make([]byte, 4096)
		for {
			select {
			case <-done:
				return
			default:
				// Read from TCP connection (local service)
				n, err := tcpConn.Read(buffer)
				if err != nil {
					if err.Error() == "EOF" {
						log.Printf("📡 TCP connection closed by remote (EOF)")
					} else if strings.Contains(err.Error(), "use of closed network connection") {
						log.Printf("📡 TCP connection was closed")
					} else {
						log.Printf("❌ TCP read error: %v", err)
					}
					return
				}

				if n > 0 {
					// Forward data to server via WebSocket
					if err := c.writeMessage(websocket.BinaryMessage, buffer[:n]); err != nil {
						log.Printf("❌ Failed to forward data to server: %v", err)
						return
					}

					// Only log occasionally to avoid spam
					if n > 1000 {
						log.Printf("📤 Forwarded %d bytes from local service to server", n)
					}
				}
			}
		}
	}()

	// Start goroutine to handle WebSocket to TCP streaming (server -> local service)
	go func() {
		defer func() {
			tcpConn.Close()
			log.Printf("📥 WebSocket to TCP stream ended for target: %s", target)
		}()

		// Create a channel to receive binary messages from the main loop
		binaryChan := make(chan []byte, 100)

		// Register this channel with the client for binary message routing
		c.registerBinaryHandler(binaryChan)
		defer c.unregisterBinaryHandler(binaryChan)

		for {
			select {
			case <-done:
				return
			case message := <-binaryChan:
				// Forward data to TCP connection (local service)
				if _, err := tcpConn.Write(message); err != nil {
					if strings.Contains(err.Error(), "use of closed network connection") {
						log.Printf("📡 TCP connection was closed, stopping write")
					} else {
						log.Printf("❌ Failed to forward data to local service: %v", err)
					}
					return
				}

				// Only log occasionally to avoid spam
				if len(message) > 1000 {
					log.Printf("📥 Forwarded %d bytes from server to local service", len(message))
				}
			}
		}
	}()

	// Keep the connection alive and monitor for closure
	select {
	case <-done:
		log.Printf("📡 TCP stream ended for target: %s", target)
	}

	// Ensure connection is closed
	tcpConn.Close()
	log.Printf("📡 TCP tunnel cleanup completed for target: %s", target)

	return nil
}

// sendTCPTunnelResponse sends a TCP tunnel response back to the server
func (c *Client) sendTCPTunnelResponse(success bool, errorMsg string) error {
	response := Message{
		Type: "tcp_tunnel_response",
		Payload: TCPTunnelResponse{
			Success: success,
			Error:   errorMsg,
		},
	}

	data, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to marshal tunnel response: %v", err)
	}

	if err := c.writeMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("failed to send tunnel response: %v", err)
	}

	return nil
}

// handleKubectlStream handles the kubectl port-forward stream
// This connects to the actual pod and processes HTTP requests
func (c *Client) handleKubectlStream(target string) error {
	log.Printf("📡 Starting kubectl stream for target: %s", target)

	// Create a channel to coordinate shutdown
	done := make(chan bool)
	defer close(done)

	// For kubectl port-forward, we need to connect to the actual pod
	// and process HTTP requests, then send responses back

	// Create a channel to receive binary messages from the main loop
	binaryChan := make(chan []byte, 100)

	// Register this channel with the client for binary message routing
	c.registerBinaryHandler(binaryChan)
	defer c.unregisterBinaryHandler(binaryChan)

	log.Printf("📡 kubectl stream ready - will connect to pod %s", target)

	// Keep the stream alive and process requests
	for {
		select {
		case <-done:
			log.Printf("📡 kubectl stream ended for target: %s", target)
			return nil
		case message := <-binaryChan:
			// Process the data from kubectl
			log.Printf("📡 Processing data from kubectl (%d bytes)", len(message))

			// Check if this looks like HTTP text or binary data
			if c.isHTTPText(message) {
				// This looks like HTTP text, process it normally
				requestStr := string(message)
				log.Printf("📡 HTTP Request: %s", requestStr[:min(len(requestStr), 200)])

				// Connect to the actual pod nginx
				if err := c.processKubectlRequest(target, message); err != nil {
					log.Printf("❌ Failed to process kubectl request: %v", err)
					// Send error response back to kubectl
					c.sendErrorResponseToKubectl(message)
				}
			} else {
				// This is binary data, handle it differently
				log.Printf("📡 Binary data received from kubectl (%d bytes)", len(message))

				// For binary data, we need to process it according to kubectl's protocol
				if err := c.processBinaryKubectlData(target, message); err != nil {
					log.Printf("❌ Failed to process binary kubectl data: %v", err)
				}
			}
		}
	}
}

// processKubectlRequest processes a kubectl request by connecting to the actual pod
func (c *Client) processKubectlRequest(target string, requestData []byte) error {
	// Extract pod information from target
	// Target format: "nginx.default" or "nginx"
	var podName, namespace string

	if strings.Contains(target, ".") {
		parts := strings.Split(target, ".")
		podName = parts[0]
		namespace = parts[1]
	} else {
		podName = target
		namespace = "default"
	}

	log.Printf("🎯 Connecting to pod %s in namespace %s", podName, namespace)

	// For now, we'll simulate a connection to the pod
	// In a real implementation, this would use the Kubernetes client to connect to the pod
	log.Printf("📡 Simulating connection to pod %s.%s", podName, namespace)

	// Parse the HTTP request to understand what we need to do
	requestStr := string(requestData)

	// Extract the HTTP method and path
	lines := strings.Split(requestStr, "\n")
	if len(lines) == 0 {
		return fmt.Errorf("invalid HTTP request")
	}

	// Parse first line: "GET / HTTP/1.1"
	firstLine := strings.TrimSpace(lines[0])
	parts := strings.Fields(firstLine)
	if len(parts) < 2 {
		return fmt.Errorf("invalid HTTP request line: %s", firstLine)
	}

	method := parts[0]
	path := parts[1]

	log.Printf("📡 HTTP %s %s", method, path)

	// Generate a mock response (in real implementation, this would come from the pod)
	response := c.generateMockResponse(method, path)

	// Send the response back to kubectl via the server
	if err := c.sendResponseToKubectl(response); err != nil {
		return fmt.Errorf("failed to send response to kubectl: %v", err)
	}

	log.Printf("✅ Successfully processed kubectl request and sent response")
	return nil
}

// generateMockResponse generates a mock HTTP response for testing
func (c *Client) generateMockResponse(method, path string) []byte {
	// Generate a simple HTML response
	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <title>Pod Response</title>
</head>
<body>
    <h1>Response from Pod</h1>
    <p>Method: %s</p>
    <p>Path: %s</p>
    <p>Timestamp: %s</p>
    <p>This is a mock response. In production, this would come from the actual pod.</p>
</body>
</html>`, method, path, time.Now().Format(time.RFC3339))

	response := fmt.Sprintf("HTTP/1.1 200 OK\r\n"+
		"Content-Type: text/html\r\n"+
		"Content-Length: %d\r\n"+
		"Connection: close\r\n"+
		"\r\n"+
		"%s", len(html), html)

	return []byte(response)
}

// sendResponseToKubectl sends the response back to kubectl via the server
func (c *Client) sendResponseToKubectl(responseData []byte) error {
	// Send the response back to the server via WebSocket
	// The server will forward it to kubectl
	if err := c.conn.WriteMessage(websocket.BinaryMessage, responseData); err != nil {
		return fmt.Errorf("failed to send response to server: %v", err)
	}

	log.Printf("📤 Sent response (%d bytes) to kubectl via server", len(responseData))
	return nil
}

// isHTTPText checks if the data looks like HTTP text
func (c *Client) isHTTPText(data []byte) bool {
	// Check if the first few bytes look like HTTP
	if len(data) < 4 {
		return false
	}

	// Common HTTP methods
	httpMethods := []string{"GET ", "POST", "PUT ", "DELE", "HEAD", "OPTI", "PATC"}
	start := string(data[:4])

	for _, method := range httpMethods {
		if start == method {
			return true
		}
	}

	// Check if it starts with HTTP response
	if len(data) >= 8 && string(data[:8]) == "HTTP/1." {
		return true
	}

	return false
}

// processBinaryKubectlData processes binary data from kubectl
func (c *Client) processBinaryKubectlData(target string, data []byte) error {
	log.Printf("📡 Processing binary kubectl data for target: %s", target)

	// For binary data, we need to understand kubectl's protocol
	// kubectl port-forward uses a specific binary protocol

	// Extract pod information from target
	var podName, namespace string

	if strings.Contains(target, ".") {
		parts := strings.Split(target, ".")
		podName = parts[0]
		namespace = parts[1]
	} else {
		podName = target
		namespace = "default"
	}

	log.Printf("🎯 Target pod: %s.%s", podName, namespace)

	// For now, we'll generate a simple response
	// In a real implementation, this would connect to the actual pod
	log.Printf("📡 Generating response for binary kubectl request")

	// Generate a response that kubectl can understand
	response := c.generateBinaryResponse(data)

	// Send the response back to kubectl
	if err := c.sendResponseToKubectl(response); err != nil {
		return fmt.Errorf("failed to send binary response to kubectl: %v", err)
	}

	log.Printf("✅ Successfully processed binary kubectl data and sent response")
	return nil
}

// generateBinaryResponse generates a response for binary kubectl data
func (c *Client) generateBinaryResponse(requestData []byte) []byte {
	// For binary data, we'll generate a simple text response
	// This is a placeholder - in production you'd need to understand kubectl's protocol

	responseText := fmt.Sprintf("Response from pod for binary request (%d bytes)\n"+
		"Timestamp: %s\n"+
		"This is a placeholder response for binary kubectl data.\n"+
		"In production, this would be the actual response from the pod.\n",
		len(requestData), time.Now().Format(time.RFC3339))

	// Convert to bytes
	return []byte(responseText)
}

// sendErrorResponseToKubectl sends an error response back to kubectl
func (c *Client) sendErrorResponseToKubectl(requestData []byte) {
	errorResponse := "HTTP/1.1 500 Internal Server Error\r\n" +
		"Content-Type: text/plain\r\n" +
		"Content-Length: 35\r\n" +
		"Connection: close\r\n" +
		"\r\n" +
		"Failed to process kubectl request"

	if err := c.conn.WriteMessage(websocket.BinaryMessage, []byte(errorResponse)); err != nil {
		log.Printf("❌ Failed to send error response: %v", err)
	}
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
