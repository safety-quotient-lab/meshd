// Package server — psychometrics_mesh.go provides GET /api/psychometrics/mesh.
//
// The mesh as a psychological entity — emergent properties of the coupled
// system (Woolley et al., 2010). Not an aggregation of individual agent
// states but a distinct organism-level measurement.
//
// Computes per-agent psychometrics from operational data in each agent's
// /api/status response, then derives mesh-level emergent properties.
// No agent-side psychometrics endpoint needed — meshd owns the domain model.
//
// Psychology-agent owns the domain model. Operations-agent serves the
// infrastructure (this endpoint + caching).
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// handlePsychometricsMesh serves GET /api/psychometrics/mesh.
// Fetches /api/status from each agent, computes psychometrics from operational
// data, then derives mesh-level emergent properties.
func (s *Server) handlePsychometricsMesh(w http.ResponseWriter, r *http.Request) {
	agents := s.Registry.Agents()
	client := &http.Client{Timeout: 4 * time.Second}

	type agentPsych struct {
		AgentID        string         `json:"agent_id"`
		PAD            meshPAD        `json:"emotional_state"`
		AffectCategory string         `json:"affect_category"`
		Load           float64        `json:"cognitive_load"`
		Reserve        float64        `json:"cognitive_reserve"`
		SelfRegulation float64        `json:"self_regulatory_resource"`
		AllostaticLoad float64        `json:"allostatic_load"`
		Flow           float64        `json:"flow_index"`
		BurnoutRisk    float64        `json:"burnout_risk"`
		Online         bool           `json:"online"`
		ResourceModel  map[string]any `json:"resource_model,omitempty"`
		Engagement     map[string]any `json:"engagement,omitempty"`
	}

	var agentStates []agentPsych
	var totalP, totalA, totalD float64
	var totalLoad, totalReserve, totalFlow float64
	online := 0

	for _, agent := range agents {
		if agent.StatusURL == "" {
			continue
		}

		ap := agentPsych{AgentID: agent.ID}

		// Fetch /api/status — the endpoint agentd actually serves
		resp, err := client.Get(agent.StatusURL)
		if err != nil {
			agentStates = append(agentStates, ap)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			agentStates = append(agentStates, ap)
			continue
		}

		var status map[string]any
		json.Unmarshal(body, &status)

		ap.Online = true
		online++

		// Derive psychometric metrics from operational status data
		m := deriveMetricsFromStatus(status)
		pad := computePAD(m)
		tlx := computeTLX(m)
		resources := computeResources(tlx, m)
		engagement := computeEngagement(m, tlx, resources)
		flow := computeFlow(m, resources)

		// Extract PAD values
		ap.PAD.Pleasure = floatFromAny(pad["hedonic_valence"])
		ap.PAD.Arousal = floatFromAny(pad["activation"])
		ap.PAD.Dominance = floatFromAny(pad["perceived_control"])
		if cat, ok := pad["affect_category"].(string); ok {
			ap.AffectCategory = cat
		} else {
			ap.AffectCategory = padLabel(ap.PAD.Pleasure, ap.PAD.Arousal, ap.PAD.Dominance)
		}
		totalP += ap.PAD.Pleasure
		totalA += ap.PAD.Arousal
		totalD += ap.PAD.Dominance

		// Extract workload
		ap.Load = floatFromAny(tlx["cognitive_load"])
		totalLoad += ap.Load

		// Extract resources
		ap.Reserve = floatFromAny(resources["cognitive_reserve"])
		ap.SelfRegulation = floatFromAny(resources["self_regulatory_resource"])
		ap.AllostaticLoad = floatFromAny(resources["allostatic_load"])
		ap.ResourceModel = resources
		totalReserve += ap.Reserve

		// Extract engagement
		ap.BurnoutRisk = floatFromAny(engagement["burnout_risk"])
		ap.Engagement = engagement

		// Extract flow
		ap.Flow = floatFromAny(flow["flow_index"])
		totalFlow += ap.Flow

		agentStates = append(agentStates, ap)
	}

	// Mesh-level emergent properties
	n := math.Max(float64(online), 1)

	// Collective affect — mean PAD across agents
	meshAffect := meshPAD{
		Pleasure:  totalP / n,
		Arousal:   totalA / n,
		Dominance: totalD / n,
	}

	// Mesh coherence — how aligned are the agents? (low variance = high coherence)
	var varP, varA, varD float64
	for _, ap := range agentStates {
		if !ap.Online {
			continue
		}
		varP += (ap.PAD.Pleasure - meshAffect.Pleasure) * (ap.PAD.Pleasure - meshAffect.Pleasure)
		varA += (ap.PAD.Arousal - meshAffect.Arousal) * (ap.PAD.Arousal - meshAffect.Arousal)
		varD += (ap.PAD.Dominance - meshAffect.Dominance) * (ap.PAD.Dominance - meshAffect.Dominance)
	}
	coherence := 1.0 - math.Min(1.0, math.Sqrt((varP+varA+varD)/(3*n)))

	// Collective intelligence factor (Woolley et al., 2010)
	// Higher when: coherence high, load balanced, flow states present
	avgLoad := totalLoad / n
	avgReserve := totalReserve / n
	avgFlow := totalFlow / n
	collectiveIQ := (coherence*0.3 + avgReserve*0.3 + avgFlow*0.2 + (1-avgLoad/100)*0.2)

	// Mesh health narrative
	affectLabel := padLabel(meshAffect.Pleasure, meshAffect.Arousal, meshAffect.Dominance)
	narrative := meshNarrative(affectLabel, coherence, avgLoad, online, len(agents))

	result := map[string]any{
		"mesh_id":       "safety-quotient-mesh",
		"agents_online": online,
		"agents_total":  len(agents),
		"collected_at":  time.Now().UTC().Format(time.RFC3339),

		"collective_affect": map[string]any{
			"pleasure":  round2(meshAffect.Pleasure),
			"arousal":   round2(meshAffect.Arousal),
			"dominance": round2(meshAffect.Dominance),
			"label":     affectLabel,
		},

		"mesh_coherence": map[string]any{
			"score":       round2(coherence),
			"description": coherenceLabel(coherence),
		},

		"collective_intelligence": map[string]any{
			"c_factor":    round2(collectiveIQ),
			"avg_load":    round2(avgLoad),
			"avg_reserve": round2(avgReserve),
			"avg_flow":    round2(avgFlow),
			"reference":   "Woolley et al., 2010 — collective intelligence factor",
		},

		"narrative": narrative,

		"per_agent": agentStates,
	}

	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, http.StatusOK, result, s.logger)
}

// deriveMetricsFromStatus extracts PsychMetrics from an agent's /api/status response.
// Maps the operational fields in the status JSON to the sensor inputs that
// computePAD/computeTLX/etc. expect.
func deriveMetricsFromStatus(status map[string]any) PsychMetrics {
	m := PsychMetrics{}

	// Unprocessed messages count
	if msgs, ok := status["unprocessed_messages"].([]any); ok {
		m.UnprocessedMessages = len(msgs)
	}

	// Total messages from totals
	if totals, ok := status["totals"].(map[string]any); ok {
		m.TotalMessages = intFromAny(totals["messages"])
	}

	// Active gates count
	if gates, ok := status["active_gates"].([]any); ok {
		m.ActiveGates = len(gates)
	}

	// Autonomy budget
	if budget, ok := status["autonomy_budget"].(map[string]any); ok {
		m.BudgetSpent = floatFromAny(budget["budget_spent"])
		m.BudgetCutoff = floatFromAny(budget["budget_cutoff"])
		m.ConsecutiveBlocks = intFromAny(budget["consecutive_blocks"])
		m.SleepMode = intFromAny(budget["sleep_mode"])
	}

	// Recent actions as proxy for actions_last_hour
	if actions, ok := status["recent_actions"].([]any); ok {
		m.ActionsLastHour = len(actions)
	}

	// Recent messages — count errors by scanning for error-like types
	if msgs, ok := status["recent_messages"].([]any); ok {
		for _, raw := range msgs {
			if msg, ok := raw.(map[string]any); ok {
				if msgType, ok := msg["message_type"].(string); ok {
					if strings.Contains(msgType, "error") || strings.Contains(msgType, "problem") {
						m.ErrorsLastHour++
					}
				}
			}
		}
	}

	return m
}

type meshPAD struct {
	Pleasure  float64 `json:"pleasure"`
	Arousal   float64 `json:"arousal"`
	Dominance float64 `json:"dominance"`
}

func coherenceLabel(c float64) string {
	if c > 0.8 {
		return "highly aligned"
	}
	if c > 0.5 {
		return "moderately aligned"
	}
	return "divergent"
}

func meshNarrative(affect string, coherence, avgLoad float64, online, total int) string {
	coh := coherenceLabel(coherence)

	if online < total {
		return fmt.Sprintf("The mesh operates at %d/%d capacity. Agents report %s affect with %s coherence. Cognitive load averages %.0f%%.",
			online, total, affect, coh, avgLoad)
	}
	return fmt.Sprintf("All %d agents online. Collective affect: %s. Coherence: %s. Average cognitive load: %.0f%%.",
		total, affect, coh, avgLoad)
}

func floatFromAny(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case int64:
		return float64(val)
	default:
		return 0
	}
}

func intFromAny(v any) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case int64:
		return int(val)
	default:
		return 0
	}
}
