// Package zmqbus provides a ZeroMQ PUB/SUB mesh transport layer.
//
// Each meshd instance runs a PUB socket (broadcasts events) and multiple
// SUB sockets (one per known peer). Gossip-on-connect propagates peer
// discovery through the mesh.
//
// Message format: topic + JSON payload, separated by a space.
// Topics: "health", "peer", "event"
package zmqbus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-zeromq/zmq4"
)

// Sentinel errors for ZMQ bus operations.
var (
	ErrPubListenFailed = errors.New("zmq pub listen failed")
	ErrSubDialFailed   = errors.New("zmq sub dial failed")
)

// Message represents a ZMQ bus message.
type Message struct {
	Topic     string    `json:"topic"`
	From      string    `json:"from"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

// PeerInfo describes a known peer for gossip exchange.
type PeerInfo struct {
	AgentID  string `json:"agent_id"`
	ZMQPub   string `json:"zmq_pub"`
	HTTPURL  string `json:"http_url,omitempty"`
	CardURL  string `json:"card_url,omitempty"`
	SeenAt   string `json:"seen_at,omitempty"`
}

// Bus manages PUB/SUB sockets and peer connections.
type Bus struct {
	agentID  string
	pubAddr  string
	httpURL  string // our HTTP base URL for reverse-registration
	pub      zmq4.Socket
	peers    map[string]*peerConn
	mu       sync.RWMutex
	handlers []func(Message)
	ctx      context.Context
	cancel   context.CancelFunc
	logger   *slog.Logger
}

type peerConn struct {
	info PeerInfo
	sub  zmq4.Socket
}

// New creates a ZMQ bus. pubAddr is the PUB socket bind address (e.g. "tcp://127.0.0.1:9001").
// httpURL is our own HTTP base URL (e.g. "http://localhost:8076") for reverse-registration.
func New(agentID, pubAddr, httpURL string, logger *slog.Logger) *Bus {
	ctx, cancel := context.WithCancel(context.Background())
	return &Bus{
		agentID: agentID,
		pubAddr: pubAddr,
		httpURL: httpURL,
		peers:   make(map[string]*peerConn),
		ctx:     ctx,
		cancel:  cancel,
		logger:  logger,
	}
}

// Start binds the PUB socket and begins periodic gossip heartbeat.
func (b *Bus) Start() error {
	b.pub = zmq4.NewPub(b.ctx)
	if err := b.pub.Listen(b.pubAddr); err != nil {
		return fmt.Errorf("%w: %w", ErrPubListenFailed, err)
	}
	b.logger.Info("PUB listening", "addr", b.pubAddr)

	// Periodic gossip heartbeat — ensures bidirectional discovery.
	// When B connects to A and subscribes, A's heartbeat reaches B,
	// and B's heartbeat reaches A (once A subscribes back via gossip).
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-b.ctx.Done():
				return
			case <-ticker.C:
				b.gossipAnnounce()
			}
		}
	}()

	return nil
}

// Stop closes all sockets.
func (b *Bus) Stop() {
	b.cancel()
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, pc := range b.peers {
		pc.sub.Close()
	}
	if b.pub != nil {
		b.pub.Close()
	}
	b.logger.Info("ZMQ bus stopped")
}

// OnMessage registers a handler for incoming messages.
func (b *Bus) OnMessage(fn func(Message)) {
	b.handlers = append(b.handlers, fn)
}

// Publish sends a message on the PUB socket.
func (b *Bus) Publish(topic string, data any) error {
	msg := Message{
		Topic:     topic,
		From:      b.agentID,
		Timestamp: time.Now().UTC(),
		Data:      data,
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("zmq marshal: %w", err)
	}

	frame := fmt.Sprintf("%s %s", topic, payload)
	return b.pub.Send(zmq4.NewMsg([]byte(frame)))
}

// ConnectPeer subscribes to a peer's PUB socket.
func (b *Bus) ConnectPeer(info PeerInfo) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.peers[info.AgentID]; exists {
		return nil // already connected
	}

	sub := zmq4.NewSub(b.ctx)
	if err := sub.Dial(info.ZMQPub); err != nil {
		return fmt.Errorf("%w: %w", ErrSubDialFailed, err)
	}

	// Subscribe to all topics
	if err := sub.SetOption(zmq4.OptionSubscribe, ""); err != nil {
		sub.Close()
		return fmt.Errorf("zmq subscribe: %w", err)
	}

	pc := &peerConn{info: info, sub: sub}
	b.peers[info.AgentID] = pc

	b.logger.Info("connected to peer", "peer", info.AgentID, "addr", info.ZMQPub)

	// Start receiving in background
	go b.recvLoop(pc)

	// Gossip: announce our known peers after SUB handshake settles.
	// ZMQ SUB connections need a brief moment to establish before
	// published messages reach the subscriber ("slow joiner" problem).
	go func() {
		time.Sleep(500 * time.Millisecond)
		b.gossipAnnounce()
	}()

	// Reverse-register: tell the peer about ourselves via their HTTP API.
	// PUB/SUB only goes publisher→subscriber, so the peer can't detect our
	// subscription. This HTTP call completes the bidirectional handshake.
	if info.HTTPURL != "" {
		go b.reverseRegister(info)
	}

	return nil
}

// KnownPeers returns the list of connected peers.
func (b *Bus) KnownPeers() []PeerInfo {
	b.mu.RLock()
	defer b.mu.RUnlock()
	peers := make([]PeerInfo, 0, len(b.peers))
	for _, pc := range b.peers {
		peers = append(peers, pc.info)
	}
	return peers
}

// recvLoop reads messages from a peer's SUB socket.
func (b *Bus) recvLoop(pc *peerConn) {
	for {
		msg, err := pc.sub.Recv()
		if err != nil {
			if b.ctx.Err() != nil {
				return // shutting down
			}
			b.logger.Warn("recv error from peer", "peer", pc.info.AgentID, "err", err)
			time.Sleep(time.Second)
			continue
		}

		frame := string(msg.Bytes())
		spaceIdx := strings.IndexByte(frame, ' ')
		if spaceIdx < 0 {
			continue
		}

		topic := frame[:spaceIdx]
		payload := frame[spaceIdx+1:]

		var m Message
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			b.logger.Warn("unmarshal error from peer", "peer", pc.info.AgentID, "err", err)
			continue
		}

		// Handle gossip: discover new peers
		if topic == "peer" {
			b.handleGossip(m)
		}

		// Dispatch to handlers
		for _, fn := range b.handlers {
			fn(m)
		}
	}
}

// gossipAnnounce publishes our known peers on the "peer" topic.
func (b *Bus) gossipAnnounce() {
	peers := b.KnownPeers()
	// Include ourselves
	self := PeerInfo{
		AgentID: b.agentID,
		ZMQPub:  b.pubAddr,
		SeenAt:  time.Now().UTC().Format(time.RFC3339),
	}
	peers = append(peers, self)
	b.Publish("peer", peers)
}

// SelfInfo returns this node's PeerInfo for registration with peers.
func (b *Bus) SelfInfo() PeerInfo {
	return PeerInfo{
		AgentID: b.agentID,
		ZMQPub:  b.pubAddr,
		HTTPURL: b.httpURL,
		SeenAt:  time.Now().UTC().Format(time.RFC3339),
	}
}

// RegisterPeer handles an inbound peer registration (called from HTTP handler).
// Returns true if a new peer was discovered and connected.
func (b *Bus) RegisterPeer(info PeerInfo) bool {
	if info.AgentID == b.agentID || info.ZMQPub == "" {
		return false
	}

	b.mu.RLock()
	_, known := b.peers[info.AgentID]
	b.mu.RUnlock()

	if known {
		return false
	}

	b.logger.Info("register: new peer via HTTP", "peer", info.AgentID, "addr", info.ZMQPub)
	go b.ConnectPeer(info)
	return true
}

// reverseRegister announces ourselves to a peer via their HTTP API.
func (b *Bus) reverseRegister(peer PeerInfo) {
	self := b.SelfInfo()
	body, err := json.Marshal(self)
	if err != nil {
		return
	}

	url := strings.TrimSuffix(peer.HTTPURL, "/") + "/api/zmq/register"
	ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.logger.Warn("reverse-register failed", "peer", peer.AgentID, "err", err)
		return
	}
	resp.Body.Close()
	b.logger.Info("reverse-registered with peer", "peer", peer.AgentID)
}

// handleGossip processes a peer announcement and connects to unknown peers.
func (b *Bus) handleGossip(m Message) {
	raw, err := json.Marshal(m.Data)
	if err != nil {
		return
	}
	var peers []PeerInfo
	if err := json.Unmarshal(raw, &peers); err != nil {
		return
	}

	for _, p := range peers {
		if p.AgentID == b.agentID {
			continue // skip self
		}
		if p.ZMQPub == "" {
			continue
		}

		b.mu.RLock()
		_, known := b.peers[p.AgentID]
		b.mu.RUnlock()

		if !known {
			b.logger.Info("gossip: discovered new peer", "peer", p.AgentID, "addr", p.ZMQPub)
			go b.ConnectPeer(p)
		}
	}
}
