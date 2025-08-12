package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// handleProxyRequest handles incoming proxy requests
func (c *Client) handleProxyRequest(payload interface{}) error {
	// Convert payload to ProxyRequest
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %v", err)
	}

	var proxyReq ProxyRequest
	if err := json.Unmarshal(payloadBytes, &proxyReq); err != nil {
		return fmt.Errorf("failed to unmarshal proxy request: %v", err)
	}

	log.Printf("🌐 Proxying %s %s to local service", proxyReq.Method, proxyReq.Path)

	// Check if this is a port-forward request
	// Port-forward requests typically go to root path or have specific patterns
	isPortForward := proxyReq.Path == "/" ||
		strings.HasPrefix(proxyReq.Path, "/api/") ||
		strings.Contains(proxyReq.Path, "portforward")

	if isPortForward {
		log.Printf("🔌 Port-forward request detected, connecting to Kubernetes API")
		// For port-forward, connect to the Kubernetes API (not local port-forward)
		schema := c.config.LocalSchema
		if schema == "" {
			schema = "https" // Kubernetes API typically uses HTTPS
		}
		localURL := fmt.Sprintf("%s://%s:%d%s", schema, c.config.LocalHost, c.config.LocalPort, proxyReq.Path)
		log.Printf("🎯 Connecting to Kubernetes API: %s", localURL)

		// Create HTTP request to Kubernetes API
		req, err := http.NewRequest(proxyReq.Method, localURL, strings.NewReader(proxyReq.Body))
		if err != nil {
			return fmt.Errorf("failed to create request: %v", err)
		}

		// Add headers
		log.Printf("📋 Headers received from server:")
		for key, value := range proxyReq.Headers {
			log.Printf("🔄 Using transformed header: %s = %s", key, value)
			req.Header.Set(key, value)
		}

		// Make request to Kubernetes API with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		req = req.WithContext(ctx)
		resp, err := c.httpClient.Do(req) // Use configured HTTP client with TLS settings
		if err != nil {
			log.Printf("❌ Failed to make request to Kubernetes API: %v", err)
			// Send error response back to server
			return c.sendProxyResponse(proxyReq.RequestID, 500, map[string]string{
				"Content-Type": "text/plain",
			}, fmt.Sprintf("Failed to connect to Kubernetes API: %v", err))
		}
		defer resp.Body.Close()

		// Read response body
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response body: %v", err)
		}

		// Convert headers to map
		headers := make(map[string]string)
		for key, values := range resp.Header {
			headers[key] = values[0] // Take first value
		}

		log.Printf("📥 Kubernetes API response: %d, Content-Length: %d", resp.StatusCode, len(bodyBytes))

		// Send response back to server
		return c.sendProxyResponse(proxyReq.RequestID, resp.StatusCode, headers, string(bodyBytes))
	}

	// Regular proxy request - use configured local service
	log.Printf("🌐 Regular proxy request, using configured local service")

	// Create local URL with schema detection
	schema := c.config.LocalSchema
	if schema == "" {
		// Auto-detect schema based on port
		schema = "http"
		if c.config.LocalPort == 443 {
			schema = "https"
		}
	}
	localURL := fmt.Sprintf("%s://%s:%d%s", schema, c.config.LocalHost, c.config.LocalPort, proxyReq.Path)

	// Create HTTP request
	req, err := http.NewRequest(proxyReq.Method, localURL, strings.NewReader(proxyReq.Body))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}

	// Add headers
	log.Printf("📋 Headers received from server:")
	for key, value := range proxyReq.Headers {

		log.Printf("🔄 Using transformed header: %s = %s", key, value)

		req.Header.Set(key, value)
	}

	// Make request to local service with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req = req.WithContext(ctx)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("❌ Failed to make request to local service: %v", err)
		// Send error response back to server
		return c.sendProxyResponse(proxyReq.RequestID, 500, map[string]string{
			"Content-Type": "text/plain",
		}, fmt.Sprintf("Failed to connect to local service: %v", err))
	}
	defer resp.Body.Close()

	// Read response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %v", err)
	}

	// Convert headers to map
	headers := make(map[string]string)
	for key, values := range resp.Header {
		headers[key] = values[0] // Take first value
	}

	log.Printf("📥 Response: %d, Content-Length: %d", resp.StatusCode, len(bodyBytes))

	// Send response back to server
	return c.sendProxyResponse(proxyReq.RequestID, resp.StatusCode, headers, string(bodyBytes))
}

// sendProxyResponse sends a proxy response back to the server
func (c *Client) sendProxyResponse(requestID string, statusCode int, headers map[string]string, body string) error {
	response := Message{
		Type: "proxy_response",
		Payload: ProxyResponse{
			RequestID:  requestID,
			StatusCode: statusCode,
			Headers:    headers,
			Body:       body,
		},
	}

	data, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to marshal proxy response: %v", err)
	}

	if err := c.writeMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("failed to send proxy response: %v", err)
	}

	log.Printf("✅ Proxy response sent: %d for request: %s", statusCode, requestID)
	return nil
}
