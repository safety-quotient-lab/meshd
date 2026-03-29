package server

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/safety-quotient-lab/meshd/internal/db"
)

// Signal weights from self-oscillation-spec.md §4.1
var signalWeights = map[string]float64{
	"new_commits":              0.25,
	"unprocessed_messages":     0.20,
	"gate_approaching_timeout": 0.20,
	"peer_heartbeat_stale":     0.10,
	"escalation_present":       0.15,
	"scheduled_task_due":       0.10,
}

const baselineThreshold = 0.30

// OscillatorState captures the current oscillator snapshot.
type OscillatorState struct {
	Activation         float64            `json:"activation"`
	Threshold          float64            `json:"threshold"`
	MonitorIntervalMs  int                `json:"monitor_interval_ms"`
	State              string             `json:"state"` // monitoring, refractory, firing, halted
	LastFireAt         string             `json:"last_fire_at,omitempty"`
	RefractoryRemainS  int                `json:"refractory_remaining_s"`
	LastTier           string             `json:"last_tier,omitempty"`
	FireHistory        []FireEvent        `json:"fire_history"`
	SignalBreakdown    map[string]float64 `json:"signal_breakdown"`
	SleepMode          bool               `json:"sleep_mode"`
	CycleCount         int64              `json:"cycle_count"`
	WouldFireCount     int64              `json:"would_fire_count"`
}

// FireEvent records a single (shadow) firing.
type FireEvent struct {
	At         string  `json:"at"`
	Activation float64 `json:"activation"`
	Tier       string  `json:"tier"`
	Trigger    string  `json:"trigger"`
}

// signalResult carries the latest value from an asynchronous signal producer.
type signalResult struct {
	name  string
	value float64
}

// Oscillator implements the self-oscillation event loop (Phase 1: shadow mode).
//
// Lock-free design: the current state lives behind an atomic.Pointer. The main
// loop builds a complete new OscillatorState snapshot (doing all slow work —
// DB queries, file reads, git fetch — freely), then publishes it with a single
// atomic store. HTTP handlers call Load() and get a consistent, immutable
// snapshot in ~1ns with zero contention.
//
// Signal producers run as independent goroutines, each at its own cadence.
// They write results to a buffered channel. The main loop drains whatever
// arrived since last cycle. A slow git fetch blocks only its own goroutine.
type Oscillator struct {
	snapshot         atomic.Pointer[OscillatorState] // lock-free read path
	agentID          string
	dbPath           string
	projectRoot      string
	refractoryUntil  time.Time
	running          bool // only accessed from Start(), guarded by startOnce
	startOnce        atomic.Bool
	stopCh           chan struct{}
	signalCh         chan signalResult
	latestSignals    map[string]float64   // owned by loop goroutine only
	signalTimestamps map[string]time.Time // owned by loop goroutine only
	onFire           func(activation float64, tier, trigger string) // callback when activation exceeds threshold
	onIdle           func(cycle int64)                              // callback every idle cycle (activation below threshold)
}

// OnFire registers a callback invoked when the oscillator fires (activation
// exceeds threshold). The callback receives the activation level, recommended
// tier, and dominant trigger signal. Setting this transitions the oscillator
// from shadow mode (log only) to active mode (trigger deliberation).
func (o *Oscillator) OnFire(fn func(activation float64, tier, trigger string)) {
	o.onFire = fn
}

// OnIdle registers a callback invoked every cycle where activation stays below
// threshold. The callback receives the cycle count for scheduling periodic
// maintenance at different cadences (e.g., GC every 30 cycles, patrol every 60).
// Neuroglial analog: glymphatic clearance + microglial surveillance during rest.
func (o *Oscillator) OnIdle(fn func(cycle int64)) {
	o.onIdle = fn
}

// NewOscillator creates a shadow-mode oscillator with asynchronous signal producers.
func NewOscillator(agentID, dbPath, projectRoot string) *Oscillator {
	o := &Oscillator{
		agentID:          agentID,
		dbPath:           dbPath,
		projectRoot:      projectRoot,
		stopCh:           make(chan struct{}),
		signalCh:         make(chan signalResult, 32), // buffered — producers never block
		latestSignals:    make(map[string]float64),
		signalTimestamps: make(map[string]time.Time),
	}
	// Publish initial snapshot
	initial := &OscillatorState{
		State:           "monitoring",
		SleepMode:       false,
		SignalBreakdown: make(map[string]float64),
		FireHistory:     make([]FireEvent, 0, 20),
	}
	o.snapshot.Store(initial)
	return o
}

// Start launches the oscillator loop and all signal producer goroutines.
// Each signal runs independently — a slow git fetch never blocks the cycle.
// Safe to call multiple times; only the first call starts the loop.
func (o *Oscillator) Start() {
	if !o.startOnce.CompareAndSwap(false, true) {
		return // already started
	}

	// Signal producers — each runs its own loop at its own cadence.
	// Slow signals (git fetch) use longer intervals; fast signals poll frequently.
	go o.signalProducer("new_commits", 120*time.Second, o.checkNewCommits)
	go o.signalProducer("unprocessed_messages", 30*time.Second, o.checkUnprocessedMessages)
	go o.signalProducer("gate_approaching_timeout", 30*time.Second, o.checkGateTimeout)
	go o.signalProducer("peer_heartbeat_stale", 60*time.Second, o.checkPeerHeartbeatStale)
	go o.signalProducer("escalation_present", 15*time.Second, o.checkEscalation)

	go o.loop()
}

// signalProducer runs a signal check function in its own goroutine at the given
// cadence and writes results to signalCh. If the check function blocks (e.g.,
// git fetch over a slow network), it only blocks THIS goroutine — the oscillator
// cycle continues reading stale values for this signal.
func (o *Oscillator) signalProducer(name string, interval time.Duration, check func() float64) {
	// Fire immediately on first cycle, then at interval
	for {
		value := check()
		select {
		case o.signalCh <- signalResult{name: name, value: value}:
		default:
			// Channel full — drop this sample. The oscillator will use the
			// previous value. Dropping represents backpressure, not data loss.
		}

		select {
		case <-o.stopCh:
			return
		case <-time.After(interval):
		}
	}
}

// Stop halts the oscillator and all signal producers.
func (o *Oscillator) Stop() {
	if o.startOnce.Load() {
		close(o.stopCh)
	}
}

// Snapshot returns the current state. Lock-free — ~1ns atomic pointer load.
// The returned struct represents an immutable snapshot; never modified after publication.
func (o *Oscillator) Snapshot() OscillatorState {
	return *o.snapshot.Load()
}

// loop runs the monitor cycle at adaptive intervals.
func (o *Oscillator) loop() {
	for {
		interval := o.computeMonitorInterval()

		select {
		case <-o.stopCh:
			return
		case <-time.After(interval):
			o.cycle()
		}
	}
}

// cycle runs one monitor → activation → threshold check.
// All slow work (DB queries, file I/O) completes before the atomic snapshot
// publication. No locks held at any point.
func (o *Oscillator) cycle() {
	// Drain all pending signal results (non-blocking channel read)
	o.drainSignals()

	// Compute everything BEFORE publishing — slow work happens here
	signals := o.decayedSignals()
	activation := o.computeActivation(signals)
	threshold := o.computeThreshold()

	// Load previous snapshot to carry forward history and counters
	prev := o.snapshot.Load()

	// Build new immutable snapshot
	snap := &OscillatorState{
		Activation:        math.Round(activation*1000) / 1000,
		Threshold:         math.Round(threshold*1000) / 1000,
		SignalBreakdown:   signals,
		CycleCount:        prev.CycleCount + 1,
		WouldFireCount:    prev.WouldFireCount,
		SleepMode:         prev.SleepMode,
		LastFireAt:        prev.LastFireAt,
		LastTier:          prev.LastTier,
	}

	// Copy fire history (immutable — never modify after publication)
	history := make([]FireEvent, len(prev.FireHistory))
	copy(history, prev.FireHistory)

	// Check refractory
	inRefractory := time.Now().Before(o.refractoryUntil)
	if inRefractory {
		snap.State = "refractory"
		threshold += 0.15 // elevate during refractory
	} else {
		snap.State = "monitoring"
	}

	wouldFire := activation > threshold
	if wouldFire {
		snap.WouldFireCount++
		snap.State = "firing"
		snap.LastFireAt = time.Now().UTC().Format(time.RFC3339)

		// Compute tier — does DB queries, but no lock held
		tier := ComputeTier(o.agentID, o.dbPath, MessageMeta{}).RecommendedTier
		snap.LastTier = tier

		// Refractory period based on tier
		refractorySec := computeRefractory(tier)
		o.refractoryUntil = time.Now().Add(time.Duration(refractorySec) * time.Second)

		// Append to fire history (keep last 20)
		trigger := o.dominantSignal(signals)
		event := FireEvent{
			At:         snap.LastFireAt,
			Activation: snap.Activation,
			Tier:       tier,
			Trigger:    trigger,
		}
		if len(history) >= 20 {
			history = history[1:]
		}
		history = append(history, event)

		// Fire callback — transitions from shadow mode to active mode.
		// The callback runs AFTER snapshot publication (below) so the
		// HTTP endpoint reflects the firing state immediately.
		defer func() {
			if o.onFire != nil {
				o.onFire(snap.Activation, tier, trigger)
			}
		}()
	}

	// Idle path — run maintenance when activation stays below threshold.
	// Deferred so it runs after snapshot publication.
	if !wouldFire && o.onIdle != nil {
		cycleNum := snap.CycleCount
		defer func() { o.onIdle(cycleNum) }()
	}

	snap.FireHistory = history
	snap.RefractoryRemainS = 0
	if time.Now().Before(o.refractoryUntil) {
		snap.RefractoryRemainS = int(time.Until(o.refractoryUntil).Seconds())
	}
	snap.MonitorIntervalMs = int(o.computeMonitorInterval().Milliseconds())

	// Atomic publish — ~1ns, readers see the new snapshot immediately
	o.snapshot.Store(snap)

	// Side effects (file I/O) — no lock, no contention
	o.logShadow(activation, threshold, signals, wouldFire)
	o.emitMeshState(activation, threshold, signals)
}

// drainSignals reads all pending results from the signal channel without blocking.
// Updates latestSignals and signalTimestamps for each received result.
func (o *Oscillator) drainSignals() {
	for {
		select {
		case result := <-o.signalCh:
			o.latestSignals[result.name] = result.value
			o.signalTimestamps[result.name] = time.Now()
		default:
			return // channel empty — done draining
		}
	}
}

// decayedSignals returns the current signal values with staleness decay.
// Signals that haven't reported within 3× their expected interval decay
// linearly toward 0.0. This prevents a stalled producer (e.g., git fetch
// hanging on DNS) from holding its last value indefinitely.
func (o *Oscillator) decayedSignals() map[string]float64 {
	// Expected reporting intervals per signal (must match signalProducer cadences)
	expectedInterval := map[string]time.Duration{
		"new_commits":              120 * time.Second,
		"unprocessed_messages":     30 * time.Second,
		"gate_approaching_timeout": 30 * time.Second,
		"peer_heartbeat_stale":     60 * time.Second,
		"escalation_present":       15 * time.Second,
	}

	now := time.Now()
	signals := make(map[string]float64, len(signalWeights))
	for name := range signalWeights {
		raw := o.latestSignals[name]
		lastSeen := o.signalTimestamps[name]

		if lastSeen.IsZero() {
			signals[name] = 0.0 // never reported yet
			continue
		}

		age := now.Sub(lastSeen)
		staleAfter := expectedInterval[name] * 3
		if staleAfter == 0 {
			staleAfter = 3 * time.Minute // default fallback
		}

		if age > staleAfter {
			// Linear decay: fully decayed after 2× the stale threshold
			decayFraction := float64(age-staleAfter) / float64(staleAfter)
			if decayFraction >= 1.0 {
				signals[name] = 0.0
			} else {
				signals[name] = raw * (1.0 - decayFraction)
			}
		} else {
			signals[name] = raw
		}
	}
	// Placeholder — no producer yet
	signals["scheduled_task_due"] = 0.0
	return signals
}

// computeActivation returns weighted sum of signals, clamped to [0, 1].
func (o *Oscillator) computeActivation(signals map[string]float64) float64 {
	activation := 0.0
	for name, weight := range signalWeights {
		activation += signals[name] * weight
	}
	return math.Min(1.0, activation)
}

// computeThreshold returns adaptive threshold from psychometric state.
func (o *Oscillator) computeThreshold() float64 {
	threshold := baselineThreshold

	psych := LoadPsychometrics(o.agentID)
	if psych.CognitiveReserve < 0.3 {
		threshold += 0.10
	}

	// Allostatic load from working memory cache
	cachePath := filepath.Join("/tmp", o.agentID+"-psychometrics.json")
	if data, err := os.ReadFile(cachePath); err == nil {
		var raw struct {
			ResourceModel map[string]float64 `json:"resource_model"`
		}
		if json.Unmarshal(data, &raw) == nil {
			if al, ok := raw.ResourceModel["allostatic_load"]; ok && al > 0.7 {
				threshold += 0.20
			}
		}
	}

	return math.Max(0.15, math.Min(0.80, threshold))
}

// computeMonitorInterval returns adaptive poll interval from activation level.
// Lock-free — reads from the atomic snapshot.
func (o *Oscillator) computeMonitorInterval() time.Duration {
	act := o.snapshot.Load().Activation

	switch {
	case act > 0.6:
		return 5 * time.Second
	case act > 0.3:
		return 15 * time.Second
	case act > 0.1:
		return 30 * time.Second
	default:
		return 60 * time.Second
	}
}

// computeRefractory returns refractory period in seconds based on tier.
func computeRefractory(tier string) int {
	base := map[string]int{
		"haiku":  60,
		"sonnet": 180,
		"opus":   300,
	}
	sec, ok := base[tier]
	if !ok {
		sec = 180
	}
	return sec
}

// dominantSignal returns the signal name with the highest value.
func (o *Oscillator) dominantSignal(signals map[string]float64) string {
	best := ""
	bestVal := 0.0
	for name, val := range signals {
		if val > bestVal {
			bestVal = val
			best = name
		}
	}
	if best == "" {
		return "threshold"
	}
	return best
}

// ── Signal Checks ──────────────────────────────────────────────────

func (o *Oscillator) checkNewCommits() float64 {
	cmd := exec.Command("git", "-C", o.projectRoot, "fetch", "--dry-run", "--all")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0.0
	}
	text := string(out)
	if strings.Contains(text, "From") && strings.Contains(text, "->") {
		return 1.0
	}
	return 0.0
}

func (o *Oscillator) checkUnprocessedMessages() float64 {
	count := db.QueryScalar(o.dbPath, "SELECT COUNT(*) FROM transport_messages WHERE processed = 0")
	if count == 0 {
		return 0.0
	}
	// One message = work that needs doing. Signal saturates quickly:
	// 1 msg → 1.0, 2+ → 1.0. The weight (0.20) determines contribution
	// to activation — the signal itself represents binary demand presence.
	return 1.0
}

func (o *Oscillator) checkGateTimeout() float64 {
	count := db.QueryScalar(o.dbPath,
		"SELECT COUNT(*) FROM transport_messages WHERE processed = 0 AND timestamp < datetime('now', '-5 minutes')")
	if count == 0 {
		return 0.0
	}
	// Any message waiting > 5 minutes represents neglected work.
	return 1.0
}

func (o *Oscillator) checkPeerHeartbeatStale() float64 {
	localCoord := filepath.Join(o.projectRoot, "transport", "sessions", "local-coordination")
	entries, err := os.ReadDir(localCoord)
	if err != nil {
		return 0.0
	}

	staleCount := 0
	now := time.Now()
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "mesh-state-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > 20*time.Minute {
			staleCount++
		}
	}
	return math.Min(1.0, float64(staleCount)/2.0)
}

func (o *Oscillator) checkEscalation() float64 {
	localCoord := filepath.Join(o.projectRoot, "transport", "sessions", "local-coordination")
	entries, err := os.ReadDir(localCoord)
	if err != nil {
		return 0.0
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "escalation-") && strings.HasSuffix(e.Name(), ".json") {
			data, err := os.ReadFile(filepath.Join(localCoord, e.Name()))
			if err != nil {
				continue
			}
			var msg map[string]any
			if json.Unmarshal(data, &msg) == nil {
				if processed, ok := msg["processed"].(bool); !ok || !processed {
					return 1.0
				}
			}
		}
	}
	return 0.0
}

// ── Activation Trace ──────────────────────────────────────────────

func (o *Oscillator) logShadow(activation, threshold float64, signals map[string]float64, wouldFire bool) {
	logDir := filepath.Join(o.projectRoot, "transport", "sessions", "local-coordination")
	os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, "activation-trace.jsonl")

	roundedSignals := make(map[string]float64, len(signals))
	for k, v := range signals {
		roundedSignals[k] = math.Round(v*1000) / 1000
	}

	entry := map[string]any{
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"activation": math.Round(activation*1000) / 1000,
		"threshold":  math.Round(threshold*1000) / 1000,
		"would_fire": wouldFire,
		"signals":    roundedSignals,
		"agent_id":   o.agentID,
	}

	data, err := json.Marshal(entry)
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

// emitMeshState writes a mesh-state JSON file to local-coordination.
// Peers check this file's modification time to assess liveness —
// the checkPeerHeartbeatStale signal reads mesh-state-*.json mtime.
// Writing on every oscillator cycle keeps the file fresh.
func (o *Oscillator) emitMeshState(activation, threshold float64, signals map[string]float64) {
	localCoord := filepath.Join(o.projectRoot, "transport", "sessions", "local-coordination")
	os.MkdirAll(localCoord, 0755)
	statePath := filepath.Join(localCoord, fmt.Sprintf("mesh-state-%s.json", o.agentID))

	state := map[string]any{
		"schema":     "mesh-state/v1",
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"agent_id":   o.agentID,
		"activation": math.Round(activation*1000) / 1000,
		"threshold":  math.Round(threshold*1000) / 1000,
		"signals":    signals,
	}

	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	os.WriteFile(statePath, data, 0644)
}

// ── HTTP Handler ───────────────────────────────────────────────────

// handleOscillator serves GET /api/oscillator — live oscillator state.
func (s *Server) handleOscillator(w http.ResponseWriter, r *http.Request) {
	if s.Oscillator == nil {
		writeJSON(w, http.StatusOK, map[string]string{
			"state": "disabled",
			"note":  "oscillator not started — shadow mode requires explicit enable",
		}, s.logger)
		return
	}
	snap := s.Oscillator.Snapshot()
	writeJSON(w, http.StatusOK, snap, s.logger)
}
