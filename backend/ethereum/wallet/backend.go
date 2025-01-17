// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of go-perun. Use
// of this source code is governed by a MIT-style license that can be found in
// the LICENSE file.

package wallet

import (
	"io"

	"github.com/pkg/errors"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	perun "perun.network/go-perun/wallet"
	"perun.network/go-perun/wire"
)

// Backend implements the utility interface defined in the wallet package.
type Backend struct{}

// SigLen length of a signature in byte.
// ref https://godoc.org/github.com/ethereum/go-ethereum/crypto/secp256k1#Sign
// ref https://github.com/ethereum/go-ethereum/blob/54b271a86dd748f3b0bcebeaf678dc34e0d6177a/crypto/signature_cgo.go#L66
const SigLen = 65

// compile-time check that the ethereum backend implements the perun backend
var _ perun.Backend = (*Backend)(nil)

// NewAddressFromBytes creates a new address from a byte array.
func (*Backend) NewAddressFromBytes(data []byte) (perun.Address, error) {
	return NewAddressFromBytes(data)
}

func (*Backend) DecodeAddress(r io.Reader) (perun.Address, error) {
	return DecodeAddress(r)
}

// DecodeSig reads a []byte with length of an ethereum signature
func (*Backend) DecodeSig(r io.Reader) (perun.Sig, error) {
	return DecodeSig(r)
}

func (*Backend) VerifySignature(msg []byte, sig perun.Sig, a perun.Address) (bool, error) {
	return VerifySignature(msg, sig, a)
}

// NewAddressFromBytes creates a new address from a byte array.
func NewAddressFromBytes(data []byte) (perun.Address, error) {
	if len(data) != common.AddressLength {
		return nil, errors.Errorf("could not create address from bytes of length: %d", len(data))
	}
	return &Address{common.BytesToAddress(data)}, nil
}

func DecodeAddress(r io.Reader) (perun.Address, error) {
	addr := new(Address)
	return addr, addr.Decode(r)
}

// DecodeSig reads a []byte with length of an ethereum signature
func DecodeSig(r io.Reader) (perun.Sig, error) {
	buf := make(perun.Sig, SigLen)
	return buf, wire.Decode(r, &buf)
}

// VerifySignature verifies if a signature was made by this account.
func VerifySignature(msg []byte, sig perun.Sig, a perun.Address) (bool, error) {
	hash := prefixedHash(msg)
	sigCopy := make([]byte, SigLen)
	copy(sigCopy, sig)
	if len(sigCopy) == SigLen && (sigCopy[SigLen-1] >= 27) {
		sigCopy[SigLen-1] -= 27
	}
	pk, err := crypto.SigToPub(hash, sigCopy)
	if err != nil {
		return false, err
	}
	addr := crypto.PubkeyToAddress(*pk)
	return a.Equals(&Address{addr}), nil
}

func prefixedHash(data []byte) []byte {
	hash := crypto.Keccak256(data)
	prefix := []byte("\x19Ethereum Signed Message:\n32")
	return crypto.Keccak256(prefix, hash)
}
