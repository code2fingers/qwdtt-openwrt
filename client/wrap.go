// SPDX-License-Identifier: MIT

package main

import (
	"crypto/cipher"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	wrapKeyLen = 32
)

// The cipher every packet is sealed with, derived from the connection
// password. Built once and passed around, rather than derived or looked up
// where it is used: the key does not change for the life of the process, and
// finding it by key on each packet cost a 32-byte allocation per packet to
// build the map key with.
func deriveWrapAEAD(password string) (cipher.AEAD, error) {
	if password == "" {
		return nil, errors.New("empty password")
	}
	key := make([]byte, wrapKeyLen)
	reader := hkdf.New(
		sha256.New,
		[]byte(password),
		[]byte("WDTT-WRAP-v1"),
		[]byte("rtp-obfs/chacha20poly1305"),
	)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, fmt.Errorf("derive wrap key: %w", err)
	}
	return chacha20poly1305.New(key)
}
