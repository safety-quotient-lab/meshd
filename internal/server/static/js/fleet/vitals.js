// ═══ VITALS STATION (was Medical) ════════════════════════════
// Per-agent diagnostic focus — select an agent, view its health.
// Data: /api/mesh/state/operational-health, per-agent /api/agent/state/*

(function () {
    "use strict";

    var _initialized = false;
    var _selectedAgent = null;

    function ensureLayout() {
        if (_initialized) return;
        _initialized = true;
        var el = document.getElementById("pane-vitals");
        if (!el) return;
        el.innerHTML =
            '<div class="lcars-zone-c" style="margin-bottom:var(--gap-xs)">' +
                '<span class="zc-title">Agent Vitals</span><span class="zc-id">11</span>' +
            '</div>' +
            '<div id="vitals-agent-selector" style="margin-bottom:var(--gap-l)"></div>' +
            '<div class="agent-panel-grid">' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-health);grid-column:1/-1">' +
                    '<div class="lcars-panel-header">Fleet Health Comparison <span class="lcars-panel-id">4471</span></div>' +
                    '<div class="lcars-panel-body" id="vitals-fleet-health"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-health)">' +
                    '<div class="lcars-panel-header">Oscillator State <span class="lcars-panel-id">8686</span></div>' +
                    '<div class="lcars-panel-body" id="vitals-oscillator"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-health)">' +
                    '<div class="lcars-panel-header">Processing Load <span class="lcars-panel-id">1988</span></div>' +
                    '<div class="lcars-panel-body" id="vitals-load"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-health)">' +
                    '<div class="lcars-panel-header">Resources <span class="lcars-panel-id">2002</span></div>' +
                    '<div class="lcars-panel-body" id="vitals-resources"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-health)">' +
                    '<div class="lcars-panel-header">Context Utilization <span class="lcars-panel-id">1986</span></div>' +
                    '<div class="lcars-panel-body" id="vitals-context"></div>' +
                '</div>' +
            '</div>';
    }

    window.refresh_vitals = function () {
        ensureLayout();

        // Fleet-wide health comparison
        lcars.catalog.fetch("Fleet Operational Health").then(function (data) {
            var perAgent = data.per_agent || data.agents || [];
            if (perAgent.length > 0) {
                var dims = perAgent.map(function (a) {
                    return {
                        label: a.agent_id || a.id || "?",
                        value: a.composite || a.health || 0,
                        color: "var(--c-health)",
                        polarity: "higher-better"
                    };
                });
                lcars.patterns.spectrumBars("vitals-fleet-health", dims);
            } else {
                // Single composite
                lcars.patterns.dataBar("vitals-fleet-health", data.composite || 0, {
                    label: "Fleet Health", color: "var(--c-health)", polarity: "higher-better"
                });
            }
        }).catch(function () {
            lcars.patterns.placeholder("vitals-fleet-health", "Fleet health unavailable");
        });

        // Per-agent details from selected agent (or first available)
        var agents = window.fleetAgents();
        if (agents.length > 0) {
            renderAgentSelector(agents);
            var target = _selectedAgent || agents[0];
            fetchAgentVitals(target);
        }
    };

    window.selectVitalsAgent = function (agentId) {
        var agents = window.fleetAgents();
        _selectedAgent = agents.find(function (a) { return a.id === agentId; }) || agents[0];
        renderAgentSelector(agents);
        fetchAgentVitals(_selectedAgent);
    };

    function renderAgentSelector(agents) {
        var el = document.getElementById("vitals-agent-selector");
        if (!el) return;
        var html = '<div style="display:flex;gap:6px;flex-wrap:wrap">';
        for (var i = 0; i < agents.length; i++) {
            var a = agents[i];
            var active = _selectedAgent && _selectedAgent.id === a.id;
            html += '<button class="lcars-badge" style="cursor:pointer;background:' +
                (active ? "var(--c-health)" : "var(--c-inactive)") +
                '" onclick="selectVitalsAgent(\'' + a.id + '\')">' +
                (a.id || "?").toUpperCase() + '</button>';
        }
        html += '</div>';
        el.innerHTML = html;
    }

    function fetchAgentVitals(agent) {
        if (!agent || !agent.url) return;
        // Fetch per-agent state endpoints via cross-origin
        Promise.allSettled([
            window.fetch(agent.url + "/api/agent/cognitive/oscillator", { signal: AbortSignal.timeout(5000) }).then(function (r) { return r.ok ? r.json() : null; }),
            window.fetch(agent.url + "/api/agent/state/processing-load", { signal: AbortSignal.timeout(5000) }).then(function (r) { return r.ok ? r.json() : null; }),
            window.fetch(agent.url + "/api/agent/state/resource-availability", { signal: AbortSignal.timeout(5000) }).then(function (r) { return r.ok ? r.json() : null; }),
            window.fetch(agent.url + "/api/agent/state/context-utilization", { signal: AbortSignal.timeout(5000) }).then(function (r) { return r.ok ? r.json() : null; })
        ]).then(function (results) {
            var osc = results[0].status === "fulfilled" ? results[0].value : null;
            var load = results[1].status === "fulfilled" ? results[1].value : null;
            var res = results[2].status === "fulfilled" ? results[2].value : null;
            var ctx = results[3].status === "fulfilled" ? results[3].value : null;

            // Oscillator
            if (osc) {
                var oscEl = document.getElementById("vitals-oscillator");
                if (oscEl) {
                    oscEl.innerHTML =
                        '<div style="display:flex;justify-content:space-between;padding:4px 0;font-family:Antonio,Oswald,sans-serif;font-size:0.85em">' +
                            '<span style="color:var(--text-primary);text-transform:uppercase">' + (osc.oscillator_state || osc.state || "—") + '</span>' +
                            '<span style="color:var(--c-health);font-weight:700">' + (osc.oscillator_coherence != null ? osc.oscillator_coherence.toFixed(2) : osc.coherence != null ? osc.coherence.toFixed(2) : "—") + '</span>' +
                            '<span style="color:var(--text-dim)">' + (osc.coupling_mode || "—") + '</span>' +
                        '</div>';
                }
            } else {
                lcars.patterns.placeholder("vitals-oscillator", "Oscillator data unavailable");
            }

            // Processing load
            if (load && load.subscales) {
                var dims = [];
                for (var key in load.subscales) {
                    dims.push({ label: key.replace(/_/g, " "), value: load.subscales[key], color: "var(--c-knowledge)", polarity: "lower-better" });
                }
                lcars.patterns.spectrumBars("vitals-load", dims);
            } else {
                lcars.patterns.placeholder("vitals-load", "Processing load unavailable");
            }

            // Resources
            if (res) {
                lcars.patterns.spectrumBars("vitals-resources", [
                    { label: "Immediate", value: res.immediate_capacity || 0, color: "var(--c-health)", polarity: "higher-better" },
                    { label: "Action Budget", value: res.action_budget || 0, color: "var(--c-knowledge)", polarity: "higher-better" },
                    { label: "Accumulated Stress", value: res.accumulated_stress || 0, color: "var(--c-alert)", polarity: "lower-better" }
                ]);
            } else {
                lcars.patterns.placeholder("vitals-resources", "Resource data unavailable");
            }

            // Context utilization
            if (ctx) {
                var ctxEl = document.getElementById("vitals-context");
                if (ctxEl) {
                    var zone = ctx.yerkes_dodson_zone || ctx.zone || "unknown";
                    ctxEl.innerHTML = '<div id="vitals-ctx-bar"></div>' +
                        '<div style="text-align:center;margin-top:8px">' +
                            lcars.patterns.badge(zone === "optimal" ? "nominal" : zone === "overloaded" ? "critical" : "advisory", zone) +
                        '</div>';
                    lcars.patterns.dataBar("vitals-ctx-bar", ctx.capacity_load || 0, {
                        label: "Context Load", color: "var(--c-epistemic)"
                    });
                }
            } else {
                lcars.patterns.placeholder("vitals-context", "Context data unavailable");
            }
        });
    }
})();
