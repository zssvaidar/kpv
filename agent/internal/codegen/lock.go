package codegen

import (
	"context"
	"sync"
)

// Two runs for the same pipeline directory in one agent do not interleave:
// the second waits until the first has finished, or until its own context
// ends. The key is the directory's real path, so two spellings of one
// directory share one lock. Different directories do not wait for each other.
//
// This is a lock inside one process. Two agents writing the same directory
// are not kept apart; there is one agent per user.

type directoryLock struct {
	held  chan struct{}
	users int
}

var directoryLocks = struct {
	sync.Mutex
	byPath map[string]*directoryLock
}{byPath: map[string]*directoryLock{}}

// lockDirectory waits for the directory's lock and returns the function that
// releases it.
func lockDirectory(ctx context.Context, key string) (func(), error) {
	directoryLocks.Lock()
	lock := directoryLocks.byPath[key]
	if lock == nil {
		lock = &directoryLock{held: make(chan struct{}, 1)}
		directoryLocks.byPath[key] = lock
	}
	lock.users++
	directoryLocks.Unlock()

	forget := func() {
		directoryLocks.Lock()
		lock.users--
		if lock.users == 0 {
			delete(directoryLocks.byPath, key)
		}
		directoryLocks.Unlock()
	}
	select {
	case lock.held <- struct{}{}:
		return func() {
			<-lock.held
			forget()
		}, nil
	case <-ctx.Done():
		forget()
		return nil, ctx.Err()
	}
}

// lockUsers is how many runs hold or wait for the directory's lock, which is
// how a test knows a second run is waiting rather than not yet started.
func lockUsers(key string) int {
	directoryLocks.Lock()
	defer directoryLocks.Unlock()
	if lock := directoryLocks.byPath[key]; lock != nil {
		return lock.users
	}
	return 0
}
