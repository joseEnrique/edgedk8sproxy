package main

// Message represents a WebSocket message
type Message struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// ProxyRequest represents an HTTP request to be proxied
type ProxyRequest struct {
	RequestID string            `json:"request_id"`
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body"`
}

// ProxyResponse represents an HTTP response from the client
type ProxyResponse struct {
	RequestID  string            `json:"request_id"`
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
}

type RegisterMessage struct {
	ClientID  string `json:"client_id"`
	Timestamp string `json:"timestamp"`
}

type HeartbeatMessage struct {
	Timestamp string `json:"timestamp"`
}

// TCPTunnelRequest represents a TCP tunnel request
type TCPTunnelRequest struct {
	Target string `json:"target"` // e.g., "service:port"
}

// TCPTunnelResponse represents a TCP tunnel response
type TCPTunnelResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}
