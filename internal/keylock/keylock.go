// Package keylock serialises work per key without keeping idle keys around.
package keylock

import "sync"

type Locks struct {
	mu sync.Mutex
	m  map[string]*entry
}

type entry struct {
	mu   sync.Mutex
	refs int
}

// Lock blocks until key is free and returns the function that frees it.
func (l *Locks) Lock(key string) (unlock func()) {
	l.mu.Lock()
	if l.m == nil {
		l.m = map[string]*entry{}
	}
	e := l.m[key]
	if e == nil {
		e = &entry{}
		l.m[key] = e
	}
	e.refs++
	l.mu.Unlock()

	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		l.mu.Lock()
		if e.refs--; e.refs == 0 {
			delete(l.m, key)
		}
		l.mu.Unlock()
	}
}
