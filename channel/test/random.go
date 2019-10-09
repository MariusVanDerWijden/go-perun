// Copyright (c) 2019 The Perun Authors. All rights reserved.
// This file is part of go-perun. Use of this source code is governed by a
// MIT-style license that can be found in the LICENSE file.

package test // import "perun.network/go-perun/channel/test"

import (
	"log"
	"math/big"
	"math/rand"

	"perun.network/go-perun/channel"
	"perun.network/go-perun/wallet"
	wallettest "perun.network/go-perun/wallet/test"
)

type Backend interface {
	NewRandomAsset(*rand.Rand) channel.Asset
	NewRandomApp(*rand.Rand) channel.App
	NewRandomData(*rand.Rand) channel.Data
}

var backend Backend = &TestBackend{}

func SetBackend(b Backend) {
	backend = b
}

func NewRandomAllocation(rng *rand.Rand, params *channel.Params) *channel.Allocation {
	assets := make([]channel.Asset, rng.Int31n(9)+2)
	for i := 0; i < len(assets); i++ {
		assets[i] = NewRandomAsset(rng)
	}

	ofparts := make([][]channel.Bal, len(params.Parts))
	for i := 0; i < len(ofparts); i++ {
		ofparts[i] = NewRandomBals(rng, len(assets))
	}

	locked := make([]channel.SubAlloc, rng.Int31n(9)+2)
	for i := 0; i < len(locked); i++ {
		locked[i] = *NewRandomSubAlloc(rng, len(assets))
	}

	return &channel.Allocation{Assets: assets, OfParts: ofparts, Locked: locked}
}

func NewRandomSubAlloc(rng *rand.Rand, size int) *channel.SubAlloc {
	return &channel.SubAlloc{ID: NewRandomChannelID(rng), Bals: NewRandomBals(rng, size)}
}

func NewRandomParams(rng *rand.Rand, app channel.App) *channel.Params {
	var challengeDuration = rng.Uint64()
	parts := make([]wallet.Address, rng.Int31n(5)+2)
	for i := 0; i < len(parts); i++ {
		parts[i] = wallettest.NewRandomAddress(rng)
	}
	nonce := big.NewInt(int64(rng.Uint32()))

	params, err := channel.NewParams(challengeDuration, parts, app, nonce)
	if err != nil {
		log.Panic("NewParams failed ", err)
	}
	return params
}

func NewRandomState(rng *rand.Rand, p *channel.Params) *channel.State {
	return &channel.State{
		ID:         p.ID(),
		Version:    rng.Uint64(),
		Allocation: *NewRandomAllocation(rng, p),
		Data:       NewRandomData(rng),
		IsFinal:    (rng.Int31n(2) == 0),
	}
}

func NewRandomApp(rng *rand.Rand) channel.App {
	return backend.NewRandomApp(rng)
}

func NewRandomData(rng *rand.Rand) channel.Data {
	return backend.NewRandomData(rng)
}

func NewRandomAsset(rng *rand.Rand) channel.Asset {
	return backend.NewRandomAsset(rng)
}

func NewRandomChannelID(rng *rand.Rand) (id channel.ID) {
	if _, err := rng.Read(id[:]); err != nil {
		log.Panic("could not read from rng")
	}
	return
}

func NewRandomBal(rng *rand.Rand) channel.Bal {
	return channel.Bal(big.NewInt(rng.Int63()))
}

func NewRandomBals(rng *rand.Rand, size int) []channel.Bal {
	bals := make([]channel.Bal, size)
	for i := 0; i < size; i++ {
		bals[i] = NewRandomBal(rng)
	}
	return bals
}
