package ratelimit

import (
	"testing"
	"time"

	"github.com/0atxl/0xbin/internal/config"
)

func TestCategoriesUseIndependentBuckets(t *testing.T) {
	now := time.Unix(0, 0)
	registry := testRegistry(t, &now, 2, time.Hour)
	for range 2 {
		if ok, _ := registry.Allow(Create, "192.0.2.1", 1); !ok {
			t.Fatal("create request unexpectedly denied")
		}
	}
	if ok, retry := registry.Allow(Create, "192.0.2.1", 1); ok || retry <= 0 {
		t.Fatalf("create allowance = %v, %v; want denied with retry", ok, retry)
	}
	if ok, _ := registry.Allow(Read, "192.0.2.1", 1); !ok {
		t.Fatal("read bucket was affected by create bucket")
	}
}

func TestMissEscalationAndSuccessfulReadReset(t *testing.T) {
	now := time.Unix(0, 0)
	registry := testRegistry(t, &now, 20, time.Hour)
	for range consecutiveMissThreshold - 1 {
		if cost := registry.RecordMiss("192.0.2.1"); cost != 1 {
			t.Fatalf("miss cost = %d, want 1", cost)
		}
	}
	if cost := registry.RecordMiss("192.0.2.1"); cost != 2 {
		t.Fatalf("escalated miss cost = %d, want 2", cost)
	}
	registry.RecordSuccess("192.0.2.1")
	if cost := registry.RecordMiss("192.0.2.1"); cost != 1 {
		t.Fatalf("miss cost after success = %d, want 1", cost)
	}
}

func TestRegistryEvictsInactiveEntries(t *testing.T) {
	now := time.Unix(0, 0)
	registry := testRegistry(t, &now, 10, time.Minute)
	registry.Allow(Create, "192.0.2.1", 1)
	registry.RecordMiss("192.0.2.1")
	now = now.Add(time.Minute)
	registry.Allow(Create, "192.0.2.2", 1)
	if got := registry.Len(); got != 1 {
		t.Fatalf("registry entries = %d, want 1", got)
	}
}

func testRegistry(t *testing.T, now *time.Time, count int, staleAfter time.Duration) *Registry {
	t.Helper()
	registry, err := NewRegistry(map[Category]config.Rate{
		Create:  {Count: count, Window: time.Hour},
		Read:    {Count: count, Window: time.Hour},
		Miss:    {Count: count, Window: time.Hour},
		Consume: {Count: count, Window: time.Hour},
	}, 100, staleAfter, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return registry
}
