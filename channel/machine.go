// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of go-perun. Use
// of this source code is governed by a MIT-style license that can be found in
// the LICENSE file.

package channel

import (
	"fmt"

	"github.com/pkg/errors"

	"perun.network/go-perun/log"
	"perun.network/go-perun/wallet"
)

// Index is the type for the number of participants, assets, sub-allocations, actions and alike.
type Index = uint16

type (
	// Phase is a phase of the channel pushdown automaton
	Phase uint8

	// PhaseTransition represents a transition between two phases
	PhaseTransition struct {
		From, To Phase
	}
)

const (
	InitActing Phase = iota
	InitSigning
	Funding
	Acting
	Signing
	Final
	Settled
)

func (p Phase) String() string {
	return [...]string{"InitActing", "InitSigning", "Funding", "Acting", "Signing", "Final", "Settled"}[p]
}

func (t PhaseTransition) String() string {
	return fmt.Sprintf("%v->%v", t.From, t.To)
}

var signingPhases = []Phase{InitSigning, Signing}

// A machine is the channel pushdown automaton that handles phase transitions.
// It checks for correct signatures and valid state transitions.
// machine only contains implementations for the state transitions common to
// both, ActionMachine and StateMachine, that is, AddSig, EnableInit, SetFunded,
// EnableUpdate, EnableFinal and SetSettled.
// The other transitions are specific to the type of machine and are implemented
// individually.
type machine struct {
	phase     Phase
	acc       wallet.Account
	idx       Index
	params    Params
	stagingTX Transaction
	currentTX Transaction
	prevTXs   []Transaction

	// subs contains subscribers to each phase transition
	subs map[Phase]map[string]chan<- PhaseTransition
	// log is a fields logger for this machine
	log log.Logger
}

// newMachine returns a new uninitialized machine for the given parameters.
func newMachine(acc wallet.Account, params Params) (*machine, error) {
	idx := wallet.IndexOfAddr(params.Parts, acc.Address())
	if idx < 0 {
		return nil, errors.New("account not part of participant set")
	}

	return &machine{
		phase:  InitActing,
		acc:    acc,
		idx:    Index(idx),
		params: params,
		subs:   make(map[Phase]map[string]chan<- PhaseTransition),
		log:    log.WithField("ID", params.id),
	}, nil

}

// ID returns the channel id
func (m *machine) ID() ID {
	return m.params.ID()
}

// Account returns the account this channel is using for signing state updates
func (m *machine) Account() wallet.Account {
	return m.acc
}

// Idx returns our index in the channel participants list.
func (m *machine) Idx() Index {
	return m.idx
}

// Params returns the channel parameters
func (m *machine) Params() *Params {
	return &m.params
}

// N returns the number of participants of the channel parameters of this machine.
func (m *machine) N() Index {
	return Index(len(m.params.Parts))
}

// Phase returns the current phase
func (m *machine) Phase() Phase {
	return m.phase
}

// setPhase is internally used to set the phase and notify all subscribers of
// the phase transition.
func (m *machine) setPhase(p Phase) {
	m.log.Tracef("phase transition: %v", PhaseTransition{m.phase, p})
	oldPhase := m.phase
	m.phase = p
	m.notifySubs(oldPhase)
}

// inPhase returns whether phase is in phases.
func inPhase(phase Phase, phases []Phase) bool {
	for _, p := range phases {
		if p == phase {
			return true
		}
	}
	return false
}

// Sig returns the own signature on the currently staged state.
// The signature is caclulated and saved to the staging TX's signature slice
// if it was not calculated before.
// A call to Sig only makes sense in a signing phase.
func (m *machine) Sig() (sig wallet.Sig, err error) {
	if !inPhase(m.phase, signingPhases) {
		return nil, m.error(m.selfTransition(), "can only create own signature in a signing phase")
	}

	if m.stagingTX.Sigs[m.idx] == nil {
		sig, err = Sign(m.acc, &m.params, m.stagingTX.State)
		if err != nil {
			return
		}
		m.stagingTX.Sigs[m.idx] = sig
	} else {
		sig = m.stagingTX.Sigs[m.idx]
	}
	return
}

// State returns the current state.
// Clone the state first if you need to modify it.
func (m *machine) State() *State {
	return m.currentTX.State
}

// SettleReq returns the settlement request for the current channel transaction
// (the current state together with all participants' signatures on it).
func (m *machine) SettleReq() SettleReq {
	return SettleReq{
		Params: &m.params,
		Idx:    m.idx,
		Tx:     m.currentTX,
	}
}

// StagingState returns the staging state. It should usually be called after
// entering a signing phase to get the new staging state, which might have been
// created during Init() or Update() (for ActionApps).
// Clone the state first if you need to modify it.
func (m *machine) StagingState() *State {
	return m.stagingTX.State
}

// AddSig verifies the provided signature of another participant on the staging
// state and if successful adds it to the staged transaction. It also checks
// whether the signature has already been set and in that case errors.
// It should not happen that a signature of the same participant is set twice.
// It is also checked that the current phase is a signing phase.
// If the index is out of bounds, a panic occurs as this is an invalid usage of
// the machine.
func (m *machine) AddSig(idx Index, sig wallet.Sig) error {
	if !inPhase(m.phase, signingPhases) {
		return m.error(m.selfTransition(), "can only add signature in a signing phase")
	}

	if m.stagingTX.Sigs[idx] != nil {
		return errors.Errorf("signature for idx %d already present (ID: %x)", idx, m.params.id)
	}

	if ok, err := Verify(m.params.Parts[idx], &m.params, m.stagingTX.State, sig); err != nil {
		return err
	} else if !ok {
		return errors.Errorf("invalid signature for idx %d (ID: %x)", idx, m.params.id)
	}

	m.stagingTX.Sigs[idx] = sig
	return nil
}

// setStaging sets the given phase and state as staging state.
func (m *machine) setStaging(phase Phase, state *State) {
	m.stagingTX = Transaction{
		State: state,
		Sigs:  make([]wallet.Sig, m.N()),
	}

	m.setPhase(phase)
}

// DiscardUpdate discards the current staging transaction and sets the machine's
// phase back to Acting. This method is useful in the case where a valid update
// request is rejected.
func (m *machine) DiscardUpdate() error {
	if err := m.expect(PhaseTransition{Signing, Acting}); err != nil {
		return err
	}

	m.stagingTX = Transaction{} // clear staging tx
	m.setPhase(Acting)
	return nil
}

// EnableInit promotes the initial staging state to the current funding state.
// A valid phase transition and the existence of all signatures is checked.
func (m *machine) EnableInit() error {
	return m.enableStaged(PhaseTransition{InitSigning, Funding})
}

// EnableUpdate promotes the current staging state to the current state.
// A valid phase transition and the existence of all signatures is checked.
func (m *machine) EnableUpdate() error {
	return m.enableStaged(PhaseTransition{Signing, Acting})
}

// EnableFinal promotes the final staging state to the final current state.
// A valid phase transition and the existence of all signatures is checked.
func (m *machine) EnableFinal() error {
	return m.enableStaged(PhaseTransition{Signing, Final})
}

// enableStaged checks that
//   1. the current phase is `expected.From` and
//   2. all signatures of the staging transactions have been set.
// If successful, the staging transaction is promoted to be the current
// transaction. If not, an error is returned.
func (m *machine) enableStaged(expected PhaseTransition) error {
	if err := m.expect(expected); err != nil {
		return errors.WithMessage(err, "no staging phase")
	}
	if (expected.To == Final) != m.stagingTX.State.IsFinal {
		return m.error(expected, "State.IsFinal and target phase don't match")
	}

	for i, sig := range m.stagingTX.Sigs {
		if sig == nil {
			return m.errorf(expected, "signature %d missing from staging TX", i)
		}
	}

	m.prevTXs = append(m.prevTXs, m.currentTX) // push current to previous
	m.currentTX = m.stagingTX                  // promote staging to current
	m.stagingTX = Transaction{}                // clear staging

	m.setPhase(expected.To)
	return nil
}

// SetFunded tells the state machine that the channel got funded and progresses
// to the Acting phase.
func (m *machine) SetFunded() error {
	if err := m.expect(PhaseTransition{Funding, Acting}); err != nil {
		return err
	}

	m.setPhase(Acting)
	return nil
}

// SetSettled tells the state machine that the final state was settled on the
// blockchain or funding channel and progresses to the Settled state.
func (m *machine) SetSettled() error {
	if err := m.expect(PhaseTransition{Final, Settled}); err != nil {
		return err
	}

	m.setPhase(Settled)
	return nil
}

var validPhaseTransitions = map[PhaseTransition]bool{
	PhaseTransition{InitActing, InitSigning}: true,
	PhaseTransition{InitSigning, Funding}:    true,
	PhaseTransition{Funding, Acting}:         true,
	PhaseTransition{Acting, Signing}:         true,
	PhaseTransition{Signing, Acting}:         true,
	PhaseTransition{Signing, Final}:          true,
	PhaseTransition{Final, Settled}:          true,
}

func (m *machine) expect(tr PhaseTransition) error {
	if m.phase != tr.From {
		return m.error(tr, "not in correct phase")
	}
	if ok := validPhaseTransitions[PhaseTransition{m.phase, tr.To}]; !ok {
		return m.error(tr, "forbidden phase transition")
	}
	return nil
}

// validTransition checks that the transition from the current to the provided
// state is valid. The following checks are run:
// * matching channel ids
// * no transition from final state
// * version increase by 1
// * preservation of balances
// A StateMachine will additionally check the validity of the app-specific
// transition whereas an ActionMachine checks each Action as being valid.
func (m *machine) validTransition(to *State) error {
	if to.ID != m.params.id {
		return errors.New("new state's ID doesn't match")
	}
	if !m.params.App.Def().Equals(to.App.Def()) {
		return errors.New("new state's App dosen't match")
	}

	newError := func(s string) error { return NewStateTransitionError(m.params.id, s) }

	if m.currentTX.IsFinal == true {
		return newError("cannot advance final state")
	}

	if m.currentTX.Version+1 != to.Version {
		return newError("version must increase by one")
	}

	if err := to.Allocation.Valid(); err != nil {
		return newError(fmt.Sprintf("invalid allocation: %v", err))
	}

	if eq, err := equalSum(m.currentTX.Allocation, to.Allocation); err != nil {
		return err
	} else if !eq {
		return newError("allocations must be preserved")
	}

	return nil
}

// Subscribe subscribes go-channel `sub` to phase `phase` under the name `who`.
// If the machine changes into phase `phase`, the phase transition is sent on
// channel `sub`.
// If a subscription for `who` to this phase already exists, it is overwritten.
func (m *machine) Subscribe(phase Phase, who string, sub chan<- PhaseTransition) {
	if m.subs[phase] == nil {
		m.subs[phase] = make(map[string]chan<- PhaseTransition)
	}

	m.subs[phase][who] = sub
}

// notifySubs notifies all subscribers to the current phase that a phase
// transition from the provided phase `from` has happened.
func (m *machine) notifySubs(from Phase) {
	if m.subs[m.phase] == nil {
		// no subscribers
		return
	}

	transition := PhaseTransition{from, m.phase}
	for who, sub := range m.subs[m.phase] {
		m.log.Tracef("phase transition: %v, notifying subscriber %s", transition, who)
		sub <- transition
	}
}

// error constructs a new PhaseTransitionError.
func (m *machine) error(expected PhaseTransition, msg string) error {
	return newPhaseTransitionError(m.params.ID(), m.phase, expected, msg)
}

// error constructs a new PhaseTransitionError.
func (m *machine) errorf(expected PhaseTransition, format string, args ...interface{}) error {
	return newPhaseTransitionErrorf(m.params.ID(), m.phase, expected, format, args...)
}

// selfTransition returns a PhaseTransition from current to current phase.
func (m *machine) selfTransition() PhaseTransition {
	return PhaseTransition{m.phase, m.phase}
}
