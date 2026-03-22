// Package server — websocket.go provides WebSocket fan-out.
//
// Replaces SSE for Cloudflare Tunnel compatibility. The WebSocket
// handler upgrades HTTP connections and broadcasts events from the
// SSEBroker to connected WebSocket clients.
package server

import (
	"encoding/json"
	"net/http"
	"time"

	"nhooyr.io/websocket"
)

// handleWebSocket serves GET /ws — WebSocket stream for real-time updates.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()

	// Register with SSE broker (reuse existing fan-out infrastructure)
	ch := make(chan SSEEvent, 16)
	s.sseBroker.register <- ch

	// Clean up on disconnect
	go func() {
		<-ctx.Done()
		s.sseBroker.unregister <- ch
	}()

	// Send initial connected message
	msg, _ := json.Marshal(map[string]any{
		"event": "connected",
		"agent": s.Config.AgentID,
	})
	conn.Write(ctx, websocket.MessageText, msg)

	// Keepalive ticker
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case evt, open := <-ch:
			if !open {
				conn.Close(websocket.StatusNormalClosure, "broker closed")
				return
			}
			data, err := json.Marshal(map[string]any{
				"event": evt.Type,
				"data":  evt.Data,
			})
			if err != nil {
				s.logger.Error("ws marshal failed", "err", err)
				continue
			}
			if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
				return
			}

		case <-ticker.C:
			if err := conn.Ping(ctx); err != nil {
				return
			}

		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "client disconnected")
			return
		}
	}
}
