// Copyright (c) 2019 The Perun Authors. All rights reserved.
// This file is part of go-perun. Use of this source code is governed by a
// MIT-style license that can be found in the LICENSE file.

package client

import (
	"context"

	"github.com/pkg/errors"

	"perun.network/go-perun/channel"
	"perun.network/go-perun/log"
	"perun.network/go-perun/pkg/sync/atomic"
)

type (
	// ChannelUpdate is a channel update proposal.
	ChannelUpdate struct {
		// State is the proposed new state.
		State *channel.State
		// ActorIdx is the actor causing the new state.  It does not need to
		// coincide with the sender of the request.
		ActorIdx uint16
	}

	UpdateHandler interface {
		Handle(ChannelUpdate, *UpdateResponder)
	}

	UpdateResponder struct {
		accept chan context.Context
		reject chan ctxUpdateRej
		err    chan error // return error
		called atomic.Bool
	}

	// The following type is only needed to bundle the ctx and channel update
	// rejection of UpdateResponder.Reject() into a single struct so that they can
	// be sent over a channel
	ctxUpdateRej struct {
		ctx    context.Context
		reason string
	}
)

func newUpdateResponder() *UpdateResponder {
	return &UpdateResponder{
		accept: make(chan context.Context),
		reject: make(chan ctxUpdateRej),
		err:    make(chan error, 1),
	}
}

// Accept lets the user signal that they want to accept the channel update.
func (r *UpdateResponder) Accept(ctx context.Context) error {
	if !r.called.TrySet() {
		log.Panic("multiple calls on channel update responder")
	}
	r.accept <- ctx
	return <-r.err
}

// Reject lets the user signal that they reject the channel update.
func (r *UpdateResponder) Reject(ctx context.Context, reason string) error {
	if !r.called.TrySet() {
		log.Panic("multiple calls on channel update responder")
	}
	r.reject <- ctxUpdateRej{ctx, reason}
	return <-r.err
}

func (c *Channel) Update(ctx context.Context, up ChannelUpdate) error {
	if err := c.validTwoPartyUpdate(up, c.machine.Idx()); err != nil {
		return err
	}

	c.machMtx.Lock() // lock machine while update is in progress
	defer c.machMtx.Unlock()

	if err := c.machine.Update(up.State, up.ActorIdx); err != nil {
		return errors.WithMessage(err, "updating machine")
	}
	sig, err := c.machine.Sig()
	if err != nil {
		if derr := c.machine.DiscardUpdate(); derr != nil {
			// this should be impossible
			return errors.WithMessagef(derr,
				"signing failed: %v, then discarding update failed", err)
		}
		return errors.WithMessage(err, "signing updated state")
	}

	msgUpAcc := &msgChannelUpdateAcc{
		ChannelID: c.ID(),
		Version:   up.State.Version,
		Sig:       sig,
	}
	return c.conn.send(ctx, msgUpAcc)

	// TODO: receive update c.conn.recvUpdateRes(ctx, version)
	// - on Accept, AddSig and EnableUpdate
	// - on Reject, DiscardUpdate
	//if err := c.machine.AddSig(pidx, acc.Sig); err != nil {
	//return errors.WithMessage(err, "adding peer signature")
	//}
}

// validTwoPartyUpdate performs additional protocol-dependent checks on the
// proposed update that go beyond the machine's checks:
// * actor and signer must be the same
// * no locked sub-allocations
func (c *Channel) validTwoPartyUpdate(up ChannelUpdate, sigIdx channel.Index) error {
	if up.ActorIdx != sigIdx {
		return errors.Errorf(
			"Currently, only update proposals with the proposing peer as actor are allowed.")
	}
	if len(up.State.Locked) > 0 {
		return errors.New("no locked sub-allocations allowed")
	}
	return nil
}
