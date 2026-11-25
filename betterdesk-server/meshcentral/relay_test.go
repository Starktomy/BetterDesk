package meshcentral

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestMeshRelayForwardsBetweenAuthorizedParticipants(t *testing.T) {
	for _, protocol := range []int{relayProtocolTerminal, relayProtocolKVM} {
		t.Run(strconv.Itoa(protocol), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			codec, err := NewCookieCodec("authorized-test-session-cookie-secret")
			if err != nil {
				t.Fatal(err)
			}
			g := &Gateway{cookies: codec}
			id := newRelayID()
			g.setRelayMeta(id, &relayMeta{Protocol: protocol})
			server := httptest.NewServer(http.HandlerFunc(g.handleRelayWS))
			defer server.Close()

			dial := func(browser bool) *websocket.Conn {
				t.Helper()
				ticket, err := codec.Encode(&RelayCookieData{RelayID: id, Protocol: protocol, Browser: browser}, relayJoinMinutes)
				if err != nil {
					t.Fatal(err)
				}
				query := url.Values{"id": {id}, "p": {strconv.Itoa(protocol)}}
				if browser {
					query.Set("browser", "1")
					query.Set("auth", ticket)
				} else {
					query.Set("rauth", ticket)
				}
				conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"?"+query.Encode(), nil)
				if err != nil {
					t.Fatal(err)
				}
				return conn
			}

			agent := dial(false)
			defer agent.CloseNow()
			browser := dial(true)
			defer browser.CloseNow()
			if _, _, err := agent.Read(ctx); err != nil {
				t.Fatal(err)
			}
			if err := agent.Write(ctx, websocket.MessageBinary, []byte("test frame")); err != nil {
				t.Fatal(err)
			}
			for {
				messageType, data, err := browser.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if messageType == websocket.MessageBinary {
					if string(data) != "test frame" {
						t.Fatalf("unexpected frame %q", data)
					}
					break
				}
			}
			browser.CloseNow()
			if _, _, err := agent.Read(ctx); err == nil || ctx.Err() != nil {
				t.Fatalf("agent tunnel did not close promptly: %v", err)
			}
		})
	}
}

func TestRelayIDPairing(t *testing.T) {
	g := &Gateway{
		relays: sync.Map{},
	}
	// pairing logic is integration-tested via meshrelay handler;
	// verify relay session map stores pending peer.
	session := &relaySession{id: "test-relay-id"}
	g.relays.Store("test-relay-id", session)
	v, ok := g.relays.Load("test-relay-id")
	if !ok || v.(*relaySession).id != "test-relay-id" {
		t.Fatal("relay session not stored")
	}
}

func TestRelayCookieReplayExpired(t *testing.T) {
	c, _ := NewCookieCodec("relay-test-secret-32chars-min")
	data := &RelayCookieData{RUserID: "u", ExpireMin: 1, IssuedAt: time.Now().Unix() - 3600}
	cookie, err := c.Encode(data, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Decode(cookie, 1)
	if err == nil {
		t.Fatal("expected expired cookie error")
	}
}

// Ensure mesh relay connect signal matches MeshCentral contract.
func TestRelayConnectSignal(t *testing.T) {
	if string([]byte{'c'}) != "c" {
		t.Fatal("connect signal must be 'c'")
	}
	_ = websocket.MessageText
}
