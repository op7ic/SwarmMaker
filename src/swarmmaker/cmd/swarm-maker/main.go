// main.go
// Author: Jerzy 'Yuri' Kramarz (op7ic)
// Copyright: See LICENSE file
// Github: https://github.com/op7ic/SwarmMaker
//
// Entry point for the swarm-maker CLI binary.
// Wires the version variable (injected via ldflags at build time) into the
// CLI package and delegates to cli.Execute(). This is the only file in the
// binary's main package -- all logic lives in internal/ and prompts/.


package main

import (
	"fmt"
	"os"

	"github.com/op7ic/swarmmaker/internal/cli"
)

// version is set at build time via ldflags:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/swarm-maker
var version = "dev"

func main() {
	cli.Version = version
	if err := cli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
