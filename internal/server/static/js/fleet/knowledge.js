// ═══ KNOWLEDGE STATION (was Knowledge + Meta + Wisdom) ══════
// Aggregated KB across agents — claims, decisions, triggers,
// lessons, epistemic flags, vocabulary.
// Data: /api/mesh/knowledge, per-agent /api/agent/knowledge/*

(function () {
    "use strict";

    var _initialized = false;

    function ensureLayout() {
        if (_initialized) return;
        _initialized = true;
        var el = document.getElementById("pane-knowledge");
        if (!el) return;
        el.innerHTML =
            '<div class="lcars-zone-c" style="margin-bottom:var(--gap-xs)">' +
                '<span class="zc-title">Knowledge Base</span><span class="zc-id">42</span>' +
            '</div>' +
            '<div class="agent-panel-grid">' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-science)">' +
                    '<div class="lcars-panel-header">Architecture Decisions <span class="lcars-panel-id">3001</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-decisions"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-science)">' +
                    '<div class="lcars-panel-header">Cognitive Triggers <span class="lcars-panel-id">0019</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-triggers"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-science)">' +
                    '<div class="lcars-panel-header">Verified Claims <span class="lcars-panel-id">8669</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-claims"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-science)">' +
                    '<div class="lcars-panel-header">Lessons <span class="lcars-panel-id">5501</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-lessons"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-science);grid-column:1/-1">' +
                    '<div class="lcars-panel-header">Epistemic Flags <span class="lcars-panel-id">7301</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-epistemic"></div>' +
                '</div>' +
                '<div class="lcars-panel panel-tristate" style="--panel-accent:var(--c-tab-science);grid-column:1/-1">' +
                    '<div class="lcars-panel-header">Vocabulary <span class="lcars-panel-id">4774</span></div>' +
                    '<div class="lcars-panel-body" id="fleet-vocab"></div>' +
                '</div>' +
            '</div>';
    }

    window.refresh_knowledge = function () {
        ensureLayout();

        lcars.catalog.fetch("Fleet Knowledge").then(function (data) {
            var kb = data.data || data;

            // Decisions
            var decisions = kb.decisions || [];
            lcars.patterns.taskListing("fleet-decisions", decisions.slice(0, 12).map(function (d) {
                return { code: d.decision_key || d.id || "—", title: d.title || d.text || "", status: "nominal" };
            }));

            // Triggers
            var triggers = kb.triggers || [];
            lcars.patterns.indicatorStrip("fleet-triggers", triggers.map(function (t) {
                return {
                    id: t.trigger_id || t.id,
                    label: (t.description || "").substring(0, 40),
                    value: t.fire_count || 0,
                    status: t.fail_count > 0 ? "fail" : "pass"
                };
            }));

            // Claims
            var claims = kb.claims || [];
            lcars.patterns.filingRecord("fleet-claims", claims.slice(0, 8).map(function (c) {
                return {
                    reference: c.claim_id || c.id || "—",
                    status: c.verified ? "nominal" : "advisory",
                    title: c.text || c.claim || "",
                    fields: [{ label: "Source", value: c.source || "—" }]
                };
            }));

            // Lessons
            var lessons = kb.lessons || [];
            lcars.patterns.taskListing("fleet-lessons", lessons.slice(0, 10).map(function (l) {
                return { code: l.id || "—", title: l.title || l.pattern || "", description: l.description || "", status: l.promoted ? "nominal" : "advisory" };
            }));

            // Epistemic flags
            var flags = kb.epistemic_flags || kb.flags || [];
            lcars.patterns.filingRecord("fleet-epistemic", flags.slice(0, 8).map(function (f) {
                return {
                    reference: f.id || "⚑",
                    status: f.resolved ? "nominal" : "warning",
                    title: f.flag || f.text || "",
                    fields: [{ label: "Session", value: f.session || "—" }]
                };
            }));

            // Vocab placeholder — vocabulary comes from /vocab endpoint
            lcars.patterns.placeholder("fleet-vocab", "Vocabulary served at /vocab/v1.0.0.jsonld");

        }).catch(function () {
            lcars.patterns.placeholder("fleet-decisions", "Knowledge base unavailable");
        });
    };
})();
