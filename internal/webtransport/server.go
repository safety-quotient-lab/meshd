// Package webtransport provides a WebTransport server for meshd.
//
// Runs on a separate QUIC/HTTP3 port (default 9443). Self-signed TLS
// certificate generated at startup. Provides two transport primitives:
//
//   - Bidirectional streams: structured interagent/v1 JSON messages
//   - Datagrams: broadcast signals (status, alerts, tempo)
//
// Session 100 spike — proving the round-trip works on localhost.
package webtransport

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
)

// Server manages WebTransport sessions for the mesh.
type Server struct {
	addr     string
	certFile string // PEM cert path (empty = self-signed)
	keyFile  string // PEM key path (empty = self-signed)
	logger    *slog.Logger
	wtServer  *webtransport.Server
	certHash  []byte // SHA-256 of the cert DER
	onMessage MessageHandler

	mu       sync.RWMutex
	sessions map[string]*webtransport.Session // agentID → session
}

// New creates a WebTransport server on the given address (e.g. ":9443").
// certFile and keyFile point to PEM-encoded TLS cert/key (e.g. from mkcert).
// If empty, a self-signed cert generates at startup.
func New(addr, certFile, keyFile string, logger *slog.Logger) *Server {
	return &Server{
		addr:     addr,
		certFile: certFile,
		keyFile:  keyFile,
		logger:   logger,
		sessions: make(map[string]*webtransport.Session),
	}
}

// Start generates a self-signed cert and begins accepting sessions.
func (s *Server) Start(ctx context.Context) error {
	var tlsCert tls.Certificate
	var certHash []byte
	var err error

	if s.certFile != "" && s.keyFile != "" {
		// Load mkcert or externally-provided cert
		tlsCert, err = tls.LoadX509KeyPair(s.certFile, s.keyFile)
		if err != nil {
			return fmt.Errorf("load cert %s: %w", s.certFile, err)
		}
		if len(tlsCert.Certificate) > 0 {
			hash := sha256.Sum256(tlsCert.Certificate[0])
			certHash = hash[:]
		}
		s.logger.Info("webtransport using mkcert certificate", "cert", s.certFile)
	} else {
		// Generate ephemeral self-signed cert
		tlsCert, certHash, err = generateSelfSignedCert()
		if err != nil {
			return fmt.Errorf("generate cert: %w", err)
		}
		s.logger.Info("webtransport using self-signed certificate")
	}
	s.certHash = certHash

	mux := http.NewServeMux()

	s.wtServer = &webtransport.Server{
		CheckOrigin: func(r *http.Request) bool { return true }, // localhost dev
		H3: &http3.Server{
			Addr:      s.addr,
			TLSConfig: &tls.Config{
				Certificates: []tls.Certificate{tlsCert},
				NextProtos:   []string{"h3"},
			},
			Handler:         mux,
			EnableDatagrams: true,
		},
	}

	// Configure HTTP/3 server to advertise WebTransport support in SETTINGS
	webtransport.ConfigureHTTP3Server(s.wtServer.H3)

	// Mesh transport endpoint — agents and dashboard connect here
	mux.HandleFunc("/mesh", func(w http.ResponseWriter, r *http.Request) {
		session, err := s.wtServer.Upgrade(w, r)
		if err != nil {
			s.logger.Error("webtransport upgrade failed", "err", err)
			return
		}
		s.handleSession(ctx, session)
	})

	// Certificate hash endpoint — browser clients need this for serverCertificateHashes
	mux.HandleFunc("/certhash", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(map[string]any{
			"hash":      fmt.Sprintf("%x", certHash),
			"algorithm": "sha-256",
		})
	})

	s.logger.Info("webtransport server starting", "addr", s.addr, "cert_hash", fmt.Sprintf("%x", certHash)[:16]+"...")

	go func() {
		<-ctx.Done()
		s.wtServer.Close()
	}()

	return s.wtServer.ListenAndServe()
}

// Broadcast sends a datagram to all connected sessions.
func (s *Server) Broadcast(data []byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, sess := range s.sessions {
		if err := sess.SendDatagram(data); err != nil {
			s.logger.Debug("datagram send failed", "agent", id, "err", err)
		}
	}
}

// BroadcastJSON marshals v and broadcasts it as a datagram.
func (s *Server) BroadcastJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.Broadcast(data)
	return nil
}

// CertHash returns the SHA-256 hash of the server's self-signed certificate.
// Returns nil before Start() completes.
func (s *Server) CertHash() []byte { return s.certHash }

// HandleCertHash returns an http.HandlerFunc that serves the cert hash as JSON.
// Mount this on the main HTTP server so browsers can fetch it before connecting.
func (s *Server) HandleCertHash() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if s.certHash == nil {
			json.NewEncoder(w).Encode(map[string]any{"error": "cert not ready"})
			return
		}
		// Include session list for observability
		s.mu.RLock()
		sessionIDs := make([]string, 0, len(s.sessions))
		for id := range s.sessions {
			sessionIDs = append(sessionIDs, id)
		}
		s.mu.RUnlock()

		json.NewEncoder(w).Encode(map[string]any{
			"hash":      fmt.Sprintf("%x", s.certHash),
			"algorithm": "sha-256",
			"port":      s.addr,
			"sessions":  len(sessionIDs),
			"connected": sessionIDs,
		})
	}
}

// SessionCount returns the number of active WebTransport sessions.
func (s *Server) SessionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// MessageHandler processes an inbound interagent/v1 message received via stream.
// Set via OnMessage before Start.
type MessageHandler func(fromAgent string, msg json.RawMessage)

// OnMessage sets the handler for inbound stream messages.
func (s *Server) OnMessage(h MessageHandler) { s.onMessage = h }

func (s *Server) handleSession(ctx context.Context, session *webtransport.Session) {
	// Read the first stream to get the agent's identity
	stream, err := session.AcceptStream(ctx)
	if err != nil {
		s.logger.Debug("no identity stream", "err", err)
		return
	}

	var identity struct {
		AgentID string `json:"agent_id"`
		Type    string `json:"type"` // "agent" or "dashboard"
	}
	decoder := json.NewDecoder(stream)
	if err := decoder.Decode(&identity); err != nil {
		s.logger.Warn("bad identity message", "err", err)
		stream.Close()
		return
	}

	agentID := identity.AgentID
	if agentID == "" {
		agentID = "anonymous-" + fmt.Sprintf("%d", time.Now().UnixMilli()%10000)
	}

	s.logger.Info("webtransport session established", "agent", agentID, "type", identity.Type)

	// Register session
	s.mu.Lock()
	s.sessions[agentID] = session
	s.mu.Unlock()

	// Send welcome acknowledgment on the identity stream
	json.NewEncoder(stream).Encode(map[string]any{
		"status":     "connected",
		"agent_id":   agentID,
		"server":     "meshd",
		"session_count": s.SessionCount(),
	})
	stream.Close()

	// Accept incoming datagrams from agent (status updates)
	go s.readDatagrams(agentID, session)

	// Accept subsequent streams (interagent/v1 messages)
	go s.acceptStreams(ctx, agentID, session)

	// Keep session alive until it closes
	<-session.Context().Done()

	s.mu.Lock()
	delete(s.sessions, agentID)
	s.mu.Unlock()
	s.logger.Info("webtransport session closed", "agent", agentID)
}

// readDatagrams reads datagrams from an agent session and rebroadcasts
// them to all other sessions (fan-out). Agent status updates flow this way.
func (s *Server) readDatagrams(agentID string, session *webtransport.Session) {
	for {
		data, err := session.ReceiveDatagram(session.Context())
		if err != nil {
			return // session closed
		}

		// Parse to extract type for logging
		var msg struct {
			Type string `json:"type"`
		}
		json.Unmarshal(data, &msg)
		s.logger.Debug("datagram received", "from", agentID, "type", msg.Type, "bytes", len(data))

		// Rebroadcast to all OTHER sessions (not back to sender)
		s.mu.RLock()
		for id, sess := range s.sessions {
			if id == agentID {
				continue
			}
			sess.SendDatagram(data)
		}
		s.mu.RUnlock()
	}
}

// acceptStreams accepts bidirectional streams after the identity stream.
// Each stream carries one interagent/v1 JSON message.
func (s *Server) acceptStreams(ctx context.Context, agentID string, session *webtransport.Session) {
	for {
		stream, err := session.AcceptStream(ctx)
		if err != nil {
			return // session closed
		}

		go func() {
			defer stream.Close()
			var raw json.RawMessage
			if err := json.NewDecoder(stream).Decode(&raw); err != nil {
				s.logger.Debug("stream decode error", "from", agentID, "err", err)
				return
			}

			s.logger.Info("stream message received", "from", agentID, "bytes", len(raw))

			// Send acknowledgment
			json.NewEncoder(stream).Encode(map[string]string{
				"status": "received",
			})

			// Route to message handler
			if s.onMessage != nil {
				s.onMessage(agentID, raw)
			}
		}()
	}
}

// generateSelfSignedCert creates a self-signed ECDSA certificate valid for
// localhost. Returns the TLS certificate, its SHA-256 hash (for browser
// serverCertificateHashes), and any error.
func generateSelfSignedCert() (tls.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour), // short-lived — regenerates on restart
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	// Compute SHA-256 hash for browser serverCertificateHashes
	hash := sha256.Sum256(certDER)

	tlsCert := tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}

	return tlsCert, hash[:], nil
}
