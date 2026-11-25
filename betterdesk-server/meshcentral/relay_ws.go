package meshcentral

import (
	"context"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/unitronix/betterdesk-server/audit"
)

const relayJoinMinutes = 2

type relayPeer struct {
	ws      *websocket.Conn
	userID  string
	browser bool
}

type relaySession struct {
	id    string
	peer1 *relayPeer
	state int // 0 waiting, 1 paired, 2 closed
	mu    sync.Mutex
	done  chan struct{}
}

func (g *Gateway) handleRelayWS(w http.ResponseWriter, r *http.Request) {
	if !g.allowIP(w, r) {
		return
	}
	q := r.URL.Query()
	relayID := q.Get("id")
	if relayID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	browser := q.Get("browser") == "1"
	token := q.Get("rauth")
	if browser {
		token = q.Get("auth")
	}
	meta := g.getRelayMeta(relayID)
	if meta == nil || token == "" || q.Get("p") != strconv.Itoa(meta.Protocol) {
		http.Error(w, "invalid relay authorization", http.StatusForbidden)
		return
	}
	cookie, err := g.cookies.Decode(token, relayJoinMinutes)
	if err != nil || cookie.RelayID != relayID || cookie.Protocol != meta.Protocol || cookie.Browser != browser {
		http.Error(w, "invalid relay authorization", http.StatusForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, g.webSocketAcceptOptions())
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(32 * 1024 * 1024)

	ctx, cancel := g.requestContext(r)
	defer cancel()
	peer := &relayPeer{ws: conn, userID: cookie.RUserID, browser: browser}
	g.pairRelay(ctx, relayID, peer, q.Get("p"), meta)
}

func isKvmRelayProto(proto string) bool {
	return proto == "2"
}

func (g *Gateway) pairRelay(ctx context.Context, relayID string, peer *relayPeer, proto string, meta *relayMeta) {
	if isKvmRelayProto(proto) {
		g.runKvmRelayHub(ctx, relayID, peer, proto, meta)
		return
	}
	entry, _ := g.relays.LoadOrStore(relayID, &relaySession{id: relayID, done: make(chan struct{})})
	session := entry.(*relaySession)

	session.mu.Lock()
	if session.peer1 == nil {
		session.peer1 = peer
		session.mu.Unlock()
		timer := time.NewTimer(relayJoinMinutes * time.Minute)
		defer timer.Stop()
		select {
		case <-session.done:
		case <-ctx.Done():
		case <-timer.C:
			session.mu.Lock()
			paired := session.state == 1
			if !paired {
				session.state = 2
				g.relays.CompareAndDelete(relayID, session)
			}
			session.mu.Unlock()
			if paired {
				select {
				case <-session.done:
				case <-ctx.Done():
				}
			}
		}
		g.relays.CompareAndDelete(relayID, session)
		return
	}
	if session.state != 0 || session.peer1.browser == peer.browser {
		session.mu.Unlock()
		return
	}
	peer2 := peer
	peer1 := session.peer1
	session.state = 1
	session.mu.Unlock()
	defer close(session.done)

	recording := meta.Record
	sig := RelayConnectSignal(recording)
	if writeRelayMessage(ctx, peer1.ws, websocket.MessageText, []byte(sig)) != nil ||
		writeRelayMessage(ctx, peer2.ws, websocket.MessageText, []byte(sig)) != nil {
		g.relayMeta.CompareAndDelete(relayID, meta)
		return
	}

	auditUser := peer1.userID
	auditPeer := relayID
	sessionType := proto
	recPath := ""
	if meta != nil {
		if meta.UserID != "" {
			auditUser = meta.UserID
		}
		if meta.PeerID != "" {
			auditPeer = meta.PeerID
		}
		if meta.SessionType != "" {
			sessionType = meta.SessionType
		}
	}

	var recorder *relayRecorder
	if recording && meta != nil && g.cfg != nil {
		dataDir := filepath.Dir(g.cfg.DBPath)
		if dataDir == "" || dataDir == "." {
			dataDir = "."
		}
		if r, path, err := openRelayRecorder(dataDir, meta.PeerID, relayID, meta.SessionType); err == nil {
			recorder = r
			recPath = path
		}
	}

	if g.auditLog != nil {
		g.auditLog.Log(audit.ActionPeerUpdated, auditUser, auditPeer, map[string]string{
			"event": "mesh_relay_paired",
			"proto": proto,
		})
	}

	pipeRelay(ctx, peer1.ws, peer2.ws, recorder)
	if recorder != nil {
		recorder.Close()
	}
	g.relayMeta.CompareAndDelete(relayID, meta)

	if g.auditLog != nil {
		fields := map[string]string{
			"event": "mesh_session_end",
			"type":  sessionType,
			"proto": proto,
		}
		if recPath != "" {
			fields["recording"] = recPath
		}
		g.auditLog.Log(audit.ActionPeerUpdated, auditUser, auditPeer, fields)
	}
	log.Printf("[mesh] Relay session %s ended", relayID)
}

func pipeRelay(ctx context.Context, a, b *websocket.Conn, recorder *relayRecorder) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	copyWS := func(dst, src *websocket.Conn) {
		defer wg.Done()
		defer cancel()
		for {
			typ, data, err := src.Read(ctx)
			if err != nil {
				return
			}
			if recorder != nil && len(data) > 0 {
				recorder.Write(int(typ), data)
			}
			if err := writeRelayMessage(ctx, dst, typ, data); err != nil {
				return
			}
		}
	}
	wg.Add(2)
	go copyWS(b, a)
	go copyWS(a, b)
	wg.Wait()
	a.CloseNow()
	b.CloseNow()
}

// KVMRelay handles opaque MNG_KVM binary relay (protocol p=2).
func KVMRelay(ctx context.Context, a, b io.ReadWriter) {
	go func() { io.Copy(b, a) }()
	io.Copy(a, b)
}
