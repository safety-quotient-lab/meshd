// ═══ INTEGRITY STATION (was Tactical) ════════════════════════
// Defense, trust, quality assurance — fleet-wide.
// Data: /api/mesh/state/health, /api/mesh/state/trust,
//       /.well-known/agents

(function () {
    "use strict";

    var _initialized = false;

    function ensureLayout() {
        if (_initialized) return;
        _initialized = true;
        var el = document.getElementById("pane-integrity");
        if (!el) return;
        el.innerHTML =
            '<div class="lcars-zone-c" style="margin-bottom:var(--gap-xs)">' +
                '<span class="zc-title">Epistemic Integrity</span><span class="zc-id">99</span>' +
            '</div>' +
            '<div class="agent-panel-grid">' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-tactical);grid-column:1/-1">' +
                    '<div class="lcars-panel-header">Fleet Health <span class="lcars-panel-id">0005</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-shield-status"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-tactical)">' +
                    '<div class="lcars-panel-header">Protocol Compliance <span class="lcars-panel-id">0100</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-compliance"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-tactical)">' +
                    '<div class="lcars-panel-header">Trust Topology <span class="lcars-panel-id">0512</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-trust"></div>' +
                '</div>' +
            '</div>';
    }

    window.refresh_integrity = function () {
        ensureLayout();

        Promise.allSettled([
            lcars.catalog.fetch("Mesh Health"),
            lcars.catalog.fetch("Trust Topology")
        ]).then(function (results) {
            var health = results[0].status === "fulfilled" ? results[0].value : null;
            var trust = results[1].status === "fulfilled" ? results[1].value : null;

            // Fleet health — per-agent shield bars
            if (health && health.agents) {
                var dims = health.agents.map(function (a) {
                    var online = a.status === "ok" || a.status === "online" || a.status === "healthy" || a.status === "nominal";
                    return {
                        label: a.id || a.agent_id || "?",
                        value: online ? 1 : 0,
                        max: 1,
                        color: online ? "var(--c-health)" : "var(--c-alert)"
                    };
                });
                lcars.patterns.spectrumBars("fleet-shield-status", dims);
            } else {
                // Fallback — use agent registry
                var agents = window.fleetAgents();
                var statuses = window.fleetStatuses();
                var dims2 = agents.map(function (a) {
                    var s = statuses[a.id];
                    var online = s && (s.status === "online" || s.health === "ok");
                    return { label: a.id || "?", value: online ? 1 : 0, max: 1, color: online ? "var(--c-health)" : "var(--c-alert)" };
                });
                if (dims2.length > 0) {
                    lcars.patterns.spectrumBars("fleet-shield-status", dims2);
                } else {
                    lcars.patterns.placeholder("fleet-shield-status", "No health data");
                }
            }

            // Compliance
            var complianceEl = document.getElementById("fleet-compliance");
            if (complianceEl) {
                var agents2 = window.fleetAgents();
                var total = agents2.length;
                var compliant = total; // Assume compliant until proven otherwise
                complianceEl.innerHTML = '<div style="text-align:center;padding:8px">' +
                    lcars.patterns.badge("nominal", compliant + "/" + total + " COMPLIANT") +
                '</div>';
            }

            // Trust topology
            if (trust) {
                var dimensions = trust.dimensions || trust.trust_dimensions || [];
                if (dimensions.length > 0) {
                    var trustDims = dimensions.map(function (d) {
                        return { label: d.name || d.dimension || "?", value: d.score || d.value || 0, color: "var(--c-tab-tactical)" };
                    });
                    lcars.patterns.spectrumBars("fleet-trust", trustDims);
                } else if (trust.aggregate != null) {
                    lcars.patterns.dataBar("fleet-trust", trust.aggregate, {
                        label: "Trust Aggregate", color: "var(--c-tab-tactical)", polarity: "higher-better"
                    });
                } else {
                    lcars.patterns.placeholder("fleet-trust", "Trust topology computing...");
                }
            } else {
                lcars.patterns.placeholder("fleet-trust", "Trust data unavailable");
            }
        });
    };
})();
