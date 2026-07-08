// Package rename implements Tidyarr's Rename workflow: propose registering
// orphaned (unmapped) files with their mode's Sonarr/Radarr instance, then —
// once a human approves a specific proposal — actually register it.
//
// Scan never mutates anything; it only reads and produces proposals.Proposal
// values. Apply is the only function in this package that calls a *arr app's
// write endpoints, and it only ever acts on one already-approved proposal at
// a time — there is no "apply everything" path, by design (see the design
// spec's staged-for-approval principle).
package rename

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/curtiswtaylorjr/tidyarr/internal/config"
	"github.com/curtiswtaylorjr/tidyarr/internal/identify"
	"github.com/curtiswtaylorjr/tidyarr/internal/mode"
	"github.com/curtiswtaylorjr/tidyarr/internal/proposals"
	"github.com/curtiswtaylorjr/tidyarr/internal/searchterm"
	"github.com/curtiswtaylorjr/tidyarr/internal/servarr"
)

// Scan walks every root folder sess's Servarr app currently reports and
// produces one proposal per orphaned item: a resolved match ready to
// register (Pending), or a record of why it couldn't be resolved on its own
// (Unmatched) — surfaced either way, never silently dropped.
func Scan(ctx context.Context, sess *mode.Session) ([]proposals.Proposal, error) {
	client := sess.Servarr

	// Adult identification runs through sess.Identify, which mode.Build leaves
	// nil when the Ollama backbone isn't configured. Fail fast with an
	// actionable message rather than nil-panicking mid-walk or burying the real
	// "you haven't configured identification" signal under N Unmatched rows.
	if sess.Mode == mode.Adult && sess.Identify == nil {
		return nil, fmt.Errorf("adult identification isn't configured — add an Ollama connection and set the Ollama model in Settings, plus at least one of StashDB/FansDB/TPDB")
	}

	folders, err := client.RootFolders(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading root folders: %w", err)
	}
	tracked, err := client.AllTracked(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading tracked items: %w", err)
	}
	profiles, err := client.QualityProfiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading quality profiles: %w", err)
	}

	var out []proposals.Proposal
	for _, root := range folders {
		for _, uf := range root.UnmappedFolders {
			if config.SidecarExts[strings.ToLower(filepath.Ext(uf.Name))] {
				continue
			}
			if sess.Mode == mode.Adult {
				out = append(out, proposeOneAdult(ctx, sess.Identify, sess.Mode, root, uf, tracked, profiles))
			} else {
				out = append(out, proposeOne(ctx, client, sess.Mode, root, uf, tracked, profiles))
			}
		}
	}
	return out, nil
}

func proposeOne(
	ctx context.Context, client *servarr.Client, m mode.Mode,
	root servarr.RootFolder, uf servarr.UnmappedFolder,
	tracked []servarr.TrackedItem, profiles []servarr.QualityProfile,
) proposals.Proposal {
	p := proposals.Proposal{
		Mode: m, Workflow: proposals.Rename,
		SourceName: uf.Name, SourcePath: uf.Path, RootFolderPath: root.Path,
	}

	term := searchterm.FromName(uf.Name)
	results, err := client.Lookup(ctx, term)
	if err != nil {
		p.Status = proposals.Unmatched
		p.Reason = fmt.Sprintf("lookup failed for search term %q: %v", term, err)
		return p
	}
	if len(results) == 0 {
		p.Status = proposals.Unmatched
		p.Reason = fmt.Sprintf("no match for search term %q", term)
		return p
	}
	lr := results[0]

	if dup := findTrackedDuplicate(tracked, client.AppType(), lr); dup != nil {
		p.Status = proposals.Unmatched
		p.Reason = fmt.Sprintf("appears to already be tracked as %q (in %s) — leaving in place for manual review", dup.Title, dup.RootFolderPath)
		return p
	}

	p.Status = proposals.Pending
	p.Title = lr.Title
	p.TVDBID = lr.TVDBID
	p.TMDBID = lr.TMDBID
	p.QualityProfileID = servarr.DefaultQualityProfileID(tracked, root.Path, profiles)
	return p
}

// findTrackedDuplicate reports whether lr's identified TVDB/TMDB ID already
// matches something the app tracks — i.e. this "orphaned" item is actually a
// duplicate copy of existing content, not a genuinely new addition.
func findTrackedDuplicate(tracked []servarr.TrackedItem, app servarr.App, lr servarr.LookupResult) *servarr.TrackedItem {
	for i, t := range tracked {
		if app == servarr.Sonarr && lr.TVDBID != 0 && t.TVDBID == lr.TVDBID {
			return &tracked[i]
		}
		if app == servarr.Radarr && lr.TMDBID != 0 && t.TMDBID == lr.TMDBID {
			return &tracked[i]
		}
	}
	return nil
}

// proposeOneAdult resolves one unmapped folder via the AI identification
// pipeline (sess.Identify) instead of the *arr app's own TVDB/TMDB Lookup.
// Duplicate detection is intentionally skipped: TrackedItem carries no
// ForeignID/StashId to key an Adult scene against (see spec §7) — an
// already-tracked duplicate surfaces safely as Whisparr's own foreignId
// uniqueness rejection at Apply, not silent corruption.
func proposeOneAdult(
	ctx context.Context, ident *identify.Identifier, m mode.Mode,
	root servarr.RootFolder, uf servarr.UnmappedFolder,
	tracked []servarr.TrackedItem, profiles []servarr.QualityProfile,
) proposals.Proposal {
	p := proposals.Proposal{
		Mode: m, Workflow: proposals.Rename,
		SourceName: uf.Name, SourcePath: uf.Path, RootFolderPath: root.Path,
	}
	res, err := ident.Identify(ctx, uf.Name, filepath.Base(root.Path))
	p.Status, p.Reason, p.Title, p.ForeignID, p.ItemType = classifyAdultMatch(res, err)
	if p.Status == proposals.Pending {
		p.QualityProfileID = servarr.DefaultQualityProfileID(tracked, root.Path, profiles)
	}
	return p
}

// classifyAdultMatch maps a completed Identify result to a proposal's
// identification-derived fields, or to an Unmatched reason. A match without a
// valid stash-box scene identifier (web_search-only, SceneID=="" || Box=="")
// is a correctness requirement to reject: it has no valid Whisparr ForeignID.
func classifyAdultMatch(res *identify.MatchResult, err error) (status proposals.Status, reason, title, foreignID, itemType string) {
	switch {
	case err != nil:
		return proposals.Unmatched, fmt.Sprintf("identification failed: %v", err), "", "", ""
	case res == nil:
		return proposals.Unmatched, "no confident identification", "", "", ""
	}
	foreignID, hasID := res.WhisparrForeignID()
	if !hasID {
		return proposals.Unmatched, "web-identified only (no scene ID) — needs manual review", "", "", ""
	}
	return proposals.Pending, "", res.Title, foreignID, res.Type
}

// Apply registers p's identified item with sess's Servarr app, then triggers
// a broad downloaded-scan so the app picks up the file already sitting on
// disk under p.RootFolderPath. p must be Pending — Apply refuses anything
// else (already applied, dismissed, or unmatched with nothing to register).
//
// If Add succeeds but the follow-up scan trigger fails, trackedID is still
// returned alongside the error: the item is genuinely registered at that
// point, so the caller should still record it as applied rather than losing
// track of it — the scan trigger can be retried independently (e.g. the
// app's own periodic scan will pick it up eventually regardless).
func Apply(ctx context.Context, sess *mode.Session, p proposals.Proposal) (trackedID int, err error) {
	if p.Status != proposals.Pending {
		return 0, fmt.Errorf("proposal %d is %q, not pending — nothing to apply", p.ID, p.Status)
	}

	// Structural safety guard at the mutation boundary: a Whisparr scene needs
	// BOTH a ForeignID and an ItemType, or Whisparr silently files it as a
	// mis-typed movie (its ItemType enum's zero value is "movie"). Refuse here
	// rather than trusting Scan-convention — even a hand-crafted or future-buggy
	// Adult proposal can never be registered without a real scene identifier.
	if sess.Servarr.AppType() == servarr.Whisparr && (p.ForeignID == "" || p.ItemType == "") {
		return 0, fmt.Errorf("proposal %d has no scene identifier — refusing to register it as a mis-typed movie", p.ID)
	}

	id, err := sess.Servarr.Add(ctx, servarr.AddRequest{
		Title: p.Title, TVDBID: p.TVDBID, TMDBID: p.TMDBID,
		ForeignID: p.ForeignID, ItemType: p.ItemType,
		QualityProfileID: p.QualityProfileID, RootFolderPath: p.RootFolderPath, Monitored: true,
	})
	if err != nil {
		return 0, fmt.Errorf("registering %q: %w", p.Title, err)
	}

	if err := sess.Servarr.ScanForDownloaded(ctx); err != nil {
		return id, fmt.Errorf("registered as id=%d but triggering the downloaded-files scan failed: %w", id, err)
	}
	return id, nil
}
