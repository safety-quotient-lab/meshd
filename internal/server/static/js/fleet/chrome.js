// ═══ Fleet Chrome — Station Switching + SSE + Agent Registry ═══
// Meshd fleet LCARS dashboard controller.

(function () {
    "use strict";

    // Agent registry — populated from /.well-known/agents or config
    var AGENTS = [];
    var agentStatuses = {};
    var activeStation = "overview";
    var eventSource = null;
    var refreshInterval = null;

    // ── Expose globally for station modules ─────────────────
    window.fleetAgents = function () { return AGENTS; };
    window.fleetStatuses = function () { return agentStatuses; };

    // ── Station switching ────────────────────────────────────
    window.switchStation = function (station) {
        activeStation = station;
        var buttons = document.querySelectorAll(".lcars-sidebar-btn");
        for (var i = 0; i < buttons.length; i++) {
            buttons[i].classList.toggle("active", buttons[i].dataset.tab === station);
        }
        var panes = document.querySelectorAll(".tab-pane");
        for (var j = 0; j < panes.length; j++) {
            panes[j].classList.toggle("active", panes[j].id === "pane-" + station);
        }
        var url = new URL(location);
        url.searchParams.set("station", station);
        history.replaceState(null, "", url);
        refreshStation(station);
    };

    // ── Station refresh dispatch ────────────────────────────
    function refreshStation(station) {
        var fn = window["refresh_" + station];
        if (fn) fn();
    }

    // ── Fetch agent statuses ────────────────────────────────
    function fetchAgentStatuses() {
        // Fetch from meshd's aggregated endpoint
        return lcars.catalog.fetch("Mesh Overview").then(function (data) {
            // Update header
            var badge = document.getElementById("fleet-agents-badge");
            if (badge) {
                var online = data.agents_online || data.online || 0;
                var total = data.agents_total || data.total || AGENTS.length;
                badge.textContent = online + "/" + total + " ONLINE";
            }
            // Store agent data
            if (data.agents) {
                for (var i = 0; i < data.agents.length; i++) {
                    var a = data.agents[i];
                    agentStatuses[a.agent_id || a.id] = a;
                }
            }
            // Alert check
            if (data.mesh_health === "degraded") {
                document.body.classList.add("alert-yellow");
            } else {
                document.body.classList.remove("alert-yellow", "alert-red");
            }
            return data;
        }).catch(function () {
            // Mesh overview unavailable — try direct agent polling
        });
    }

    // ── Real-time (WebSocket preferred, SSE fallback) ──────
    var ws = null;
    function connectRealtime() {
        var wsProto = location.protocol === "https:" ? "wss:" : "ws:";
        var wsUrl = wsProto + "//" + location.host + "/ws";
        try {
            ws = new WebSocket(wsUrl);
            ws.onmessage = function (evt) {
                try {
                    var data = JSON.parse(evt.data);
                    if (data.event === "refresh" || data.event === "event") {
                        refreshStation(activeStation);
                    }
                } catch (e) {}
            };
            ws.onclose = function () {
                ws = null;
                setTimeout(connectRealtime, 5000);
            };
            ws.onerror = function () {
                ws.close();
                ws = null;
                connectSSE();
            };
        } catch (e) {
            connectSSE();
        }
    }
    function connectSSE() {
        if (eventSource) eventSource.close();
        eventSource = new EventSource("/events");
        eventSource.onmessage = function () { refreshStation(activeStation); };
        eventSource.onerror = function () { console.warn("[sse] reconnecting..."); };
    }

    // ── Load agent registry ─────────────────────────────────
    function loadAgents() {
        return window.fetch("/.well-known/agents", {
            signal: AbortSignal.timeout(5000)
        }).then(function (resp) {
            if (!resp.ok) throw new Error("agents fetch failed");
            return resp.json();
        }).then(function (agents) {
            if (Array.isArray(agents)) AGENTS = agents;
        }).catch(function () {
            // Fallback — use hardcoded defaults
            AGENTS = [
                { id: "psychology-agent", url: "https://psychology-agent.safety-quotient.dev" },
                { id: "safety-quotient-agent", url: "https://psq-agent.safety-quotient.dev" },
                { id: "unratified-agent", url: "https://unratified-agent.unratified.org" },
                { id: "observatory-agent", url: "https://observatory-agent.unratified.org" }
            ];
        });
    }

    // ── Periodic refresh ────────────────────────────────────
    function startPeriodicRefresh() {
        if (refreshInterval) clearInterval(refreshInterval);
        refreshInterval = setInterval(function () {
            fetchAgentStatuses().then(function () {
                refreshStation(activeStation);
            });
        }, 30000);
    }

    // ── Init ────────────────────────────────────────────────
    function init() {
        lcars.catalog.load("").then(function () {
            return loadAgents();
        }).then(function () {
            return fetchAgentStatuses();
        }).then(function () {
            var params = new URLSearchParams(location.search);
            var station = params.get("station");
            if (station && document.getElementById("pane-" + station)) {
                switchStation(station);
            } else {
                refreshStation(activeStation);
            }
            connectRealtime();
            startPeriodicRefresh();
        });
    }

    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", init);
    } else {
        init();
    }
})();
