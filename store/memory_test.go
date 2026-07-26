package store

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMemory_GetSet(t *testing.T) {
	m := NewMemory(time.Minute)
	defer m.Close()
	ctx := context.Background()

	// Get non-existent key.
	_, exists, err := m.Get(ctx, "missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Fatal("expected exists=false for missing key")
	}

	// Set and retrieve.
	s := State{Tokens: 42.5, Version: 1}
	if err := m.Set(ctx, "k1", s, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, exists, err := m.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !exists {
		t.Fatal("expected exists=true")
	}
	if got.Tokens != 42.5 || got.Version != 1 {
		t.Errorf("got %+v, want Tokens=42.5, Version=1", got)
	}
}

func TestMemory_CompareAndSwap(t *testing.T) {
	m := NewMemory(time.Minute)
	defer m.Close()
	ctx := context.Background()

	old := State{Version: 0}
	new := State{Tokens: 10, Version: 1}

	// CAS on missing key with version 0 should succeed.
	ok, err := m.CompareAndSwap(ctx, "k1", old, new, time.Minute)
	if err != nil {
		t.Fatalf("CAS: %v", err)
	}
	if !ok {
		t.Fatal("CAS should succeed for new key with version 0")
	}

	// CAS with stale version should fail.
	stale := State{Version: 0}
	newer := State{Tokens: 20, Version: 2}
	ok, err = m.CompareAndSwap(ctx, "k1", stale, newer, time.Minute)
	if err != nil {
		t.Fatalf("CAS: %v", err)
	}
	if ok {
		t.Fatal("CAS should fail with stale version")
	}

	// CAS with correct version should succeed.
	ok, err = m.CompareAndSwap(ctx, "k1", new, newer, time.Minute)
	if err != nil {
		t.Fatalf("CAS: %v", err)
	}
	if !ok {
		t.Fatal("CAS should succeed with correct version")
	}
}

func TestMemory_Delete(t *testing.T) {
	m := NewMemory(time.Minute)
	defer m.Close()
	ctx := context.Background()

	m.Set(ctx, "k1", State{Tokens: 5}, time.Minute)
	m.Delete(ctx, "k1")

	_, exists, _ := m.Get(ctx, "k1")
	if exists {
		t.Fatal("key should be deleted")
	}
}

func TestMemory_TTLExpiration(t *testing.T) {
	m := NewMemory(time.Minute)
	defer m.Close()
	ctx := context.Background()

	m.Set(ctx, "k1", State{Tokens: 5}, 10*time.Millisecond)

	time.Sleep(20 * time.Millisecond)

	_, exists, _ := m.Get(ctx, "k1")
	if exists {
		t.Fatal("key should have expired")
	}
}

func TestMemory_ConcurrentAccess(t *testing.T) {
	m := NewMemory(time.Minute)
	defer m.Close()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "concurrent-key"
			m.Set(ctx, key, State{Tokens: float64(i), Version: uint64(i)}, time.Minute)
			m.Get(ctx, key)
		}(i)
	}
	wg.Wait()
}
