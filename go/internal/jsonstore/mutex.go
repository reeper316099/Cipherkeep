package jsonstore

import "sync"

// KeyedMutex serialises operations on the same key; different keys run in parallel.
type KeyedMutex struct {
	mu    sync.Mutex
	locks map[string]*keyLock
}

type keyLock struct {
	mu   sync.Mutex
	refs int
}

// Lock blocks until the key is free and returns the function that releases it.
func (k *KeyedMutex) Lock(key string) (unlock func()) {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = map[string]*keyLock{}
	}
	l := k.locks[key]
	if l == nil {
		l = &keyLock{}
		k.locks[key] = l
	}
	l.refs++
	k.mu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		k.mu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}

// Run runs fn while holding the key.
func (k *KeyedMutex) Run(key string, fn func() error) error {
	defer k.Lock(key)()
	return fn()
}
