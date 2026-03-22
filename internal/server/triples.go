// Package server — triples.go serves the triple store query API
// and the ontology namespace document.
//
// GET /api/triples — query triples by subject, predicate, object, graph
// GET /ns/mesh/ontology.jsonld — serve the ontology definition
// GET /api/triples/stats — triple count per named graph
package server

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/safety-quotient-lab/meshd/internal/triplestore"
)

// handleTriples serves GET /api/triples — query the triple store.
// Accepts query parameters: subject, predicate, object, graph.
// Content negotiation: application/ld+json (default), application/n-triples.
func (s *Server) handleTriples(w http.ResponseWriter, r *http.Request) {
	if s.TripleStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "triple store not initialized",
		}, s.logger)
		return
	}

	subject := r.URL.Query().Get("subject")
	predicate := r.URL.Query().Get("predicate")
	object := r.URL.Query().Get("object")
	graph := r.URL.Query().Get("graph")

	result, err := s.TripleStore.QueryCurrent(subject, predicate, object, graph)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		}, s.logger)
		return
	}

	// Check Accept header for content negotiation
	accept := r.Header.Get("Accept")
	if accept == "application/n-triples" {
		w.Header().Set("Content-Type", "application/n-triples")
		for _, t := range result.Triples {
			// Simple N-Triples serialization (prefix-compressed)
			obj := t.Object
			if t.ObjectType == "literal" {
				obj = `"` + t.Object + `"`
				if t.Datatype != "" {
					obj += "^^<" + t.Datatype + ">"
				}
			} else {
				obj = "<" + t.Object + ">"
			}
			line := "<" + t.Subject + "> <" + t.Predicate + "> " + obj + " .\n"
			w.Write([]byte(line))
		}
		return
	}

	// Default: JSON-LD response
	triples := make([]map[string]any, 0, len(result.Triples))
	for _, t := range result.Triples {
		triple := map[string]any{
			"subject":   t.Subject,
			"predicate": t.Predicate,
			"object":    t.Object,
		}
		if t.ObjectType != "literal" {
			triple["object_type"] = t.ObjectType
		}
		if t.Datatype != "" {
			triple["datatype"] = t.Datatype
		}
		if t.Graph != "default" {
			triple["graph"] = t.Graph
		}
		if t.CreatedAt != "" {
			triple["created_at"] = t.CreatedAt
		}
		triples = append(triples, triple)
	}

	w.Header().Set("Content-Type", "application/ld+json")
	w.Header().Set("Cache-Control", "public, max-age=10")
	writeJSON(w, http.StatusOK, map[string]any{
		"@context": "https://safety-quotient.dev/ns/mesh/ontology.jsonld",
		"@type":    "schema:Dataset",
		"triples":  triples,
		"count":    result.Count,
		"query": map[string]string{
			"subject":   subject,
			"predicate": predicate,
			"object":    object,
			"graph":     graph,
		},
	}, s.logger)
}

// handleSparql serves GET /api/sparql — execute a SPARQL query.
// Query passed via ?query= parameter or POST body.
func (s *Server) handleSparql(w http.ResponseWriter, r *http.Request) {
	if s.TripleStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "triple store not initialized",
		}, s.logger)
		return
	}

	var query string
	if r.Method == "POST" {
		body := make([]byte, 1<<16) // 64KB max
		n, _ := r.Body.Read(body)
		query = string(body[:n])
		// Check Content-Type for application/sparql-query
		if ct := r.Header.Get("Content-Type"); ct == "application/sparql-query" {
			// Query is the raw body
		} else {
			// Might be form-encoded
			if q := r.FormValue("query"); q != "" {
				query = q
			}
		}
	} else {
		query = r.URL.Query().Get("query")
	}

	if strings.TrimSpace(query) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "missing query parameter — use ?query=SELECT...",
		}, s.logger)
		return
	}

	result := s.TripleStore.ExecuteSparql(query)

	w.Header().Set("Content-Type", "application/sparql-results+json")
	w.Header().Set("Cache-Control", "public, max-age=5")
	writeJSON(w, http.StatusOK, result, s.logger)
}

// handleTripleStats serves GET /api/triples/stats — triple counts per graph.
func (s *Server) handleTripleStats(w http.ResponseWriter, r *http.Request) {
	if s.TripleStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "triple store not initialized",
		}, s.logger)
		return
	}

	counts, err := s.TripleStore.CountByGraph()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		}, s.logger)
		return
	}

	total := s.TripleStore.CountTotal()

	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, http.StatusOK, map[string]any{
		"total":      total,
		"by_graph":   counts,
	}, s.logger)
}

// handleOntologyFile serves GET /ns/mesh/ontology.jsonld — the ontology definition.
func (s *Server) handleOntologyFile(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(s.Config.OntologyPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "ontology file not found: " + err.Error(),
		}, s.logger)
		return
	}

	// Validate JSON before serving
	var check json.RawMessage
	if err := json.Unmarshal(data, &check); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "ontology file contains invalid JSON",
		}, s.logger)
		return
	}

	w.Header().Set("Content-Type", "application/ld+json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(data)
}

// initTripleStore creates the triple store and loads the ontology.
// Called during server startup.
func (s *Server) initTripleStore() {
	store, err := triplestore.NewStore(s.Config.BudgetDBPath, s.logger)
	if err != nil {
		s.logger.Error("triplestore init failed", "error", err)
		return
	}

	if s.Config.OntologyPath != "" {
		ont, err := triplestore.LoadOntology(s.Config.OntologyPath, s.logger)
		if err != nil {
			s.logger.Warn("ontology load failed — validation disabled", "error", err)
		} else {
			store.SetOntology(ont)
		}
	}

	s.TripleStore = store
	s.logger.Info("triplestore initialized", "db", s.Config.BudgetDBPath)

	// Wire registry refresh → agent triple emission
	if s.Registry != nil {
		s.Registry.OnRefresh(func(agents []AgentInfo) {
			s.emitAgentTriples(agents)
		})
		// Emit triples for any agents already in the registry
		// (initial refresh may have completed before this callback registered)
		if agents := s.Registry.Agents(); len(agents) > 0 {
			go s.emitAgentTriples(agents)
		}
	}
}

// emitPsychometricTriples emits A2A-Psychology observations to the triple store.
// Runs as a goroutine to avoid blocking the HTTP response. Single batch transaction.
func (s *Server) emitPsychometricTriples(pad, tlx, resources, wm, engagement, flow map[string]any) {
	sensor := "agent:" + s.Config.AgentID
	var triples []triplestore.Triple

	// Helper to safely extract float64 from map[string]any
	f := func(m map[string]any, key string) (float64, bool) {
		v, ok := m[key]
		if !ok {
			return 0, false
		}
		switch val := v.(type) {
		case float64:
			return val, true
		case int:
			return float64(val), true
		}
		return 0, false
	}

	// PAD affect
	if v, ok := f(pad, "pleasure"); ok {
		triples = append(triples, triplestore.EmitObservation(sensor, "vocab:affect-pleasure", v, "agent-status")...)
	}
	if v, ok := f(pad, "arousal"); ok {
		triples = append(triples, triplestore.EmitObservation(sensor, "vocab:affect-arousal", v, "agent-status")...)
	}
	if v, ok := f(pad, "dominance"); ok {
		triples = append(triples, triplestore.EmitObservation(sensor, "vocab:affect-dominance", v, "agent-status")...)
	}
	if cat, ok := pad["category"].(string); ok {
		triples = append(triples, triplestore.EmitObservationString(sensor, "vocab:affect-category", cat, "agent-status")...)
	}

	// NASA-TLX cognitive load
	if v, ok := f(tlx, "cognitive_load"); ok {
		triples = append(triples, triplestore.EmitObservation(sensor, "vocab:cognitive-load", v, "agent-status")...)
	}

	// Resources
	if v, ok := f(resources, "cognitive_reserve"); ok {
		triples = append(triples, triplestore.EmitObservation(sensor, "vocab:cognitive-reserve", v, "agent-status")...)
	}

	// Working memory
	if v, ok := f(wm, "capacity_load"); ok {
		triples = append(triples, triplestore.EmitObservation(sensor, "vocab:working-memory-load", v, "agent-status")...)
	}

	// Engagement
	if v, ok := f(engagement, "vigor"); ok {
		triples = append(triples, triplestore.EmitObservation(sensor, "vocab:engagement-vigor", v, "agent-status")...)
	}

	// Flow
	if v, ok := f(flow, "challenge_skill_balance"); ok {
		triples = append(triples, triplestore.EmitObservation(sensor, "vocab:flow-balance", v, "agent-status")...)
	}

	if len(triples) > 0 {
		if err := s.TripleStore.AssertBatch(triples); err != nil {
			s.logger.Warn("psychometric triple emission failed", "error", err)
		}
	}
}

// emitMeshStateTriples emits mesh-level emergent properties.
// Runs as a goroutine — single batch transaction.
func (s *Server) emitMeshStateTriples(affect, bottleneck, coordination, immune, distribution map[string]any, agentsReporting int) {
	f := func(m map[string]any, key string) float64 {
		if v, ok := m[key].(float64); ok {
			return v
		}
		return 0
	}

	affectCat, _ := affect["category"].(string)
	bottleneckAgent, _ := bottleneck["agent_id"].(string)

	coherence := f(immune, "composite")
	intelligence := f(distribution, "gini")

	triples := triplestore.EmitMeshState(
		agentsReporting,
		affectCat,
		coherence,
		intelligence,
		f(coordination, "process_loss_ratio"),
		f(immune, "composite"),
		bottleneckAgent,
	)

	if err := s.TripleStore.ReplaceGraph("mesh-state", triples); err != nil {
		s.logger.Warn("mesh-state triple emission failed", "error", err)
	}
}

// emitAgentTriples replaces the agent-registry graph with fresh triples.
// Runs on each registry refresh cycle — performance: single batch transaction.
func (s *Server) emitAgentTriples(agents []AgentInfo) {
	var allTriples []triplestore.Triple

	for _, agent := range agents {
		triples := triplestore.EmitAgent(
			agent.ID,
			agent.Name,
			agent.Version,
			agent.Role,
			agent.CardURL,
			agent.StatusURL,
			agent.Repo,
			agent.Skills,
			!agent.Unavailable,
		)
		allTriples = append(allTriples, triples...)
	}

	if err := s.TripleStore.ReplaceGraph("agent-registry", allTriples); err != nil {
		s.logger.Warn("agent triple emission failed", "error", err)
	} else {
		s.logger.Debug("agent triples emitted", "count", len(allTriples))
	}
}
