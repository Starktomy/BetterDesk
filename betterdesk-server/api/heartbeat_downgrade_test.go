package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/unitronix/betterdesk-server/db"
)

func postPlainHeartbeat(t *testing.T, srv *Server, body, remoteAddr string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/heartbeat", bytes.NewReader([]byte(body)))
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	srv.handleClientHeartbeat(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat: %d %s", rec.Code, rec.Body.String())
	}
}

// Once a device has signed its heartbeats, an unsigned heartbeat naming it is
// only a liveness signal: it cannot move the device's address or write
// metrics.
func TestUnsignedHeartbeatCannotSpeakForAVerifiedDevice(t *testing.T) {
	srv, deviceID, deviceUUID, devicePriv, responsePub, _ := newConnectionModeFixture(t)
	srv.heartbeatLimiter = nil
	postHeartbeat(t, srv, deviceID, deviceUUID,
		sealClientHeartbeat(t, srv, deviceID, devicePriv, 1, desktopIdentity(deviceID, deviceUUID, "normal"), responsePub))
	if !srv.hasVerifiedIdentity(deviceID) {
		t.Fatal("signed heartbeat did not record a verified identity")
	}
	before, err := srv.db.GetPeer(deviceID)
	if err != nil || before == nil {
		t.Fatalf("GetPeer: %v", err)
	}

	postPlainHeartbeat(t, srv, `{"id":"`+deviceID+`","uuid":"`+deviceUUID+`","cpu":99,"memory":88,"disk":77}`, "203.0.113.66:4000")

	after, err := srv.db.GetPeer(deviceID)
	if err != nil || after == nil {
		t.Fatalf("GetPeer: %v", err)
	}
	if after.IP != before.IP {
		t.Fatalf("unsigned heartbeat moved the device from %q to %q", before.IP, after.IP)
	}
	if after.Status != "ONLINE" {
		t.Fatalf("status = %q, want the liveness signal kept", after.Status)
	}
	if metric, _ := srv.db.GetLatestPeerMetric(deviceID); metric != nil && metric.CPU == 99 {
		t.Fatal("unsigned heartbeat wrote metrics for a verified device")
	}
}

// The liveness update for an unsigned heartbeat must not write back an
// address read before a concurrent signed heartbeat changed it, nor bind
// the device's owner from a client session.
func TestUnsignedHeartbeatLeavesAddressAndOwnerAlone(t *testing.T) {
	srv, deviceID, deviceUUID, devicePriv, responsePub, _ := newConnectionModeFixture(t)
	srv.heartbeatLimiter = nil
	postHeartbeat(t, srv, deviceID, deviceUUID,
		sealClientHeartbeat(t, srv, deviceID, devicePriv, 1, desktopIdentity(deviceID, deviceUUID, "normal"), responsePub))

	// A signed heartbeat moved the device after the unsigned one read it.
	if err := srv.db.UpdatePeerStatus(deviceID, "ONLINE", "192.0.2.77", "heartbeat"); err != nil {
		t.Fatal(err)
	}
	user := &db.User{Username: "attacker", PasswordHash: "x", Role: "operator"}
	if err := srv.db.CreateUser(user); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.CreateClientSession(&db.ClientSession{
		TokenHash: "attacker-session", UserID: user.ID, ClientID: deviceID, ClientUUID: deviceUUID,
		ExpiresAt: "2999-01-01 00:00:00",
	}); err != nil {
		t.Fatal(err)
	}

	postPlainHeartbeat(t, srv, `{"id":"`+deviceID+`","uuid":"`+deviceUUID+`"}`, "203.0.113.66:4000")

	peer, err := srv.db.GetPeer(deviceID)
	if err != nil || peer == nil {
		t.Fatalf("GetPeer: %v", err)
	}
	if peer.IP != "192.0.2.77" {
		t.Fatalf("IP = %q, want the newer address kept", peer.IP)
	}
	if peer.User == "attacker" {
		t.Fatal("unsigned heartbeat bound the device owner")
	}
}

func TestStockRustDeskHeartbeatIsUnchanged(t *testing.T) {
	srv := newHeartbeatBenchServer(t, 0, 0, 0)
	postPlainHeartbeat(t, srv, `{"id":"`+benchHeartbeatPeer+`","cpu":42,"memory":1,"disk":1}`, "198.51.100.20:5000")

	peer, err := srv.db.GetPeer(benchHeartbeatPeer)
	if err != nil || peer == nil {
		t.Fatalf("GetPeer: %v", err)
	}
	if peer.IP != "198.51.100.20" {
		t.Fatalf("IP = %q", peer.IP)
	}
	if metric, _ := srv.db.GetLatestPeerMetric(benchHeartbeatPeer); metric == nil || metric.CPU != 42 {
		t.Fatalf("metric = %+v", metric)
	}
}

func TestPublicAuditReportsMustNameAKnownDevice(t *testing.T) {
	srv := newHeartbeatBenchServer(t, 0, 0, 0)
	post := func(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/audit", bytes.NewReader([]byte(body)))
		req.RemoteAddr = "198.51.100.30:6000"
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}

	for name, handler := range map[string]http.HandlerFunc{
		"conn":  srv.handleAuditConnPost,
		"file":  srv.handleAuditFilePost,
		"alarm": srv.handleAuditAlarmPost,
	} {
		if rec := post(handler, `{"host_id":"nosuchpeer","conn_type":0,"direction":0,"alarm_type":0}`); rec.Code != http.StatusNotFound {
			t.Errorf("%s: unknown host accepted with %d", name, rec.Code)
		}
	}

	rec := post(srv.handleAuditConnPost, `{"host_id":"`+benchHeartbeatPeer+`","conn_type":0,"ip":"203.0.113.9:21118"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("known host rejected: %d %s", rec.Code, rec.Body.String())
	}
	rec = post(srv.handleAuditConnPost, `{"host_id":"`+benchHeartbeatPeer+`","conn_type":0,"ip":"<script>"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("known host rejected: %d %s", rec.Code, rec.Body.String())
	}
	rows, err := srv.db.ListAuditConnections(db.AuditFilter{HostID: benchHeartbeatPeer, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, row := range rows {
		got[row.IP] = true
	}
	if !got["203.0.113.9"] || !got["198.51.100.30"] || len(got) != 2 {
		t.Fatalf("recorded IPs = %v, want the parsed remote IP and the reporter fallback", got)
	}
}
