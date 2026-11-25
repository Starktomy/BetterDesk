package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/db"
)

func callStrategyHandler(t *testing.T, handler http.HandlerFunc, method, path, guid, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if guid != "" {
		req.SetPathValue("guid", guid)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// Strategy GUIDs are 36-character UUIDs. Truncating them to the 32-character
// peer ID limit made every GUID-addressed strategy route miss.
func TestStrategyRoutesAcceptFullUUIDs(t *testing.T) {
	srv := newHeartbeatBenchServer(t, 0, 0, 0)
	const groupGUID = "0f8fad5b-d9cb-469f-a165-70867728950e"

	rec := callStrategyHandler(t, srv.handleStrategiesPost, http.MethodPost, "/api/strategies", "",
		`{"name":"audio","permissions":{"enable-audio":"Y"},"device_group_guid":"`+groupGUID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created db.Strategy
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.GUID) != 36 {
		t.Fatalf("expected a UUID strategy GUID, got %q", created.GUID)
	}
	if created.DeviceGroupGUID != groupGUID {
		t.Fatalf("device group GUID stored as %q", created.DeviceGroupGUID)
	}

	rec = callStrategyHandler(t, srv.handleStrategiesPost, http.MethodPost, "/api/strategies", "",
		`{"guid":"`+created.GUID+`","name":"audio renamed","permissions":{"enable-audio":"Y"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}

	rec = callStrategyHandler(t, srv.handleStrategiesAssign, http.MethodPost, "/api/strategies/assign", "",
		`{"strategy":"`+created.GUID+`","peers":["`+benchHeartbeatPeer+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign: %d %s", rec.Code, rec.Body.String())
	}
	if got := srv.clientStrategyConfigOptions(benchHeartbeatPeer)["enable-audio"]; got != "Y" {
		t.Fatalf("assigned strategy not delivered: %q", got)
	}

	rec = callStrategyHandler(t, srv.handleStrategiesStatus, http.MethodPut, "/api/strategies/x/status", created.GUID, "false")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	if got := srv.clientStrategyConfigOptions(benchHeartbeatPeer); len(got) != 0 {
		t.Fatalf("disabled strategy still delivered: %v", got)
	}

	rec = callStrategyHandler(t, srv.handleStrategiesGetByGUID, http.MethodGet, "/api/strategies/x", created.GUID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}

	rec = callStrategyHandler(t, srv.handleStrategiesDelete, http.MethodDelete, "/api/strategies/x", created.GUID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
}
