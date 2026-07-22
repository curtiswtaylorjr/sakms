package api

import (
	"net/http"
	"strings"

	"github.com/labbersanon/sakms/internal/auth"
	"github.com/labbersanon/sakms/internal/nodekeys"
	"github.com/labbersanon/sakms/internal/nodes"
	"github.com/labbersanon/sakms/internal/nodesettings"
	"github.com/labbersanon/sakms/internal/settings"
)

// NewNodesMux returns a ServeMux for all /api/nodes/* routes with per-handler
// authentication. Node-agent routes (stream, heartbeat, job result) require
// Authorization: Bearer <nodeKey>. Operator routes (list, approve, reject,
// settings) require the master API key or a valid session cookie, identical to
// the rest of the API.
//
// This mux is mounted WITHOUT wrapping it in auth.Middleware so that operator
// and node routes can each enforce their own distinct credential type. The
// unauthenticated pairing endpoint (GET /api/nodes/pair) is mounted separately
// on the top-level mux as an exact match that beats this subtree.
func NewNodesMux(
	reg *nodes.Registry,
	pairingReg *nodes.PairingRegistry,
	nodeKeyStore *nodekeys.Store,
	enc auth.TokenEncryptor,
	authStore *auth.Store,
	settingsStore *settings.Store,
	nodeSettingsStore *nodesettings.Store,
) *http.ServeMux {
	mux := http.NewServeMux()

	// Node-agent routes — validated by per-node bearer key only.
	nodeKey := func(h http.Handler) http.Handler { return auth.NodeKeyMiddleware(nodeKeyStore, h) }
	mux.Handle("GET /api/nodes/stream", nodeKey(nodeStreamHandler(reg, settingsStore, nodeSettingsStore)))
	mux.Handle("POST /api/nodes/heartbeat", nodeKey(nodeHeartbeatHandler(reg)))
	mux.Handle("POST /api/nodes/jobs/{id}/result", nodeKey(nodeJobResultHandler(reg)))
	mux.Handle("POST /api/nodes/browse/{requestId}/result", nodeKey(nodeBrowseResultHandler(reg)))

	// Operator routes — validated by master API key or session cookie.
	op := func(h http.Handler) http.Handler { return auth.Middleware(enc, authStore, h) }
	mux.Handle("GET /api/nodes", op(listNodesHandler(reg, pairingReg, nodeSettingsStore)))
	mux.Handle("POST /api/nodes/{id}/approve", op(approveNodeHandler(pairingReg, nodeKeyStore, settingsStore, nodeSettingsStore)))
	mux.Handle("DELETE /api/nodes/{id}/pending", op(rejectPendingHandler(pairingReg)))
	mux.Handle("GET /api/nodes/{id}/path-mappings", op(nodePathMappingsHandler(settingsStore, nodeSettingsStore)))
	mux.Handle("GET /api/nodes/{id}/browse", op(nodeBrowseHandler(reg)))

	// Dual-auth route (D1): the settings PUT accepts EITHER a node bearer key
	// (the node authoring its own PathMap) OR operator credentials (changing
	// MaxJobs). A request presenting Authorization: Bearer is routed through
	// NodeKeyMiddleware — which injects the node identity the handler keys by —
	// and anything else falls through to the operator Middleware. An INVALID
	// bearer is rejected 401 by NodeKeyMiddleware and never silently retried as
	// an operator check: a bad node key must not downgrade to a different
	// credential type. The handler itself partitions the write by which one
	// authenticated (see updateNodeSettingsHandler).
	dualAuth := func(h http.Handler) http.Handler {
		node := auth.NodeKeyMiddleware(nodeKeyStore, h)
		operator := auth.Middleware(enc, authStore, h)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				node.ServeHTTP(w, r)
				return
			}
			operator.ServeHTTP(w, r)
		})
	}
	mux.Handle("PUT /api/nodes/{id}/settings", dualAuth(updateNodeSettingsHandler(reg, settingsStore, nodeSettingsStore)))

	// Dedicated dual-auth pause toggle (P1): a genuinely separate route from
	// PUT /settings, reusing the same dualAuth wrapper. Node bearer keys by
	// bearer identity (ignoring the URL {id}, D2); operator keys by the URL {id}.
	// Kept off the settings handler so pause never entangles with the
	// path-mapping verification gate.
	mux.Handle("PUT /api/nodes/{id}/pause", dualAuth(updateNodePauseHandler(reg, settingsStore, nodeSettingsStore)))

	return mux
}
