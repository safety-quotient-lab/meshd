// meshd — Event-driven mesh daemon for the safety-quotient agent mesh.
//
// Replaces cron-based polling with reactive event processing.
// Receives signals from GitHub webhooks, filesystem watchers,
// and periodic polls. Routes events through a priority queue
// with budget-aware gating before spawning Claude contexts.
//
// Usage:
//
//	meshd                    # start with defaults from .dev.vars
//	meshd -port 8081         # override port
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/lmittmann/tint"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/safety-quotient-lab/meshd/internal/budget"
	"github.com/safety-quotient-lab/meshd/internal/config"
	"github.com/safety-quotient-lab/meshd/internal/db"
	"github.com/safety-quotient-lab/meshd/internal/events"
	"github.com/safety-quotient-lab/meshd/internal/health"
	"github.com/safety-quotient-lab/meshd/internal/kvstore"
	"github.com/safety-quotient-lab/meshd/internal/monitor"
	"github.com/safety-quotient-lab/meshd/internal/notify"
	"github.com/safety-quotient-lab/meshd/internal/server"
	"github.com/safety-quotient-lab/meshd/internal/spawner"
	"github.com/safety-quotient-lab/meshd/internal/transport"
	"github.com/safety-quotient-lab/meshd/internal/webhook"
	wt "github.com/safety-quotient-lab/meshd/internal/webtransport"
	"github.com/safety-quotient-lab/meshd/internal/zmqbus"
)

const version = "0.1.0"

// Prometheus metrics.
var (
	meshAgentsAvailable = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "meshd_agents_available",
		Help: "Number of agents currently reporting available.",
	})
	meshAgentsTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "meshd_agents_total",
		Help: "Total registered agents in the mesh.",
	})
	meshEventsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "meshd_events_total",
		Help: "Total events processed by type.",
	}, []string{"type"})
	meshHTTPRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "meshd_http_requests_total",
		Help: "Total HTTP requests by path and status code.",
	}, []string{"path", "code"})
	meshHTTPRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "meshd_http_request_duration_seconds",
		Help:    "HTTP request latency distribution.",
		Buckets: prometheus.DefBuckets,
	}, []string{"path"})
)

func init() {
	prometheus.MustRegister(meshAgentsAvailable, meshAgentsTotal, meshEventsTotal)
	prometheus.MustRegister(meshHTTPRequestsTotal, meshHTTPRequestDuration)
}

func main() {
	var (
		configPath  string
		port        int
		logLevel    string
		projectRoot string
		agentID     string
		zmqPub      string
		zmqPeers    string
		cacheTTL    string
	)
	flag.StringVar(&configPath, "config", "", "path to .dev.vars config file")
	flag.IntVar(&port, "port", 0, "override MESHD_PORT")
	flag.StringVar(&logLevel, "log-level", "", "override LOG_LEVEL (debug|info|warn|error)")
	// Platform-compatible flags (drop-in replacement for /home/kashif/platform/meshd)
	flag.StringVar(&projectRoot, "project-root", "", "path to agent project root")
	flag.StringVar(&agentID, "agent-id", "", "agent identity within the mesh")
	flag.StringVar(&zmqPub, "zmq-pub", "", "ZMQ PUB bind address (accepted but not yet implemented)")
	flag.StringVar(&zmqPeers, "zmq-peers", "", "ZMQ peer addresses (accepted but not yet implemented)")
	flag.StringVar(&cacheTTL, "cache-ttl", "", "cache TTL for collector results (accepted for compat)")
	flag.Parse()

	// When --project-root provided, set working directory so config.Load()
	// finds .dev.vars relative to the project root
	if projectRoot != "" {
		if err := os.Chdir(projectRoot); err != nil {
			fmt.Fprintf(os.Stderr, "failed to chdir to project-root %s: %v\n", projectRoot, err)
			os.Exit(1)
		}
	}

	// Load configuration from .dev.vars + environment
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration load failed: %v\n", err)
		os.Exit(1)
	}
	// CLI flags override config file values
	if port > 0 {
		cfg.Port = port
	}
	if agentID != "" {
		cfg.AgentID = agentID
	}
	if logLevel != "" {
		cfg.LogLevel = logLevel
	}

	// Initialize structured logger
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	// Structured logging: tint (colored) for terminals, JSON for daemons.
	var logHandler slog.Handler
	if fileInfo, _ := os.Stderr.Stat(); fileInfo != nil && fileInfo.Mode()&os.ModeCharDevice != 0 {
		logHandler = tint.NewHandler(os.Stderr, &tint.Options{
			Level:      level,
			TimeFormat: time.Kitchen,
		})
	} else {
		logHandler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	}
	logger := slog.New(logHandler)
	slog.SetDefault(logger)

	logger.Info("meshd starting",
		"version", version,
		"agent_id", cfg.AgentID,
		"port", cfg.Port,
		"repo_root", cfg.RepoRoot,
	)

	// Create root context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── Wire subsystems ────────────────────────────────────────────

	// Event queue — priority lanes with deduplication and batch accumulation
	queueCfg := events.DefaultQueueConfig()
	queue := events.NewQueue(queueCfg)

	// ZMQ publish function — set later when ZMQ bus initializes.
	var zmqPublishFn func(string, any) error

	// SSE broadcast function — set after server creation.
	// Used by ZMQ handler to push messages to dashboard.
	var sseBroadcastFn func(server.SSEEvent)

	// Input channel — subsystems push events here, main drains into queue
	eventChan := make(chan events.Event, 256)

	// Budget gate — queries state.db before allowing spawns
	budgetGate := budget.NewGate(cfg.BudgetDBPath, cfg.AgentID, logger)
	budgetGate.MeshMaxConcurrent = cfg.MaxConcurrent
	budgetGate.MeshReserveSlots = cfg.ReserveSlots

	// Spawner — manages Claude process lifecycle with circuit breaker
	spawnr := spawner.New(cfg.AgentID, logger)
	spawnr.Command = cfg.SpawnCommand
	spawnr.MaxConcurrent = cfg.MaxConcurrent
	spawnr.Timeout = time.Duration(cfg.SpawnTimeout) * time.Second

	// Dispatcher — routes events from queue → budget check → spawner
	dispatcher := events.NewDispatcher(
		queue,
		func(dctx context.Context, req events.SpawnRequest) error {
			// Acquire mesh-wide spawn slot (max 2 across entire mesh)
			slotPath, slotErr := budgetGate.AcquireSlot()
			if slotErr != nil {
				return fmt.Errorf("spawn slot unavailable: %w", slotErr)
			}
			defer budgetGate.ReleaseSlot(slotPath)

			// Model tier: cognitive-tempo selects haiku/sonnet/opus from
			// psychometric state + task metadata (Adaptive Gain Theory).
			// Falls back to static DELIBERATION_MODEL if configured.
			var spawnFlags []string
			tierResult := server.ComputeTier(cfg.AgentID, cfg.BudgetDBPath, server.MessageMeta{
				MessageType: string(req.Event.Type),
			})
			selectedModel := tierResult.RecommendedTier
			if selectedModel == "" && cfg.DeliberationModel != "" {
				selectedModel = cfg.DeliberationModel
			}
			if selectedModel != "" {
				spawnFlags = append(spawnFlags, "--model", selectedModel)
			}
			logger.Info("cognitive-tempo tier selected",
				"tier", tierResult.RecommendedTier,
				"gain", tierResult.Gain,
				"complexity", tierResult.TaskComplexity,
				"override", tierResult.OverrideReason,
			)
			result, spawnErr := spawnr.Spawn(dctx, req.Prompt, spawnFlags...)

			// Dual-write: persist spawn result to state.db
			spawnStatus := "completed"
			spawnError := ""
			exitCode := 0
			durationMs := int64(0)
			if result != nil {
				exitCode = result.ExitCode
				durationMs = result.Duration.Milliseconds()
			}
			if spawnErr != nil {
				spawnStatus = "failed"
				spawnError = spawnErr.Error()
			} else if result != nil && result.ExitCode != 0 {
				spawnStatus = "error"
				spawnError = result.Stderr
			}
			cost := budgetGate.EstimateCost(budget.Priority(req.Event.Priority))
			logSQL := fmt.Sprintf(
				"INSERT INTO deliberation_log (agent_id, event_id, prompt, exit_code, duration_ms, cost, status, error, started_at) "+
					"VALUES ('%s', '%s', '%s', %d, %d, %d, '%s', '%s', datetime('now'));",
				db.SanitizeID(cfg.AgentID),
				db.EscapeString(req.Event.ID),
				db.EscapeString(req.Prompt[:min(len(req.Prompt), 200)]),
				exitCode, durationMs, cost,
				db.EscapeString(spawnStatus),
				db.EscapeString(spawnError[:min(len(spawnError), 500)]),
			)
			if _, dbErr := db.Exec(cfg.BudgetDBPath, logSQL); dbErr != nil {
				logger.Warn("spawn log write failed", "err", dbErr)
			}

			if spawnErr != nil {
				return spawnErr
			}
			if result.ExitCode != 0 {
				return fmt.Errorf("claude exited with code %d: %s", result.ExitCode, result.Stderr)
			}
			logger.Info("spawn completed",
				"event_id", req.Event.ID,
				"duration", result.Duration,
			)

			// Broadcast spawn completion to mesh via ZMQ — include result summary
			if zmqPublishFn != nil {
				summary := ""
				if result != nil && len(result.Stdout) > 0 {
					// Extract last meaningful line as summary
					lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
					for i := len(lines) - 1; i >= 0; i-- {
						line := strings.TrimSpace(lines[i])
						if len(line) > 10 && !strings.HasPrefix(line, "{") {
							summary = line
							if len(summary) > 200 {
								summary = summary[:200]
							}
							break
						}
					}
				}
				zmqPublishFn("event", map[string]any{
					"agent_id":    cfg.AgentID,
					"event":       "spawn_completed",
					"event_id":    req.Event.ID,
					"duration_ms": durationMs,
					"status":      spawnStatus,
					"cost":        cost,
					"summary":     summary,
				})
			}

			return nil
		},
		func(cost int) (bool, string) { return budgetGate.CanSpawn(cost) },
		func(cost int) error { return budgetGate.Record(cost) },
		logger,
	)

	// Notification channel — alerts operator when shadow mode blocks spawns
	notifier := notify.New(notify.Config{
		Channel:      cfg.NotifyChannel,
		FilePath:     cfg.NotifyFilePath,
		ZulipURL:     cfg.ZulipNotifyURL,
		ZulipEmail:   cfg.ZulipNotifyEmail,
		ZulipKey:     cfg.ZulipNotifyKey,
		ZulipStream:  cfg.ZulipNotifyStream,
		ZulipTopic:   cfg.ZulipNotifyTopic,
		WebhookURL:   cfg.NotifyWebhookURL,
	}, logger)
	logger.Info("notification channel configured", "channel", notifier.Name())

	// Wire notifier into dispatcher
	dispatcher.SetNotifier(cfg.AgentID, func(ctx context.Context, agentID, eventType, priority, reason, session string) error {
		return notifier.Notify(ctx, notify.Message{
			AgentID:   agentID,
			EventType: eventType,
			Priority:  priority,
			Reason:    reason,
			Session:   session,
			Timestamp: time.Now(),
		})
	})

	// Gc handler — crystallized intelligence layer (no LLM cost)
	// Handles PollTick, HealthCheck, TransportACK without spawning Claude.
	// Only events requiring fluid intelligence (Gf) proceed to the spawner.
	gcHandler := events.NewGcHandler(events.GcConfig{
		RepoRoot:     cfg.RepoRoot,
		TransportDir: cfg.TransportDir,
		AgentID:      cfg.AgentID,
		Logger:       logger,
	})
	dispatcher.SetGcHandler(gcHandler)

	// GitHub webhook handler
	webhookHandler := webhook.NewGitHubHandler(cfg.GitHubSecret, eventChan, logger)

	// Wire CI failure notifications through the notifier
	webhookHandler.CIFailureFn = func(repo, workflow, branch, url string) {
		notifier.Notify(context.Background(), notify.Message{
			AgentID:   cfg.AgentID,
			EventType: "ci-failure",
			Priority:  "high",
			Reason:    fmt.Sprintf("CI FAILED: %s/%s on %s — %s", repo, workflow, branch, url),
			Timestamp: time.Now(),
		})
	}

	// Transport filesystem watcher (with persisted seen-set to prevent spawn storms)
	watcher := transport.NewWatcherWithContext(
		ctx,
		cfg.TransportDir,
		time.Duration(cfg.PollInterval)*time.Second,
		eventChan,
		logger,
	)
	watcher.SeenFile = filepath.Join(cfg.RepoRoot, ".watcher-seen.json")

	// Health monitor — tracks all subsystem health
	healthMon := health.NewMonitor(logger)
	healthMon.OnObserve = func(agentID, checkType, status, detail string) {
		if agentID == "" {
			agentID = cfg.AgentID
		}
		sql := fmt.Sprintf(
			"INSERT INTO health_observations (agent_id, check_type, status, detail) "+
				"VALUES ('%s', '%s', '%s', '%s');",
			db.SanitizeID(agentID), db.EscapeString(checkType),
			db.EscapeString(status), db.EscapeString(detail[:min(len(detail), 500)]),
		)
		if _, err := db.Exec(cfg.BudgetDBPath, sql); err != nil {
			logger.Debug("health observation write failed", "err", err)
		}
	}

	// ZMQ bus — real-time mesh communication (replaces cron-based polling)
	var zmqBus *zmqbus.Bus
	if zmqPub != "" {
		httpBase := fmt.Sprintf("http://localhost:%d", cfg.Port)
		zmqBus = zmqbus.New(cfg.AgentID, zmqPub, httpBase, logger)
		if err := zmqBus.Start(); err != nil {
			logger.Error("ZMQ bus failed to start", "err", err)
		} else {
			// Wire publish function for spawn handler
			zmqPublishFn = zmqBus.Publish

			// Connect to initial peers from --zmq-peers flag
			// Format: "agent-id=tcp://host:port|http://host:port,..."
			if zmqPeers != "" {
				for _, peerSpec := range strings.Split(zmqPeers, ",") {
					peerID, addrsStr, found := strings.Cut(peerSpec, "=")
					if !found {
						continue
					}
					peerZMQ, peerHTTP, _ := strings.Cut(addrsStr, "|")
					zmqBus.ConnectPeer(zmqbus.PeerInfo{
						AgentID: peerID,
						ZMQPub:  peerZMQ,
						HTTPURL: peerHTTP,
					})
				}
			}

			// Handle ALL incoming ZMQ messages — broadcast via SSE + emit transport events
			zmqBus.OnMessage(func(m zmqbus.Message) {
				// Broadcast every ZMQ message to SSE (dashboard ZMQ viewer)
				if sseBroadcastFn != nil {
					sseBroadcastFn(server.SSEEvent{
						Type: "zmq",
						Data: map[string]any{
							"topic": m.Topic,
							"from":  m.From,
							"timestamp": m.Timestamp.Format(time.RFC3339),
							"data":  m.Data,
						},
					})
				}

				// Transport-topic messages also enter the event queue for spawn processing
				if m.Topic == "transport" {
					evt := events.NewEvent(events.EventTransportMessage, events.PriorityHigh, "zmq", map[string]string{
						"from":    m.From,
						"topic":   m.Topic,
						"zmq":     "true",
					})
					select {
					case eventChan <- evt:
						logger.Info("ZMQ transport event received",
							"from", m.From,
							"topic", m.Topic,
						)
					default:
						logger.Warn("ZMQ event dropped — channel full", "from", m.From)
					}
				}
			})
		}
	}

	// Agent registry — compositor discovery (background refresh)
	registry := server.NewAgentRegistry(cfg.AgentID, cfg.AgentCardURLs, 5*time.Minute, logger, cfg.AgentFetchTimeout, cfg.CardFetchTimeout)

	// CI monitor — polls GitHub Actions across all peer repos for build failures
	meshRepos := []string{
		"safety-quotient-lab/psychology-agent",
		"safety-quotient-lab/safety-quotient",
		"safety-quotient-lab/unratified",
		"safety-quotient-lab/observatory",
	}
	ciMon := monitor.NewCIMonitorWithContext(ctx, meshRepos, 5*time.Minute, logger)
	ciMon.OnFailure = func(status monitor.CIStatus) {
		evt := events.NewEvent(events.EventHealthCheck, events.PriorityHigh, "ci-monitor", map[string]string{
			"repo":       status.Repo,
			"run_id":     fmt.Sprintf("%d", status.RunID),
			"conclusion": status.Conclusion,
			"workflow":   status.Workflow,
			"commit":     status.CommitMsg,
		})
		select {
		case eventChan <- evt:
		default:
			logger.Warn("CI failure event dropped — channel full", "repo", status.Repo)
		}

		// Notify the responsible agent via meshd HTTP inbound
		go notifyAgentCIFailure(registry, status, logger)
	}
	ciMon.OnRecovery = func(status monitor.CIStatus) {
		logger.Info("CI recovered", "repo", status.Repo, "run_id", status.RunID)

		// Notify recovery too
		go notifyAgentCIRecovery(registry, status, logger)
	}

	// Cross-repo fetcher — polls peer repos for transport messages addressed to us
	fetcherPeers := []transport.PeerConfig{
		{AgentID: "psychology-agent", Repo: "safety-quotient-lab/psychology-agent"},
		{AgentID: "safety-quotient-agent", Repo: "safety-quotient-lab/safety-quotient"},
		{AgentID: "unratified-agent", Repo: "safety-quotient-lab/unratified"},
		{AgentID: "observatory-agent", Repo: "safety-quotient-lab/observatory"},
	}
	fetcher := transport.NewFetcherWithContext(ctx, cfg.AgentID, cfg.TransportDir, fetcherPeers, 5*time.Minute, logger)
	fetcher.GitHubToken = os.Getenv("GITHUB_TOKEN")

	// Trigger function for manual events via POST /api/trigger
	triggerFunc := func(eventType string, payload map[string]string) error {
		evt := events.NewEvent(events.EventType(eventType), events.PriorityNormal, "manual", payload)
		select {
		case eventChan <- evt:
			return nil
		default:
			return fmt.Errorf("event channel full")
		}
	}

	// HTTP server
	srv := server.New(cfg, healthMon, webhookHandler, triggerFunc, logger)
	srv.Registry = registry
	srv.GitHubToken = cfg.GitHubToken
	srv.OperatorSecret = cfg.OperatorSecret

	// Self-oscillation shadow mode — logs when it would fire, does not trigger
	osc := server.NewOscillator(cfg.AgentID, cfg.BudgetDBPath, cfg.RepoRoot)
	srv.Oscillator = osc
	osc.Start()
	logger.Info("oscillator started (shadow mode)", "agent_id", cfg.AgentID)

	// KV self-observation — write status to Cloudflare KV for compositor fallback
	kvClient := kvstore.New(cfg.CFAccountID, cfg.KVNamespaceID, cfg.CFAPIToken, logger)
	if kvClient != nil {
		srv.KVClient = kvClient
		go server.RunKVSelfObservation(ctx, srv, kvClient, cfg.AgentID, 2*time.Minute, logger)
	}
	sseBroadcastFn = srv.SSEBroadcast
	if zmqBus != nil {
		srv.ZMQPublish = zmqBus.Publish
		srv.ZMQRegister = func(info json.RawMessage) bool {
			var peer zmqbus.PeerInfo
			if err := json.Unmarshal(info, &peer); err != nil {
				return false
			}
			return zmqBus.RegisterPeer(peer)
		}
	}

	// ── Start subsystems ───────────────────────────────────────────

	var wg sync.WaitGroup

	// ── On-wake: broadcast "I'm alive" to mesh ────────────────────
	startupEvt := events.NewEvent(events.EventHealthCheck, events.PriorityNormal, "startup", map[string]string{
		"agent_id": cfg.AgentID,
		"version":  version,
		"event":    "meshd_started",
	})
	select {
	case eventChan <- startupEvt:
		logger.Info("startup event emitted", "agent_id", cfg.AgentID)
	default:
	}

	// ZMQ broadcast: immediate "online" announcement (no gossip delay)
	if zmqBus != nil {
		zmqBus.Publish("health", map[string]string{
			"agent_id": cfg.AgentID,
			"status":   "online",
			"version":  version,
			"event":    "meshd_started",
		})
		logger.Info("ZMQ startup broadcast sent")
	}

	// Agent registry background refresh
	wg.Add(1)
	go func() {
		defer wg.Done()
		registry.StartBackgroundRefresh(ctx)
	}()

	// Channel → Queue pump: drain eventChan into PriorityQueue
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-eventChan:
				if !ok {
					return
				}
				queue.Push(evt)
				srv.RecordEvent(evt)
			}
		}
	}()

	// Event dispatcher goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		dispatchLoop(ctx, queue, dispatcher, logger)
	}()

	// Transport watcher goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		watcher.Run()
	}()

	// Health monitor goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		healthMon.Run(ctx)
	}()

	// CI monitor goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		ciMon.Run()
	}()

	// Cross-repo fetcher goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		fetcher.Run()
	}()

	// Safety-net poll ticker
	wg.Add(1)
	go func() {
		defer wg.Done()
		runPollTicker(ctx, cfg, eventChan, logger)
	}()

	// HTTP server (blocks in its own goroutine)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if listenErr := srv.ListenAndServeContext(ctx); listenErr != nil {
			logger.Error("HTTP server stopped", "error", listenErr)
		}
	}()

	// ── WebTransport server (QUIC/HTTP3, separate port) ─────────
	wtAddr := fmt.Sprintf(":%d", cfg.Port+1000) // e.g., 8081 → 9081
	// Use mkcert cert (CA-trusted, 2-year) for Safari compat (no serverCertificateHashes).
	// Chrome uses serverCertificateHashes as fallback regardless of cert lifetime.
	wtCert := filepath.Join(cfg.RepoRoot, "certs", "localhost+2.pem")
	wtKey := filepath.Join(cfg.RepoRoot, "certs", "localhost+2-key.pem")
	// Fall back to self-signed if mkcert certs not present
	if _, err := os.Stat(wtCert); err != nil {
		wtCert, wtKey = "", ""
	}
	wtSrv := wt.New(wtAddr, wtCert, wtKey, logger)
	// Expose cert hash on the main HTTP server (TCP) so browsers can fetch
	// it before opening the QUIC connection
	srv.HandleFunc("GET /api/webtransport/certhash", wtSrv.HandleCertHash())
	// Wire WT broadcast into the event pipeline
	srv.WTBroadcast = func(v any) { wtSrv.BroadcastJSON(v) }
	// Wire inbound WT messages to the event pipeline
	wtSrv.OnMessage(func(fromAgent string, msg json.RawMessage) {
		logger.Info("webtransport message received", "from", fromAgent, "bytes", len(msg))
		// TODO: parse interagent/v1 message and route to event queue
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := wtSrv.Start(ctx); err != nil && ctx.Err() == nil {
			logger.Error("webtransport server stopped", "error", err)
		}
	}()

	logger.Info("meshd ready",
		"port", cfg.Port,
		"wt_port", cfg.Port+1000,
		"subsystems", "queue,dispatcher,watcher,monitor,server,poll,fetcher,webtransport",
	)

	// ── Wait for shutdown signal ───────────────────────────────────

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	logger.Info("shutdown signal received", "signal", sig.String())

	// Graceful shutdown sequence — cancel context triggers all subsystems
	cancel()

	// Stop subsystems that use stopCh (not context)
	watcher.Stop()
	fetcher.Stop()
	ciMon.Stop()
	osc.Stop()
	if zmqBus != nil {
		zmqBus.Stop()
	}

	// Drain the queue
	remaining := queue.Drain()
	logger.Info("queue drained", "remaining_events", len(remaining))

	// Wait for all goroutines with a hard deadline
	shutdownDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
		logger.Info("all goroutines completed")
	case <-time.After(10 * time.Second):
		logger.Warn("shutdown deadline exceeded — forcing exit")
	}

	dispatched, dropped, batched := dispatcher.Stats()
	logger.Info("meshd shutdown complete",
		"dispatched", dispatched,
		"dropped", dropped,
		"batched", batched,
	)
}

// dispatchLoop pulls events from the PriorityQueue and feeds them
// to the dispatcher. Exits when the context cancels and the queue drains.
func dispatchLoop(ctx context.Context, queue *events.Queue, dispatcher *events.Dispatcher, logger *slog.Logger) {
	logger.Info("dispatch loop started")
	defer logger.Info("dispatch loop stopped")

	for {
		evt, ok := queue.Pop(ctx)
		if !ok {
			return // queue closed
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		dispatcher.HandleEvent(ctx, evt)
	}
}

// runPollTicker emits PollTick events at the configured interval.
func runPollTicker(ctx context.Context, cfg *config.Config, eventChan chan<- events.Event, logger *slog.Logger) {
	interval := time.Duration(cfg.PollInterval) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	logger.Info("poll ticker started", "interval", interval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			evt := events.NewEvent(events.EventPollTick, events.PriorityLow, "poll", nil)
			select {
			case eventChan <- evt:
				logger.Debug("poll tick emitted")
			default:
				logger.Debug("poll tick dropped — channel full")
			}
		}
	}
}

// notifyAgentCIFailure sends a CI failure notification to the responsible
// agent's meshd via HTTP POST /api/messages/inbound.
func notifyAgentCIFailure(registry *server.AgentRegistry, status monitor.CIStatus, logger *slog.Logger) {
	agent := findAgentByRepo(registry, status.Repo)
	if agent == nil {
		logger.Debug("CI failure: no agent found for repo", "repo", status.Repo)
		return
	}
	if agent.StatusURL == "" {
		return
	}

	meshBase := strings.TrimSuffix(agent.StatusURL, "/api/status")
	msg := map[string]any{
		"schema":     "interagent/v1",
		"session_id": "ci-failure-notify",
		"turn":       1,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"message_type": "notification",
		"from": map[string]string{
			"agent_id": "mesh",
		},
		"to": map[string]string{
			"agent_id": agent.ID,
		},
		"subject": fmt.Sprintf("CI build failure: %s — %s", status.Workflow, status.CommitMsg),
		"urgency": "high",
		"body": map[string]any{
			"type":       "ci_failure",
			"repo":       status.Repo,
			"run_id":     status.RunID,
			"conclusion": status.Conclusion,
			"workflow":   status.Workflow,
			"branch":     status.Branch,
			"commit":     status.CommitMsg,
			"run_url":    fmt.Sprintf("https://github.com/%s/actions/runs/%d", status.Repo, status.RunID),
		},
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meshBase+"/api/messages/inbound", strings.NewReader(string(body)))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Debug("CI failure notification delivery failed", "agent", agent.ID, "err", err)
		return
	}
	resp.Body.Close()
	logger.Info("CI failure notification sent", "agent", agent.ID, "repo", status.Repo, "status", resp.StatusCode)
}

// notifyAgentCIRecovery sends a CI recovery notification to the agent.
func notifyAgentCIRecovery(registry *server.AgentRegistry, status monitor.CIStatus, logger *slog.Logger) {
	agent := findAgentByRepo(registry, status.Repo)
	if agent == nil || agent.StatusURL == "" {
		return
	}

	meshBase := strings.TrimSuffix(agent.StatusURL, "/api/status")
	msg := map[string]any{
		"schema":     "interagent/v1",
		"session_id": "ci-failure-notify",
		"turn":       1,
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"message_type": "notification",
		"from": map[string]string{
			"agent_id": "mesh",
		},
		"to": map[string]string{
			"agent_id": agent.ID,
		},
		"subject": fmt.Sprintf("CI build recovered: %s", status.Workflow),
		"urgency": "normal",
		"body": map[string]any{
			"type":       "ci_recovery",
			"repo":       status.Repo,
			"run_id":     status.RunID,
			"conclusion": status.Conclusion,
			"workflow":   status.Workflow,
			"run_url":    fmt.Sprintf("https://github.com/%s/actions/runs/%d", status.Repo, status.RunID),
		},
	}

	body, _ := json.Marshal(msg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, meshBase+"/api/messages/inbound", strings.NewReader(string(body)))
	if req == nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
	logger.Info("CI recovery notification sent", "agent", agent.ID, "repo", status.Repo)
}

// findAgentByRepo looks up an agent by its GitHub repo path.
func findAgentByRepo(registry *server.AgentRegistry, repo string) *server.AgentInfo {
	agents := registry.Agents()
	// Map repo slug to agent (repo format: "safety-quotient-lab/psychology-agent")
	repoSlug := repo
	for i := range agents {
		if agents[i].Repo == repoSlug {
			return &agents[i]
		}
	}
	// Fallback: match by repo name suffix against agent ID
	if _, repoName, found := strings.Cut(repo, "/"); found {
		for i := range agents {
			if agents[i].ID == repoName || strings.HasPrefix(agents[i].ID, repoName) {
				return &agents[i]
			}
		}
	}
	return nil
}
