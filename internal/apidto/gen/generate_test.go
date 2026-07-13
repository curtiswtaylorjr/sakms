package gen

import (
	"os"
	"path/filepath"
	"testing"
)

// committedOutputPath is the .ts file checked into the repo (see
// cmd/gendto). Resolved relative to this test file's own directory so the
// test works regardless of the caller's working directory (`go test ./...`
// from the module root, or `go test .` from inside this package).
const committedOutputRel = "../ts/dto.gen.ts"

// TestNoDrift is the build-fails-on-drift gate Guardrail #4 requires: it
// regenerates the TypeScript from the CURRENT internal/apidto source into a
// throwaway temp file and byte-compares it against the committed
// internal/apidto/ts/dto.gen.ts. A DTO change that isn't regenerated (or a
// regeneration that isn't committed) fails `go test ./...` — and therefore
// the build — rather than drifting silently, per the plan's explicit
// requirement.
//
// This is a pure-Go, git-independent gate: it never shells out to git or
// depends on the working tree's VCS state, only on what's on disk, so it
// runs the same in CI, in a plain checkout, or in a non-git environment.
func TestNoDrift(t *testing.T) {
	committedPath, err := filepath.Abs(committedOutputRel)
	if err != nil {
		t.Fatalf("resolving committed output path: %v", err)
	}
	committed, err := os.ReadFile(committedPath)
	if err != nil {
		t.Fatalf("reading committed output %s: %v\n"+
			"If internal/apidto/ts/dto.gen.ts doesn't exist yet, generate it first with:\n"+
			"  go run ./cmd/gendto", committedPath, err)
	}

	freshPath := filepath.Join(t.TempDir(), "dto.gen.ts")
	if err := Generate(freshPath); err != nil {
		t.Fatalf("regenerating TypeScript from internal/apidto: %v", err)
	}
	fresh, err := os.ReadFile(freshPath)
	if err != nil {
		t.Fatalf("reading freshly generated output: %v", err)
	}

	if string(fresh) != string(committed) {
		t.Fatalf("internal/apidto/ts/dto.gen.ts is out of date with internal/apidto's Go source.\n" +
			"Regenerate and commit it with:\n" +
			"  go run ./cmd/gendto\n" +
			"then re-run tests.")
	}
}
