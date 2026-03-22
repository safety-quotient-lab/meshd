// ═══ WINDOW GLOBALS ══════════════════════════════════════════
// ES modules scope all declarations. onclick handlers in HTML
// need these functions on window.
window.setTheme = setTheme;
window.switchTab = switchTab;
window.refreshAll = refreshAll;
window.switchAgent = switchAgent;
window.sortTable = sortTable;
window.filterTable = filterTable;
window.goToPage = goToPage;
window.filterDictionary = filterDictionary;
window.toggleDecisionRow = toggleDecisionRow;
window.setManualAlert = setManualAlert;
window.meshControl = meshControl;
window.openLcarsDetail = openLcarsDetail;
window.closeLcarsDetail = closeLcarsDetail;
window.toggleNarrativeDrawer = toggleNarrativeDrawer;
window.runDiagnostic = runDiagnostic;
window.switchGovSubsystem = switchGovSubsystem;

// ── Init ───────────────────────────────────────────────────────
(async function init() {
    // Default to LCARS — the canonical mesh interface
    const savedTheme = localStorage.getItem("theme") || "lcars";
    setTheme(savedTheme);

    // Restore tab from URL hash — AFTER theme, so it overrides any default
    const hashTab = location.hash.replace("#", "");
    const urlSub = new URLSearchParams(location.search).get("sub");

    // If URL specifies a subsystem, switch to its parent tab immediately
    // to avoid flash of default tab content
    const sciSubs = ["psychometrics", "linguistics", "ontology"];
    const opsSubs = ["mesh-status", "resources-autonomy", "transport-overview", "resources-capacity", "deliberations-log", "governance-record"];
    if (urlSub && sciSubs.includes(urlSub)) {
        switchTab("analysis", false);
    } else if (urlSub && opsSubs.includes(urlSub)) {
        switchTab("governance", false);
    } else if (hashTab && VALID_TABS.includes(hashTab)) {
        switchTab(hashTab, false);
    }

    // Restore subsystem — retry until async station scripts load
    if (urlSub) {
        const restoreSub = (attempts) => {
            if (sciSubs.includes(urlSub)) {
                if (typeof switchAnalysisSubsystem === "function") {
                    switchAnalysisSubsystem(urlSub, false);
                } else if (attempts > 0) {
                    setTimeout(() => restoreSub(attempts - 1), 150);
                }
            } else if (opsSubs.includes(urlSub) && typeof switchGovSubsystem === "function") {
                switchGovSubsystem(urlSub);
            }
        };
        restoreSub(15);
    }

    // Reveal content now that the correct tab is active
    const contentEl = document.querySelector(".lcars-content");
    if (contentEl) contentEl.style.visibility = "";

    buildAgentSwitcher();
    await refreshAll();

    // Check auth for control surfaces (non-blocking)
    checkAuth();

    // Fetch agent cards for structural schema (non-blocking)
    fetchAgentCards();

    // Try WebSocket — real-time event-driven updates (beta-band relay)
    connectWebSocket();
    // Safety net — polling only until WS connects (WS handler clears timer)
    refreshTimer = setInterval(refreshAll, _pollInterval);
})();
