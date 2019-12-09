// Copyright (c) 2019 The Perun Authors. All rights reserved.
// This file is part of go-perun. Use of this source code is governed by a
// MIT-style license that can be found in the LICENSE file.

// Package test contains helpers for testing the client
package test // import "perun.network/go-perun/client/test"

import (
	"context"
	"fmt"
	"math/big"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"

	"perun.network/go-perun/channel"
	"perun.network/go-perun/client"
	wallettest "perun.network/go-perun/wallet/test"
)

type Alice struct {
	Role
	rng *rand.Rand
}

func NewAlice(setup RoleSetup, t *testing.T) *Alice {
	rng := rand.New(rand.NewSource(0x471CE))
	propHandler := newAcceptAllPropHandler(rng, setup.Timeout)
	role := &Alice{
		Role: MakeRole(setup, propHandler, t),
		rng:  rng,
	}

	propHandler.log = role.Role.log
	return role
}

func (r *Alice) Execute(cfg ExecConfig) {
	assert := assert.New(r.t)
	// We don't start the proposal listener because Alice only receives proposals

	r.addClose()

	initBals := &channel.Allocation{
		Assets: []channel.Asset{cfg.Asset},
		OfParts: [][]*big.Int{
			[]*big.Int{cfg.InitBals[0]}, // Alice
			[]*big.Int{cfg.InitBals[1]}, // Bob
		},
	}
	prop := &client.ChannelProposal{
		ChallengeDuration: 10,           // 10 sec
		Nonce:             new(big.Int), // nonce 0
		Account:           wallettest.NewRandomAccount(r.rng),
		AppDef:            cfg.AppDef,
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
	assert.NoError(err)
	assert.NotNil(ch)
	if err != nil {
		return
	}
	r.log.Info("New Channel opened: %v", ch)
	idx := ch.Idx()

	// start update handler
	upHandler := newAcceptAllUpHandler(r.log, r.timeout)
	listenUpDone := make(chan struct{})
	go func() {
		defer close(listenUpDone)
		r.log.Info("Starting update listener")
		ch.ListenUpdates(upHandler)
		r.log.Debug("Update listener returned.")
	}()
	defer func() {
		r.log.Debug("Waiting for update listener to return...")
		<-listenUpDone
	}()

	// 1st Alice receives some updates from Bob
	for i := 0; i < cfg.NumUpdatesBob; i++ {
		r.recvUpdate(upHandler, fmt.Sprintf("#%d", i))
	}

	// 2nd Alice sends some updates to Bob
	for i := 0; i < cfg.NumUpdatesAlice; i++ {
		r.sendUpdate(ch,
			func(state *channel.State) {
				transferBal(state, idx, cfg.TxAmountAlice)
			},
			fmt.Sprintf("#%d", i))
	}

	// 3rd Alice receives final state from Bob
	r.recvUpdate(upHandler, "final")
	assert.True(ch.State().IsFinal)

	// 4th Settle channel
	r.settleChan(ch)

	// finally, close the channel and client
	r.waitClose()
	assert.NoError(ch.Close())
	assert.NoError(r.Close())
}
