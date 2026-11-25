package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/config"
	bdcrypto "github.com/unitronix/betterdesk-server/crypto"
	"github.com/unitronix/betterdesk-server/db"
	"github.com/unitronix/betterdesk-server/peer"
)

const benchHeartbeatPeer = "benchpeer01"

// newHeartbeatBenchServer builds a fleet-shaped database: many peers, several
// strategies with large assignment lists, and a branding logo stored inline.
func newHeartbeatBenchServer(tb testing.TB, peers, strategies, perStrategy int) *Server {
	tb.Helper()
	database, err := db.OpenSQLite(tb.TempDir() + "/bench.db")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { database.Close() })
	if err := database.Migrate(); err != nil {
		tb.Fatal(err)
	}
	ids := []string{benchHeartbeatPeer}
	for i := 0; i < peers; i++ {
		ids = append(ids, fmt.Sprintf("bp%06d", i))
	}
	for _, id := range ids {
		if err := database.UpsertPeer(&db.Peer{ID: id, UUID: id + "-uuid"}); err != nil {
			tb.Fatal(err)
		}
	}
	// Assignments store the resolved peer key, as the assignment API does.
	keys := make([]string, len(ids))
	for i, id := range ids {
		key, err := database.ResolvePeerAssignmentKey(id)
		if err != nil {
			tb.Fatal(err)
		}
		keys[i] = key
	}
	for i := 0; i < strategies; i++ {
		st := &db.Strategy{Name: fmt.Sprintf("s%d", i), Enabled: true, Permissions: `{"enable-audio":"Y"}`}
		if err := database.CreateStrategy(st); err != nil {
			tb.Fatal(err)
		}
		assigned := make([]string, 0, perStrategy+1)
		for j := 0; j < perStrategy && 1+i*perStrategy+j < len(keys); j++ {
			assigned = append(assigned, keys[1+i*perStrategy+j])
		}
		if i == strategies-1 {
			assigned = append(assigned, keys[0])
		}
		if err := database.AssignStrategy(st.GUID, assigned, nil, nil); err != nil {
			tb.Fatal(err)
		}
	}
	logo, _ := json.Marshal(BrandingLogo{Mime: "image/png", DataBase64: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 96*1024))})
	for key, value := range map[string]string{
		"branding_company_name": "Bench Co",
		"branding_logo":         string(logo),
		"branding_revision":     "1767225600000",
	} {
		if err := database.SetConfig(key, value); err != nil {
			tb.Fatal(err)
		}
	}
	keyPair, err := bdcrypto.GenerateKeyPair()
	if err != nil {
		tb.Fatal(err)
	}
	srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "bench")
	srv.SetKeyPair(keyPair)
	// Measure the handler, not the per-IP limiter's reject path.
	srv.heartbeatLimiter = nil
	return srv
}

func heartbeatOnce(tb testing.TB, srv *Server) *httptest.ResponseRecorder {
	body := `{"id":"` + benchHeartbeatPeer + `","uuid":"` + benchHeartbeatPeer + `-uuid","modified_at":1767225600000}`
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.handleClientHeartbeat(rec, req)
	if rec.Code != http.StatusOK {
		tb.Fatalf("heartbeat status %d: %s", rec.Code, rec.Body.String())
	}
	return rec
}

func TestHeartbeatBenchFixtureDeliversStrategy(t *testing.T) {
	srv := newHeartbeatBenchServer(t, 50, 3, 10)
	rec := heartbeatOnce(t, srv)
	if !strings.Contains(rec.Body.String(), `"enable-audio":"Y"`) {
		t.Fatalf("heartbeat response lacks the assigned strategy: %s", rec.Body.String())
	}
}

func BenchmarkClientHeartbeat(b *testing.B) {
	srv := newHeartbeatBenchServer(b, 2000, 20, 100)
	heartbeatOnce(b, srv)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		heartbeatOnce(b, srv)
	}
}
