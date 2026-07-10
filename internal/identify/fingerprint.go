package identify

import "context"

// fingerprintCascadeOrder is the fixed lookup order restored from the prior
// CLI this project descended from: StashDB, then FansDB, then TPDB's
// stash-box-protocol GraphQL endpoint (NOT the tpdbrest.Client used for text
// search — see GiveBack's doc comment for why the same stashbox.Client map
// serves both give-back and lookup). A stale comment in the prior CLI once
// claimed a 4th "TPDB-REST" fingerprint stage; it never existed in the real
// implementation and is deliberately not restored here.
var fingerprintCascadeOrder = []string{"stashdb", "fansdb", "tpdb"}

// fingerprintBatchSize matches stashapi.FindSceneInfoByPaths' existing chunk
// size, for consistency rather than any protocol requirement.
const fingerprintBatchSize = 25

// LookupFingerprints batch-resolves phashes against every configured
// fingerprint box in cascade order, reusing GiveBack.Boxes — the exact set
// of stash-box clients already built for fingerprint/draft submission,
// rather than constructing a second one (see mode.buildIdentifier). A phash
// absent from the result map simply matched nothing in any configured box;
// nil id.GiveBack (identification not configured) or an empty phashes slice
// both just return an empty map, no error.
func (id *Identifier) LookupFingerprints(ctx context.Context, phashes []string) (map[string]*MatchResult, error) {
	results := make(map[string]*MatchResult)
	if id.GiveBack == nil || len(phashes) == 0 {
		return results, nil
	}

	order := dedupPreserveOrder(phashes)
	remaining := order
	for _, box := range fingerprintCascadeOrder {
		if len(remaining) == 0 {
			break
		}
		client := id.GiveBack.Boxes[box]
		if client == nil {
			continue // not configured — skip this stage, don't abort the cascade
		}
		for start := 0; start < len(remaining); start += fingerprintBatchSize {
			chunk := remaining[start:min(start+fingerprintBatchSize, len(remaining))]
			if err := id.Throttle.Wait(ctx, box); err != nil {
				return results, err
			}
			scenes, err := client.FindScenesByFingerprints(ctx, chunk)
			if err != nil {
				continue // best-effort: whole chunk retried at the next stage
			}
			for i, phash := range chunk {
				if i < len(scenes) && scenes[i] != nil {
					results[phash] = &MatchResult{
						Title: scenes[i].Title, Studio: scenes[i].StudioName, Date: scenes[i].ReleaseDate,
						Type: "scene", Source: box + "_fingerprint", SceneID: scenes[i].ID, Box: box,
					}
				}
			}
		}
		// Recomputed from the ORIGINAL order, not the shrinking remaining —
		// matches the prior CLI's exact behavior.
		remaining = missingFrom(order, results)
	}
	return results, nil
}

func dedupPreserveOrder(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func missingFrom(order []string, results map[string]*MatchResult) []string {
	var out []string
	for _, s := range order {
		if _, ok := results[s]; !ok {
			out = append(out, s)
		}
	}
	return out
}
