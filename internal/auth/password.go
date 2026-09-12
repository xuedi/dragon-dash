// Package auth holds the pieces of the login: password hashes, sessions and
// the failed-login limiter. The shell wires them to the login page and to the
// write endpoints; nothing here knows about HTTP routes.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PBKDF2 rather than bcrypt or argon2 because it is in the standard library,
// and the project takes no external modules. 600,000 rounds of HMAC-SHA256 is
// OWASP's current figure for it.
const (
	scheme     = "pbkdf2-sha256"
	Iterations = 600_000
	saltLen    = 16
	keyLen     = 32

	// A login runs one derivation, so the ceiling bounds what a mistyped hash
	// could cost per attempt.
	minIterations = 10_000
	maxIterations = 10_000_000
)

// MinPasswordLen is enforced when a hash is made, not when one is checked.
const MinPasswordLen = 8

// The encoding is base64url without padding, so a hash holds no $, = or
// quote that an env file loader or Compose could try to interpret.
var b64 = base64.RawURLEncoding

// Hash returns "pbkdf2-sha256:<iterations>:<salt>:<key>".
func Hash(password string) (string, error) {
	if len(password) < MinPasswordLen {
		return "", fmt.Errorf("the password needs at least %d characters", MinPasswordLen)
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, Iterations, keyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d:%s:%s", scheme, Iterations, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

type parsed struct {
	iter      int
	salt, key []byte
}

func parse(encoded string) (parsed, error) {
	parts := strings.Split(encoded, ":")
	if len(parts) != 4 || parts[0] != scheme {
		return parsed{}, errors.New("not a " + scheme + " hash, generate one with dragon-dash passwd")
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < minIterations || iter > maxIterations {
		return parsed{}, fmt.Errorf("iteration count %q is outside %d to %d", parts[1], minIterations, maxIterations)
	}
	salt, err := b64.DecodeString(parts[2])
	if err != nil || len(salt) < saltLen {
		return parsed{}, errors.New("the salt is damaged")
	}
	key, err := b64.DecodeString(parts[3])
	if err != nil || len(key) != keyLen {
		return parsed{}, errors.New("the key is damaged")
	}
	return parsed{iter, salt, key}, nil
}

// Check reports what is wrong with a configured hash, so a damaged one stops
// startup instead of quietly refusing every login.
func Check(encoded string) error {
	_, err := parse(encoded)
	return err
}

// Verify reports whether password matches the hash, in constant time. A
// malformed hash matches nothing.
func Verify(password, encoded string) bool {
	p, err := parse(encoded)
	if err != nil {
		return false
	}
	key, err := pbkdf2.Key(sha256.New, password, p.salt, p.iter, keyLen)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(key, p.key) == 1
}
