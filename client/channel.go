// Copyright (c) 2019 The Perun Authors. All rights reserved.
// This file is part of go-perun. Use of this source code is governed by a
// MIT-style license that can be found in the LICENSE file.

package client

import (
	"context"
	"sync"

	"github.com/pkg/errors"

	"perun.network/go-perun/channel"
	"perun.network/go-perun/log"
	"perun.network/go-perun/peer"
	perunsync "perun.network/go-perun/pkg/sync"
	"perun.network/go-perun/wallet"
	wire "perun.network/go-perun/wire/msg"
)

// Channel is the channel controller, progressing the channel state machine and
// executing the channel update and dispute protocols.
type Channel struct {
	perunsync.Closer
	log log.Logger

	conn    channelConn
	machine channel.StateMachine
	machMtx sync.RWMutex
}

func newChannel(acc wallet.Account, peers []*peer.Peer, params channel.Params) (*Channel, error) {
	machine, err := channel.NewStateMachine(acc, params)
	if err != nil {
		return nil, errors.WithMessage(err, "creating state machine")
	}

	// bundle peers into channel connection
	conn, err := newChannelConn(params.ID(), peers, machine.Idx())
	if err != nil {
		return nil, errors.WithMessagef(err, "setting up channel connection")
	}

	return &Channel{
		log:     log.WithField("channel", params.ID), // default to global field logger
		conn:    *conn,
		machine: *machine,
	}, nil
}

func (c *Channel) setLogger(l log.Logger) {
	c.log = l
}

func (c *Channel) logPeer(idx channel.Index) log.Logger {
	return c.log.WithField("peerIdx", idx)
}

func (c *Channel) ID() channel.ID {
	return c.machine.ID()
}

// init brings the state machine into the InitSigning phase. It is not callable
// by the user since the Client initializes the channel controller.
func (c *Channel) init(initBals *channel.Allocation, initData channel.Data) error {
	c.machMtx.Lock()
	defer c.machMtx.Unlock()
	return c.machine.Init(*initBals, initData)
}

// A channelConn bundles a peer receiver and broadcaster. It is an abstraction
// over a set of peers. Peers are translated into their index in the channel.
type channelConn struct {
	r       *peer.Receiver
	b       *peer.Broadcaster
	peerIdx map[*peer.Peer]channel.Index
}

func (c *Channel) initExchangeSigsAndEnable(ctx context.Context) error {
	sig, err := c.machine.Sig()
	if err != nil {
		return err
	}

	send := make(chan error)
	go func() {
		send <- c.conn.send(ctx, &msgChannelUpdateAcc{
			ChannelID: c.ID(),
			Version:   0,
			Sig:       sig,
		})
	}()

	pidx, cm := c.conn.recv(ctx) // ignore index in 2PC
	acc, ok := cm.(*msgChannelUpdateAcc)
	if !ok {
		return errors.Errorf(
			"received unexpected message type (%T) from peer",
			cm)
	}
	if acc.Version != 0 {
		return errors.Errorf(
			"received signature on unexpected version %d from peer",
			acc.Version)
	}

	if err := c.machine.AddSig(pidx, acc.Sig); err != nil {
		return err
	}
	if err := c.machine.EnableInit(); err != nil {
		return err
	}

	return errors.WithMessage(<-send, "sending initial signature")
}

// newChannelConn creates a new channel connection for the given channel ID. It
// subscribes on all peers to all messages regarding this channel. The order of
// the peers is important: it must match their position in the channel
// participant slice, or one less if their index is above our index, since we
// are not part of the peer slice.
func newChannelConn(id channel.ID, peers []*peer.Peer, idx channel.Index) (*channelConn, error) {
	forThisChannel := func(m wire.Msg) bool {
		cm, ok := m.(ChannelMsg)
		if !ok {
			return false
		}
		return cm.ID() == id
	}

	rec := peer.NewReceiver()
	peerIdx := make(map[*peer.Peer]channel.Index)
	for i, peer := range peers {
		i := channel.Index(i)
		peerIdx[peer] = i
		// We are not in the peer list, so the peer index is increased after our index
		if i >= idx {
			peerIdx[peer]++
		}
		if err := rec.Subscribe(peer, forThisChannel); err != nil {
			return nil, errors.WithMessagef(err, "subscribing peer[%d] (%v)", i, peer)
		}
	}

	return &channelConn{
		r:       rec,
		b:       peer.NewBroadcaster(peers),
		peerIdx: peerIdx,
	}, nil
}

// send broadcasts the message to all channel participants
func (c *channelConn) send(ctx context.Context, msg wire.Msg) error {
	return c.b.Send(ctx, msg)
}

func (c *channelConn) recv(ctx context.Context) (channel.Index, ChannelMsg) {
	peer, msg := c.r.Next(ctx)
	idx, ok := c.peerIdx[peer]
	if !ok {
		log.Panicf("channel connection received message from unknown peer %v", peer)
	}
	return idx, msg.(ChannelMsg) // safe by the predicate
}

func (c *channelConn) close() {
	c.r.Close()
}