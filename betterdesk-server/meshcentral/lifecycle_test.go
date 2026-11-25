package meshcentral

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestGatewayStopClosesPairedRelays(t *testing.T) {
	for _, protocol := range []int{relayProtocolTerminal, relayProtocolKVM} {
		t.Run(strconv.Itoa(protocol), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			codec, err := NewCookieCodec("lifecycle-test-session-cookie-secret")
			if err != nil {
				t.Fatal(err)
			}
			g := &Gateway{cookies: codec}
			g.ctx, g.cancel = context.WithCancel(context.Background())
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

			g.Stop()
			if _, _, err := agent.Read(ctx); err == nil || ctx.Err() != nil {
				t.Fatalf("relay survived gateway stop: %v", err)
			}
		})
	}
}

func TestRecordingNameKeepsPeerForUnderscoredSessionTypes(t *testing.T) {
	name := fmtFilename("site_a_42", "relay_identifier", "tcp_relay")
	peerID, sessionType := meshRecordingIdentity(name)
	if peerID != "site_a_42" || sessionType != "tcp-relay" {
		t.Fatalf("%q parsed as peer %q type %q", name, peerID, sessionType)
	}
}
