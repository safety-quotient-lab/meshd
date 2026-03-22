// ═══ OVERVIEW STATION (was Pulse) ════════════════════════════
// Fleet-wide health dashboard — agent cards, mesh topology, activity.
// Data: /api/mesh (root), per-agent /api/status via agent URLs

(function () {
    "use strict";

    var _initialized = false;

    function ensureLayout() {
        if (_initialized) return;
        _initialized = true;
        var el = document.getElementById("pane-overview");
        if (!el) return;
        el.innerHTML =
            '<div class="lcars-zone-c" style="margin-bottom:var(--gap-xs)">' +
                '<span class="zc-title">Mesh Overview</span><span class="zc-id">01</span>' +
            '</div>' +
            '<div id="overview-vitals-grid" class="number-grid" style="margin-bottom:var(--gap-l)"></div>' +
            '<div class="agent-panel-grid">' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-health);grid-column:1/-1">' +
                    '<div class="lcars-panel-header">Agent Fleet <span class="lcars-panel-id">0001</span></div>' +
                    '<div class="lcars-panel-body" id="overview-agents"></div>' +
                    '<div class="lcars-panel-footer">Registered Agents<span class="lcars-panel-footer-num"></span></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-transport)">' +
                    '<div class="lcars-panel-header">Mesh Health <span class="lcars-panel-id">0010</span></div>' +
                    '<div class="lcars-panel-body" id="overview-health"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-knowledge)">' +
                    '<div class="lcars-panel-header">Activity Stream <span class="lcars-panel-id">0100</span></div>' +
                    '<div class="lcars-panel-body" id="overview-activity"></div>' +
                '</div>' +
            '</div>';
    }

    window.refresh_overview = function () {
        ensureLayout();

        lcars.catalog.fetch("Mesh Overview").then(function (data) {
            // Vitals grid
            var cells = [
                { value: data.agents_online || data.online || 0, label: "ONLINE", type: "count" },
                { value: data.agents_total || data.total || 0, label: "TOTAL", type: "id" },
                { value: data.pending_messages || 0, label: "PENDING", type: data.pending_messages > 0 ? "count" : "val" },
                { value: data.active_gates || 0, label: "GATES", type: data.active_gates > 0 ? "count" : "val" },
                { value: data.total_budget_spent != null ? Math.round(data.total_budget_spent) : "—", label: "BUDGET SPENT", type: "val" }
            ];
            lcars.patterns.numberGrid("overview-vitals-grid", cells);

            // Agent cards
            renderAgentCards(data.agents || []);

            // Health badge
            var healthEl = document.getElementById("overview-health");
            if (healthEl) {
                var health = data.mesh_health || "unknown";
                healthEl.innerHTML =
                    '<div style="text-align:center;padding:12px">' +
                        lcars.patterns.badge(health) +
                        '<div style="font-size:0.75em;color:var(--text-dim);margin-top:8px">' +
                            (data.mesh_mode || "—") +
                        '</div>' +
                    '</div>';
            }
        }).catch(function () {
            lcars.patterns.placeholder("overview-agents", "Mesh overview unavailable");
        });
    };

    function renderAgentCards(agents) {
        var el = document.getElementById("overview-agents");
        if (!el) return;
        if (!agents || agents.length === 0) {
            el.innerHTML = '<div class="panel-placeholder">No agents registered</div>';
            return;
        }
        var html = '<div style="display:flex;flex-direction:column;gap:6px">';
        for (var i = 0; i < agents.length; i++) {
            var a = agents[i];
            var id = a.agent_id || a.id || "unknown";
            var status = a.status || a.health || "unknown";
            var budget = a.autonomy_budget;
            var spent = budget ? (budget.budget_spent || 0) : "—";
            html +=
                '<div style="display:flex;justify-content:space-between;align-items:center;padding:4px 0;border-bottom:1px solid var(--border)">' +
                    '<span style="font-family:Antonio,Oswald,sans-serif;font-size:0.85em;color:var(--text-primary);text-transform:uppercase;letter-spacing:0.04em">' + id + '</span>' +
                    '<div style="display:flex;gap:8px;align-items:center">' +
                        '<span style="font-family:Antonio,Oswald,sans-serif;font-size:0.75em;color:var(--text-dim)">' + spent + '</span>' +
                        lcars.patterns.badge(status === "online" || status === "ok" || status === "nominal" ? "nominal" : "warning") +
                    '</div>' +
                '</div>';
        }
        html += '</div>';
        el.innerHTML = html;
    }
})();
