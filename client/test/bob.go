// Copyright (c) 2019 The Perun Authors. All rights reserved.
// This file is part of go-perun. Use of this source code is governed by a
// MIT-style license that can be found in the LICENSE file.

// Package test contains helpers for testing the client
package test // import "perun.network/go-perun/client/test"

import (
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"perun.network/go-perun/client"
	"perun.network/go-perun/log"
)

type Bob struct {
	Role
	log         log.Logger
	propHandler *acceptAllPropHandler
}

func NewBob(setup RoleSetup) *Bob {
	rng := rand.New(rand.NewSource(0xB0B))
	propHandler := newAcceptAllPropHandler(rng, setup.Timeout)
	role := &Bob{
		Role:        MakeRole(setup, propHandler),
		propHandler: propHandler,
	}

	// append role field to client logger
	role.log = role.Log().WithField("role", "Bob")
	propHandler.log = role.log
	return role
}

func (r *Bob) Execute(t *testing.T, cfg ExecConfig) {
	assert := assert.New(t)
	var listenWg sync.WaitGroup

	listenWg.Add(2)
	go func() {
		defer listenWg.Done()
		r.log.Info("Starting peer listener.")
		r.Listen(r.setup.Listener)
		r.log.Debug("Peer listener returned.")
	}()

	// receive one accepted proposal
	var chErr channelAndError
	select {
	case chErr = <-r.propHandler.chans:
	case <-time.After(r.timeout):
		t.Fatal("expected incoming channel proposal from Alice")
	}
	assert.NoError(chErr.err)
	assert.NotNil(chErr.channel)
	if chErr.err != nil {
		return
	}
	ch := chErr.channel
	r.log.Info("New Channel opened: %v", ch)
	idx := ch.Idx()

	upHandler := newAcceptAllUpHandler(r.log, r.timeout)
	go func() {
		defer listenWg.Done()
		r.log.Info("Starting update listener.")
		ch.ListenUpdates(upHandler)
		r.log.Debug("Update listener returned.")
	}()
	defer func() {
		r.log.Debug("Waiting for listeners to return...")
		listenWg.Wait()
	}()

	// 1st Bob sends some updates to Alice
	for i := 0; i < cfg.NumUpdatesBob; i++ {
		func() {
			r.log.Infof("Sending update %d", i)
			ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
			defer cancel()
			state := ch.State().Clone()
			transferBal(state, idx, cfg.TxAmountBob)
			err := ch.Update(ctx, client.ChannelUpdate{
				State:    state,
				ActorIdx: idx,
			})
			assert.NoError(err)
		}()
	}

	// 2nd Bob receives some updates from Alice
	for i := 0; i < cfg.NumUpdatesAlice; i++ {
		var err error
		select {
		case err = <-upHandler.err:
			r.log.Infof("Received update %d", i)
		case <-time.After(r.timeout):
			t.Fatal("expected incoming channel updates from Alice")
		}
		assert.NoError(err)
	}

	time.Sleep(100 * time.Millisecond) // wait for channel updates to finish
	// finally, close the channel and client
	assert.NoError(ch.Close())
	assert.NoError(r.Close())
}
