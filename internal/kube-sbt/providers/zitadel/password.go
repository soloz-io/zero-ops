package zitadel

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// generateInitialPassword returns a password that satisfies a default complexity
// policy: length, upper, lower, digit and symbol.
//
// Built from four alphabets with one character guaranteed from each rather than
// drawn from a single pool, because a pooled draw satisfies the policy only
// PROBABLY — and the failure is a rejected user creation on some tenants and not
// others, which reads as an intermittent provisioning fault.
//
// The symbol set excludes quotes, backslashes and shell metacharacters. This
// value is carried through JSON, a secret store and very likely a terminal, and
// a password that breaks one of those is a support incident rather than a
// security improvement.
func generateInitialPassword() (string, error) {
	const (
		lower   = "abcdefghijkmnopqrstuvwxyz"
		upper   = "ABCDEFGHJKLMNPQRSTUVWXYZ"
		digits  = "23456789"
		symbols = "-_.+=@#%^"
		length  = 24
	)
	all := lower + upper + digits + symbols

	out := make([]byte, 0, length)
	for _, set := range []string{lower, upper, digits, symbols} {
		c, err := pick(set)
		if err != nil {
			return "", err
		}
		out = append(out, c)
	}
	for len(out) < length {
		c, err := pick(all)
		if err != nil {
			return "", err
		}
		out = append(out, c)
	}

	// Shuffle, or the first four characters always come from the same alphabets
	// in the same order — a structure an attacker can assume.
	for i := len(out) - 1; i > 0; i-- {
		jj, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", err
		}
		j := jj.Int64()
		out[i], out[j] = out[j], out[i]
	}
	return string(out), nil
}

func pick(set string) (byte, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
	if err != nil {
		return 0, fmt.Errorf("zitadel: generate password: %w", err)
	}
	return set[n.Int64()], nil
}
