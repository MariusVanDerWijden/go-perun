// Copyright (c) 2019 The Perun Authors. All rights reserved.
// This file is part of go-perun. Use of this source code is governed by a
// MIT-style license that can be found in the LICENSE file.

// Package test contains helpers for testing the client
package test // import "perun.network/go-perun/client/test"

import (
	"context"
	"math/big"
	"math/rand"
	"time"

	"perun.network/go-perun/channel"
	"perun.network/go-perun/client"
	"perun.network/go-perun/log"
	"perun.network/go-perun/peer"
	wallettest "perun.network/go-perun/wallet/test"
)

type (
	// A Role is a client.Client together with a protocol execution path
	Role struct {
		*client.Client
		setup RoleSetup
		// we use the Client as Closer
		timeout time.Duration
	}

	// RoleSetup contains the injectables for setting up the client
	RoleSetup struct {
		Identity peer.Identity
		Dialer   peer.Dialer
		Listener peer.Listener
		Funder   channel.Funder
		Settler  channel.Settler
		Timeout  time.Duration
	}

	ExecConfig struct {
		PeerAddrs       []peer.Address // must match RoleSetup.Identity of [Alice, Bob]
		InitBals        []*big.Int     // channel deposit of [Alice, Bob]
		NumUpdatesBob   int            // 1st Bob sends updates
		NumUpdatesAlice int            // then 2nd Alice sends updates
		TxAmountBob     *big.Int       // amount that Bob sends per udpate
		TxAmountAlice   *big.Int       // amount that Alice sends per udpate
	}
)

// NewRole creates a client for the given setup and wraps it into a Role.
func MakeRole(setup RoleSetup, propHandler client.ProposalHandler) Role {
	cl := client.New(setup.Identity, setup.Dialer, propHandler, setup.Funder)
	return Role{
		Client:  cl,
		setup:   setup,
		timeout: setup.Timeout,
	}
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
	otherIdx := (ourIdx + 1) % 2
	ourBal := state.Allocation.OfParts[ourIdx][0]
	otherBal := state.Allocation.OfParts[otherIdx][0]
	otherBal.Add(otherBal, amount)
	ourBal.Add(ourBal, amount.Neg(amount))
	state.Version++
}
