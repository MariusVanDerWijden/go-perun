// Copyright (c) 2019 The Perun Authors. All rights reserved.
// This file is part of go-perun. Use of this source code is governed by a
// MIT-style license that can be found in the LICENSE file.

// Package test contains helpers for testing the client
package test // import "perun.network/go-perun/client/test"

import (
	"context"
	"math/big"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	//"perun.network/go-perun/channel"
	"perun.network/go-perun/channel"
	channeltest "perun.network/go-perun/channel/test"
	"perun.network/go-perun/client"
	"perun.network/go-perun/log"
	wallettest "perun.network/go-perun/wallet/test"
	//"perun.network/go-perun/peer"
)

type Alice struct {
	Role
	log log.Logger
	rng *rand.Rand
}

func NewAlice(setup RoleSetup) *Alice {
	rng := rand.New(rand.NewSource(0x471CE))
	propHandler := newAcceptAllPropHandler(rng, setup.Timeout)
	alice := &Alice{
		Role: MakeRole(setup, propHandler),
		rng:  rng,
	}

	// append role field to client logger
	alice.log = alice.Log().WithField("role", "Alice")
	propHandler.log = alice.log
	return alice
}

func (r *Alice) Execute(t *testing.T, cfg ExecConfig) {
	//go func() {
	//r.log.Info("Alice: starting peer listener")
	//r.Listen(r.setup.Listener)
	//}()

	initBals := &channel.Allocation{
		Assets: []channel.Asset{channeltest.NewRandomAsset(r.rng)},
		OfParts: [][]*big.Int{
			[]*big.Int{cfg.InitBals[0]}, // Alice
			[]*big.Int{cfg.InitBals[1]}, // Bob
		},
	}
	prop := &client.ChannelProposal{
		ChallengeDuration: 10,           // 10 sec
		Nonce:             new(big.Int), // nonce 0
		Account:           wallettest.NewRandomAccount(r.rng),
		AppDef:            wallettest.NewRandomAddress(r.rng), // every address is a valid MockApp
		InitData:          channel.NewMockOp(channel.OpValid),
		InitBals:          initBals,
		PeerAddrs:         cfg.PeerAddrs,
	}

	var ch *client.Channel
	var err error
	// send channel proposal
	func() {
		ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
		defer cancel()
		ch, err = r.ProposeChannel(ctx, prop)
	}()
	require.NoError(t, err)
	require.NotNil(t, ch)
	r.log.Info("New Channel opened: %v", ch)
	idx := ch.Idx()

	// 1st Alice receives some updates from Bob
	upHandler := newAcceptAllUpHandler(r.log, r.timeout)
	t.Run("Alice: Channel(w/Bob) update request listener", func(t *testing.T) {
		t.Parallel()
		ch.ListenUpdates(upHandler)
	})

	for i := 0; i < cfg.NumUpdatesBob; i++ {
		var err error
		select {
		case err = <-upHandler.err:
		case <-time.After(r.timeout):
			t.Fatal("expected incoming channel updates from Bob")
		}
		assert.NoError(t, err)
	}

	// 2nd Alice sends some updates to Bob
	for i := 0; i < cfg.NumUpdatesAlice; i++ {
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
			defer cancel()
			state := ch.State().Clone()
			transferBal(state, idx, cfg.TxAmountAlice)
			err := ch.Update(ctx, client.ChannelUpdate{
				State:    state,
				ActorIdx: idx,
			})
			assert.NoError(t, err)
		}()
	}
}
