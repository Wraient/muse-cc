package proxy

import "crypto/rand"

// readRand fills b with cryptographically random bytes. It is a thin wrapper
// around crypto/rand.Read so callers (randHex) can swap or test it easily.
func readRand(b []byte) (int, error) {
	return rand.Read(b)
}
