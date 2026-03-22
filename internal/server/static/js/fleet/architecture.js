// ═══ ARCHITECTURE STATION (was Engineering) ══════════════════
// Cognitive structure — tempo, deliberation rate, flow, Gc cascade.
// Data: /api/mesh/cognitive/*

(function () {
    "use strict";

    var _initialized = false;

    function ensureLayout() {
        if (_initialized) return;
        _initialized = true;
        var el = document.getElementById("pane-architecture");
        if (!el) return;
        el.innerHTML =
            '<div class="lcars-zone-c" style="margin-bottom:var(--gap-xs)">' +
                '<span class="zc-title">Cognitive Architecture</span><span class="zc-id">47</span>' +
            '</div>' +
            '<div class="agent-panel-grid">' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-engineering);grid-column:1/-1">' +
                    '<div class="lcars-panel-header">Fleet Tempo <span class="lcars-panel-id">2246</span></div>' +
                    '<div class="lcars-panel-body" id="arch-fleet-tempo"></div>' +
                    '<div class="lcars-panel-footer">Dispatch Dynamics<span class="lcars-panel-footer-num"></span></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-engineering)">' +
                    '<div class="lcars-panel-header">Deliberation Rate <span class="lcars-panel-id">7209</span></div>' +
                    '<div class="lcars-panel-body" id="arch-delib-rate"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-engineering)">' +
                    '<div class="lcars-panel-header">Cognitive Flow <span class="lcars-panel-id">3140</span></div>' +
                    '<div class="lcars-panel-body" id="arch-flow"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-engineering)">' +
                    '<div class="lcars-panel-header">Processing Tier <span class="lcars-panel-id">0001</span></div>' +
                    '<div class="lcars-panel-body" id="arch-tier"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-engineering)">' +
                    '<div class="lcars-panel-header">Mesh Oscillator <span class="lcars-panel-id">8686</span></div>' +
                    '<div class="lcars-panel-body" id="arch-oscillator"></div>' +
                '</div>' +
            '</div>';
    }

    window.refresh_architecture = function () {
        ensureLayout();

        Promise.allSettled([
            lcars.catalog.fetch("Fleet Tempo"),
            lcars.catalog.fetch("Deliberation Rate"),
            lcars.catalog.fetch("Cognitive Flow"),
            lcars.catalog.fetch("Processing Tier"),
            lcars.catalog.fetch("Mesh Oscillator")
        ]).then(function (results) {
            var tempo = results[0].status === "fulfilled" ? results[0].value : null;
            var delib = results[1].status === "fulfilled" ? results[1].value : null;
            var flow = results[2].status === "fulfilled" ? results[2].value : null;
            var tier = results[3].status === "fulfilled" ? results[3].value : null;
            var osc = results[4].status === "fulfilled" ? results[4].value : null;

            // Tempo — per-agent rates
            if (tempo && tempo.per_agent) {
                var dims = tempo.per_agent.map(function (a) {
                    return { label: a.agent_id || a.id || "?", value: a.rate || a.actions_per_hour || 0, color: "var(--c-tab-engineering)" };
                });
                lcars.patterns.spectrumBars("arch-fleet-tempo", dims);
            } else if (tempo) {
                lcars.patterns.numberGrid("arch-fleet-tempo", [
                    { value: tempo.total_actions || 0, label: "ACTIONS", type: "count" },
                    { value: tempo.rate || 0, label: "RATE/HR", type: "val" }
                ]);
            } else {
                lcars.patterns.placeholder("arch-fleet-tempo", "Tempo data unavailable");
            }

            // Deliberation rate
            if (delib && delib.per_agent) {
                var delibDims = delib.per_agent.map(function (a) {
                    return { label: a.agent_id || a.id || "?", value: a.count || a.deliberations || 0, color: "var(--c-knowledge)" };
                });
                lcars.patterns.spectrumBars("arch-delib-rate", delibDims);
            } else {
                lcars.patterns.placeholder("arch-delib-rate", "Deliberation rate unavailable");
            }

            // Flow — concurrency slots
            if (flow) {
                var flowEl = document.getElementById("arch-flow");
                if (flowEl) {
                    var slots = flow.slots || flow.concurrency || {};
                    flowEl.innerHTML = '<div style="display:flex;flex-direction:column;gap:6px">';
                    for (var key in slots) {
                        flowEl.innerHTML += '<div style="display:flex;justify-content:space-between;font-family:Antonio,Oswald,sans-serif;font-size:0.82em">' +
                            '<span style="color:var(--text-dim);text-transform:uppercase">' + key + '</span>' +
                            '<span style="color:var(--text-primary);font-weight:700">' + slots[key] + '</span>' +
                        '</div>';
                    }
                    flowEl.innerHTML += '</div>';
                }
            } else {
                lcars.patterns.placeholder("arch-flow", "Flow data unavailable");
            }

            // Processing tier
            if (tier) {
                var tierEl = document.getElementById("arch-tier");
                if (tierEl) {
                    var recommended = tier.recommended_tier || tier.tier || "—";
                    tierEl.innerHTML = '<div style="text-align:center;padding:12px">' +
                        '<div style="font-family:Antonio,Oswald,sans-serif;font-size:1.4em;font-weight:700;color:var(--text-primary);text-transform:uppercase">' + recommended + '</div>' +
                        lcars.patterns.badge(recommended === "deliberative" ? "advisory" : "nominal", recommended) +
                    '</div>';
                }
            } else {
                lcars.patterns.placeholder("arch-tier", "Processing tier unavailable");
            }

            // Oscillator
            if (osc) {
                var oscEl = document.getElementById("arch-oscillator");
                if (oscEl) {
                    oscEl.innerHTML =
                        '<div style="display:flex;justify-content:space-between;padding:4px 0;font-family:Antonio,Oswald,sans-serif;font-size:0.85em">' +
                            '<span style="color:var(--text-primary);text-transform:uppercase">' + (osc.state || "—") + '</span>' +
                            '<span style="color:var(--c-health);font-weight:700">' + (osc.coherence != null ? osc.coherence.toFixed(2) : "—") + '</span>' +
                        '</div>';
                }
            } else {
                lcars.patterns.placeholder("arch-oscillator", "Oscillator data unavailable");
            }
        });
    };
})();
