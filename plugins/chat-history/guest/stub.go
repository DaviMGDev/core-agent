//go:build !wasip1

// Stub keeps the guest package buildable on the host. The real guest is
// wasip1-only; cmd/build-plugins.sh compiles it.
package main

func main() {}
