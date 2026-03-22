// ═══ GOVERNANCE STATION (was Operations) ═════════════════════
// Fleet budgets, actions, schedules, CI, consensus.
// Data: /api/mesh/governance, /api/mesh/governance/ci,
//       /api/mesh/governance/consensus, /api/mesh/governance/deliberations

(function () {
    "use strict";

    var _initialized = false;

    function ensureLayout() {
        if (_initialized) return;
        _initialized = true;
        var el = document.getElementById("pane-governance");
        if (!el) return;
        el.innerHTML =
            '<div class="lcars-zone-c" style="margin-bottom:var(--gap-xs)">' +
                '<span class="zc-title">Fleet Governance</span><span class="zc-id">01</span>' +
            '</div>' +
            '<div class="agent-panel-grid">' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-ops);grid-column:1/-1">' +
                    '<div class="lcars-panel-header">Autonomy Budgets <span class="lcars-panel-id">0020</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-budgets"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-ops)">' +
                    '<div class="lcars-panel-header">Recent Actions <span class="lcars-panel-id">1001</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-actions"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-ops)">' +
                    '<div class="lcars-panel-header">Schedule <span class="lcars-panel-id">2001</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-schedule"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-ops)">' +
                    '<div class="lcars-panel-header">CI Status <span class="lcars-panel-id">3001</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-ci"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-ops)">' +
                    '<div class="lcars-panel-header">Consensus <span class="lcars-panel-id">4001</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-consensus"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-ops);grid-column:1/-1">' +
                    '<div class="lcars-panel-header">Deliberation History <span class="lcars-panel-id">5001</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-deliberations"></div>' +
                '</div>' +
            '</div>';
    }

    window.refresh_governance = function () {
        ensureLayout();

        Promise.allSettled([
            lcars.catalog.fetch("Fleet Governance"),
            lcars.catalog.fetch("CI Status"),
            lcars.catalog.fetch("Consensus"),
            lcars.catalog.fetch("Deliberation History")
        ]).then(function (results) {
            var gov = results[0].status === "fulfilled" ? results[0].value : null;
            var ci = results[1].status === "fulfilled" ? results[1].value : null;
            var consensus = results[2].status === "fulfilled" ? results[2].value : null;
            var delib = results[3].status === "fulfilled" ? results[3].value : null;

            // Budgets — per-agent bars
            if (gov) {
                var budgets = gov.budgets || gov.per_agent || [];
                if (budgets.length > 0) {
                    var dims = budgets.map(function (b) {
                        var spent = b.budget_spent || 0;
                        var cutoff = b.budget_cutoff || 20;
                        return { label: b.agent_id || b.id || "?", value: spent, max: cutoff, color: "var(--c-warning)", polarity: "lower-better" };
                    });
                    lcars.patterns.spectrumBars("fleet-budgets", dims);
                } else {
                    lcars.patterns.placeholder("fleet-budgets", "No budget data");
                }

                // Actions
                var actions = gov.recent_actions || [];
                lcars.patterns.taskListing("fleet-actions", actions.slice(0, 10).map(function (a) {
                    return { code: a.action_type || "—", title: a.description || "", status: a.approved ? "nominal" : "advisory" };
                }));

                // Schedule
                var schedule = gov.schedules || gov.schedule || [];
                lcars.patterns.taskListing("fleet-schedule", schedule.map(function (s) {
                    return { code: s.name || s.task || "—", title: s.interval || s.schedule || "", status: "nominal" };
                }));
            } else {
                lcars.patterns.placeholder("fleet-budgets", "Governance unavailable");
            }

            // CI
            if (ci) {
                var workflows = ci.workflows || ci.runs || [];
                lcars.patterns.indicatorStrip("fleet-ci", workflows.slice(0, 10).map(function (w) {
                    return {
                        id: "",
                        label: w.name || w.workflow || "",
                        value: w.status || "—",
                        status: w.conclusion === "success" || w.status === "completed" ? "pass" : w.conclusion === "failure" ? "fail" : "inactive"
                    };
                }));
            } else {
                lcars.patterns.placeholder("fleet-ci", "CI status unavailable");
            }

            // Consensus
            if (consensus) {
                var consensusEl = document.getElementById("fleet-consensus");
                if (consensusEl) {
                    consensusEl.innerHTML = '<div style="text-align:center;padding:8px">' +
                        lcars.patterns.badge(consensus.quorum_met ? "nominal" : "advisory", consensus.quorum_met ? "QUORUM MET" : "PENDING") +
                    '</div>';
                }
            } else {
                lcars.patterns.placeholder("fleet-consensus", "Consensus unavailable");
            }

            // Deliberations
            if (delib) {
                var deliberations = delib.deliberations || delib.data || [];
                lcars.patterns.taskListing("fleet-deliberations", deliberations.slice(0, 10).map(function (d) {
                    return { code: d.id || "—", title: d.description || d.action || "", description: d.agent_id || "", status: d.result || "nominal" };
                }));
            } else {
                lcars.patterns.placeholder("fleet-deliberations", "Deliberation history unavailable");
            }
        });
    };
})();
