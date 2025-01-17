// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of go-perun. Use
// of this source code is governed by a MIT-style license that can be found in
// the LICENSE file.

package peer

import (
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"perun.network/go-perun/pkg/sync/atomic"
	"perun.network/go-perun/pkg/test"
	wallettest "perun.network/go-perun/wallet/test"
	"perun.network/go-perun/wire/msg"
)

var _ Dialer = (*mockDialer)(nil)

type mockDialer struct {
	dial   chan Conn
	mutex  sync.RWMutex
	closed atomic.Bool
}

func (d *mockDialer) Close() error {
	if !d.closed.TrySet() {
		return errors.New("dialer already closed")
	}
	close(d.dial)
	return nil
}

func (d *mockDialer) Dial(ctx context.Context, addr Address) (Conn, error) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	select {
	case <-ctx.Done():
		return nil, errors.New("aborted manually")
	case conn := <-d.dial:
		if conn != nil {
			return conn, nil
		} else {
			return nil, errors.New("dialer closed")
		}
	}
}

func (d *mockDialer) isClosed() bool {
	return d.closed.IsSet()
}

func (d *mockDialer) put(conn Conn) {
	d.dial <- conn
}

func newMockDialer() *mockDialer {
	return &mockDialer{dial: make(chan Conn)}
}

var _ Listener = (*mockListener)(nil)

type mockListener struct {
	dialer mockDialer
}

func (l *mockListener) Accept() (Conn, error) {
	return l.dialer.Dial(context.Background(), nil)
}

func (l *mockListener) Close() error {
	return l.dialer.Close()
}

func (l *mockListener) put(conn Conn) {
	l.dialer.put(conn)
}

func (l *mockListener) isClosed() bool {
	return l.dialer.isClosed()
}

func newMockListener() *mockListener {
	return &mockListener{dialer: mockDialer{dial: make(chan Conn)}}
}

// TestRegistry_Get tests that when calling Get(), existing peers are returned,
// and when unknown peers are requested, a temporary peer is create that is
// dialed in the background. It also tests that the dialing process combines
// with the Listener, so that if a connection to a peer that is still being
// dialed comes in, the peer is assigned that connection.
func TestRegistry_Get(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(0xDDDDDeDe))
	id := wallettest.NewRandomAccount(rng)
	peerId := wallettest.NewRandomAccount(rng)
	peerAddr := peerId.Address()

	t.Run("peer already in progress (nonexisting)", func(t *testing.T) {
		t.Parallel()

		dialer := newMockDialer()
		r := NewRegistry(id, func(*Peer) {}, dialer)
		closed := newPeer(peerAddr, nil, nil)

		r.peers = []*Peer{closed}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		p, err := r.Get(ctx, peerAddr)
		assert.Error(t, err)
		assert.Nil(t, p)
	})

	t.Run("peer already in progress (existing)", func(t *testing.T) {
		t.Parallel()

		dialer := newMockDialer()
		r := NewRegistry(id, func(*Peer) {}, dialer)
		existing := newPeer(peerAddr, newMockConn(nil), nil)

		r.peers = []*Peer{existing}
		test.AssertTerminates(t, timeout, func() {
			p, err := r.Get(context.Background(), peerAddr)
			assert.NoError(t, err)
			assert.Same(t, p, existing)
		})
	})

	t.Run("new peer (failed dial)", func(t *testing.T) {
		t.Parallel()

		dialer := newMockDialer()
		r := NewRegistry(id, func(*Peer) {}, dialer)

		dialer.Close()
		test.AssertTerminates(t, timeout, func() {
			p, err := r.Get(context.Background(), peerAddr)
			assert.Error(t, err)
			assert.Nil(t, p)
		})

		<-time.After(timeout)

	})

	t.Run("new peer (successful dial)", func(t *testing.T) {
		t.Parallel()

		dialer := newMockDialer()
		r := NewRegistry(id, func(*Peer) {}, dialer)

		a, b := newPipeConnPair()
		go func() {
			dialer.put(a)
			ExchangeAddrs(context.Background(), peerId, b)
		}()
		test.AssertTerminates(t, timeout, func() {
			p, err := r.Get(context.Background(), peerAddr)
			require.NoError(t, err)
			require.NotNil(t, p)
			require.True(t, p.exists())
			require.False(t, p.IsClosed())
		})
	})
}

func TestRegistry_authenticatedDial(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(0xb0baFEDD))
	id := wallettest.NewRandomAccount(rng)
	d := &mockDialer{dial: make(chan Conn)}
	r := NewRegistry(id, func(*Peer) {}, d)

	remoteId := wallettest.NewRandomAccount(rng)
	remoteAddr := remoteId.Address()

	t.Run("dial fail, existing peer", func(t *testing.T) {
		p := newPeer(nil, nil, nil)
		p.create(newMockConn(nil))
		go d.put(nil)
		test.AssertTerminates(t, timeout, func() {
			err := r.authenticatedDial(context.Background(), p, wallettest.NewRandomAddress(rng))
			assert.NoError(t, err)
		})
	})

	t.Run("dial fail, nonexisting peer", func(t *testing.T) {
		p := newPeer(nil, nil, nil)
		go d.put(nil)
		test.AssertTerminates(t, timeout, func() {
			err := r.authenticatedDial(context.Background(), p, wallettest.NewRandomAddress(rng))
			assert.Error(t, err)
		})
	})

	t.Run("dial success, ExchangeAddrs fail, nonexisting peer", func(t *testing.T) {
		p := newPeer(nil, nil, nil)
		a, b := newPipeConnPair()
		go d.put(a)
		go b.Send(msg.NewPingMsg())
		test.AssertTerminates(t, timeout, func() {
			err := r.authenticatedDial(context.Background(), p, remoteAddr)
			assert.Error(t, err)
		})
	})

	t.Run("dial success, ExchangeAddrs fail, existing peer", func(t *testing.T) {
		p := newPeer(nil, newMockConn(nil), nil)
		a, b := newPipeConnPair()
		go d.put(a)
		go b.Send(msg.NewPingMsg())
		test.AssertTerminates(t, timeout, func() {
			err := r.authenticatedDial(context.Background(), p, remoteAddr)
			assert.Nil(t, err)
		})
	})

	t.Run("dial success, ExchangeAddrs imposter, nonexisting peer", func(t *testing.T) {
		p := newPeer(nil, nil, nil)
		a, b := newPipeConnPair()
		go d.put(a)
		go ExchangeAddrs(context.Background(), wallettest.NewRandomAccount(rng), b)
		test.AssertTerminates(t, timeout, func() {
			err := r.authenticatedDial(context.Background(), p, remoteAddr)
			assert.Error(t, err)
		})
	})

	t.Run("dial success, ExchangeAddrs imposter, existing peer", func(t *testing.T) {
		p := newPeer(nil, newMockConn(nil), nil)
		a, b := newPipeConnPair()
		go d.put(a)
		go ExchangeAddrs(context.Background(), wallettest.NewRandomAccount(rng), b)
		test.AssertTerminates(t, timeout, func() {
			err := r.authenticatedDial(context.Background(), p, remoteAddr)
			assert.NoError(t, err)
		})
	})

	t.Run("dial success, ExchangeAddrs success", func(t *testing.T) {
		p := newPeer(nil, nil, nil)
		a, b := newPipeConnPair()
		go d.put(a)
		go ExchangeAddrs(context.Background(), remoteId, b)
		test.AssertTerminates(t, timeout, func() {
			err := r.authenticatedDial(context.Background(), p, remoteAddr)
			assert.NoError(t, err)
		})
	})
}

func TestRegistry_setupConn(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(0xb0baFEDD))
	id := wallettest.NewRandomAccount(rng)
	remoteId := wallettest.NewRandomAccount(rng)

	t.Run("ExchangeAddrs fail", func(t *testing.T) {
		d := &mockDialer{dial: make(chan Conn)}
		r := NewRegistry(id, func(*Peer) {}, d)
		a, b := newPipeConnPair()
		go b.Send(msg.NewPingMsg())
		test.AssertTerminates(t, timeout, func() {
			assert.Error(t, r.setupConn(a))
		})
	})

	t.Run("ExchangeAddrs success (peer already exists)", func(t *testing.T) {
		d := &mockDialer{dial: make(chan Conn)}
		r := NewRegistry(id, func(*Peer) {}, d)
		a, b := newPipeConnPair()
		go ExchangeAddrs(context.Background(), remoteId, b)

		r.addPeer(remoteId.Address(), nil)
		test.AssertTerminates(t, timeout, func() {
			assert.NoError(t, r.setupConn(a))
		})
	})

	t.Run("ExchangeAddrs success (peer did not exist)", func(t *testing.T) {
		d := &mockDialer{dial: make(chan Conn)}
		r := NewRegistry(id, func(*Peer) {}, d)
		a, b := newPipeConnPair()
		go ExchangeAddrs(context.Background(), remoteId, b)

		test.AssertTerminates(t, timeout, func() {
			assert.NoError(t, r.setupConn(a))
		})
	})
}

func TestRegistry_Listen(t *testing.T) {
	t.Parallel()
	assert, require := assert.New(t), require.New(t)

	rng := rand.New(rand.NewSource(0xDDDDDeDe))

	id := wallettest.NewRandomAccount(rng)
	addr := id.Address()
	remoteId := wallettest.NewRandomAccount(rng)
	remoteAddr := remoteId.Address()

	d := newMockDialer()
	l := newMockListener()
	r := NewRegistry(id, func(*Peer) {}, d)

	go func() {
		// Listen() will only terminate if the listener is closed.
		test.AssertTerminates(t, 2*timeout, func() { r.Listen(l) })
	}()

	a, b := newPipeConnPair()
	l.put(a)
	test.AssertTerminates(t, timeout, func() {
		address, err := ExchangeAddrs(context.Background(), remoteId, b)
		require.NoError(err)
		assert.True(address.Equals(addr))
	})

	<-time.After(timeout)
	assert.True(r.Has(remoteAddr))

	assert.NoError(r.Close())
	assert.True(l.isClosed(), "closing the registry should close the listener")

	l2 := newMockListener()
	test.AssertTerminates(t, timeout, func() {
		r.Listen(l2)
		assert.True(l2.isClosed(),
			"Listen on closed registry should close the listener immediately")
	})
}

// TestRegistry_addPeer tests that addPeer() calls the Registry's subscription
// function. Other aspects of the function are already tested in other tests.
func TestRegistry_addPeer_Subscribe(t *testing.T) {
	rng := rand.New(rand.NewSource(0xDDDDDeDe))
	called := false
	r := NewRegistry(wallettest.NewRandomAccount(rng), func(*Peer) { called = true }, nil)

	assert.False(t, called, "subscription must not have been called yet")
	r.addPeer(nil, nil)
	assert.True(t, called, "subscription must have been called")
}

func TestRegistry_delete(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(0xb0baFEDD))
	d := &mockDialer{dial: make(chan Conn)}
	r := NewRegistry(wallettest.NewRandomAccount(rng), func(*Peer) {}, d)

	id := wallettest.NewRandomAccount(rng)
	addr := id.Address()
	assert.Equal(t, 0, r.NumPeers())
	p := newPeer(addr, nil, nil)
	r.peers = []*Peer{p}
	assert.True(t, r.Has(addr))
	p2, _ := r.find(addr)
	assert.Equal(t, p, p2)

	r.delete(p2)
	assert.Equal(t, 0, r.NumPeers())
	assert.False(t, r.Has(addr))
	p2, _ = r.find(addr)
	assert.Nil(t, p2)

	assert.Panics(t, func() { r.delete(p) }, "double delete must panic")
}

func TestRegistry_Close(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(0xb0baFEDD))

	t.Run("double close error", func(t *testing.T) {
		r := NewRegistry(wallettest.NewRandomAccount(rng), func(*Peer) {}, nil)
		r.Close()
		assert.Error(t, r.Close())
	})

	t.Run("peer close error", func(t *testing.T) {
		d := &mockDialer{dial: make(chan Conn)}
		r := NewRegistry(wallettest.NewRandomAccount(rng), func(*Peer) {}, d)

		mc := newMockConn(nil)
		p := newPeer(nil, mc, nil)
		// we close the mockConn so that a second Close() call returns an error.
		// Note that the mockConn doesn't return an AlreadyClosedError on a
		// double-close.
		mc.Close()
		r.peers = append(r.peers, p)
		assert.Error(t, r.Close(),
			"a close error from a peer should be propagated to Registry.Close()")
	})

	t.Run("dialer close error", func(t *testing.T) {
		d := &mockDialer{dial: make(chan Conn)}
		d.Close()
		r := NewRegistry(wallettest.NewRandomAccount(rng), func(*Peer) {}, d)

		assert.Error(t, r.Close())
	})
}
