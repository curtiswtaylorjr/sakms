// Package nodesettings persists a connected node's operator-set path
// mappings and concurrency cap (MaxJobs) — the durable record backing the
// authoritative reconnect re-push and the settings-edit form's "current
// values" prefill (both previously nonexistent: nodes.Registry is pure
// in-memory, so before this package a node's settings only ever lived in
// its own local config.json, never on the server). Keyed by the durable
// node id resolved via internal/nodekeys/internal/auth (see
// internal/nodes/registry.go's Connect).
package nodesettings

import (
	"context"
	"database/sql"
	"time"
)

// VerificationStatus records how a PathMappingEntry's NodePath was
// confirmed to correspond to its LibraryPathKey's server-side value, per
// the security-hardening addendum's mapping-verification safeguard.
type VerificationStatus string

const (
	// VerificationVerified means a live directory-listing comparison ran
	// between the server's ServerPath and the node's NodePath and passed
	// the containment threshold.
	VerificationVerified VerificationStatus = "verified"
	// VerificationUnverifiedBootstrap means a live comparison ran but one
	// or both listings were empty — nothing to compare, so the row is
	// accepted but not confirmed correct.
	VerificationUnverifiedBootstrap VerificationStatus = "unverified_bootstrap"
	// VerificationUnverifiedApproval means the row was persisted at
	// approval time, before the node has an authenticated channel a live
	// comparison could use at all (see the Reachability constraint in the
	// security-hardening addendum) — structurally distinct from the
	// bootstrap case, which did attempt a comparison.
	VerificationUnverifiedApproval VerificationStatus = "unverified_approval"
)

// PathMappingEntry is one persisted (library path key → node-local path)
// mapping row.
type PathMappingEntry struct {
	LibraryPathKey     string
	NodePath           string
	VerificationStatus VerificationStatus
	VerifiedAt         *time.Time
}

// Settings is everything persisted for one node: its path mappings and its
// concurrency cap. Both travel together because the wire format pushed to
// the node (nodes.NodeSettings) carries both together — see this package's
// doc comment and node_max_jobs's migration comment for why MaxJobs must be
// persisted alongside PathMappings, not just the mappings alone.
type Settings struct {
	PathMappings []PathMappingEntry
	MaxJobs      int
}

// Store is a SQLite-backed store for persisted per-node settings.
type Store struct {
	db *sql.DB
}

// New returns a Store backed by db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Get returns nodeID's persisted settings. ok is false when nothing has ever
// been persisted for this node (e.g. every already-approved node before this
// feature shipped, or before its first save) — callers must not treat a
// zero-value Settings{} as "MaxJobs should be reset to unlimited" in that
// case; ok=false means "nothing to push," not "push zero values."
func (s *Store) Get(ctx context.Context, nodeID string) (settings Settings, ok bool, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT library_path_key, node_path, verification_status, verified_at FROM node_path_mappings WHERE node_id = ?`, nodeID)
	if err != nil {
		return Settings{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var e PathMappingEntry
		var status string
		var verifiedAt sql.NullString
		if err := rows.Scan(&e.LibraryPathKey, &e.NodePath, &status, &verifiedAt); err != nil {
			return Settings{}, false, err
		}
		e.VerificationStatus = VerificationStatus(status)
		if verifiedAt.Valid {
			t, err := time.Parse(time.RFC3339, verifiedAt.String)
			if err != nil {
				return Settings{}, false, err
			}
			e.VerifiedAt = &t
		}
		settings.PathMappings = append(settings.PathMappings, e)
	}
	if err := rows.Err(); err != nil {
		return Settings{}, false, err
	}

	var maxJobs sql.NullInt64
	row := s.db.QueryRowContext(ctx, `SELECT max_jobs FROM node_max_jobs WHERE node_id = ?`, nodeID)
	switch err := row.Scan(&maxJobs); {
	case err == sql.ErrNoRows:
		// No max_jobs row yet — fine on its own, doesn't change whether
		// this Get found anything overall (path mappings may still exist).
	case err != nil:
		return Settings{}, false, err
	default:
		settings.MaxJobs = int(maxJobs.Int64)
	}

	found := len(settings.PathMappings) > 0 || maxJobs.Valid
	return settings, found, nil
}

// Set persists nodeID's settings, transactionally: every path mapping entry
// is upserted (existing rows for keys not present in entries.PathMappings
// are left as-is — this Store never deletes a row implicitly), and MaxJobs
// is upserted into node_max_jobs.
func (s *Store) Set(ctx context.Context, nodeID string, settings Settings) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op if Commit already succeeded

	now := time.Now().UTC().Format(time.RFC3339)
	for _, e := range settings.PathMappings {
		var verifiedAt sql.NullString
		if e.VerifiedAt != nil {
			verifiedAt = sql.NullString{String: e.VerifiedAt.UTC().Format(time.RFC3339), Valid: true}
		}
		status := e.VerificationStatus
		if status == "" {
			status = VerificationUnverifiedBootstrap
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO node_path_mappings (node_id, library_path_key, node_path, verification_status, verified_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)
			 ON CONFLICT (node_id, library_path_key) DO UPDATE SET node_path = excluded.node_path, verification_status = excluded.verification_status, verified_at = excluded.verified_at, updated_at = excluded.updated_at`,
			nodeID, e.LibraryPathKey, e.NodePath, string(status), verifiedAt, now,
		); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO node_max_jobs (node_id, max_jobs, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT (node_id) DO UPDATE SET max_jobs = excluded.max_jobs, updated_at = excluded.updated_at`,
		nodeID, settings.MaxJobs, now,
	); err != nil {
		return err
	}

	return tx.Commit()
}
