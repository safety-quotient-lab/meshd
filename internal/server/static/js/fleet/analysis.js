// ═══ ANALYSIS STATION (was Science) ══════════════════════════
// Mesh-level analytics — emergent properties, generator balance,
// collective state.
// Data: /api/mesh/state, /api/mesh/state/operational-health

(function () {
    "use strict";

    var _initialized = false;

    function ensureLayout() {
        if (_initialized) return;
        _initialized = true;
        var el = document.getElementById("pane-analysis");
        if (!el) return;
        el.innerHTML =
            '<div class="lcars-zone-c" style="margin-bottom:var(--gap-xs)">' +
                '<span class="zc-title">Mesh Analysis</span><span class="zc-id">47</span>' +
            '</div>' +
            '<div class="agent-panel-grid">' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-science);grid-column:1/-1">' +
                    '<div class="lcars-panel-header">Emergent State <span class="lcars-panel-id">0042</span></div>' +
                    '<div class="lcars-panel-body" id="analysis-emergent"></div>' +
                    '<div class="lcars-panel-footer">Mesh-Level Properties<span class="lcars-panel-footer-num"></span></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-science)">' +
                    '<div class="lcars-panel-header">Fleet Operational Health <span class="lcars-panel-id">1974</span></div>' +
                    '<div class="lcars-panel-body" id="analysis-health"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-science)">' +
                    '<div class="lcars-panel-header">Coordination Ratio <span class="lcars-panel-id">2024</span></div>' +
                    '<div class="lcars-panel-body" id="analysis-coordination"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-science)">' +
                    '<div class="lcars-panel-header">Immune Response <span class="lcars-panel-id">1968</span></div>' +
                    '<div class="lcars-panel-body" id="analysis-immune"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-science)">' +
                    '<div class="lcars-panel-header">Bottleneck Analysis <span class="lcars-panel-id">3140</span></div>' +
                    '<div class="lcars-panel-body" id="analysis-bottleneck"></div>' +
                '</div>' +
            '</div>';
    }

    window.refresh_analysis = function () {
        ensureLayout();

        Promise.allSettled([
            lcars.catalog.fetch("Emergent State"),
            lcars.catalog.fetch("Fleet Operational Health")
        ]).then(function (results) {
            var emergent = results[0].status === "fulfilled" ? results[0].value : null;
            var health = results[1].status === "fulfilled" ? results[1].value : null;

            // Emergent state
            if (emergent) {
                var dims = [];
                var fields = ["affect", "bottleneck", "coordination", "immune", "distribution"];
                for (var i = 0; i < fields.length; i++) {
                    var key = fields[i];
                    var val = emergent[key];
                    if (val != null && typeof val === "object") {
                        // Object — render key fields
                        for (var subKey in val) {
                            if (typeof val[subKey] === "number") {
                                dims.push({ label: key + "." + subKey, value: val[subKey], color: "var(--c-tab-science)" });
                            }
                        }
                    } else if (typeof val === "number") {
                        dims.push({ label: key, value: val, color: "var(--c-tab-science)" });
                    }
                }
                if (dims.length > 0) {
                    lcars.patterns.spectrumBars("analysis-emergent", dims);
                } else {
                    lcars.patterns.placeholder("analysis-emergent", "No emergent properties computed");
                }

                // Coordination
                var coord = emergent.coordination;
                if (coord && typeof coord === "object") {
                    var coordEl = document.getElementById("analysis-coordination");
                    if (coordEl) {
                        coordEl.innerHTML =
                            '<div style="text-align:center;padding:8px">' +
                                '<div style="font-family:Antonio,Oswald,sans-serif;font-size:1.4em;font-weight:700;color:var(--text-primary)">' +
                                    (coord.ratio != null ? coord.ratio.toFixed(1) + "x" : "—") +
                                '</div>' +
                                lcars.patterns.badge(coord.status === "over-coordinated" ? "critical" : coord.status === "coordination-heavy" ? "warning" : "nominal", coord.status || "—") +
                            '</div>';
                    }
                } else {
                    lcars.patterns.placeholder("analysis-coordination", "No coordination data");
                }

                // Immune
                if (emergent.immune != null) {
                    var immuneEl = document.getElementById("analysis-immune");
                    if (immuneEl) {
                        immuneEl.innerHTML = '<div style="text-align:center;padding:8px">' +
                            lcars.patterns.badge(emergent.immune === "active" ? "nominal" : "advisory", String(emergent.immune)) +
                        '</div>';
                    }
                }

                // Bottleneck
                if (emergent.bottleneck) {
                    var bnEl = document.getElementById("analysis-bottleneck");
                    if (bnEl) {
                        var bn = emergent.bottleneck;
                        bnEl.innerHTML = '<div style="font-family:Antonio,Oswald,sans-serif;font-size:0.85em;color:var(--text-primary);text-transform:uppercase;padding:8px">' +
                            (typeof bn === "string" ? bn : JSON.stringify(bn)) +
                        '</div>';
                    }
                }
            } else {
                lcars.patterns.placeholder("analysis-emergent", "Emergent state unavailable");
            }

            // Fleet operational health
            if (health) {
                lcars.patterns.dataBar("analysis-health", health.composite || 0, {
                    label: "Fleet Composite", color: "var(--c-health)", polarity: "higher-better"
                });
            } else {
                lcars.patterns.placeholder("analysis-health", "Fleet health unavailable");
            }
        });
    };
})();
