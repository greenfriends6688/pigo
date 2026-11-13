//go:build !darwin

package main

// platformNotify does nothing off macOS: the in-app toast and the sound are
// the whole notification there.
func platformNotify(title, body string) {}
