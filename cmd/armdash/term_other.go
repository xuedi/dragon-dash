//go:build !linux

package main

import "errors"

// Releases are Linux only. Elsewhere the password is read with echo on.
func echoOff(int) (func(), error) { return nil, errors.New("not supported") }
