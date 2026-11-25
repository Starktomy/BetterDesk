package api

import (
	"encoding/json"
	"maps"
	"slices"
	"sync"
	"time"
)

// heartbeatCacheTTL bounds how stale branding and strategy data can be when
// it changes outside this API: the console writes strategies to the shared
// database directly. Writes through this API invalidate at once.
const heartbeatCacheTTL = 10 * time.Second

// ttlCache holds one value that every heartbeat reads. The load runs outside
// the lock so a slow database never blocks readers that can use the cache,
// and concurrent misses share one load instead of each querying the database.
type ttlCache[T any] struct {
	mu       sync.Mutex
	value    T
	expires  time.Time
	gen      uint64
	inflight *ttlLoad[T]
}

type ttlLoad[T any] struct {
	done  chan struct{}
	value T
}

// get returns the cached value, or loads it. A load that reports !ok is
// returned to its callers but not cached, so a transient database error does
// not stick for the whole TTL.
func (c *ttlCache[T]) get(load func() (T, bool)) T {
	now := time.Now()
	c.mu.Lock()
	if now.Before(c.expires) {
		value := c.value
		c.mu.Unlock()
		return value
	}
	if pending := c.inflight; pending != nil {
		c.mu.Unlock()
		<-pending.done
		return pending.value
	}
	pending := &ttlLoad[T]{done: make(chan struct{})}
	c.inflight = pending
	gen := c.gen
	c.mu.Unlock()

	ok := false
	defer func() {
		c.mu.Lock()
		// An invalidation during the load means the value may predate the write.
		if ok && c.gen == gen {
			c.value = pending.value
			c.expires = now.Add(heartbeatCacheTTL)
		}
		if c.inflight == pending {
			c.inflight = nil
		}
		c.mu.Unlock()
		close(pending.done)
	}()
	pending.value, ok = load()
	return pending.value
}

// invalidate drops the cached value. A load already running may predate the
// write, so later callers start a new one instead of waiting for it.
func (c *ttlCache[T]) invalidate() {
	c.mu.Lock()
	c.gen++
	c.expires = time.Time{}
	c.inflight = nil
	c.mu.Unlock()
}

// loadBrandingConfig returns the branding configuration. Every heartbeat
// reads it (for modified_at), and the uncached read costs nine queries plus
// decoding the inline logo. Callers get their own copy and may modify it.
func (s *Server) loadBrandingConfig() BrandingConfig {
	if s == nil || s.db == nil {
		return s.readBrandingConfig()
	}
	return cloneBrandingConfig(s.brandingCache.get(func() (BrandingConfig, bool) {
		return s.readBrandingConfig(), true
	}))
}

func cloneBrandingConfig(cfg BrandingConfig) BrandingConfig {
	if cfg.Logo != nil {
		logo := *cfg.Logo
		cfg.Logo = &logo
	}
	cfg.Colors = maps.Clone(cfg.Colors)
	cfg.Profiles.RustDesk.ConfigOptions = maps.Clone(cfg.Profiles.RustDesk.ConfigOptions)
	cfg.Profiles.BetterDesk.Apply = slices.Clone(cfg.Profiles.BetterDesk.Apply)
	cfg.SyncModes = slices.Clone(cfg.SyncModes)
	return cfg
}

// strategyOptionIndex maps a peer assignment key to the RustDesk options of
// its enabled strategies. Later strategies win for a repeated key, as before.
type strategyOptionIndex map[string]map[string]string

// readStrategyIndex resolves every strategy's assignments once instead of
// once per heartbeat. The result is cached only when every lookup succeeded.
func (s *Server) readStrategyIndex() (strategyOptionIndex, bool) {
	strategies, err := s.db.ListStrategies()
	if err != nil {
		return nil, false
	}
	complete := true
	index := strategyOptionIndex{}
	for _, strategy := range strategies {
		if strategy == nil || !strategy.Enabled {
			continue
		}
		summary, err := s.db.GetStrategyAssignmentSummary(strategy.GUID)
		if err != nil {
			complete = false
			continue
		}
		if summary == nil {
			continue
		}
		options := strategyOptionsFromPermissions(strategy.Permissions)
		if len(options) == 0 {
			continue
		}
		for _, peerKey := range summary.Peers {
			out := index[peerKey]
			if out == nil {
				out = map[string]string{}
				index[peerKey] = out
			}
			maps.Copy(out, options)
		}
	}
	return index, complete
}

func strategyOptionsFromPermissions(raw string) map[string]string {
	var permissions map[string]any
	if json.Unmarshal([]byte(raw), &permissions) != nil {
		return nil
	}
	out := map[string]string{}
	for key, value := range permissions {
		if len(key) == 0 || len(key) > maxStrategyPermissionKeyLength {
			continue
		}
		if _, allowed := rustDeskStrategyOptionKeys[key]; !allowed {
			continue
		}
		if normalized, ok := normalizeStrategyOptionValue(value); ok {
			out[key] = normalized
		}
	}
	return out
}
