// ═══ RENDER: MSD (Master Systems Display) ═══════════════════
function renderOverview() {
    try { renderMSDTree(); } catch(e) { console.warn("renderMSDTree:", e.message); }
    try { renderVitals(); } catch(e) { console.warn("renderVitals:", e.message); }
    try { renderLcarsDataGrid(); } catch(e) { console.warn("renderLcarsDataGrid:", e.message); }
    try { renderAgentCards(); } catch(e) { console.warn("renderAgentCards:", e.message); }
    try { renderTopology(); } catch(e) { console.warn("renderTopology:", e.message); }
    try { renderActivity(); } catch(e) { console.warn("renderActivity:", e.message); }
    try { renderMeshStatusDots(); } catch(e) { console.warn("renderMeshStatusDots:", e.message); }
    // Update MSD status line
    const pulseStatus = document.getElementById("msd-status-line");
    if (pulseStatus) {
        const agents = Object.values(agentData);
        const online = agents.filter(a => a?.status === "online").length;
        const total = AGENTS.length;
        const time = new Date().toLocaleTimeString();
        const health = online === total ? "Nominal" : "Degraded";
        pulseStatus.textContent = `Mesh Status: ${health} \u00B7 Agents: ${online}/${total} Online \u00B7 Last Sync: ${time}`;
    }
}

// ── MSD Tree — mesh-level dependency structure ──────────────
function renderMSDTree() {
    const el = document.getElementById("msd-tree");
    if (!el) return;
    const agents = Object.values(agentData).filter(a => a.status === "online");
    const total = AGENTS.length;
    const online = agents.length;
    const pending = agents.reduce((s, a) => s + ((a.data?.totals || {}).unprocessed || 0), 0);
    const gates = agents.reduce((s, a) => s + (a.data?.active_gates || []).length, 0);
    const budgetSpent = agents.reduce((s, a) => s + (parseFloat(a.data?.autonomy_budget?.budget_spent) || 0), 0);
    const budgetMax = agents.reduce((s, a) => s + (parseFloat(a.data?.autonomy_budget?.budget_cutoff) || 20), 0);

    // Build tree structure
    const statusDot = (ok) => `<span style="display:inline-block;width:8px;height:8px;border-radius:50%;background:${ok ? 'var(--c-health)' : 'var(--c-alert)'};margin-right:6px"></span>`;
    const bar = (pct, color) => `<span style="display:inline-block;width:60px;height:8px;background:#111122;border-radius:4px;overflow:hidden;margin-left:6px"><span style="display:block;height:100%;width:${Math.round(pct)}%;background:${color};border-radius:4px"></span></span>`;
    const val = (v, unit) => `<span style="color:var(--text-primary);font-weight:700;margin-left:auto">${v}</span>${unit ? `<span style="color:var(--text-dim);font-size:0.85em;margin-left:4px">${unit}</span>` : ''}`;

    let html = '';
    // MESH root
    html += `<div style="display:flex;align-items:center;gap:6px">${statusDot(online === total)}<span style="color:var(--text-primary);text-transform:uppercase;letter-spacing:0.04em;font-weight:700">MESH</span>${val(online + '/' + total, 'agents')}${bar(online/total*100, 'var(--c-health)')}</div>`;

    // Per-agent nodes
    for (const agent of AGENTS) {
        const a = agentData[agent.id];
        const isOnline = a?.status === "online";
        const d = a?.data || {};
        const spent = parseFloat(d.autonomy_budget?.budget_spent) || 0;
        const cutoff = parseFloat(d.autonomy_budget?.budget_cutoff) || 20;
        const unproc = (d.totals || {}).unprocessed || 0;
        const agGates = (d.active_gates || []).length;
        const name = agent.name || agent.id.replace(/-agent$/, '');

        html += `<div style="padding-left:24px;display:flex;align-items:center;gap:6px">`;
        html += `<span style="color:var(--text-dim)">├─</span>`;
        html += statusDot(isOnline);
        html += `<span style="color:var(--text-primary);text-transform:uppercase;letter-spacing:0.03em">${name}</span>`;
        if (isOnline) {
            html += val(spent + '/' + cutoff, 'budget');
            html += bar((1 - spent/cutoff) * 100, 'var(--c-knowledge)');
            if (unproc > 0) html += `<span style="color:var(--c-warning);font-size:0.8em;margin-left:8px">${unproc} pending</span>`;
            if (agGates > 0) html += `<span style="color:var(--c-alert);font-size:0.8em;margin-left:8px">${agGates} gates</span>`;
        } else {
            html += `<span style="color:var(--c-alert);font-size:0.8em;margin-left:auto">UNREACHABLE</span>`;
        }
        html += `</div>`;
    }

    // Summary row
    html += `<div style="padding-left:24px;display:flex;align-items:center;gap:6px;margin-top:4px;border-top:1px solid var(--border);padding-top:4px">`;
    html += `<span style="color:var(--text-dim)">└─</span>`;
    html += `<span style="color:var(--text-dim);text-transform:uppercase;font-size:0.85em">TOTALS</span>`;
    html += val(Math.round(budgetSpent) + '/' + Math.round(budgetMax), 'budget');
    html += `<span style="margin-left:12px">${val(pending, 'pending')}</span>`;
    html += `<span style="margin-left:12px">${val(gates, 'gates')}</span>`;
    html += `</div>`;

    el.innerHTML = html;

    // Footer
    const footer = document.getElementById("msd-footer-num");
    if (footer) footer.textContent = ` ${online}/${total} online`;
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

// ── Knowledge Tab ────────────────────────────────────────────


