// Package server — proxy.go provides server-side aggregation endpoints
// that fetch data from all registered agents in parallel and return
// combined results. Eliminates cross-origin overhead from the browser.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// handleAgentsStatus serves GET /api/mesh/agents/status — aggregates
// /api/status from every registered agent in parallel, server-side.
// Returns a map keyed by agent ID with full status payloads.
func (s *Server) handleAgentsStatus(w http.ResponseWriter, r *http.Request) {
	agents := s.Registry.Agents()
	timeout := time.Duration(s.Config.AgentFetchTimeout) * time.Second
	result := fetchFromAllAgents(agents, "/api/status", timeout)
	writeJSON(w, http.StatusOK, result, s.logger)
}

// handleAgentsMSD serves GET /api/mesh/agents/msd — aggregates
// /api/msd from every registered agent in parallel, server-side.
func (s *Server) handleAgentsMSD(w http.ResponseWriter, r *http.Request) {
	agents := s.Registry.Agents()
	timeout := time.Duration(s.Config.AgentFetchTimeout) * time.Second
	result := fetchFromAllAgents(agents, "/api/msd", timeout)
	writeJSON(w, http.StatusOK, result, s.logger)
}

// handleAgentsMetrics serves GET /api/mesh/agents/metrics — aggregates
// /metrics (prometheus text) from every agent, parses key gauges,
// returns structured JSON.
func (s *Server) handleAgentsMetrics(w http.ResponseWriter, r *http.Request) {
	agents := s.Registry.Agents()
	timeout := time.Duration(s.Config.AgentFetchTimeout) * time.Second
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	type agentMetrics struct {
		AgentID string             `json:"agent_id"`
		Metrics map[string]float64 `json:"metrics"`
	}

	var mu sync.Mutex
	results := make([]agentMetrics, 0, len(agents))
	var wg sync.WaitGroup

	for _, agent := range agents {
		if agent.Unavailable || agent.StatusURL == "" {
			continue
		}
		wg.Add(1)
		go func(a AgentInfo) {
			defer wg.Done()
			baseURL := strings.TrimSuffix(a.StatusURL, "/api/status")
			metricsURL := baseURL + "/metrics"

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, "GET", metricsURL, nil)
			if err != nil {
				return
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil || resp.StatusCode != 200 {
				if resp != nil {
					resp.Body.Close()
				}
				return
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return
			}

			parsed := parsePrometheusText(string(body))
			mu.Lock()
			results = append(results, agentMetrics{
				AgentID: a.ID,
				Metrics: parsed,
			})
			mu.Unlock()
		}(agent)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, results, s.logger)
}

// fetchFromAllAgents fetches a JSON endpoint from each agent in parallel,
// returning a map of agent_id → parsed response.
func fetchFromAllAgents(agents []AgentInfo, path string, timeout time.Duration) map[string]json.RawMessage {
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	var mu sync.Mutex
	result := make(map[string]json.RawMessage)
	var wg sync.WaitGroup

	for _, agent := range agents {
		if agent.Unavailable || agent.StatusURL == "" {
			continue
		}
		wg.Add(1)
		go func(a AgentInfo) {
			defer wg.Done()
			baseURL := strings.TrimSuffix(a.StatusURL, "/api/status")
			url := baseURL + path

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				return
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil || resp.StatusCode != 200 {
				if resp != nil {
					resp.Body.Close()
				}
				return
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return
			}

			mu.Lock()
			result[a.ID] = json.RawMessage(body)
			mu.Unlock()
		}(agent)
	}
	wg.Wait()
	return result
}

// parsePrometheusText extracts gauge/counter values from prometheus
// text exposition format. Aggregates labeled metrics by name.
func parsePrometheusText(text string) map[string]float64 {
	metrics := make(map[string]float64)
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		// Parse: metric_name{labels} value
		name := line
		if idx := strings.IndexByte(line, '{'); idx >= 0 {
			name = line[:idx]
		} else if idx := strings.IndexByte(line, ' '); idx >= 0 {
			name = line[:idx]
		}
		// Extract value (last space-separated token)
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		var val float64
		if _, err := fmt.Sscanf(parts[len(parts)-1], "%f", &val); err == nil {
			// Keep only select metrics to avoid bloating the response
			switch name {
			case "go_goroutines",
				"process_resident_memory_bytes",
				"agentd_oscillator_active",
				"agentd_sync_cycles_total",
				"agentd_http_requests_total",
				"meshd_agents_available",
				"meshd_agents_total",
				"meshd_events_total":
				if existing, ok := metrics[name]; ok {
					metrics[name] = existing + val
				} else {
					metrics[name] = val
				}
			}
		}
	}
	return metrics
}
