package httpapi

import "sync"

// livePublicationRegistry serializes durable room changes with their outbound
// publications per room. Different rooms never share this ordering path, and
// reference counting removes idle entries so arbitrary room slugs cannot grow
// process state indefinitely.
type livePublicationRegistry struct {
	mu    sync.Mutex
	rooms map[string]*livePublicationRoom
}

type livePublicationRoom struct {
	mu   sync.Mutex
	refs int
}

func newLivePublicationRegistry() *livePublicationRegistry {
	return &livePublicationRegistry{rooms: make(map[string]*livePublicationRoom)}
}

// lock returns an unlock function. Callers must release it exactly once.
func (registry *livePublicationRegistry) lock(slug string) func() {
	registry.mu.Lock()
	room := registry.rooms[slug]
	if room == nil {
		room = &livePublicationRoom{}
		registry.rooms[slug] = room
	}
	room.refs++
	registry.mu.Unlock()

	room.mu.Lock()
	return func() {
		room.mu.Unlock()
		registry.mu.Lock()
		room.refs--
		if room.refs == 0 {
			delete(registry.rooms, slug)
		}
		registry.mu.Unlock()
	}
}
