package main

// statusResponse decode tests. statusResponse stays in the tray (it models the
// read-only GET /status payload the tray still polls); the picker-ladder test
// that used to live here was removed in Stage 3 along with the folder picker.

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/labbersanon/sakms/internal/nodecontrol"
)

func TestStatusResponseDecode_MediaRootScopesAndWarning(t *testing.T) {
	const body = `{
		"state": "connected",
		"serverUrl": "https://sak.example",
		"deviceName": "node-1",
		"nodeId": "abc",
		"warning": "mediaRoots is not configured",
		"mediaRootScopes": [
			{"path": "/mnt/Movies", "scope": "namespace_scoped"},
			{"path": "/mnt/TV Shows", "scope": "app_level_only"}
		]
	}`
	var s statusResponse
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Warning != "mediaRoots is not configured" {
		t.Errorf("warning = %q", s.Warning)
	}
	want := []nodecontrol.MediaRootStatus{
		{Path: "/mnt/Movies", Scope: "namespace_scoped"},
		{Path: "/mnt/TV Shows", Scope: "app_level_only"},
	}
	if !reflect.DeepEqual(s.MediaRootScopes, want) {
		t.Errorf("scopes = %+v, want %+v", s.MediaRootScopes, want)
	}
}

func TestStatusResponseDecode_FieldsAbsent(t *testing.T) {
	// omitempty means an older daemon (or the grace period) omits both fields;
	// decoding must not error and must leave them zero-valued.
	const body = `{"state":"pending","pairingCode":"ABC123","serverUrl":"","deviceName":"n"}`
	var s statusResponse
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Warning != "" {
		t.Errorf("warning = %q, want empty", s.Warning)
	}
	if s.MediaRootScopes != nil {
		t.Errorf("scopes = %+v, want nil", s.MediaRootScopes)
	}
	if s.PairingCode != "ABC123" {
		t.Errorf("pairingCode = %q", s.PairingCode)
	}
}
