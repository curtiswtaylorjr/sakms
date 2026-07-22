package main

import "sync"

// nodeSession holds the per-connection facts the daemon learns from the SSE
// ConnectAck and needs to expose to the control socket and the debounced pusher:
//
//   - nodeID: the durable node_keys.id the server minted (ConnectAck.NodeID).
//     The node-auth settings push keys server-side by the bearer identity, NOT
//     this id (D2), so the id is only a route-pattern formality on the PUT URL;
//     it is used when known and falls back to "self" before the first connect.
//   - catalog: the bounded library-path-key catalog (ConnectAck.LibraryPathKeys,
//     D4), so GET /pathmap can show the tray which keys exist to configure.
//
// It outlives individual connect() cycles (created once in main, reused across
// reconnects) and is written on every ack, so it carries a mutex.
type nodeSession struct {
	mu      sync.RWMutex
	nodeID  string
	catalog []string
}

// setAck records the identity + catalog from a fresh ConnectAck.
func (s *nodeSession) setAck(nodeID string, catalog []string) {
	s.mu.Lock()
	s.nodeID = nodeID
	s.catalog = append([]string(nil), catalog...)
	s.mu.Unlock()
}

// id returns the last-known durable node id, or "" before the first connect.
func (s *nodeSession) id() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nodeID
}

// libraryPathKeys returns a copy of the last-received catalog.
func (s *nodeSession) libraryPathKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.catalog...)
}
