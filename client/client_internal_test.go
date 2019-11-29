// Copyright (c) 2019 The Perun Authors. All rights reserved.
// This file is part of go-perun. Use of this source code is governed by a
// MIT-style license that can be found in the LICENSE file.

package client

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	simwallet "perun.network/go-perun/backend/sim/wallet"
	"perun.network/go-perun/peer"
	peertest "perun.network/go-perun/peer/test"
)

func TestClient_getPeers(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	rng := rand.New(rand.NewSource(0xdeadbeef))
	peerAcc0, peerAcc1 := simwallet.NewRandomAccount(rng), simwallet.NewRandomAccount(rng)

	var hub peertest.ConnHub
	// TODO: change in #208
	dialer, _, err := hub.Create(peerAcc0)
	require.NoError(err)
	_, _, err = hub.Create(peerAcc1)
	require.NoError(err)

	// dummy client that only has an id and a registry
	c := &Client{
		id:    simwallet.NewRandomAccount(rng),
		peers: peer.NewRegistry(func(*peer.Peer) {}, dialer),
	}

	ps := c.getPeers(nil)
	assert.Len(ps, 0, "getPeers on nil list should return empty list")
	ps = c.getPeers(make([]peer.Address, 0))
	assert.Len(ps, 0, "getPeers on empty list should return empty list")
	ps = c.getPeers([]peer.Address{c.id.Address()})
	assert.Len(ps, 0, "getPeers on list only containing us should return empty list")
	ps = c.getPeers([]peer.Address{peerAcc0.Address(), c.id.Address()})
	require.Len(ps, 1, "getPeers on [0, us] should return [0]")
	assert.True(ps[0].PerunAddress.Equals(peerAcc0.Address()), "getPeers on [0, us] should return [0]")
	ps = c.getPeers([]peer.Address{c.id.Address(), peerAcc1.Address()})
	require.Len(ps, 1, "getPeers on [us, 1] should return [1]")
	assert.True(ps[0].PerunAddress.Equals(peerAcc1.Address()), "getPeers on [us, 1] should return [1]")
	ps = c.getPeers([]peer.Address{peerAcc0.Address(), peerAcc1.Address()})
	require.Len(ps, 2, "getPeers on [0, 1] should return [0, 1]")
	assert.True(ps[0].PerunAddress.Equals(peerAcc0.Address()), "getPeers on [0, 1] should return [0, 1]")
	assert.True(ps[1].PerunAddress.Equals(peerAcc1.Address()), "getPeers on [0, 1] should return [0, 1]")
	ps = c.getPeers([]peer.Address{peerAcc0.Address(), c.id.Address(), peerAcc1.Address()})
	require.Len(ps, 2, "getPeers on [0, us, 1] should return [0, 1]")
	assert.True(ps[0].PerunAddress.Equals(peerAcc0.Address()), "getPeers on [0, us, 1] should return [0, 1]")
	assert.True(ps[1].PerunAddress.Equals(peerAcc1.Address()), "getPeers on [0, us, 1] should return [0, 1]")
}
