// Copyright (c) 2019 The Perun Authors. All rights reserved.
// This file is part of go-perun. Use of this source code is governed by a
// MIT-style license that can be found in the LICENSE file.

package client

import (
	"context"

	"github.com/pkg/errors"

	"perun.network/go-perun/log"
	"perun.network/go-perun/peer"
	"perun.network/go-perun/pkg/sync/atomic"
	"perun.network/go-perun/wallet"
	wire "perun.network/go-perun/wire/msg"
)

type (
	ProposalHandler interface {
		Handle(*ChannelProposal, *ProposalResponder)
	}

	// ProposalResponder lets the user respond to a channel proposal. If the user
	// wants to accept the proposal, they should call Accept(), otherwise Reject().
	// Only a single function must be called and every further call causes a
	// panic.
	ProposalResponder struct {
		accept chan ctxProposalAcc
		reject chan ctxProposalRej
		err    chan error // return error
		called atomic.Bool
	}

	ProposalAcc struct {
		Participant wallet.Account
		// TODO add Funder
		// TODO add UpdateHandler
	}

	// The following type is only needed to bundle the ctx and res of
	// ProposalResponder.Accept() into a single struct so that they can be sent
	// over a go channel
	ctxProposalAcc struct {
		ctx context.Context
		ProposalAcc
	}

	// The following type is only needed to bundle the ctx and reason of
	// ProposalResponder.Reject() into a single struct so that they can be sent
	// over a go channel
	ctxProposalRej struct {
		ctx    context.Context
		reason string
	}
)

func newProposalResponder() *ProposalResponder {
	return &ProposalResponder{
		accept: make(chan ctxProposalAcc),
		reject: make(chan ctxProposalRej),
		err:    make(chan error, 1),
	}
}

// Accept lets the user signal that they want to accept the channel proposal.
// Returns whether the acceptance message was successfully sent. Panics if the
// proposal was already accepted or rejected.
//
// TODO Add channel controller to return values
func (r *ProposalResponder) Accept(ctx context.Context, res ProposalAcc) error {
	if !r.called.TrySet() {
		log.Panic("multiple calls on proposal responder")
	}
	r.accept <- ctxProposalAcc{ctx, res}
	// TODO return (*Channel, error) when first version of channel controller is present
	return <-r.err
}

// Reject lets the user signal that they reject the channel proposal.
// Returns whether the rejection message was successfully sent. Panics if the
// proposal was already accepted or rejected.
func (r *ProposalResponder) Reject(ctx context.Context, reason string) error {
	if !r.called.TrySet() {
		log.Panic("multiple calls on proposal responder")
	}
	r.reject <- ctxProposalRej{ctx, reason}
	return <-r.err
}

// This function is called during the setup of new peers by the registry. The
// passed peer is not yet receiving any messages, thus, subscription is
// race-free. After the function returns, the peer starts receiving messages.
func (c *Client) subChannelProposals(p *peer.Peer) {
	proposalReceiver, err := p.Subscribe(
		func(m wire.Msg) bool { return m.Type() == wire.ChannelProposal },
	)
	if err != nil {
		c.logPeer(p).Errorf("failed to subscribe to channel proposals on new peer")
		return
	}

	// Aborts the proposal handler loop when the Client is closed.
	go func() { <-c.quit; proposalReceiver.Close() }()

	// proposal handler loop.
	go func() {
		for {
			_p, m := proposalReceiver.Next(context.Background())
			if _p == nil {
				c.logPeer(p).Debugf("proposal subscription closed")
				return
			}
			proposal := m.(*ChannelProposal) // safe because that's the predicate
			go c.handleChannelProposal(p, proposal)
		}
	}()
}

func (c *Client) handleChannelProposal(p *peer.Peer, proposal *ChannelProposal) {
	if err := proposal.Valid(); err != nil {
		c.logPeer(p).Debugf("received invalid channel proposal")
		return
	}

	responder := newProposalResponder()
	go c.propHandler.Handle(proposal, responder)

	// wait for user response
	select {
	case acc := <-responder.accept:
		if acc.Participant == nil {
			c.logPeer(p).Error("user returned nil Participant in ProposalAcc")
			responder.err <- errors.New("nil Participant in ProposalAcc")
			return
		}

		msgAccept := &ChannelProposalAcc{
			SessID:          proposal.SessID(),
			ParticipantAddr: acc.Participant.Address(),
		}
		if err := p.Send(acc.ctx, msgAccept); err != nil {
			c.logPeer(p).Warn("error sending proposal acceptance")
			responder.err <- err
			return
		}
		// TODO setup channel controller and start it

	case rej := <-responder.reject:
		msgReject := &ChannelProposalRej{
			SessID: proposal.SessID(),
			Reason: rej.reason,
		}
		if err := p.Send(rej.ctx, msgReject); err != nil {
			c.logPeer(p).Warn("error sending proposal rejection")
			responder.err <- err
			return
		}
	}
	responder.err <- nil
}
