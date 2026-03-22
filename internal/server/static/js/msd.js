// ═══ RENDER: MSD (Master Systems Display) ═══════════════════

// Cache for per-agent MSD tree data (fetched from /api/msd)
const msdCache = {};

function renderMSD() {
    try { renderMSDTree(); } catch(e) { console.warn("renderMSDTree:", e.message); }
    try { renderVitals(); } catch(e) { console.warn("renderVitals:", e.message); }
    if (typeof renderLcarsDataGrid === "function") try { renderLcarsDataGrid(); } catch(e) {}
    try { renderAgentCards(); } catch(e) { console.warn("renderAgentCards:", e.message); }
    try { renderTopology(); } catch(e) { console.warn("renderTopology:", e.message); }
    try { renderActivity(); } catch(e) { console.warn("renderActivity:", e.message); }
    try { renderMeshStatusDots(); } catch(e) { console.warn("renderMeshStatusDots:", e.message); }
    try { renderPrometheus(); } catch(e) { console.warn("renderPrometheus:", e.message); }
    // Update MSD status line
    const statusLine = document.getElementById("msd-status-line");
    if (statusLine) {
        const agents = Object.values(agentData);
        const online = agents.filter(a => a?.status === "online").length;
        const total = AGENTS.length;
        const time = new Date().toLocaleTimeString();
        const health = online === total ? "Nominal" : "Degraded";
        statusLine.textContent = `Mesh Status: ${health} \u00B7 Agents: ${online}/${total} Online \u00B7 Last Sync: ${time}`;
    }
}

// ── MSD Tree — cognitive architecture dependency tree ─────────
// Data populated by refreshAll() via /api/mesh/agents/msd proxy.
// msdCache keyed by agent ID with full MSD tree payloads.

// Prometheus cache — populated by refreshAll() via /api/mesh/agents/metrics proxy.
const promCache = {};

// Collapse state — persists across re-renders. Keys: "agent:{id}" or "node:{agentId}:{nodeId}"
// Initialized lazily: nodes at depth >= 2 start collapsed.
const msdCollapseState = {};
let msdInitialized = false;

function toggleMSDNode(key) {
    msdCollapseState[key] = !msdCollapseState[key];
    renderMSDTree();
}
// Expose to onclick
window.toggleMSDNode = toggleMSDNode;

function renderMSDTree() {
    const el = document.getElementById("msd-tree");
    if (!el) return;

    const onlineAgents = Object.values(agentData).filter(a => a.status === "online");
    const total = AGENTS.length;
    const online = onlineAgents.length;

    // Initialize default collapse state: expand agents + top-level subsystems,
    // collapse everything deeper (depth >= 2 in the subsystem tree).
    if (!msdInitialized) {
        for (const agent of AGENTS) {
            // Agents start expanded
            // msdCollapseState[`agent:${agent.id}`] left undefined = expanded
            const msd = msdCache[agent.id];
            if (msd?.tree) {
                for (const node of msd.tree) {
                    // Top-level subsystems (transport, oscillator, etc.) start expanded
                    // msdCollapseState[`node:${agent.id}:${node.id}`] left undefined = expanded
                    // Their children start collapsed
                    if (node.children) {
                        for (const child of node.children) {
                            if (child.children && child.children.length > 0) {
                                msdCollapseState[`node:${agent.id}:${child.id}`] = true;
                            }
                        }
                    }
                }
            }
        }
        msdInitialized = true;
    }

    // ── Helper functions ──────────────────────────────────────
    const dot = (status) => {
        const colors = {
            nominal: "var(--c-health)", active: "var(--c-health)",
            degraded: "var(--c-warning)", warning: "var(--c-warning)",
            alert: "var(--c-alert)", failed: "var(--c-alert)",
            online: "var(--c-health)", unreachable: "var(--c-alert)"
        };
        const c = colors[status] || "var(--text-dim)";
        return `<span class="msd-dot" style="background:${c}"></span>`;
    };

    const miniBar = (pct, color) => {
        const w = Math.max(0, Math.min(100, Math.round(pct)));
        return `<span class="msd-bar"><span class="msd-bar-fill" style="width:${w}%;background:${color}"></span></span>`;
    };

    const fmtVal = (v, unit) => {
        if (v === undefined || v === null || v === "") return "";
        let display = v;
        if (typeof v === "number") {
            display = v % 1 === 0 ? v : v.toFixed(2);
        }
        return `<span class="msd-val">${display}</span>` +
            (unit ? `<span class="msd-unit">${unit}</span>` : "");
    };

    // ── Render a tree node recursively ────────────────────────
    function renderNode(node, depth, isLast, agentId) {
        const indent = depth * 20;
        const connector = depth === 0 ? "" : (isLast ? "└─" : "├─");
        const label = (node.label || node.id).toUpperCase();
        const status = node.status || "";
        const hasChildren = node.children && node.children.length > 0;
        const collapseKey = `node:${agentId}:${node.id}`;
        const collapsed = hasChildren && msdCollapseState[collapseKey];

        let valueHtml = "";
        if (node.value !== undefined && node.value !== null && node.value !== "") {
            valueHtml = fmtVal(node.value, node.unit);
            if (typeof node.value === "number" && node.value >= 0 && node.value <= 1 && node.unit !== "sessions") {
                const barColor = node.value > 0.7 ? "var(--c-health)" :
                    node.value > 0.3 ? "var(--c-warning)" : "var(--c-alert)";
                valueHtml += miniBar(node.value * 100, barColor);
            }
        }

        const toggle = hasChildren
            ? `<span class="msd-toggle" onclick="toggleMSDNode('${collapseKey}')">${collapsed ? "▶" : "▼"}</span>`
            : `<span class="msd-toggle-spacer"></span>`;
        const childCount = hasChildren && collapsed
            ? `<span class="msd-child-count">${node.children.length}</span>` : "";

        let html = `<div class="msd-row" style="padding-left:${indent}px">`;
        if (connector) html += `<span class="msd-connector">${connector}</span>`;
        html += toggle;
        if (status) html += dot(status);
        html += `<span class="msd-label">${label}</span>`;
        html += childCount;
        html += `<span class="msd-value-group">${valueHtml}</span>`;
        html += `</div>`;

        if (hasChildren && !collapsed) {
            for (let i = 0; i < node.children.length; i++) {
                html += renderNode(node.children[i], depth + 1, i === node.children.length - 1, agentId);
            }
        }
        return html;
    }

    // ── Build full tree ───────────────────────────────────────
    let html = "";

    // MESH root node
    html += `<div class="msd-row msd-root">`;
    html += dot(online === total ? "nominal" : "degraded");
    html += `<span class="msd-label msd-root-label">MESH</span>`;
    html += fmtVal(online + "/" + total, "agents");
    html += miniBar(online / total * 100, "var(--c-health)");
    html += `</div>`;

    // Per-agent subtrees
    for (let i = 0; i < AGENTS.length; i++) {
        const agent = AGENTS[i];
        const a = agentData[agent.id];
        const isOnline = a?.status === "online";
        const isLastAgent = i === AGENTS.length - 1;
        const connector = isLastAgent ? "└─" : "├─";
        const name = (agent.name || agent.id.replace(/-agent$/, "")).toUpperCase();
        const agentColor = getComputedStyle(document.documentElement)
            .getPropertyValue(`--c-${agent.id.replace(/-agent$/, "")}`)?.trim() || "var(--text-primary)";

        const agentKey = `agent:${agent.id}`;
        const agentCollapsed = msdCollapseState[agentKey];
        const agentToggle = isOnline
            ? `<span class="msd-toggle" onclick="toggleMSDNode('${agentKey}')">${agentCollapsed ? "▶" : "▼"}</span>`
            : `<span class="msd-toggle-spacer"></span>`;

        html += `<div class="msd-row msd-agent" style="padding-left:20px">`;
        html += `<span class="msd-connector">${connector}</span>`;
        html += agentToggle;
        html += dot(isOnline ? "online" : "unreachable");
        html += `<span class="msd-label msd-agent-label" style="color:${agentColor}">${name}</span>`;

        if (!isOnline) {
            html += `<span class="msd-unreachable">UNREACHABLE</span>`;
            html += `</div>`;
            continue;
        }

        // Show summary when collapsed
        const msd = msdCache[agent.id];
        if (agentCollapsed && msd?.tree) {
            const subsystems = msd.tree.map(n => (n.label || n.id).toLowerCase()).join(", ");
            html += `<span class="msd-child-count">${msd.tree.length} subsystems</span>`;
        }
        html += `</div>`;

        // Render subtree when expanded
        if (!agentCollapsed) {
            if (msd?.tree) {
                for (let j = 0; j < msd.tree.length; j++) {
                    html += renderNode(msd.tree[j], 2, j === msd.tree.length - 1, agent.id);
                }
            } else {
                html += `<div class="msd-row" style="padding-left:40px">`;
                html += `<span class="msd-connector">└─</span>`;
                html += `<span class="msd-label" style="color:var(--text-dim);font-style:italic">awaiting subsystem data...</span>`;
                html += `</div>`;
            }
        }
    }

    el.innerHTML = html;

    // Footer
    const footer = document.getElementById("msd-footer-num");
    if (footer) footer.textContent = ` ${online}/${total} online`;
}

// ── Prometheus Metrics Panel ──────────────────────────────────
// Data populated by refreshAll() via /api/mesh/agents/metrics proxy.
// promCache keyed by agent ID with parsed metric maps.

const PROM_GAUGES = [
    { name: "agentd_oscillator_active", label: "OSCILLATOR", unit: "" },
    { name: "agentd_sync_cycles_total", label: "SYNC CYCLES", unit: "" },
    { name: "agentd_http_requests_total", label: "HTTP REQUESTS", unit: "" },
    { name: "process_resident_memory_bytes", label: "MEMORY", unit: "MB", scale: 1 / (1024 * 1024) },
    { name: "go_goroutines", label: "GOROUTINES", unit: "" },
];

function renderPrometheus() {
    const el = document.getElementById("prom-metrics");
    if (!el) return;

    const onlineAgents = AGENTS.filter(a => agentData[a.id]?.status === "online");
    if (onlineAgents.length === 0) {
        el.innerHTML = `<span class="msd-label" style="color:var(--text-dim)">No agents online</span>`;
        return;
    }

    // Build grid from promCache (populated by refreshAll proxy fetch)
    let html = `<div class="prom-grid">`;

    // Header row
    html += `<div class="prom-row prom-header">`;
    html += `<span class="prom-cell prom-label-cell">METRIC</span>`;
    for (const agent of onlineAgents) {
        const name = (agent.name || agent.id.replace(/-agent$/, "")).toUpperCase();
        const color = getComputedStyle(document.documentElement)
            .getPropertyValue(`--c-${agent.id.replace(/-agent$/, "")}`)?.trim() || "var(--text-primary)";
        html += `<span class="prom-cell prom-agent-cell" style="color:${color}">${name}</span>`;
    }
    html += `</div>`;

    // Metric rows
    for (const gauge of PROM_GAUGES) {
        html += `<div class="prom-row">`;
        html += `<span class="prom-cell prom-label-cell">${gauge.label}</span>`;
        for (const agent of onlineAgents) {
            const m = promCache[agent.id];
            let val = m ? m[gauge.name] : undefined;
            if (val !== undefined && gauge.scale) val *= gauge.scale;
            const display = val !== undefined ? (val % 1 === 0 ? val.toString() : val.toFixed(1)) : "—";
            html += `<span class="prom-cell prom-val-cell">${display}</span>`;
        }
        html += `</div>`;
    }

    html += `</div>`;
    el.innerHTML = html;
}

function renderMeshStatusDots() {
    const el = document.getElementById("mesh-status-dots");
    if (!el) return;
    const online = Object.values(agentData).filter(a => a.status === "online");
    const total = AGENTS.length;
    const pending = online.reduce((s, a) => s + ((a.data?.totals || {}).unprocessed || 0), 0);
    const gates = online.reduce((s, a) => s + (a.data?.active_gates || []).length, 0);

    const subsystems = [
        { label: "TRANSPORT LINK", color: online.length === total ? "green" : online.length > 0 ? "amber" : "red" },
        { label: "AGENT DISCOVERY", color: online.length >= Math.ceil(total * 0.8) ? "green" : "amber" },
        { label: "VOCABULARY DATABASE", color: "green" },
        { label: "MESSAGE QUEUE", color: pending > 5 ? "amber" : pending > 0 ? "blue" : "green" },
        { label: "CONSENSUS GATES", color: gates > 3 ? "amber" : gates > 0 ? "blue" : "green" },
    ];

    el.innerHTML = subsystems.map(s =>
        `<div class="lcars-status-dot-row">
            <span class="lcars-status-dot-indicator ${s.color}"></span>
            <span class="lcars-status-dot-label">${s.label}</span>
        </div>`
    ).join("");
}
