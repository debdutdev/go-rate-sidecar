package store

import (
	"context"
	"sync"
	"time"
)

type entry struct {
	mu        sync.Mutex
	state     State
	expiresAt time.Time
}

// Memory is an in-memory Store using sync.Map for key-level lookup
// and per-entry mutexes to avoid global lock contention.
type Memory struct {
	entries sync.Map // map[string]*entry

	stopOnce sync.Once
	stopCh   chan struct{}
}

// NewMemory creates an in-memory store with a background goroutine
// that evicts expired entries every cleanupInterval.
func NewMemory(cleanupInterval time.Duration) *Memory {
	if cleanupInterval <= 0 {
		cleanupInterval = time.Minute
	}
	m := &Memory{
		stopCh: make(chan struct{}),
	}
	go m.evictionLoop(cleanupInterval)
	return m
}

func (m *Memory) evictionLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			m.entries.Range(func(key, value any) bool {
				e := value.(*entry)
				e.mu.Lock()
				expired := !e.expiresAt.IsZero() && now.After(e.expiresAt)
				e.mu.Unlock()
				if expired {
					m.entries.Delete(key)
				}
				return true
			})
		case <-m.stopCh:
			return
		}
	}
}

// Get retrieves the state for key, returning false if not found or expired.
func (m *Memory) Get(_ context.Context, key string) (State, bool, error) {
	val, ok := m.entries.Load(key)
	if !ok {
		return State{}, false, nil
	}
	e := val.(*entry)
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		m.entries.Delete(key)
		return State{}, false, nil
	}
	return e.state, true, nil
}

// Set writes state for key with the given TTL.
func (m *Memory) Set(_ context.Context, key string, state State, ttl time.Duration) error {
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	val, loaded := m.entries.LoadOrStore(key, &entry{
		state:     state,
		expiresAt: expiresAt,
	})
	if loaded {
		e := val.(*entry)
		e.mu.Lock()
		e.state = state
		e.expiresAt = expiresAt
		e.mu.Unlock()
	}
	return nil
}

// CompareAndSwap updates state only if the stored version matches old.Version.
func (m *Memory) CompareAndSwap(_ context.Context, key string, old, new State, ttl time.Duration) (bool, error) {
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	val, loaded := m.entries.LoadOrStore(key, &entry{
		state:     new,
		expiresAt: expiresAt,
	})
	if !loaded {
		// Key didn't exist; only succeed if old version is 0 (initial state).
		if old.Version == 0 {
			return true, nil
		}
		m.entries.Delete(key)
		return false, nil
	}

	e := val.(*entry)
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.state.Version != old.Version {
		return false, nil
	}
	e.state = new
	e.expiresAt = expiresAt
	return true, nil
}

// Delete removes state for key.
func (m *Memory) Delete(_ context.Context, key string) error {
	m.entries.Delete(key)
	return nil
}

// Close stops the background eviction goroutine.
func (m *Memory) Close() error {
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
	return nil
}
