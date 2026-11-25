package meshcentral

import (
	"context"
	"log"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/unitronix/betterdesk-server/audit"
)

// relayHub multiplexes one mesh agent tunnel to multiple browser viewers (KVM p=2).
type relayHub struct {
	gateway     *Gateway
	relayID     string
	proto       string
	meta        *relayMeta
	ready       chan struct{}
	closed      chan struct{}
	mu          sync.Mutex
	agent       *relayPeer
	viewers     []*relayPeer
	recorder    *relayRecorder
	recPath     string
	auditUser   string
	auditPeer   string
	sessionType string
	started     bool
	done        atomic.Bool
}

func (g *Gateway) runKvmRelayHub(ctx context.Context, relayID string, peer *relayPeer, proto string, meta *relayMeta) {
	hub := &relayHub{
		gateway: g,
		relayID: relayID,
		proto:   proto,
		meta:    meta,
		ready:   make(chan struct{}),
		closed:  make(chan struct{}),
	}
	if v, loaded := g.relayHubs.LoadOrStore(relayID, hub); loaded {
		hub = v.(*relayHub)
	}

	viewerDone := make(chan struct{})
	defer hub.detach(peer)
	if !hub.attach(ctx, peer, viewerDone) {
		return
	}
	timer := time.NewTimer(relayJoinMinutes * time.Minute)
	defer timer.Stop()
	select {
	case <-hub.ready:
		select {
		case <-viewerDone:
		case <-hub.closed:
		case <-ctx.Done():
		}
	case <-viewerDone:
	case <-hub.closed:
	case <-timer.C:
	case <-ctx.Done():
	}
}

func (h *relayHub) attach(ctx context.Context, peer *relayPeer, viewerDone chan struct{}) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done.Load() || (!peer.browser && h.agent != nil) {
		return false
	}
	if peer.browser {
		h.viewers = append(h.viewers, peer)
		sig := RelayConnectSignal(h.recorder != nil)
		if err := writeRelayMessage(ctx, peer.ws, websocket.MessageText, []byte(sig)); err != nil {
			h.viewers = h.viewers[:len(h.viewers)-1]
			return false
		}
		if !h.started && h.agent != nil {
			h.startPiping(ctx)
		}
		go h.readViewer(ctx, peer, viewerDone)
		return true
	}
	h.agent = peer
	if !h.started && len(h.viewers) > 0 {
		h.startPiping(ctx)
	}
	return true
}

func (h *relayHub) detach(peer *relayPeer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	agentLeft := h.agent == peer
	if peer.browser {
		for i, v := range h.viewers {
			if v == peer {
				h.viewers = append(h.viewers[:i], h.viewers[i+1:]...)
				break
			}
		}
	} else if agentLeft {
		h.agent = nil
	}
	if (h.agent == nil && len(h.viewers) == 0) || (h.started && (agentLeft || len(h.viewers) == 0)) {
		h.finishLocked()
	}
}

func (h *relayHub) startPiping(ctx context.Context) {
	if h.started {
		return
	}
	h.started = true
	close(h.ready)

	meta := h.meta
	recording := meta != nil && meta.Record
	h.auditUser = h.relayID
	h.auditPeer = h.relayID
	h.sessionType = h.proto
	if meta != nil {
		if meta.UserID != "" {
			h.auditUser = meta.UserID
		}
		if meta.PeerID != "" {
			h.auditPeer = meta.PeerID
		}
		if meta.SessionType != "" {
			h.sessionType = meta.SessionType
		}
		if recording && h.gateway.cfg != nil {
			dataDir := filepath.Dir(h.gateway.cfg.DBPath)
			if dataDir == "" || dataDir == "." {
				dataDir = "."
			}
			if r, path, err := openRelayRecorder(dataDir, meta.PeerID, h.relayID, meta.SessionType); err == nil {
				h.recorder = r
				h.recPath = path
			}
		}
	}

	sig := RelayConnectSignal(recording)
	if h.agent != nil {
		_ = writeRelayMessage(ctx, h.agent.ws, websocket.MessageText, []byte(sig))
	}
	for _, v := range h.viewers {
		_ = writeRelayMessage(ctx, v.ws, websocket.MessageText, []byte(sig))
	}

	if h.gateway.auditLog != nil {
		h.gateway.auditLog.Log(audit.ActionPeerUpdated, h.auditUser, h.auditPeer, map[string]string{
			"event": "mesh_relay_paired",
			"proto": h.proto,
			"mode":  "multiplex",
		})
	}

	if h.agent != nil {
		// Viewer disconnects do not end the single agent feed.
		agentCtx := h.gateway.ctx
		if agentCtx == nil {
			agentCtx = context.WithoutCancel(ctx)
		}
		go h.readAgent(agentCtx, h.agent, h.recorder)
	}
}

func (h *relayHub) readAgent(ctx context.Context, agent *relayPeer, recorder *relayRecorder) {
	defer h.finish()
	for {
		typ, data, err := agent.ws.Read(ctx)
		if err != nil {
			return
		}
		if recorder != nil && len(data) > 0 {
			recorder.Write(int(typ), data)
		}
		h.mu.Lock()
		viewers := append([]*relayPeer{}, h.viewers...)
		h.mu.Unlock()
		for _, v := range viewers {
			if err := writeRelayMessage(ctx, v.ws, typ, data); err != nil {
				v.ws.CloseNow()
			}
		}
	}
}

func (h *relayHub) readViewer(ctx context.Context, viewer *relayPeer, done chan struct{}) {
	defer close(done)
	for {
		typ, data, err := viewer.ws.Read(ctx)
		if err != nil {
			return
		}
		h.mu.Lock()
		agent := h.agent
		recorder := h.recorder
		h.mu.Unlock()
		if agent == nil {
			continue
		}
		if recorder != nil && len(data) > 0 {
			recorder.Write(int(typ), data)
		}
		if err := writeRelayMessage(ctx, agent.ws, typ, data); err != nil {
			return
		}
	}
}

func (h *relayHub) finish() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.finishLocked()
}

func (h *relayHub) finishLocked() {
	if h.done.Swap(true) {
		return
	}
	close(h.closed)
	if h.recorder != nil {
		h.recorder.Close()
	}
	h.gateway.relayHubs.CompareAndDelete(h.relayID, h)
	h.gateway.relayMeta.CompareAndDelete(h.relayID, h.meta)
	if h.gateway.auditLog != nil {
		fields := map[string]string{
			"event": "mesh_session_end",
			"type":  h.sessionType,
			"proto": h.proto,
		}
		if h.recPath != "" {
			fields["recording"] = h.recPath
		}
		h.gateway.auditLog.Log(audit.ActionPeerUpdated, h.auditUser, h.auditPeer, fields)
	}
	log.Printf("[mesh] Relay hub %s ended", h.relayID)
}

func writeRelayMessage(ctx context.Context, conn *websocket.Conn, typ websocket.MessageType, data []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return conn.Write(writeCtx, typ, data)
}
