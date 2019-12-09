// Copyright (c) 2019 The Perun Authors. All rights reserved.
// This file is part of go-perun. Use of this source code is governed by a
// MIT-style license that can be found in the LICENSE file.

// Package test contains helpers for testing the client
package test // import "perun.network/go-perun/client/test"

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"perun.network/go-perun/channel"
)

type Bob struct {
	Role
	propHandler *acceptAllPropHandler
}

func NewBob(setup RoleSetup, t *testing.T) *Bob {
	rng := rand.New(rand.NewSource(0xB0B))
	propHandler := newAcceptAllPropHandler(rng, setup.Timeout)
	role := &Bob{
		Role:        MakeRole(setup, propHandler, t),
		propHandler: propHandler,
	}

	propHandler.log = role.Role.log
	return role
}

func (r *Bob) Execute(cfg ExecConfig) {
	assert := assert.New(r.t)

	r.addClose()
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
		r.t.Fatal("expected incoming channel proposal from Alice")
	}
	assert.NoError(chErr.err)
	assert.NotNil(chErr.channel)
	if chErr.err != nil {
		return
	}
	ch := chErr.channel
	r.log.Info("New Channel opened: %v", ch)
	idx := ch.Idx()

	// start update handler
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
		r.sendUpdate(ch,
			func(state *channel.State) {
				transferBal(state, idx, cfg.TxAmountBob)
			},
			fmt.Sprintf("#%d", i))
	}

	// 2nd Bob receives some updates from Alice
	for i := 0; i < cfg.NumUpdatesAlice; i++ {
		r.recvUpdate(upHandler, fmt.Sprintf("#%d", i))
	}

	// 3rd Bob sends a final state
	r.sendUpdate(ch, func(state *channel.State) {
		state.IsFinal = true
	}, "final")

	// 4th Settle channel
	r.settleChan(ch)

	// finally, close the channel and client
	r.waitClose()
	assert.NoError(ch.Close())
	assert.NoError(r.Close())
}
