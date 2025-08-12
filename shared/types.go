package shared

import (
	"time"
)

// Message represents a WebSocket message
type Message struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// TunnelData represents data flowing through a tunnel
type TunnelData struct {
	TunnelID  string `json:"tunnel_id"`
	Data      []byte `json:"data"`
	Direction string `json:"direction"` // "kubectl_to_client" or "client_to_kubectl"
}

// TunnelResponse represents a response to a tunnel request
type TunnelResponse struct {
	TunnelID string `json:"tunnel_id"`
	Success  bool   `json:"success"`
	Error    string `json:"error,omitempty"`
}

// KubectlConnection represents a kubectl connection request
type KubectlConnection struct {
	TunnelID string `json:"tunnel_id"`
	Port     int    `json:"port"`
	Action   string `json:"action"`
}

// ClientInfo represents client information
type ClientInfo struct {
	ID             string    `json:"id"`
	URL            string    `json:"url"`
	Connected      time.Time `json:"connected"`
	LastSeen       time.Time `json:"last_seen"`
	Requests       int64     `json:"requests"`
	BytesIn        int64     `json:"bytes_in"`
	BytesOut       int64     `json:"bytes_out"`
	StreamingReady bool      `json:"streaming_ready"`
	ActiveTunnels  int       `json:"active_tunnels"`
}
