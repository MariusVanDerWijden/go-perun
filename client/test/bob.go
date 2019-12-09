// Copyright (c) 2019 The Perun Authors. All rights reserved.
// This file is part of go-perun. Use of this source code is governed by a
// MIT-style license that can be found in the LICENSE file.

// Package test contains helpers for testing the client
package test // import "perun.network/go-perun/client/test"

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	//"perun.network/go-perun/channel"
	"perun.network/go-perun/client"
	"perun.network/go-perun/log"
	//"perun.network/go-perun/peer"
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
	go func() {
		r.log.Info("Bob: starting peer listener")
		r.Listen(r.setup.Listener)
	}()

	// receive one accepted proposal
	var chErr channelAndError
	select {
	case chErr = <-r.propHandler.chans:
	case <-time.After(r.timeout):
		t.Fatal("expected incoming channel proposal from Alice")
	}
	require.NoError(t, chErr.err)
	require.NotNil(t, chErr.channel)
	ch := chErr.channel
	r.log.Info("New Channel opened: %v", ch)
	idx := ch.Idx()

	// 1st Bob sends some updates to Alice
	for i := 0; i < cfg.NumUpdatesBob; i++ {
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
			defer cancel()
			state := ch.State().Clone()
			transferBal(state, idx, cfg.TxAmountBob)
			err := ch.Update(ctx, client.ChannelUpdate{
				State:    state,
				ActorIdx: idx,
			})
			assert.NoError(t, err)
		}()
	}

	// 2nd Bob receives some updates from Alice
	upHandler := newAcceptAllUpHandler(r.log, r.timeout)
	t.Run("Bob: Channel(w/Alice) update request listener", func(t *testing.T) {
		t.Parallel()
		ch.ListenUpdates(upHandler)
	})

	for i := 0; i < cfg.NumUpdatesAlice; i++ {
		var err error
		select {
		case err = <-upHandler.err:
		case <-time.After(r.timeout):
			t.Fatal("expected incoming channel updates from Alice")
		}
		assert.NoError(t, err)
	}
}
