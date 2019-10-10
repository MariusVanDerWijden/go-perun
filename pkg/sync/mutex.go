// Copyright (c) 2019 The Perun Authors. All rights reserved.
// This file is part of go-perun. Use of this source code is governed by a
// MIT-style license that can be found in the LICENSE file.

// Package sync contains a mutex that can be used in a select statement.
package sync // import "perun.network/go-perun/pkg/sync"

import (
	"context"
	"sync"

	"perun.network/go-perun/log"
)

// Mutex is a replacement of the standard mutex type.
// It supports the additional TryLock() function, as well as a variant that can
// be used in a select statement.
type Mutex struct {
	locked chan struct{} // The internal mutex is modelled by a channel.
	once   sync.Once     // Needed to initialize the mutex on its first use.
}

// initOnce initialises the mutex if it has not been initialised yet.
func (m *Mutex) initOnce() {
	m.once.Do(func() { m.locked = make(chan struct{}, 1) })
}

// Lock blockingly locks the mutex.
func (m *Mutex) Lock() {
	m.initOnce()
	m.locked <- struct{}{}
}

// TryLock tries to lock the mutex within a timeout provided by a context.
// For an instant timeout, a nil context has to be passed. Returns whether the
// mutex was acquired.
func (m *Mutex) TryLock(ctx context.Context) bool {
	m.initOnce()

	if ctx != nil {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		select {
		case m.locked <- struct{}{}:
			return true
		case <-ctx.Done():
			return false
		}
	}

	select {
	case m.locked <- struct{}{}:
		return true
	default:
		return false
	}
}

// Unlock unlocks the mutex.
// If the mutex was not locked, panics.
func (m *Mutex) Unlock() {
	select {
	case <-m.locked:
	default:
		log.Fatal("tried to unlock unlocked mutex")
	}
}
