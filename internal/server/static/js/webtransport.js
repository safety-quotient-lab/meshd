// webtransport.js — WebTransport client for LCARS dashboard
// Session 100 spike — proves round-trip works on localhost.
//
// Connects to meshd's WebTransport endpoint (port = HTTP port + 1000).
// Sends identity, receives welcome, listens for broadcast datagrams.

let wtSession = null;
let wtConnected = false;

async function connectWebTransport() {
    const httpPort = parseInt(location.port) || 8081;
    const wtPort = httpPort + 1000;

    const url = `https://localhost:${wtPort}/mesh`;

    try {
        // mkcert CA trusted by the system — no serverCertificateHashes needed
        const transport = new WebTransport(url);
        // Timeout: Safari 26.4 exposes the API but connections fail silently.
        // Abort after 5s to avoid infinite pending state.
        const ready = await Promise.race([
            transport.ready,
            new Promise((_, reject) => setTimeout(() => reject(new Error("timeout")), 5000)),
        ]);
        void ready;

        wtSession = transport;
        wtConnected = true;
        updateWtIndicator(true);

        // Send identity on first bidirectional stream
        const stream = await transport.createBidirectionalStream();
        const writer = stream.writable.getWriter();
        const encoder = new TextEncoder();
        await writer.write(encoder.encode(JSON.stringify({
            agent_id: "lcars-dashboard",
            type: "dashboard",
        })));
        await writer.close();

        // Read welcome response
        const reader = stream.readable.getReader();
        const { value } = await reader.read();
        if (value) {
            const welcome = JSON.parse(new TextDecoder().decode(value));
            addNarrativeEntry(`WebTransport connected: ${welcome.status} (server: ${welcome.server})`);
        }

        // Listen for broadcast datagrams
        listenForDatagrams(transport);

        // Monitor connection close
        transport.closed.then(() => {
            wtConnected = false;
            wtSession = null;
            updateWtIndicator(false);
            addNarrativeEntry("WebTransport disconnected");
            // Reconnect after delay
            setTimeout(connectWebTransport, 5000);
        });

    } catch (err) {
        wtConnected = false;
        updateWtIndicator(false);
        // Back off aggressively — if the browser's WT implementation doesn't
        // work (Safari 26.4 exposes API but connections fail), stop retrying
        // after 3 failures to avoid console noise.
        if (!connectWebTransport._failures) connectWebTransport._failures = 0;
        connectWebTransport._failures++;
        if (connectWebTransport._failures >= 3) {
            // Give up — WebSocket/SSE/fetch continue to work
            return;
        }
        setTimeout(connectWebTransport, 15000);
    }
}

async function listenForDatagrams(transport) {
    const reader = transport.datagrams.readable.getReader();
    const decoder = new TextDecoder();
    try {
        while (true) {
            const { value, done } = await reader.read();
            if (done) break;
            try {
                const msg = JSON.parse(decoder.decode(value));
                handleDatagram(msg);
            } catch { /* ignore malformed datagrams */ }
        }
    } catch { /* connection closed */ }
}

function handleDatagram(msg) {
    // Route all datagrams through the existing realtime event handler
    // (ws-sse.js handleRealtimeEvent). Unified event pipeline —
    // WebTransport datagrams, WebSocket messages, and SSE events
    // all converge on the same handler.
    if (typeof handleRealtimeEvent === "function") {
        handleRealtimeEvent(msg);
    }
}

function updateWtIndicator(connected) {
    // Update the stardate tray or header to show WT status
    const existing = document.getElementById("wt-indicator");
    if (existing) {
        existing.textContent = connected ? "● WT" : "○ WT";
        existing.style.color = connected ? "#22cc44" : "var(--text-dim)";
        return;
    }
    // Create indicator in the stardate tray area
    const tray = document.querySelector(".stardate-tray");
    if (tray) {
        const pill = document.createElement("div");
        pill.className = "stardate-tray-pill";
        pill.id = "wt-indicator";
        pill.style.cssText = "background:var(--lcars-tertiary);font-size:0.75em";
        pill.textContent = connected ? "● WT" : "○ WT";
        pill.style.color = connected ? "#22cc44" : "var(--text-dim)";
        tray.appendChild(pill);
    }
}

// Initialize when DOM ready and WebTransport available
if (typeof WebTransport !== "undefined") {
    document.addEventListener("DOMContentLoaded", () => {
        setTimeout(connectWebTransport, 5000); // let other subsystems + WebTransport API init
    });
}
