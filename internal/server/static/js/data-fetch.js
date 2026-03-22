// ═══ DATA FETCH ═════════════════════════════════════════════

// Version check — detect when server has newer frontend
let _loadedVersion = null;
function checkVersionHeader(resp) {
    const sv = resp.headers.get("X-Meshd-Version");
    if (!sv) return;
    if (!_loadedVersion) { _loadedVersion = sv; return; }
    if (sv !== _loadedVersion) {
        const pill = document.getElementById("lcars-version-pill");
        if (pill) { pill.style.display = "flex"; pill.title = `New version: ${sv}`; }
        const sd = document.getElementById("lcars-hdr-stardate");
        if (sd) sd.classList.add("hdr-attract");
    }
}

// Adaptive polling — backs off when agents unreachable
let _failedAgents = new Set();
let _pollInterval = 30000;
const POLL_MIN = 30000;
const POLL_MAX = 120000;
const FETCH_TIMEOUT = 5000;

// Fetch meshd's own status (same-origin, always fast)
async function fetchLocalStatus() {
    try {
        const resp = await fetch("/api/status", { signal: AbortSignal.timeout(FETCH_TIMEOUT) });
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        checkVersionHeader(resp);
        return await resp.json();
    } catch { return null; }
}

// Fetch agent discovery list from /.well-known/agents (same-origin)
async function fetchAgentDiscovery() {
    try {
        const resp = await fetch("/.well-known/agents", { signal: AbortSignal.timeout(FETCH_TIMEOUT) });
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        return await resp.json();
    } catch { return null; }
}

// Fetch aggregated agent statuses via meshd proxy (same-origin, no CORS)
async function fetchAgentsStatusProxy() {
    try {
        const resp = await fetch("/api/mesh/agents/status", { signal: AbortSignal.timeout(8000) });
        if (!resp.ok) return null;
        return await resp.json();
    } catch { return null; }
}

let _refreshing = false;
async function refreshAll() {
    if (_refreshing) return;
    _refreshing = true;
    try {
    // All fetches in parallel — same-origin via meshd proxy endpoints.
    // Discovery (lightweight) + MSD proxy + metrics proxy in one batch.
    // Full /api/status fetched only for selected agent (on-demand).
    const [localData, discoveryData, msdProxyData, metricsProxyData] =
        await Promise.allSettled([
            fetchLocalStatus(),
            fetchAgentDiscovery(),
            fetch("/api/mesh/agents/msd", { signal: AbortSignal.timeout(8000) }).then(r => r.ok ? r.json() : null).catch(() => null),
            fetch("/api/mesh/agents/metrics", { signal: AbortSignal.timeout(8000) }).then(r => r.ok ? r.json() : null).catch(() => null),
        ]);
    const local = localData.status === "fulfilled" ? localData.value : null;
    const discovery = discoveryData.status === "fulfilled" ? discoveryData.value : null;
    const msdProxy = msdProxyData.status === "fulfilled" ? msdProxyData.value : null;
    const metricsProxy = metricsProxyData.status === "fulfilled" ? metricsProxyData.value : null;

    // Initialize all agents with stub data
    for (const agent of AGENTS) {
        if (!agentData[agent.id]) {
            agentData[agent.id] = { id: agent.id, status: "unreachable", data: { agent_id: agent.id } };
        }
    }

    // Populate from discovery (availability + basic identity)
    if (discovery && Array.isArray(discovery)) {
        for (const da of discovery) {
            const match = AGENTS.find(a => a.id === da.id) ||
                          AGENTS.find(a => da.id.toLowerCase().includes(a.id.split("-")[0]));
            const aid = match ? match.id : da.id;

            if (da.available) {
                // Preserve existing rich data if we have it, augment with discovery
                const existing = agentData[aid]?.data || {};
                agentData[aid] = {
                    id: aid, status: "online",
                    data: { ...existing, agent_id: aid, version: da.version || existing.version || "" }
                };
                _failedAgents.delete(aid);
            } else {
                agentData[aid] = { id: aid, status: "unreachable", data: { agent_id: aid } };
                _failedAgents.add(aid);
            }
        }
    }

    // Enrich local agent with full status (overrides proxy data for mesh itself)
    if (local) {
        const aid = local.agent_id || "mesh";
        agentData[aid] = { id: aid, status: "online", data: local };
        _failedAgents.delete(aid);
    }

    // Populate MSD cache from proxy (no cross-origin needed)
    if (msdProxy && typeof msdCache !== "undefined") {
        for (const [agentId, msdData] of Object.entries(msdProxy)) {
            // Match proxy keys to AGENTS array
            const match = AGENTS.find(a => a.id === agentId) ||
                          AGENTS.find(a => agentId.toLowerCase().includes(a.id.split("-")[0]));
            const aid = match ? match.id : agentId;
            msdCache[aid] = msdData;
        }
    }

    // Populate prometheus data from proxy
    if (metricsProxy && typeof promCache !== "undefined") {
        for (const entry of metricsProxy) {
            promCache[entry.agent_id] = entry.metrics;
        }
    }

    // Phase 5: Render
    try { renderMSD(); } catch(e) { console.error("renderMSD failed:", e); }
    try { renderGovernance(); } catch(e) { console.error("renderGovernance failed:", e); }
    refreshActiveStation();

    // Fetch KB data (non-blocking)
    refreshKnowledge();

    // Adaptive backoff
    const failRate = _failedAgents.size / AGENTS.length;
    if (failRate > 0.5) {
        _pollInterval = Math.min(_pollInterval * 1.5, POLL_MAX);
    } else if (failRate === 0) {
        _pollInterval = POLL_MIN;
    }
    if (refreshTimer && !sseActive) {
        clearInterval(refreshTimer);
        refreshTimer = setInterval(refreshAll, _pollInterval);
    }

    const intervalSec = Math.round(_pollInterval / 1000);
    const mode = sseActive ? "\u25CF live" : `\u25CB poll ${intervalSec}s`;
    const ftr = document.getElementById("footer-status");
    if (ftr) ftr.textContent = `Updated ${new Date().toLocaleTimeString()} · ${mode}`;

    if (document.body.classList.contains("theme-lcars")) {
        updateLcarsHeaderData();
        evaluateAlertLevel();
        const ftrFeed = document.getElementById("lcars-ftr-feed");
        if (ftrFeed) ftrFeed.textContent = `Feed: ${sseActive ? "\u25CF Live" : "\u25CB Polling"}`;
        addNarrativeEntry(generateMeshNarrative());
    }
    } finally { _refreshing = false; }
}

// renderAll — re-render all tabs from cached agentData (no fetch)
function renderAll() {
    try { renderMSD(); } catch(e) {}
    try { renderGovernance(); } catch(e) {}
    refreshActiveStation();
    if (document.body.classList.contains("theme-lcars")) {
        updateLcarsHeaderData();
        evaluateAlertLevel();
    }
}

// refreshActiveStation — fetch + render data for the currently visible station tab.
function refreshActiveStation() {
    const activeTab = document.querySelector('.lcars-tab.active')?.dataset?.tab ||
                      document.querySelector('.lcars-sidebar-btn.active')?.dataset?.tab;
    if (!activeTab) return;
    try {
        if (activeTab === "engineering" && typeof fetchArchitectureData === "function") fetchArchitectureData();
        else if (activeTab === "science" && typeof fetchAnalysisData === "function") fetchAnalysisData();
        else if (activeTab === "medical" && typeof fetchVitalsData === "function") fetchVitalsData();
        else if (activeTab === "helm" && typeof fetchTransportData === "function") fetchTransportData();
        else if (activeTab === "tactical" && typeof fetchIntegrityData === "function") fetchIntegrityData();
    } catch(e) { console.error("refreshActiveStation failed:", e); }
}

// ── Render: Vitals ─────────────────────────────────────────────
function renderVitals() {
    const online = Object.values(agentData).filter(a => a.status === "online");
    const total = AGENTS.length;

    // Autonomy: deliberations = autonomous actions taken (counter model)
    const totalDeliberations = online.reduce((sum, a) =>
        sum + getDeliberations(a.data?.autonomy_budget), 0);
    const totalCutoff = online.reduce((sum, a) =>
        sum + getCutoff(a.data?.autonomy_budget), 0);
    const pending = online.reduce((sum, a) => sum + ((a.data?.totals || {}).unprocessed || 0), 0);
    const gates = online.reduce((sum, a) => sum + (a.data?.active_gates || []).length, 0);
    const debt = online.reduce((sum, a) => sum + ((a.data?.totals || {}).epistemic_flags_unresolved || 0), 0);

    const agentsEl = document.getElementById("vital-agents");
    agentsEl.className = "vital-value " + (online.length === total ? "healthy" : online.length > 0 ? "degraded" : "critical");
    setTrackedValue("vital-agents", online.length, { suffix: `/${total}` });
    setTrackedValue("vital-budget", totalDeliberations, {
        suffix: totalCutoff > 0 ? `/${totalCutoff}` : ""
    });
    setTrackedValue("vital-pending", pending);
    setTrackedValue("vital-gates", gates);
    setTrackedValue("vital-debt", debt);

    // Sparklines
    pushSparkValue("vital-agents", online.length);
    pushSparkValue("vital-budget", totalDeliberations);
    pushSparkValue("vital-pending", pending);
    pushSparkValue("vital-gates", gates);

    // Per-agent deliberation sparklines
    for (const agent of AGENTS) {
        const d = agentData[agent.id];
        if (d?.status === "online") {
            pushSparkValue(`delib-${agent.id}`, getDeliberations(d.data?.autonomy_budget));
        }
    }
}

function renderActivity() {
    const el = document.getElementById("activity-feed");
    if (!el) return;
    const items = [];
    for (const a of Object.values(agentData)) {
        if (a.status !== "online") continue;
        for (const m of (a.data?.recent_messages || [])) {
            items.push({ agent: a.id, ...m });
        }
        for (const d of (a.data?.recent_deliberations || [])) {
            items.push({ agent: a.id, type: "deliberation", ...d });
        }
    }
    items.sort((a, b) => (b.timestamp || "").localeCompare(a.timestamp || ""));
    if (items.length === 0) {
        el.innerHTML = `<div class="phase-stub"><div class="phase-stub-text">No recent activity</div></div>`;
        return;
    }
    el.innerHTML = items.slice(0, 15).map(item => {
        const time = item.timestamp ? new Date(item.timestamp).toLocaleTimeString() : "";
        const agent = (item.agent || "").replace(/-agent$/, "");
        const subject = item.subject || item.type || "event";
        return `<div class="activity-row">
            <span class="activity-time">${time}</span>
            <span class="activity-agent">${agent}</span>
            <span class="activity-subject">${subject}</span>
        </div>`;
    }).join("");
}

// ── Agent Cards ───────────────────────────────────────────────
function renderAgentCards() {
    const el = document.getElementById("agent-card-grid");
    if (!el) return;
    el.innerHTML = AGENTS.map(agent => {
        const d = agentData[agent.id];
        const isOnline = d?.status === "online";
        const name = (agent.name || agent.id.replace(/-agent$/, "")).toUpperCase();
        const version = d?.data?.version || "";
        const spent = getDeliberations(d?.data?.autonomy_budget);
        const cutoff = getCutoff(d?.data?.autonomy_budget);
        const health = d?.data?.health || (isOnline ? "healthy" : "offline");
        const statusClass = isOnline ? "card-online" : "card-offline";
        const colorVar = `var(--c-${agent.id.replace(/-agent$/, "")}, var(--text-primary))`;

        return `<div class="agent-card ${statusClass}">
            <div class="agent-card-header" style="border-left:3px solid ${colorVar}">
                <span class="agent-card-name">${name}</span>
                <span class="agent-card-version">${version}</span>
            </div>
            <div class="agent-card-body">
                <div class="agent-card-metric">
                    <span class="agent-card-label">STATUS</span>
                    <span class="agent-card-value ${health}">${health.toUpperCase()}</span>
                </div>
                <div class="agent-card-metric">
                    <span class="agent-card-label">BUDGET</span>
                    <span class="agent-card-value">${spent}/${cutoff}</span>
                </div>
            </div>
        </div>`;
    }).join("");
}

// ── Topology ──────────────────────────────────────────────────
function renderTopology() {
    const el = document.getElementById("topology-view");
    if (!el) return;
    const online = Object.values(agentData).filter(a => a.status === "online");
    const total = AGENTS.length;
    const meshNode = `<span class="topo-node topo-mesh">MESH</span>`;
    const agentNodes = AGENTS.map(agent => {
        const d = agentData[agent.id];
        const isOnline = d?.status === "online";
        const name = (agent.name || agent.id.replace(/-agent$/, ""));
        const cls = isOnline ? "topo-online" : "topo-offline";
        return `<span class="topo-node ${cls}">${name}</span>`;
    }).join("");
    el.innerHTML = `<div class="topo-hub">${meshNode}</div><div class="topo-spokes">${agentNodes}</div>`;
}

// ── Budget helpers ────────────────────────────────────────────
function getDeliberations(budget) {
    if (!budget) return 0;
    return parseInt(budget.budget_spent) || 0;
}
function getCutoff(budget) {
    if (!budget) return 0;
    return parseInt(budget.budget_cutoff) || 0;
}
