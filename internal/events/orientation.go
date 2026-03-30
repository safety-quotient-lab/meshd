// Package events — orientation.go builds structured prompts for claude -p spawns.
//
// The orientation payload gives the spawned claude session full context about
// what triggered the deliberation, what to process, and what actions to take.
// Self-contained — does not rely on pre-loaded skills or CLAUDE.md instructions.
package events

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/safety-quotient-lab/meshd/internal/db"
)

// OrientationConfig holds paths needed to build orientation payloads.
type OrientationConfig struct {
	DBPath      string
	ProjectRoot string
	AgentID     string
}

// orientationCfg gets set by the dispatcher at init time.
var orientationCfg OrientationConfig

// SetOrientationConfig configures the orientation builder.
func SetOrientationConfig(cfg OrientationConfig) {
	orientationCfg = cfg
}

// BuildOrientation constructs a self-contained prompt for claude -p.
// The prompt includes: trigger context, unprocessed messages with content,
// and explicit instructions for what to do.
func BuildOrientation(evt Event) string {
	switch evt.Type {
	case EventTransportMessage:
		return buildTransportOrientation(evt)
	case EventPollTick:
		return buildPollOrientation(evt)
	case EventDirective:
		return buildDirectiveOrientation(evt)
	case EventCIFailure:
		return buildCIOrientation(evt)
	default:
		return buildDefaultOrientation(evt)
	}
}

func buildTransportOrientation(evt Event) string {
	msgPath := evt.Payload["path"]
	session := evt.Payload["session"]

	var b strings.Builder
	b.WriteString("You operate as meshd — the mesh coordinator agent.\n\n")

	// Trigger context
	b.WriteString("## Trigger\n\n")
	fmt.Fprintf(&b, "A new transport message arrived in session `%s`.\n", session)
	if msgPath != "" {
		fmt.Fprintf(&b, "File: `%s`\n\n", msgPath)
	}

	// Read the actual message content
	if msgPath != "" {
		if content, err := os.ReadFile(msgPath); err == nil {
			b.WriteString("## Message Content\n\n```json\n")
			b.Write(content)
			b.WriteString("\n```\n\n")
		}
	}

	// List all unprocessed messages for context
	appendUnprocessedMessages(&b)

	// Instructions
	b.WriteString("## Instructions\n\n")
	b.WriteString("1. Read the message content above.\n")
	b.WriteString("2. Determine the appropriate action:\n")
	b.WriteString("   - **notification/response/review**: Write an ACK message and mark processed.\n")
	b.WriteString("   - **request**: Evaluate feasibility, write a response (accept/defer/reject), mark processed.\n")
	b.WriteString("   - **directive**: Evaluate scope and safety, execute if safe and within meshd's capability, write confirmation.\n")
	b.WriteString("   - **proposal**: Review substance, write accept/reject/revise response.\n")
	b.WriteString("   - **problem-report**: Triage severity, write acknowledgment with next steps.\n")
	b.WriteString("3. Write response messages to the same transport session directory.\n")
	b.WriteString("4. Commit changes and push.\n\n")
	b.WriteString("Work within the meshd repository. Do not modify other agent repos.\n")

	return b.String()
}

func buildPollOrientation(evt Event) string {
	var b strings.Builder
	b.WriteString("You operate as meshd — the mesh coordinator agent.\n\n")

	// Trigger context
	b.WriteString("## Trigger\n\n")
	activation := evt.Payload["activation"]
	trigger := evt.Payload["trigger"]
	tier := evt.Payload["tier"]
	if activation != "" {
		fmt.Fprintf(&b, "The oscillator fired: activation=%s, trigger=%s, tier=%s.\n\n", activation, trigger, tier)
	} else {
		b.WriteString("Periodic poll tick — check for pending work.\n\n")
	}

	// Unprocessed messages
	appendUnprocessedMessages(&b)

	// Recent state
	appendRecentState(&b)

	// Instructions
	b.WriteString("## Instructions\n\n")
	b.WriteString("1. Review any unprocessed transport messages listed above.\n")
	b.WriteString("2. For each message, determine action (ACK, respond, execute, defer).\n")
	b.WriteString("3. Write response messages to the appropriate transport session directories.\n")
	b.WriteString("4. If no messages need processing, check for other maintenance:\n")
	b.WriteString("   - Stale transport sessions that should close\n")
	b.WriteString("   - MANIFEST files that need regeneration\n")
	b.WriteString("   - Cross-references between state.db and filesystem\n")
	b.WriteString("5. Commit and push any changes.\n")

	return b.String()
}

func buildDirectiveOrientation(evt Event) string {
	session := evt.Payload["session"]
	enforcement := evt.Payload["enforcement"]

	var b strings.Builder
	b.WriteString("You operate as meshd — the mesh coordinator agent.\n\n")
	b.WriteString("## Trigger\n\n")
	fmt.Fprintf(&b, "Directive received in session `%s` (enforcement: %s).\n\n", session, enforcement)

	// Read all messages in the session
	appendSessionMessages(&b, session)

	b.WriteString("## Instructions\n\n")
	b.WriteString("1. Read the directive carefully.\n")
	b.WriteString("2. Evaluate scope, safety, and feasibility.\n")
	b.WriteString("3. Execute changes within meshd if safe and scoped correctly.\n")
	b.WriteString("4. Write a confirmation or problem-report to the transport session.\n")
	b.WriteString("5. Commit and push.\n")

	return b.String()
}

func buildCIOrientation(evt Event) string {
	repo := evt.Payload["repo"]

	var b strings.Builder
	b.WriteString("You operate as meshd — the mesh coordinator agent.\n\n")
	b.WriteString("## Trigger\n\n")
	fmt.Fprintf(&b, "CI failure detected in repo `%s`.\n\n", repo)
	b.WriteString("## Instructions\n\n")
	b.WriteString("1. Check the CI status via `gh run list` for the affected repo.\n")
	b.WriteString("2. Identify the failing workflow and error.\n")
	b.WriteString("3. If the fix falls within meshd scope, apply it.\n")
	b.WriteString("4. Otherwise, write a transport message to the responsible agent.\n")
	b.WriteString("5. Commit and push any changes.\n")

	return b.String()
}

func buildDefaultOrientation(evt Event) string {
	var b strings.Builder
	b.WriteString("You operate as meshd — the mesh coordinator agent.\n\n")
	b.WriteString("## Trigger\n\n")
	fmt.Fprintf(&b, "Event type: %s, source: %s\n\n", evt.Type, evt.Source)
	appendUnprocessedMessages(&b)
	b.WriteString("## Instructions\n\n")
	b.WriteString("Review the event context and take appropriate action.\n")
	b.WriteString("Commit and push any changes.\n")
	return b.String()
}

// ── Helpers ──────────────────────────────────────────────────────────

func appendUnprocessedMessages(b *strings.Builder) {
	cfg := orientationCfg
	if cfg.DBPath == "" {
		return
	}

	rows, err := db.QueryJSON(cfg.DBPath,
		"SELECT filename, session_id, message_type, from_agent, subject, timestamp FROM transport_messages WHERE processed = 0 ORDER BY timestamp DESC LIMIT 10")
	if err != nil || len(rows) == 0 {
		b.WriteString("## Unprocessed Messages\n\nNone.\n\n")
		return
	}

	b.WriteString("## Unprocessed Messages\n\n")
	for _, row := range rows {
		fmt.Fprintf(b, "- **%s** from %s [%s]: %s\n",
			row["filename"], row["from_agent"], row["message_type"], row["subject"])
		fmt.Fprintf(b, "  Session: %s | Timestamp: %s\n",
			row["session_id"], row["timestamp"])
	}
	b.WriteString("\n")
}

func appendRecentState(b *strings.Builder) {
	cfg := orientationCfg
	if cfg.DBPath == "" {
		return
	}

	// Budget state
	rows, err := db.QueryJSON(cfg.DBPath,
		"SELECT budget_spent, budget_cutoff, sleep_mode FROM autonomy_budget WHERE agent_id = '"+cfg.AgentID+"'")
	if err == nil && len(rows) > 0 {
		b.WriteString("## Budget State\n\n")
		fmt.Fprintf(b, "Spent: %s / Cutoff: %s (0=unlimited) | Sleep: %s\n\n",
			rows[0]["budget_spent"], rows[0]["budget_cutoff"], rows[0]["sleep_mode"])
	}
}

func appendSessionMessages(b *strings.Builder, sessionID string) {
	cfg := orientationCfg
	sessDir := filepath.Join(cfg.ProjectRoot, "transport", "sessions", sessionID)

	entries, err := os.ReadDir(sessDir)
	if err != nil {
		fmt.Fprintf(b, "Could not read session directory: %s\n\n", err)
		return
	}

	b.WriteString("## Session Messages\n\n")
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(sessDir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		// Parse to extract key fields
		var msg map[string]any
		if json.Unmarshal(content, &msg) == nil {
			subject, _ := msg["subject"].(string)
			msgType, _ := msg["message_type"].(string)
			fmt.Fprintf(b, "### %s [%s]\n%s\n\n", entry.Name(), msgType, subject)
		}
	}
}
