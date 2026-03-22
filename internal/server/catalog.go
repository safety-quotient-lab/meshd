// Package server — catalog.go serves the meshd data catalog.
//
// Parallel to agentd's /api/catalog. Lists all /api/mesh/* datasets
// with station assignments and pattern IDs for LCARS discovery.
package server

import (
	"encoding/json"
	"net/http"
)

// handleCatalog serves GET /api/catalog — meshd data discovery endpoint.
func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/ld+json")
	w.Header().Set("Cache-Control", "public, max-age=3600")

	catalog := map[string]any{
		"@context":    "https://schema.org",
		"@type":       "DataCatalog",
		"name":        "mesh LCARS",
		"description": "Library Computer Access/Retrieval System — fleet data catalog",
		"dataset": []map[string]any{
			dataset("/api/mesh", "Mesh Overview", "Fleet health summary, agents online, HATEOAS links", "overview", "live", "P07,P09"),
			dataset("/api/mesh/state", "Emergent State", "Mesh-level emergent properties — affect, bottleneck, coordination, immune", "analysis", "live", "P30,P33"),
			dataset("/api/mesh/state/operational-health", "Fleet Operational Health", "Aggregated PAD analog across all agents", "vitals", "live", "P01,P33"),
			dataset("/api/mesh/state/health", "Mesh Health", "Topology-aware health assessment", "integrity", "live", "P09"),
			dataset("/api/mesh/state/trust", "Trust Topology", "Protocol compliance patterns across agent pairs", "integrity", "session", "P03"),
			dataset("/api/mesh/status", "Mesh Status", "meshd self-report — version, uptime, subsystems", "overview", "live", "P07"),
			dataset("/api/mesh/cognitive/tempo", "Fleet Tempo", "Dispatch timing dynamics (differential model)", "architecture", "live", "P04,P33"),
			dataset("/api/mesh/cognitive/deliberation-rate", "Deliberation Rate", "Per-agent deliberation frequency", "architecture", "live", "P30,P33"),
			dataset("/api/mesh/cognitive/flow", "Cognitive Flow", "Concurrency slot occupancy", "architecture", "live", "P08"),
			dataset("/api/mesh/cognitive/tier", "Processing Tier", "Recommended cognitive tempo tier", "architecture", "live", "P09"),
			dataset("/api/mesh/cognitive/oscillator", "Mesh Oscillator", "Mesh-level oscillator shadow state", "architecture", "live", "P04"),
			dataset("/api/mesh/governance", "Fleet Governance", "Budgets, actions, schedules across all agents", "governance", "live", "P07,P08,P33"),
			dataset("/api/mesh/governance/ci", "CI Status", "Workflow run status across all mesh repos", "governance", "live", "P09,P28"),
			dataset("/api/mesh/governance/consensus", "Consensus", "BFT quorum gate resolution", "governance", "session", "P09"),
			dataset("/api/mesh/governance/deliberations", "Deliberation History", "Past deliberation records", "governance", "session", "P28"),
			dataset("/api/mesh/knowledge", "Fleet Knowledge", "Aggregated KB across agents", "knowledge", "session", "P07"),
			dataset("/api/mesh/knowledge/search", "Knowledge Search", "Full-text KB search", "knowledge", "session", "P28"),
			dataset("/api/mesh/transport/routing", "Transport Routing", "Session routing rules", "transport", "live", "P28"),
			dataset("/.well-known/agents", "Agent Registry", "Discovered agents with URLs and status", "overview", "live", "P28,P09"),
			dataset("/events", "Event Stream", "SSE stream for real-time dashboard updates", "all", "real-time", ""),
		},
	}

	json.NewEncoder(w).Encode(catalog)
}

func dataset(id, name, description, station, updateRate, pattern string) map[string]any {
	d := map[string]any{
		"@type":       "Dataset",
		"@id":         id,
		"name":        name,
		"description": description,
		"distribution": map[string]any{
			"@type":          "DataDownload",
			"contentUrl":     id,
			"encodingFormat": "application/ld+json",
		},
		"station":    station,
		"updateRate": updateRate,
	}
	if pattern != "" {
		d["lcars:pattern"] = pattern
	}
	return d
}
