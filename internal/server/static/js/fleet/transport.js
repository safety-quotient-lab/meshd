// ═══ TRANSPORT STATION (was Helm) ════════════════════════════
// Mesh transport — routing, session overview, message flow.
// Data: /api/mesh/transport/routing, per-agent /api/agent/transport

(function () {
    "use strict";

    var _initialized = false;

    function ensureLayout() {
        if (_initialized) return;
        _initialized = true;
        var el = document.getElementById("pane-transport");
        if (!el) return;
        el.innerHTML =
            '<div class="lcars-zone-c" style="margin-bottom:var(--gap-xs)">' +
                '<span class="zc-title">Inter-Agent Transport</span><span class="zc-id">8747</span>' +
            '</div>' +
            '<div class="agent-panel-grid">' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-helm);grid-column:1/-1">' +
                    '<div class="lcars-panel-header">Transport Routing <span class="lcars-panel-id">8686</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-routing"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-helm);grid-column:1/-1">' +
                    '<div class="lcars-panel-header">Message Volume <span class="lcars-panel-id">4343</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-message-volume"></div>' +
                '</div>' +
            '</div>';
    }

    window.refresh_transport = function () {
        ensureLayout();

        lcars.catalog.fetch("Transport Routing").then(function (data) {
            var routes = data.routes || data.routing || [];
            if (Array.isArray(routes) && routes.length > 0) {
                lcars.patterns.taskListing("fleet-routing", routes.map(function (r) {
                    return {
                        code: r.domain || r.session || "—",
                        title: r.agent || r.target || "",
                        description: r.description || "",
                        status: "nominal"
                    };
                }));
            } else if (typeof routes === "object") {
                // Object format — render as key-value
                var items = [];
                for (var key in routes) {
                    items.push({ code: key, title: String(routes[key]), status: "nominal" });
                }
                lcars.patterns.taskListing("fleet-routing", items);
            } else {
                lcars.patterns.placeholder("fleet-routing", "No routing data");
            }
        }).catch(function () {
            lcars.patterns.placeholder("fleet-routing", "Routing unavailable");
        });

        // Aggregate message volume from agent statuses
        var statuses = window.fleetStatuses();
        var agents = window.fleetAgents();
        var cells = [];
        for (var i = 0; i < agents.length; i++) {
            var a = agents[i];
            var s = statuses[a.id];
            var msgCount = s ? (s.total_messages || s.totals && s.totals.messages || 0) : 0;
            cells.push({ value: msgCount, label: (a.id || "?").substring(0, 10), type: "count" });
        }
        if (cells.length > 0) {
            lcars.patterns.numberGrid("fleet-message-volume", cells);
        } else {
            lcars.patterns.placeholder("fleet-message-volume", "No message data");
        }
    };
})();
