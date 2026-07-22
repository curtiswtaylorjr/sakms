//go:build !linux

package main

import (
	"context"
	"log"
)

// startControlSocket is a no-op on non-Linux platforms. The local mediaRoots
// control socket relies on AF_UNIX + SO_PEERCRED (Linux-specific) and the whole
// tray/native-picker/RPM feature is Linux-desktop-scoped, so windows/darwin
// daemon builds simply omit it — they still build cleanly (this stub satisfies
// the same signature main() calls) and set mediaRoots by editing the config
// file. See control_socket.go for the real implementation.
func startControlSocket(_ context.Context, _ *NodeConfig, _ string, _ *pathmapPusher, _ *nodeSession) {
	log.Printf("sakms-node: local control socket unsupported on this platform; set mediaRoots by editing the config file")
}
