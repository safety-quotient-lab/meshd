// Package events — Dispatcher routes individual events through budget gating
// and spawns Claude contexts. The main loop calls HandleEvent for each event
// popped from the PriorityQueue.
package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/safety-quotient-lab/meshd/internal/db"
)

// SpawnRequest describes what the dispatcher wants the spawner to execute.
type SpawnRequest struct {
	Prompt   string
	Flags    []string
	Cost     int
	Event    Event
	Priority Priority
}

// SpawnFunc accepts a spawn request and returns an error.
type SpawnFunc func(ctx context.Context, req SpawnRequest) error

// BudgetCheckFunc returns (canSpawn, reason) for a given cost.
type BudgetCheckFunc func(cost int) (bool, string)

// BudgetDeductFunc debits the budget after a successful spawn decision.
type BudgetDeductFunc func(cost int) error

// NotifyFunc sends a notification when the budget gate blocks a spawn.
// The dispatcher calls this so human operators learn about pending work.
type NotifyFunc func(ctx context.Context, agentID, eventType, priority, reason, session string) error

// GcHandlerFunc handles an event using crystallized intelligence (Go code,
// no LLM spawn). Returns true if handled — dispatcher skips the spawn.
// Returns false if the event requires fluid intelligence (Claude deliberation).
type GcHandlerFunc func(ctx context.Context, evt Event) bool

// Dispatcher evaluates individual events, applies budget gating,
// and dispatches spawn requests.
type Dispatcher struct {
	spawn        SpawnFunc
	gcHandler    GcHandlerFunc // optional — Gc layer intercepts routine events
	budgetCheck  BudgetCheckFunc
	budgetDeduct BudgetDeductFunc
	notify       NotifyFunc
	agentID      string
	dbPath       string // state.db path — for marking messages processed post-spawn
	queue        *Queue
	logger       *slog.Logger

	// Metrics
	dispatched int64
	dropped    int64
	notified   int64
	batched    int64
	mu         sync.RWMutex
}

// SetDBPath configures the state.db path for post-spawn dedup.
func (d *Dispatcher) SetDBPath(path string) {
	d.dbPath = path
}

// NewDispatcher creates a dispatcher wired to the given queue and spawn function.
func NewDispatcher(
	queue *Queue,
	spawnFn SpawnFunc,
	budgetCheck BudgetCheckFunc,
	budgetDeduct BudgetDeductFunc,
	logger *slog.Logger,
) *Dispatcher {
	return &Dispatcher{
		queue:        queue,
		spawn:        spawnFn,
		budgetCheck:  budgetCheck,
		budgetDeduct: budgetDeduct,
		notify:       func(_ context.Context, _, _, _, _, _ string) error { return nil },
		logger:       logger,
	}
}

// SetNotifier configures the notification callback for blocked spawns.
func (d *Dispatcher) SetNotifier(agentID string, fn NotifyFunc) {
	d.agentID = agentID
	d.notify = fn
}

// SetGcHandler configures the crystallized intelligence handler.
// Events handled by Gc skip the Claude spawn entirely.
func (d *Dispatcher) SetGcHandler(fn GcHandlerFunc) {
	d.gcHandler = fn
}

// HandleEvent processes a single event — tries Gc first, then budget gate + spawn.
func (d *Dispatcher) HandleEvent(ctx context.Context, evt Event) {
	d.logger.Info("dispatching event",
		"type", evt.Type,
		"priority", evt.Priority.String(),
		"source", evt.Source,
		"id", evt.ID,
		"path", evt.Payload["path"],
		"session", evt.Payload["session"],
	)

	// Gc layer — try crystallized intelligence first (no LLM cost)
	if d.gcHandler != nil && d.gcHandler(ctx, evt) {
		d.logger.Info("event handled by Gc layer (no spawn)",
			"type", evt.Type,
			"id", evt.ID,
			"path", evt.Payload["path"],
		)
		d.mu.Lock()
		d.batched++ // reuse batched counter for Gc-handled events
		d.mu.Unlock()
		return
	}

	cost := estimateCost(evt.Priority)
	allowed, reason := d.budgetCheck(cost)

	if !allowed {
		d.logger.Warn("spawn blocked by budget gate",
			"event_id", evt.ID,
			"type", evt.Type,
			"cost", cost,
			"reason", reason,
			"path", evt.Payload["path"],
			"session", evt.Payload["session"],
		)
		d.mu.Lock()
		d.dropped++
		d.mu.Unlock()

		// Notify the operator about the blocked event
		session := evt.Payload["session"]
		if session == "" {
			session = evt.Payload["path"]
		}
		if err := d.notify(ctx, d.agentID, string(evt.Type), evt.Priority.String(), reason, session); err != nil {
			d.logger.Warn("notification delivery failed", "error", err)
		} else {
			d.mu.Lock()
			d.notified++
			d.mu.Unlock()
		}
		return
	}

	prompt := BuildOrientation(evt)
	req := SpawnRequest{
		Prompt:   prompt,
		Cost:     cost,
		Event:    evt,
		Priority: evt.Priority,
	}

	if err := d.budgetDeduct(cost); err != nil {
		d.logger.Error("budget deduction failed", "error", err)
		// Proceed anyway — acting outweighs silent failure
	}

	if err := d.spawn(ctx, req); err != nil {
		d.logger.Error("spawn failed",
			"event_id", evt.ID,
			"error", err,
		)
		// Refund the budget — spawn did not consume resources
		if refundErr := d.budgetDeduct(-cost); refundErr != nil {
			d.logger.Warn("budget refund failed", "cost", cost, "error", refundErr)
		} else {
			d.logger.Info("budget refunded after spawn failure", "cost", cost)
		}
		// Re-queue if retries remain
		if evt.Attempts < evt.MaxRetries {
			evt.Attempts++
			d.queue.Push(evt)
			d.logger.Info("re-queued event for retry",
				"event_id", evt.ID,
				"attempt", evt.Attempts,
			)
		}
		return
	}

	// Post-spawn dedup: mark the triggering message as processed in state.db.
	// Prevents the watcher from re-dispatching the same message on the next scan.
	if evt.Type == EventTransportMessage && d.dbPath != "" {
		d.markMessageProcessed(evt)
	}

	d.mu.Lock()
	d.dispatched++
	d.mu.Unlock()
}

// markMessageProcessed updates state.db to mark the transport message that
// triggered this event as processed. Uses the filename from the event payload.
func (d *Dispatcher) markMessageProcessed(evt Event) {
	path := evt.Payload["path"]
	if path == "" {
		return
	}
	// Extract filename from full path
	filename := path
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			filename = path[i+1:]
			break
		}
	}

	// Escape single quotes for SQL safety
	escaped := ""
	for _, c := range filename {
		if c == '\'' {
			escaped += "''"
		} else {
			escaped += string(c)
		}
	}
	sql := fmt.Sprintf(
		"UPDATE transport_messages SET processed = 1, task_state = 'completed' WHERE filename = '%s' AND processed = 0;",
		escaped,
	)
	if _, err := db.Exec(d.dbPath, sql); err != nil {
		d.logger.Warn("failed to mark message processed",
			"filename", filename,
			"error", err,
		)
	} else {
		d.logger.Info("message marked processed post-spawn",
			"filename", filename,
		)
	}
}

// Stats returns dispatcher metrics.
func (d *Dispatcher) Stats() (dispatched, dropped, batched int64) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.dispatched, d.dropped, d.batched
}

// NotifiedCount returns how many notifications the dispatcher sent.
func (d *Dispatcher) NotifiedCount() int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.notified
}

// estimateCost maps event priority to budget units.
func estimateCost(p Priority) int {
	switch p {
	case PriorityCritical:
		return 5
	case PriorityHigh:
		return 3
	case PriorityNormal:
		return 2
	case PriorityLow:
		return 1
	default:
		return 2
	}
}

// buildPrompt constructs the Claude prompt for a single event.
func buildPrompt(evt Event) string {
	switch evt.Type {
	case EventDirective:
		return fmt.Sprintf("/sync --directive --session %s --enforcement %s",
			evt.Payload["session"],
			evt.Payload["enforcement"],
		)
	case EventContextRotate:
		return "/cycle --context-rotate"
	case EventTransportMessage:
		session := evt.Payload["session"]
		if session != "" {
			return fmt.Sprintf("/sync --session %s", session)
		}
		return "/sync"
	case EventPollTick:
		return "/sync --quick"
	case EventHealthCheck:
		return "/sync --health-only"
	case EventTransportACK:
		// Should not reach here — Gc handles ACKs. Fallback to sync.
		return "/sync --quick"
	case EventCIFailure:
		return fmt.Sprintf("/sync --ci-failure --repo %s", evt.Payload["repo"])
	default:
		return "/sync"
	}
}
