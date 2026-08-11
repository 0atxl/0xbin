package httpapi

import (
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestLivePublicationRegistryOrdersSameRoomWithoutBlockingDifferentRooms(t *testing.T) {
	registry := newLivePublicationRegistry()
	firstUnlock := registry.lock("calmbrightotter")

	sameRoomAcquired := make(chan struct{})
	sameRoomReleased := make(chan struct{})
	go func() {
		unlock := registry.lock("calmbrightotter")
		close(sameRoomAcquired)
		unlock()
		close(sameRoomReleased)
	}()
	differentRoomAcquired := make(chan struct{})
	differentRoomReleased := make(chan struct{})
	go func() {
		unlock := registry.lock("quietbrightotter")
		close(differentRoomAcquired)
		unlock()
		close(differentRoomReleased)
	}()

	select {
	case <-differentRoomAcquired:
	case <-time.After(time.Second):
		t.Fatal("different-room publication was blocked")
	}
	select {
	case <-differentRoomReleased:
	case <-time.After(time.Second):
		t.Fatal("different-room publication did not release")
	}
	select {
	case <-sameRoomAcquired:
		t.Fatal("same-room publication bypassed ordering lock")
	case <-time.After(25 * time.Millisecond):
	}
	firstUnlock()
	select {
	case <-sameRoomAcquired:
	case <-time.After(time.Second):
		t.Fatal("same-room publication did not resume")
	}
	select {
	case <-sameRoomReleased:
	case <-time.After(time.Second):
		t.Fatal("same-room publication did not release")
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if len(registry.rooms) != 0 {
		t.Fatalf("idle publication entries = %d, want 0", len(registry.rooms))
	}
}

func BenchmarkLivePublicationRegistry(b *testing.B) {
	for _, roomCount := range []int{1, 16} {
		b.Run("rooms="+strconv.Itoa(roomCount), func(b *testing.B) {
			registry := newLivePublicationRegistry()
			var next atomic.Uint64
			b.RunParallel(func(parallel *testing.PB) {
				for parallel.Next() {
					slug := "room-" + strconv.Itoa(int(next.Add(1)%uint64(roomCount)))
					unlock := registry.lock(slug)
					unlock()
				}
			})
		})
	}
}
