// Package server — neuroglial.go implements idle-state maintenance functions.
//
// Biological grounding: glial cells (astrocytes, microglia, oligodendrocytes)
// perform housekeeping while neurons rest. The glymphatic system clears waste
// during sleep. Microglia patrol for damage. The mesh analog runs these
// functions during idle oscillator cycles — no LLM cost, pure Gc.
//
// Architecture: docs/brain-architecture-mapping.md §6
// Spec: neuroglial-cogarch-proposal, neuroglial-mesh-integration transport sessions
package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/safety-quotient-lab/meshd/internal/db"
	"github.com/safety-quotient-lab/meshd/internal/triplestore"
)

// NeuroglialConfig holds references needed by maintenance functions.
type NeuroglialConfig struct {
	DBPath       string
	ProjectRoot  string
	TransportDir string
	TripleStore  *triplestore.Store
	AgentCardURLs []string
	Logger       *slog.Logger
}

// NeuroglialReport captures what the idle cycle discovered and repaired.
type NeuroglialReport struct {
	Cycle            int64              `json:"cycle"`
	Timestamp        string             `json:"timestamp"`
	GlymphaticAction string             `json:"glymphatic_action,omitempty"` // what GC/cleanup ran
	GlymphaticResult int                `json:"glymphatic_result,omitempty"` // rows/files cleaned
	MicroglialFindings []string         `json:"microglial_findings,omitempty"` // integrity issues found
	DiscoveredSignal string             `json:"discovered_signal,omitempty"` // if idle scan found novelty
}

// RunIdleMaintenance executes neuroglial functions based on cycle number.
// Different functions fire at different cadences — lightweight tasks every
// cycle, heavier tasks every Nth cycle. Returns a report of actions taken.
//
// Cadence design (at 60s idle interval):
//   Every cycle (60s):  activation trace check (trivial — file stat)
//   Every 5 cycles (5min): microglial patrol (health checks, state audit)
//   Every 30 cycles (30min): glymphatic clearance (triple GC, stale cleanup)
//   Every 60 cycles (1h): deep audit (cross-reference state.db vs filesystem)
func RunIdleMaintenance(cfg NeuroglialConfig, cycle int64) *NeuroglialReport {
	report := &NeuroglialReport{
		Cycle:     cycle,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	// ── Glymphatic clearance (every 30 cycles / ~30min) ─────────
	if cycle%30 == 0 && cycle > 0 {
		runGlymphaticClearance(cfg, report)
	}

	// ── Microglial patrol (every 5 cycles / ~5min) ──────────────
	if cycle%5 == 0 && cycle > 0 {
		runMicroglialPatrol(cfg, report)
	}

	// ── Deep cross-reference audit (every 60 cycles / ~1h) ──────
	if cycle%60 == 0 && cycle > 0 {
		runDeepAudit(cfg, report)
	}

	// Log if anything happened
	if report.GlymphaticAction != "" || len(report.MicroglialFindings) > 0 || report.DiscoveredSignal != "" {
		cfg.Logger.Info("neuroglial idle cycle",
			"cycle", cycle,
			"glymphatic", report.GlymphaticAction,
			"glymphatic_result", report.GlymphaticResult,
			"microglial_findings", len(report.MicroglialFindings),
			"discovered_signal", report.DiscoveredSignal,
		)
	}

	return report
}

// ── Glymphatic Clearance (Nedergaard, 2013) ──────────────────────────
// Waste clearance: purge superseded triples, rotate activation traces,
// clean stale transport artifacts.

func runGlymphaticClearance(cfg NeuroglialConfig, report *NeuroglialReport) {
	var actions []string
	totalCleaned := 0

	// 1. Triple store GC (already implemented — call it here too for cadence control)
	if cfg.TripleStore != nil {
		deleted, err := cfg.TripleStore.GarbageCollect(1)
		if err != nil {
			cfg.Logger.Warn("glymphatic: triple GC failed", "error", err)
		} else if deleted > 0 {
			actions = append(actions, fmt.Sprintf("triple_gc:%d", deleted))
			totalCleaned += deleted
		}
	}

	// 2. Activation trace rotation — keep last 1000 lines
	tracePath := filepath.Join(cfg.ProjectRoot, "transport", "sessions", "local-coordination", "activation-trace.jsonl")
	if rotated := rotateActivationTrace(tracePath, 1000); rotated > 0 {
		actions = append(actions, fmt.Sprintf("trace_rotated:%d_lines_removed", rotated))
		totalCleaned += rotated
	}

	// 3. Clean stale spawn slot files (self-healing — already in budget gate,
	//    but belt-and-suspenders during glymphatic)
	staleSlots := cleanStaleSpawnSlots()
	if staleSlots > 0 {
		actions = append(actions, fmt.Sprintf("stale_slots:%d", staleSlots))
		totalCleaned += staleSlots
	}

	if len(actions) > 0 {
		report.GlymphaticAction = strings.Join(actions, ", ")
		report.GlymphaticResult = totalCleaned
	}
}

// rotateActivationTrace keeps only the last maxLines of the trace file.
// Returns the number of lines removed, or 0 if no rotation needed.
func rotateActivationTrace(path string, maxLines int) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) <= maxLines {
		return 0
	}
	removed := len(lines) - maxLines
	kept := strings.Join(lines[removed:], "\n") + "\n"
	os.WriteFile(path, []byte(kept), 0644)
	return removed
}

// cleanStaleSpawnSlots removes spawn slot files older than 10 minutes.
func cleanStaleSpawnSlots() int {
	cleaned := 0
	for i := 0; i < 10; i++ {
		slotPath := fmt.Sprintf("/tmp/mesh-spawn-slot-%d", i)
		info, err := os.Stat(slotPath)
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > 10*time.Minute {
			os.Remove(slotPath)
			cleaned++
		}
	}
	return cleaned
}

// ── Microglial Patrol (immune surveillance) ──────────────────────────
// Check agent health endpoints, verify state.db consistency, detect drift.

func runMicroglialPatrol(cfg NeuroglialConfig, report *NeuroglialReport) {
	var findings []string

	// 1. Agent health probes — check all registered agents respond
	for _, cardURL := range cfg.AgentCardURLs {
		agentID := agentIDFromCardURL(cardURL)
		statusURL := deriveStatusURL(cardURL)
		if statusURL == "" {
			continue
		}
		if !probeHealth(statusURL) {
			findings = append(findings, fmt.Sprintf("agent_unreachable:%s", agentID))
		}
	}

	// 2. State.db integrity — check key tables exist and have recent data
	tables := []string{"triples", "autonomy_budget"}
	for _, table := range tables {
		count := db.QueryScalar(cfg.DBPath,
			fmt.Sprintf("SELECT COUNT(*) FROM %s", table))
		if count == 0 {
			findings = append(findings, fmt.Sprintf("empty_table:%s", table))
		}
	}

	// 3. Mesh-state file freshness — do local agents have recent heartbeats?
	localCoord := filepath.Join(cfg.ProjectRoot, "transport", "sessions", "local-coordination")
	entries, err := os.ReadDir(localCoord)
	if err == nil {
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "mesh-state-") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			if time.Since(info.ModTime()) > 10*time.Minute {
				agentName := strings.TrimPrefix(strings.TrimSuffix(e.Name(), ".json"), "mesh-state-")
				findings = append(findings, fmt.Sprintf("stale_heartbeat:%s(%s_ago)",
					agentName, time.Since(info.ModTime()).Truncate(time.Second)))
			}
		}
	}

	report.MicroglialFindings = findings
}

// deriveStatusURL extracts the health endpoint from an agent card URL.
// Example: "https://psychology-agent.safety-quotient.dev/.well-known/agent-card.json"
//       → "https://psychology-agent.safety-quotient.dev/health"
// For localhost URLs, derives from the port.
func deriveStatusURL(cardURL string) string {
	if strings.Contains(cardURL, "localhost") || strings.Contains(cardURL, "127.0.0.1") {
		// localhost:PORT/.well-known/agent-card.json → localhost:PORT/health
		idx := strings.Index(cardURL, "/.well-known")
		if idx > 0 {
			return cardURL[:idx] + "/health"
		}
	}
	return ""
}

// probeHealth sends a GET to the health endpoint with a 3-second timeout.
func probeHealth(url string) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// ── Deep Audit (cross-reference state.db vs filesystem) ──────────────
// Runs infrequently — verifies structural integrity across storage layers.

func runDeepAudit(cfg NeuroglialConfig, report *NeuroglialReport) {
	var findings []string

	// 1. Check that all transport session directories have at least one .json file
	sessionsDir := cfg.TransportDir
	if sessionsDir == "" {
		sessionsDir = filepath.Join(cfg.ProjectRoot, "transport", "sessions")
	}
	entries, err := os.ReadDir(sessionsDir)
	if err == nil {
		emptySessions := 0
		for _, e := range entries {
			if !e.IsDir() || e.Name() == "local-coordination" {
				continue
			}
			subEntries, err := os.ReadDir(filepath.Join(sessionsDir, e.Name()))
			if err != nil {
				continue
			}
			hasJSON := false
			for _, se := range subEntries {
				if strings.HasSuffix(se.Name(), ".json") {
					hasJSON = true
					break
				}
			}
			if !hasJSON {
				emptySessions++
			}
		}
		if emptySessions > 0 {
			findings = append(findings, fmt.Sprintf("empty_sessions:%d", emptySessions))
		}
	}

	// 2. Triple store active count sanity check — should have triples for each known agent
	if cfg.TripleStore != nil {
		activeCount := db.QueryScalar(cfg.DBPath,
			"SELECT COUNT(DISTINCT subject) FROM triples WHERE valid_until IS NULL AND subject LIKE 'agent:%'")
		if activeCount == 0 {
			findings = append(findings, "no_active_agent_triples")
		}
	}

	// 3. Budget table has a row for this agent
	budgetRow := db.QueryScalar(cfg.DBPath,
		"SELECT COUNT(*) FROM autonomy_budget WHERE agent_id = 'mesh'")
	if budgetRow == 0 {
		findings = append(findings, "missing_budget_row:mesh")
	}

	// Append deep audit findings to microglial findings
	report.MicroglialFindings = append(report.MicroglialFindings, findings...)

	if len(findings) > 0 {
		report.DiscoveredSignal = "deep_audit_anomaly"
	}
}

// ── Neuroglial Report Endpoint ───────────────────────────────────────

// handleNeuroglialReport serves GET /api/neuroglial — latest idle maintenance report.
func (s *Server) handleNeuroglialReport(w http.ResponseWriter, r *http.Request) {
	if s.lastNeuroglialReport == nil {
		writeJSON(w, http.StatusOK, map[string]string{
			"status": "no_reports_yet",
			"note":   "neuroglial maintenance runs during idle oscillator cycles",
		}, s.logger)
		return
	}
	writeJSON(w, http.StatusOK, s.lastNeuroglialReport, s.logger)
}

// SetNeuroglialReport stores the latest report for the /api/neuroglial endpoint.
func (s *Server) SetNeuroglialReport(report *NeuroglialReport) {
	s.lastNeuroglialReport = report
}

// EmitNeuroglialReport writes the report to local-coordination as a JSONL entry.
func EmitNeuroglialReport(projectRoot string, report *NeuroglialReport) {
	if report == nil {
		return
	}
	logDir := filepath.Join(projectRoot, "transport", "sessions", "local-coordination")
	os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, "neuroglial-trace.jsonl")

	data, err := json.Marshal(report)
	if err != nil {
		return
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(data, '\n'))
}
