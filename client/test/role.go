// Copyright (c) 2019 The Perun Authors. All rights reserved.
// This file is part of go-perun. Use of this source code is governed by a
// MIT-style license that can be found in the LICENSE file.

// Package test contains helpers for testing the client
package test // import "perun.network/go-perun/client/test"

import (
	"context"
	"math/big"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"perun.network/go-perun/channel"
	"perun.network/go-perun/client"
	"perun.network/go-perun/log"
	"perun.network/go-perun/peer"
	"perun.network/go-perun/wallet"
	wallettest "perun.network/go-perun/wallet/test"
)

type (
	// A Role is a client.Client together with a protocol execution path
	Role struct {
		*client.Client
		setup RoleSetup
		// we use the Client as Closer
		timeout      time.Duration
		log          log.Logger
		t            *testing.T
		closeBarrier *sync.WaitGroup // synchronizes all participants before closing
	}

	// RoleSetup contains the injectables for setting up the client
	RoleSetup struct {
		Name     string
		Identity peer.Identity
		Dialer   peer.Dialer
		Listener peer.Listener
		Funder   channel.Funder
		Settler  channel.Settler
		Timeout  time.Duration
	}

	ExecConfig struct {
		PeerAddrs       []peer.Address // must match RoleSetup.Identity of [Alice, Bob]
		Asset           channel.Asset  // single Asset to use in this channel
		InitBals        []*big.Int     // channel deposit of [Alice, Bob]
		AppDef          wallet.Address // AppDef of channel
		NumUpdatesBob   int            // 1st Bob sends updates
		NumUpdatesAlice int            // then 2nd Alice sends updates
		TxAmountBob     *big.Int       // amount that Bob sends per udpate
		TxAmountAlice   *big.Int       // amount that Alice sends per udpate
	}
)

// NewRole creates a client for the given setup and wraps it into a Role.
func MakeRole(setup RoleSetup, propHandler client.ProposalHandler, t *testing.T) Role {
	cl := client.New(setup.Identity, setup.Dialer, propHandler, setup.Funder, setup.Settler)
	return Role{
		Client:  cl,
		setup:   setup,
		timeout: setup.Timeout,
		log:     cl.Log().WithField("role", setup.Name),
		t:       t,
	}
}

// SetCloseBarrier optionally sets a WaitGroup barrier so that all roles can
// synchronize before closing their channels and clients.
// The WaitGroup should be in its initial state, without any Add() calls.
func (r *Role) SetCloseBarrier(wg *sync.WaitGroup) {
	r.closeBarrier = wg
}

func (r *Role) addClose() {
	if r.closeBarrier != nil {
		r.closeBarrier.Add(1)
	}
}

func (r *Role) waitClose() {
	if r.closeBarrier != nil {
		r.closeBarrier.Done()
		r.closeBarrier.Wait()
	}
}

func (r *Role) sendUpdate(ch *client.Channel, update func(*channel.State), desc string) {
	r.log.Debugf("Sending update: %s", desc)
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()

	state := ch.State().Clone()
	update(state)
	state.Version++

	err := ch.Update(ctx, client.ChannelUpdate{
		State:    state,
		ActorIdx: ch.Idx(),
	})
	r.log.Infof("Sent update: %s, err: %v", desc, err)
	assert.NoError(r.t, err)
}

func (r *Role) recvUpdate(upHandler *acceptAllUpHandler, desc string) {
	r.log.Debugf("Receiving update: %s", desc)
	var err error
	select {
	case err = <-upHandler.err:
		r.log.Infof("Received update: %s, err: %v", desc, err)
	case <-time.After(r.timeout):
		r.t.Error("timeout: expected incoming channel update")
	}
	assert.NoError(r.t, err)
}

func (r *Role) settleChan(ch *client.Channel) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	assert.NoError(r.t, ch.Settle(ctx))
}

type (
	// acceptAllPropHandler is a channel proposal handler that accepts all channel
	// requests. It generates a random account for each channel.
	// Each accepted channel is put on the chans go channel.
	acceptAllPropHandler struct {
		chans   chan channelAndError
		log     log.Logger
		rng     *rand.Rand
		timeout time.Duration
	}

	// channelAndError bundles the return parameters of ProposalResponder.Accept
	// to be able to send them over a channel.
	channelAndError struct {
		channel *client.Channel
		err     error
	}
)

func newAcceptAllPropHandler(rng *rand.Rand, timeout time.Duration) *acceptAllPropHandler {
	return &acceptAllPropHandler{
		chans:   make(chan channelAndError),
		rng:     rng,
		timeout: timeout,
		log:     log.Get(), // default logger without fields
	}
}

func (h *acceptAllPropHandler) Handle(req *client.ChannelProposalReq, res *client.ProposalResponder) {
	h.log.Infof("Accepting incoming channel request: %v", req)
	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()

	ch, err := res.Accept(ctx, client.ProposalAcc{
		Participant: wallettest.NewRandomAccount(h.rng),
	})
	h.chans <- channelAndError{ch, err}
}

type acceptAllUpHandler struct {
	log     log.Logger
	timeout time.Duration
	err     chan error
}

func newAcceptAllUpHandler(logger log.Logger, timeout time.Duration) *acceptAllUpHandler {
	return &acceptAllUpHandler{
		log:     logger,
		timeout: timeout,
		err:     make(chan error),
	}
}

func (h *acceptAllUpHandler) Handle(up client.ChannelUpdate, res *client.UpdateResponder) {
	h.log.Infof("Accepting channel update: %v", up)
	ctx, cancel := context.WithTimeout(context.Background(), h.timeout)
	defer cancel()

	h.err <- res.Accept(ctx)
}

func transferBal(state *channel.State, ourIdx channel.Index, amount *big.Int) {
	otherIdx := ourIdx ^ 1
	ourBal := state.Allocation.OfParts[ourIdx][0]
	otherBal := state.Allocation.OfParts[otherIdx][0]
	otherBal.Add(otherBal, amount)
	ourBal.Add(ourBal, amount.Neg(amount))
}
