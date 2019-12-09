// Copyright (c) 2019 The Perun Authors. All rights reserved.
// This file is part of go-perun. Use of this source code is governed by a
// MIT-style license that can be found in the LICENSE file.

package client_test

import (
	"context"
	"math/big"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	_ "perun.network/go-perun/backend/sim/channel" // backend init
	_ "perun.network/go-perun/backend/sim/wallet"  // backend init
	"perun.network/go-perun/channel"
	clienttest "perun.network/go-perun/client/test"
	"perun.network/go-perun/log"
	plogrus "perun.network/go-perun/log/logrus"
	"perun.network/go-perun/peer"
	peertest "perun.network/go-perun/peer/test"
	wallettest "perun.network/go-perun/wallet/test"
)

func init() {
	logger := logrus.New()
	logger.SetLevel(logrus.TraceLevel)
	logger.SetFormatter(&logrus.TextFormatter{
		ForceColors: true,
	})
	log.Set(plogrus.FromLogrus(logger))
}

var defaultTimeout = 10000 * time.Second

func TestHappyAliceBob(t *testing.T) {
	log.Info("Starting happy test")
	rng := rand.New(rand.NewSource(0x1337))
	var hub peertest.ConnHub

	aliceAcc := wallettest.NewRandomAccount(rng)
	bobAcc := wallettest.NewRandomAccount(rng)

	setupAlice := clienttest.RoleSetup{
		Identity: aliceAcc,
		Dialer:   hub.NewDialer(),
		Listener: hub.NewListener(aliceAcc.Address()),
		Funder:   &logFunder{log.WithField("role", "Alice")},
		Settler:  nil, // TODO
		Timeout:  defaultTimeout,
	}

	setupBob := clienttest.RoleSetup{
		Identity: bobAcc,
		Dialer:   hub.NewDialer(),
		Listener: hub.NewListener(bobAcc.Address()),
		Funder:   &logFunder{log.WithField("role", "Bob")},
		Settler:  nil, // TODO
		Timeout:  defaultTimeout,
	}

	execConfig := clienttest.ExecConfig{
		PeerAddrs:       []peer.Address{aliceAcc.Address(), bobAcc.Address()},
		InitBals:        []*big.Int{big.NewInt(100), big.NewInt(100)},
		NumUpdatesBob:   2,
		NumUpdatesAlice: 2,
		TxAmountBob:     big.NewInt(5),
		TxAmountAlice:   big.NewInt(3),
	}

	alice := clienttest.NewAlice(setupAlice)
	bob := clienttest.NewBob(setupBob)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		log.Info("Starting Alice.Execute")
		alice.Execute(t, execConfig)
	}()

	go func() {
		defer wg.Done()
		log.Info("Starting Bob.Execute")
		bob.Execute(t, execConfig)
	}()

	wg.Wait()
	log.Info("Happy test done")
}

type logFunder struct {
	log log.Logger
}

func (f *logFunder) Fund(_ context.Context, req channel.FundingReq) error {
	f.log.Infof("Funding: %v", req)
	return nil
}
