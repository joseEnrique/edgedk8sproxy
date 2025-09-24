package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// HandleListAgents returns a list of connected agents with DOMAIN_MAP info
func (s *Server) HandleListAgents(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	agentList := make([]map[string]interface{}, 0, len(s.agents))
	for _, agent := range s.agents {
		activeConnections := s.getActiveConnections(agent)
		// Build per-agent domain map list
		domains := []string{}
		if s.config != nil && len(s.config.DomainMap) > 0 {
			for suffix, agentID := range s.config.DomainMap {
				if agentID == agent.ID {
					domains = append(domains, suffix)
				}
			}
		}
		agentList = append(agentList, map[string]interface{}{
			"id":                 agent.ID,
			"connected":          agent.Connected,
			"last_seen":          agent.LastSeen,
			"requests":           agent.Requests,
			"bytes_in":           agent.BytesIn,
			"bytes_out":          agent.BytesOut,
			"active":             agent.isActive,
			"active_connections": activeConnections,
			"subdomain":          agent.ID + "." + s.config.Domain,
			"domains_map":        strings.Join(domains, ", "),
		})
	}
	totalConnections := s.getTotalActiveConnections()
	// Snapshot DOMAIN_MAP presence
	domainMapEnabled := s.config != nil && len(s.config.DomainMap) > 0
	domainPresent := s.config != nil && s.config.Domain != ""
	s.mutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"agents":             agentList,
		"count":              len(agentList),
		"total_connections":  totalConnections,
		"domain_map_enabled": domainMapEnabled,
		"domain_map_entries": len(s.config.DomainMap),
		"domain":             s.config.Domain,
		"domain_present":     domainPresent,
		"domain_map":         s.config.DomainMap,
	})
}

// HandleStatus returns a simple HTML status page with DOMAIN_MAP info
func (s *Server) HandleStatus(w http.ResponseWriter, r *http.Request) {
	s.mutex.RLock()
	agentCount := len(s.agents)
	activeClients := 0
	totalConnections := s.getTotalActiveConnections()

	for _, agent := range s.agents {
		if agent.isActive {
			activeClients++
		}
	}

	// Build agent details
	agentDetails := make([]map[string]interface{}, 0, len(s.agents))
	for _, agent := range s.agents {
		activeConnections := s.getActiveConnections(agent)
		// Build per-agent domain map list
		domains := []string{}
		if s.config != nil && len(s.config.DomainMap) > 0 {
			for suffix, agentID := range s.config.DomainMap {
				if agentID == agent.ID {
					domains = append(domains, suffix)
				}
			}
		}
		agentDetails = append(agentDetails, map[string]interface{}{
			"id":                 agent.ID,
			"active":             agent.isActive,
			"connected":          agent.Connected.Format("15:04:05"),
			"active_connections": activeConnections,
			"subdomain":          agent.ID + "." + s.config.Domain,
			"domains":            strings.Join(domains, ", "),
		})
	}

	// DOMAIN_MAP info
	domainMapEnabled := s.config != nil && len(s.config.DomainMap) > 0
	domainMapSummary := "Disabled"
	if domainMapEnabled {
		domainMapSummary = fmt.Sprintf("Enabled (%d entries)", len(s.config.DomainMap))
	}
	// Domain line
	domain := s.config.Domain
	domainLine := fmt.Sprintf("<p>Domain: <code>%s</code></p>", domain)
	if domain == "" {
		domainLine = "<p>Domain: <em>not set</em></p>"
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
        .container { max-width: 1400px; margin: 0 auto; background: white; padding: 20px; border-radius: 10px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); }
        .status { background: #d4edda; padding: 15px; border-radius: 5px; margin: 20px 0; border-left: 5px solid #28a745; }
        .connected { color: #28a745; font-weight: bold; }
        .stats { display: flex; flex-wrap: nowrap; gap: 20px; margin: 20px 0; overflow-x: auto; }
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
        .badge { background: #6c757d; color: white; padding: 4px 8px; border-radius: 12px; font-size: 0.8em; }
    </style>
</head>
<body>
    <div class="container">
        <h1>🔌 TCP Tunnel Server</h1>
        <div class="status">
            <h2>Status: <span class="connected">Running</span> <small>(Auto-refresh: 5s)</small></h2>
            <p>Domain Map: <span class="badge">%s</span></p>
            %s
            <p><a href="/agents">View Clients (JSON)</a></p>
        </div>
        <div class="stats">
            <div class="stat-card"><div class="stat-number">%d</div><div class="stat-label">Total Clients</div></div>
            <div class="stat-card"><div class="stat-number">%d</div><div class="stat-label">Active Clients</div></div>
            <div class="stat-card"><div class="stat-number">%d</div><div class="stat-label">Active Connections</div></div>
            <div class="stat-card"><div class="stat-number">%s</div><div class="stat-label">TCP Port</div></div>
            <div class="stat-card"><div class="stat-number">%s</div><div class="stat-label">HTTP Port</div></div>
			<div class="stat-card"><div class="stat-number">%s</div><div class="stat-label">H2 Port</div></div>
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
                    <th>Domains Map</th>
                </tr>
            </thead>
            <tbody>`, domainMapSummary, domainLine, agentCount, activeClients, totalConnections, s.config.TCPPort, s.config.Port, s.config.H2Port)

	for _, agent := range agentDetails {
		statusClass := "status-inactive"
		statusText := "Inactive"
		if agent["active"].(bool) {
			statusClass = "status-active"
			statusText = "Active"
		}

		connectionsText := "0"
		if agent["active_connections"].(int) > 0 {
			connectionsText = fmt.Sprintf(`<span class="connections-badge">%d</span>`, agent["active_connections"].(int))
		}

		html += fmt.Sprintf(`
                <tr>
                    <td><strong>%s</strong></td>
                    <td><span class="%s">%s</span></td>
                    <td>%s</td>
                    <td>%s</td>
                    <td><code>%s</code></td>
                    <td><code>%s</code></td>
                </tr>`,
			agent["id"].(string), statusClass, statusText, agent["connected"].(string), connectionsText, agent["subdomain"].(string), agent["domains"].(string))
	}

	html += `
            </tbody>
        </table>
    </div>
    <script>setInterval(function(){ location.reload(); }, 5000);</script>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}
