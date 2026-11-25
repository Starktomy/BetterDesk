package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/db"
)

func TestStrategyChangeThroughAPIReachesNextHeartbeat(t *testing.T) {
	srv := newHeartbeatBenchServer(t, 20, 2, 5)
	if got := srv.clientStrategyConfigOptions(benchHeartbeatPeer); got["enable-audio"] != "Y" {
		t.Fatalf("options before = %v", got)
	}

	// An empty strategy unassigns the peer through the public assign API.
	req := httptest.NewRequest(http.MethodPost, "/api/strategies/assign",
		strings.NewReader(`{"strategy":"","peers":["`+benchHeartbeatPeer+`"]}`))
	rec := httptest.NewRecorder()
	srv.handleStrategiesAssign(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign handler: %d %s", rec.Code, rec.Body.String())
	}

	if got := srv.clientStrategyConfigOptions(benchHeartbeatPeer); len(got) != 0 {
		t.Fatalf("unassigned strategy still delivered from the cache: %v", got)
	}
}

func TestStrategyWrittenOutsideTheAPIAppearsAfterTheTTL(t *testing.T) {
	srv := newHeartbeatBenchServer(t, 0, 0, 0)
	if got := srv.clientStrategyConfigOptions(benchHeartbeatPeer); len(got) != 0 {
		t.Fatalf("options before = %v", got)
	}
	// The console writes to the shared database without calling this API.
	key, err := srv.db.ResolvePeerAssignmentKey(benchHeartbeatPeer)
	if err != nil {
		t.Fatal(err)
	}
	st := &db.Strategy{Name: "console", Enabled: true, Permissions: `{"enable-audio":"N"}`}
	if err := srv.db.CreateStrategy(st); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.AssignStrategy(st.GUID, []string{key}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := srv.clientStrategyConfigOptions(benchHeartbeatPeer); len(got) != 0 {
		t.Fatalf("expected the cached (empty) options within the TTL, got %v", got)
	}
	srv.strategyCache.mu.Lock()
	srv.strategyCache.expires = time.Now().Add(-time.Second)
	srv.strategyCache.mu.Unlock()
	if got := srv.clientStrategyConfigOptions(benchHeartbeatPeer)["enable-audio"]; got != "N" {
		t.Fatalf("enable-audio = %q after the TTL", got)
	}
}

func TestBrandingSaveIsVisibleAtOnce(t *testing.T) {
	srv := newHeartbeatBenchServer(t, 0, 0, 0)
	before := srv.clientBrandingModifiedAt()
	if srv.loadBrandingConfig().CompanyName != "Bench Co" {
		t.Fatal("fixture branding not loaded")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/branding", strings.NewReader(`{"company_name":"Renamed Co"}`))
	rec := httptest.NewRecorder()
	srv.handleSaveBranding(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save branding: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Renamed Co") {
		t.Fatalf("save response carries stale branding: %s", rec.Body.String())
	}
	if got := srv.loadBrandingConfig().CompanyName; got != "Renamed Co" {
		t.Fatalf("CompanyName = %q after save", got)
	}
	if srv.clientBrandingModifiedAt() == before {
		t.Fatal("modified_at did not advance, so clients would not refetch")
	}
}

func TestBrandingCallersCannotChangeTheCache(t *testing.T) {
	srv := newHeartbeatBenchServer(t, 0, 0, 0)
	first := srv.loadBrandingConfig()
	first.CompanyName = "mutated"
	first.Logo.DataBase64 = "mutated"
	first.Profiles.RustDesk.ConfigOptions["injected"] = "Y"

	second := srv.loadBrandingConfig()
	if second.CompanyName != "Bench Co" || second.Logo.DataBase64 == "mutated" {
		t.Fatal("a caller's change leaked into the cached branding")
	}
	if _, leaked := second.Profiles.RustDesk.ConfigOptions["injected"]; leaked {
		t.Fatal("a caller's map change leaked into the cached branding")
	}
}

func TestTTLCacheDropsLoadsRacedByInvalidation(t *testing.T) {
	var cache ttlCache[int]
	got := cache.get(func() (int, bool) {
		cache.invalidate() // a write lands while this load is running
		return 1, true
	})
	if got != 1 {
		t.Fatalf("get = %d", got)
	}
	if again := cache.get(func() (int, bool) { return 2, true }); again != 2 {
		t.Fatalf("stale value %d was cached across an invalidation", again)
	}
	if v := cache.get(func() (int, bool) { return 3, true }); v != 2 {
		t.Fatalf("fresh value not cached: got %d", v)
	}

	var failing ttlCache[int]
	failing.get(func() (int, bool) { return 9, false })
	if v := failing.get(func() (int, bool) { return 4, true }); v != 4 {
		t.Fatalf("failed load was cached: got %d", v)
	}
}

func TestTTLCacheSharesOneLoadBetweenConcurrentMisses(t *testing.T) {
	var cache ttlCache[int]
	var loads atomic.Int32
	release := make(chan struct{})
	load := func() (int, bool) {
		loads.Add(1)
		<-release
		return 7, true
	}

	var wg sync.WaitGroup
	results := make([]int, 20)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = cache.get(load)
		}(i)
	}
	// Let every goroutine reach the cache before the load finishes.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if n := loads.Load(); n != 1 {
		t.Fatalf("%d loads for one miss, want 1", n)
	}
	for i, v := range results {
		if v != 7 {
			t.Fatalf("caller %d got %d", i, v)
		}
	}
}

func TestTTLCacheInvalidationDoesNotJoinAnOlderLoad(t *testing.T) {
	var cache ttlCache[int]
	started := make(chan struct{})
	release := make(chan struct{})
	go cache.get(func() (int, bool) {
		close(started)
		<-release
		return 1, true // read before the write below
	})
	<-started
	cache.invalidate() // a write landed while that load was running

	got := cache.get(func() (int, bool) { return 2, true })
	close(release)
	if got != 2 {
		t.Fatalf("caller after the write got %d from the older load", got)
	}
}
