// Package rpm hosts test-only coverage for the packaging helper scripts.
//
// apply-mediaroots.sh is a root-run bash helper (no bats/shellcheck is
// available in this repo's toolchain), so it is exercised here by shelling out
// to it with fixture configs and stubbed external commands, asserting on exit
// code and generated file content — the "Go test that shells out to invoke the
// script" harness sanctioned by the PT3-1..3 acceptance criteria.
//
// Testability seams the script exposes (see its header): SAKMS_NODE_CONFIG,
// SAKMS_DROPIN_DIR, SAKMS_MARKER_FILE, SAKMS_REALPATH_TIMEOUT,
// SAKMS_CONNECT_TIMEOUT env overrides, plus --generate-only and --yes flags.
// External commands (systemctl, curl, findmnt, realpath, ...) are bare PATH
// names so a stub dir prepended to PATH shadows them; no test ever reaches the
// real systemctl/sakms-node service.
package rpm

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func scriptPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	p := filepath.Join(filepath.Dir(thisFile), "apply-mediaroots.sh")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("script not found: %v", err)
	}
	return p
}

type runResult struct {
	exit   int
	stdout string
	stderr string
}

// runScript invokes the script under bash with the given extra env, args, and
// optional stdin. extraEnv entries are appended to (and override) os.Environ.
func runScript(t *testing.T, extraEnv []string, stdin string, args ...string) runResult {
	t.Helper()
	cmd := exec.Command("bash", append([]string{scriptPath(t)}, args...)...)
	cmd.Env = append(os.Environ(), extraEnv...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("failed to run script: %v (stderr: %s)", err, errb.String())
		}
		code = ee.ExitCode()
	}
	return runResult{exit: code, stdout: out.String(), stderr: errb.String()}
}

// writeConfig writes a config.json with the given mediaRoots. Pass nil to omit
// the mediaRoots key entirely (the "not set" case); an empty non-nil slice
// writes an explicit "[]" (the empty-list case).
func writeConfig(t *testing.T, dir string, roots []string) {
	t.Helper()
	cfg := map[string]any{"serverUrl": "https://x", "apiKey": "", "nodeName": "n"}
	if roots != nil {
		cfg["mediaRoots"] = roots
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// setup creates a work dir with config.json (given roots) and returns the base
// env pointing the script at it, plus the dropin/marker paths.
func setup(t *testing.T, roots []string, extra ...string) (env []string, dropin, marker string) {
	t.Helper()
	dir := t.TempDir()
	writeConfig(t, dir, roots)
	dropinDir := filepath.Join(dir, "dropin")
	dropin = filepath.Join(dropinDir, "mediaroots.conf")
	marker = filepath.Join(dir, "mediaroots-applied.json")
	env = append([]string{
		"SAKMS_NODE_CONFIG=" + filepath.Join(dir, "config.json"),
		"SAKMS_DROPIN_DIR=" + dropinDir,
		"SAKMS_MARKER_FILE=" + marker,
	}, extra...)
	return env, dropin, marker
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// writeStub writes an executable stub named `name` into binDir and returns a
// PATH= env entry placing binDir ahead of the real PATH.
func writeStubDir(t *testing.T, stubs map[string]string) string {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return "PATH=" + binDir + ":" + os.Getenv("PATH")
}

// resolvePath returns the realpath of p, matching how the script canonicalizes a
// resolvable root with `realpath -e` (so a mountinfo fixture can carry the exact
// resolved string the positive assertion compares against — RC-1).
func resolvePath(t *testing.T, p string) string {
	t.Helper()
	rp, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", p, err)
	}
	return rp
}

// escapeMountinfo applies the kernel's octal escaping of a mountinfo field-5
// value (proc_pid_mountinfo(5)): space \040, tab \011, newline \012, backslash
// \134. Building fixtures through this proves RC-1's shell-side parser reverses
// it (a space-containing resolved root would false-fail without the unescape).
func escapeMountinfo(s string) string {
	return strings.NewReplacer(
		`\`, `\134`,
		" ", `\040`,
		"\t", `\011`,
		"\n", `\012`,
	).Replace(s)
}

// writeMountinfo writes a synthetic /proc/<pid>/mountinfo whose lines carry the
// given mount points in field 5 (octal-escaped), and returns a
// SAKMS_PROC_MOUNTINFO= env entry pointing the positive assertion at it (RC-4
// seam). The other fields are plausible filler the parser ignores.
func writeMountinfo(t *testing.T, mountpoints []string) string {
	t.Helper()
	var b strings.Builder
	for i, mp := range mountpoints {
		// id parent major:minor root MOUNTPOINT opts... - fstype source superopts
		b.WriteString(fmt.Sprintf("%d 25 0:%d / %s rw,relatime shared:%d - tmpfs tmpfs rw\n",
			36+i, 50+i, escapeMountinfo(mp), i+1))
	}
	p := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return "SAKMS_PROC_MOUNTINFO=" + p
}

// writeProcRootWithOsRelease creates a fixture proc-root dir that DOES contain
// etc/os-release and returns a SAKMS_PROC_ROOT= env entry pointing the negative
// containment probe at it (RC-4 seam) — driving the containment-fail branch.
func writeProcRootWithOsRelease(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "os-release"), []byte("ID=test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return "SAKMS_PROC_ROOT=" + root
}

// assertRestoredBaseline verifies RC-2's restore-to-baseline outcome on a failed
// post-restart apply: non-zero exit, no marker written, the drop-in removed, and
// the restore's own daemon-reload + restart logged. wantRestartCount is the total
// number of `systemctl restart sakms-node` calls expected (the original + the
// single restore attempt = 2 in every post-restart branch; more would mean the
// restore looped).
func assertRestoredBaseline(t *testing.T, r runResult, dropin, marker, logPath string, wantRestartCount int) {
	t.Helper()
	if r.exit == 0 {
		t.Fatalf("expected non-zero exit on a failed post-restart apply; stderr=%s", r.stderr)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("no marker should be written on a failed apply")
	}
	if _, err := os.Stat(dropin); err == nil {
		t.Error("drop-in must be removed (restore-to-baseline), still present")
	}
	log := readFile(t, logPath)
	if !strings.Contains(log, "systemctl daemon-reload") {
		t.Error("expected a baseline systemctl daemon-reload during restore")
	}
	if n := strings.Count(log, "systemctl restart sakms-node"); n != wantRestartCount {
		t.Errorf("expected %d 'systemctl restart sakms-node' calls (original + one restore, no loop), got %d\nlog:\n%s",
			wantRestartCount, n, log)
	}
}

// ---------------------------------------------------------------------------
// PT3-1: read + validate
// ---------------------------------------------------------------------------

func TestValidation_RejectsRelativePath(t *testing.T) {
	env, dropin, _ := setup(t, []string{"relative/media"})
	r := runScript(t, env, "", "--generate-only")
	if r.exit == 0 {
		t.Fatalf("expected non-zero exit for relative path, got 0; stderr=%s", r.stderr)
	}
	if !strings.Contains(r.stderr, "absolute path") {
		t.Errorf("expected an 'absolute path' error, got: %s", r.stderr)
	}
	if _, err := os.Stat(dropin); err == nil {
		t.Error("drop-in must not be generated on validation failure")
	}
}

func TestValidation_RejectsControlChar(t *testing.T) {
	// Embedded newline with a directive-like payload — the Pre-mortem #4
	// injection case. JSON encodes the "\n" which python3 decodes back to a
	// real control character before the NUL-separated read.
	env, _, _ := setup(t, []string{"/media\nProtectSystem=false"})
	r := runScript(t, env, "", "--generate-only")
	if r.exit == 0 {
		t.Fatalf("expected non-zero exit for control character, got 0; stderr=%s", r.stderr)
	}
	if !strings.Contains(r.stderr, "control character") {
		t.Errorf("expected a 'control character' error, got: %s", r.stderr)
	}
}

func TestValidation_RejectsLiteralPercent(t *testing.T) {
	env, _, _ := setup(t, []string{"/media/%h/movies"})
	r := runScript(t, env, "", "--generate-only")
	if r.exit == 0 {
		t.Fatalf("expected non-zero exit for literal %%, got 0; stderr=%s", r.stderr)
	}
	if !strings.Contains(r.stderr, "literal '%'") {
		t.Errorf("expected a literal-%% error, got: %s", r.stderr)
	}
}

func TestValidation_RejectsEmptyList(t *testing.T) {
	env, dropin, _ := setup(t, []string{})
	r := runScript(t, env, "", "--generate-only")
	if r.exit == 0 {
		t.Fatalf("expected non-zero exit for empty mediaRoots, got 0")
	}
	if !strings.Contains(r.stderr, "empty") {
		t.Errorf("expected an 'empty' error, got: %s", r.stderr)
	}
	if _, err := os.Stat(dropin); err == nil {
		t.Error("drop-in must not be generated for an empty list")
	}
}

func TestValidation_RejectsMissingMediaRoots(t *testing.T) {
	env, _, _ := setup(t, nil) // omit the key
	r := runScript(t, env, "", "--generate-only")
	if r.exit == 0 {
		t.Fatalf("expected non-zero exit for missing mediaRoots, got 0")
	}
	if !strings.Contains(r.stderr, "not set") {
		t.Errorf("expected a 'not set' error, got: %s", r.stderr)
	}
}

func TestValidation_ResolvableEntryUsesResolvedPath(t *testing.T) {
	// A symlinked root must be emitted as its realpath'd target (canonicalization).
	//
	// Design-fork note (RC-1): this is a resolved==true root, yet it is STILL
	// emitted with the '-' non-mandatory prefix below. That is intended, not
	// incidental — every root is bound non-mandatory deliberately (availability-
	// first, Principle 5 / Decision Driver 2): a resolved root whose backing mount
	// later drops must not block the daemon from starting. RC-1's positive
	// containment assertion is what separately proves a resolved root actually
	// bound; the non-mandatory bind and that assertion are complementary.
	base := t.TempDir()
	target := filepath.Join(base, "real-movies")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link-movies")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	env, dropin, _ := setup(t, []string{link})
	r := runScript(t, env, "", "--generate-only")
	if r.exit != 0 {
		t.Fatalf("expected exit 0 for a resolvable symlinked root, got %d; stderr=%s", r.exit, r.stderr)
	}
	content := readFile(t, dropin)
	if !strings.Contains(content, `BindReadOnlyPaths="-`+target+`"`) {
		t.Errorf("expected the resolved target %q in the drop-in, got:\n%s", target, content)
	}
	if strings.Contains(content, link) {
		t.Errorf("drop-in must bind the resolved target, not the symlink path %q", link)
	}
}

func TestValidation_ResolvedPathControlCharHardRejected(t *testing.T) {
	// Pre-mortem #4 via the resolution step: the raw config entry is clean, but
	// it is a symlink whose on-disk target NAME contains a newline. realpath
	// yields that attacker-named target; re-validation of the resolved value
	// must hard-reject (a newline would otherwise inject a fresh directive line).
	base := t.TempDir()
	evil := filepath.Join(base, "ev\nProtectSystem=false")
	if err := os.Mkdir(evil, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "clean-link")
	if err := os.Symlink(evil, link); err != nil {
		t.Fatal(err)
	}
	env, dropin, _ := setup(t, []string{link})
	r := runScript(t, env, "", "--generate-only")
	if r.exit == 0 {
		t.Fatalf("expected hard-reject when the resolved target name has a control char, got 0")
	}
	if !strings.Contains(r.stderr, "resolved path contains a control character") {
		t.Errorf("expected a resolved-path control-char error, got: %s", r.stderr)
	}
	if _, err := os.Stat(dropin); err == nil {
		t.Error("drop-in must not be generated when the resolved path is rejected")
	}
}

func TestValidation_TransientlyUnresolvableKeptNonMandatory(t *testing.T) {
	// A non-existent path (down network mount) is kept raw and bound
	// non-mandatory rather than aborting the apply. The '-' prefix here is the
	// availability-first design decision (RC-1): a down mount must not block unit
	// start. Such a resolved==false root is EXCLUDED from RC-1's positive
	// containment assertion precisely because it is legitimately expected to be
	// absent from the namespace.
	missing := filepath.Join(t.TempDir(), "down-mount-root")
	env, dropin, _ := setup(t, []string{missing})
	r := runScript(t, env, "", "--generate-only")
	if r.exit != 0 {
		t.Fatalf("a transiently-unresolvable root must not abort the apply; exit=%d stderr=%s", r.exit, r.stderr)
	}
	if !strings.Contains(readFile(t, dropin), `BindReadOnlyPaths="-`+missing+`"`) {
		t.Errorf("expected the raw non-mandatory bind for %q", missing)
	}
}

func TestValidation_EnotdirHardRejected(t *testing.T) {
	// A path under a regular file (ENOTDIR): exists-check fails but this is a
	// real config mistake, not a transient down-mount — hard-reject. This case
	// is uid-independent (unlike EACCES below).
	base := t.TempDir()
	file := filepath.Join(base, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, dropin, _ := setup(t, []string{filepath.Join(file, "sub")})
	r := runScript(t, env, "", "--generate-only")
	if r.exit == 0 {
		t.Fatalf("expected hard-reject (non-zero) for an ENOTDIR entry, got 0")
	}
	if !strings.Contains(r.stderr, "inaccessible or not a directory") {
		t.Errorf("expected an inaccessible/not-a-directory error, got: %s", r.stderr)
	}
	if _, err := os.Stat(dropin); err == nil {
		t.Error("drop-in must not be generated when an entry is hard-rejected")
	}
}

func TestValidation_EaccesHardRejected(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root bypasses search-permission; EACCES is unreproducible")
	}
	base := t.TempDir()
	noacc := filepath.Join(base, "noacc")
	if err := os.MkdirAll(filepath.Join(noacc, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(noacc, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(noacc, 0o755) })
	env, _, _ := setup(t, []string{filepath.Join(noacc, "target")})
	r := runScript(t, env, "", "--generate-only")
	if r.exit == 0 {
		t.Fatalf("expected hard-reject (non-zero) for an EACCES entry, got 0")
	}
	if !strings.Contains(r.stderr, "inaccessible or not a directory") {
		t.Errorf("expected an inaccessible/not-a-directory error, got: %s", r.stderr)
	}
}

func TestValidation_RealpathTimeoutTreatedAsEnoent(t *testing.T) {
	// A realpath that hangs (stale-but-mounted unresponsive share) must trip
	// its bounded timeout and be treated as ENOENT (kept raw), not abort. Kept
	// raw means resolved==false and bound non-mandatory ('-') — the same
	// availability-first design decision (RC-1) as the down-mount case above; it
	// is likewise excluded from RC-1's positive containment assertion.
	stubPATH := writeStubDir(t, map[string]string{
		"realpath": "#!/bin/bash\nsleep 3\n",
	})
	hanging := "/mnt/hanging-share/movies"
	env, dropin, _ := setup(t, []string{hanging}, "SAKMS_REALPATH_TIMEOUT=1", stubPATH)
	r := runScript(t, env, "", "--generate-only")
	if r.exit != 0 {
		t.Fatalf("a realpath timeout must be treated as ENOENT (kept raw), not abort; exit=%d stderr=%s", r.exit, r.stderr)
	}
	if !strings.Contains(readFile(t, dropin), `BindReadOnlyPaths="-`+hanging+`"`) {
		t.Errorf("expected the raw non-mandatory bind for the hung path %q", hanging)
	}
}

// ---------------------------------------------------------------------------
// PT3-2: drop-in generation
// ---------------------------------------------------------------------------

// findmntStub returns stub bodies for findmnt (fixed TARGET/FSTYPE) so mount
// topology can be simulated without real mounts.
func findmntStub(target, fstype string) map[string]string {
	return map[string]string{
		"findmnt": "#!/bin/bash\n" +
			"# args: -no TARGET|FSTYPE --target <path>\n" +
			"case \"$2\" in\n" +
			"  TARGET) echo '" + target + "' ;;\n" +
			"  FSTYPE) echo '" + fstype + "' ;;\n" +
			"esac\n",
	}
}

func TestGeneration_AfterUnderUnitNotService(t *testing.T) {
	stubPATH := writeStubDir(t, findmntStub("/mnt/Media-NAS", "cifs"))
	env, dropin, _ := setup(t, []string{"/mnt/Media-NAS/Movies"}, stubPATH)
	r := runScript(t, env, "", "--generate-only")
	if r.exit != 0 {
		t.Fatalf("exit=%d stderr=%s", r.exit, r.stderr)
	}
	content := readFile(t, dropin)
	unitIdx := strings.Index(content, "[Unit]")
	svcIdx := strings.Index(content, "[Service]")
	afterIdx := strings.Index(content, "After=")
	if unitIdx < 0 || svcIdx < 0 || afterIdx < 0 {
		t.Fatalf("missing section/After in drop-in:\n%s", content)
	}
	if !(unitIdx < afterIdx && afterIdx < svcIdx) {
		t.Errorf("After= must land under [Unit] (before [Service]); unit=%d after=%d svc=%d\n%s",
			unitIdx, afterIdx, svcIdx, content)
	}
	// The mount unit must be derived from the backing mount, not the subdir path.
	if !strings.Contains(content, `After=mnt-Media\x2dNAS.mount`) {
		t.Errorf("expected After derived from the CIFS mount, got:\n%s", content)
	}
	if strings.Contains(content, `mnt-Media\x2dNAS-Movies.mount`) {
		t.Errorf("After must not be escaped from the subdir path itself:\n%s", content)
	}
}

func TestGeneration_RootIsMountpoint(t *testing.T) {
	stubPATH := writeStubDir(t, findmntStub("/data", "ext4"))
	env, dropin, _ := setup(t, []string{"/data"}, stubPATH)
	r := runScript(t, env, "", "--generate-only")
	if r.exit != 0 {
		t.Fatalf("exit=%d stderr=%s", r.exit, r.stderr)
	}
	if !strings.Contains(readFile(t, dropin), "After=data.mount") {
		t.Errorf("expected After=data.mount for a root that IS a mountpoint")
	}
}

func TestGeneration_NoBackingMountNoAfter(t *testing.T) {
	// findmnt resolving to "/" => a plain local dir under the root fs => no After=.
	stubPATH := writeStubDir(t, findmntStub("/", "ext4"))
	env, dropin, _ := setup(t, []string{"/srv/media"}, stubPATH)
	r := runScript(t, env, "", "--generate-only")
	if r.exit != 0 {
		t.Fatalf("exit=%d stderr=%s", r.exit, r.stderr)
	}
	if strings.Contains(readFile(t, dropin), "After=") {
		t.Errorf("no After= line should be emitted when the backing mount is '/'")
	}
}

func TestGeneration_DedupesDistinctMounts(t *testing.T) {
	// Two roots under the same backing mount => a single After= line.
	stubPATH := writeStubDir(t, findmntStub("/mnt/Media-NAS", "cifs"))
	env, dropin, _ := setup(t, []string{"/mnt/Media-NAS/Movies", "/mnt/Media-NAS/TV"}, stubPATH)
	r := runScript(t, env, "", "--generate-only")
	if r.exit != 0 {
		t.Fatalf("exit=%d stderr=%s", r.exit, r.stderr)
	}
	if n := strings.Count(readFile(t, dropin), "After="); n != 1 {
		t.Errorf("expected exactly one After= line for two roots on the same mount, got %d", n)
	}
}

func TestGeneration_AutomountSuffix(t *testing.T) {
	stubPATH := writeStubDir(t, findmntStub("/mnt/auto", "autofs"))
	env, dropin, _ := setup(t, []string{"/mnt/auto/media"}, stubPATH)
	r := runScript(t, env, "", "--generate-only")
	if r.exit != 0 {
		t.Fatalf("exit=%d stderr=%s", r.exit, r.stderr)
	}
	if !strings.Contains(readFile(t, dropin), "After=mnt-auto.automount") {
		t.Errorf("expected an .automount suffix for an autofs-backed mount")
	}
}

func TestGeneration_FullDirectiveSet(t *testing.T) {
	stubPATH := writeStubDir(t, findmntStub("/", "ext4"))
	env, dropin, _ := setup(t, []string{"/srv/media"}, stubPATH)
	if r := runScript(t, env, "", "--generate-only"); r.exit != 0 {
		t.Fatalf("exit=%d stderr=%s", r.exit, r.stderr)
	}
	content := readFile(t, dropin)
	required := []string{
		"ProtectSystem=false",
		"\nReadWritePaths=\n", // empty reset, not the base's /etc/sakms-node value
		"TemporaryFileSystem=/:ro",
		"PrivateDevices=yes",
		"ProtectProc=invisible",
		"PrivateTmp=yes",
		"BindPaths=/etc/sakms-node",
		"BindReadOnlyPaths=/etc/resolv.conf",
		"BindReadOnlyPaths=/etc/hosts",
		"BindReadOnlyPaths=/etc/nsswitch.conf",
		"BindReadOnlyPaths=/etc/pki",
		"BindReadOnlyPaths=/etc/ssl",
		"BindReadOnlyPaths=/usr/bin/sakms-node",
		"NoNewPrivileges=yes",
		"RestrictNamespaces=~user",
		"SystemCallFilter=~@mount",
		"\nCapabilityBoundingSet=\n",
	}
	for _, want := range required {
		if !strings.Contains(content, want) {
			t.Errorf("drop-in missing required directive %q\n---\n%s", want, content)
		}
	}
}

// TestGeneration_SpaceContainingPathVerifies asserts a space-containing root is
// double-quoted AND that the generated directives parse under a real
// systemd-analyze verify with no "unknown key" warning and no whitespace split
// (which would surface as a "not absolute" complaint on the second token).
func TestGeneration_SpaceContainingPathVerifies(t *testing.T) {
	stubPATH := writeStubDir(t, findmntStub("/mnt/Media-NAS", "cifs"))
	env, dropin, _ := setup(t, []string{"/mnt/Media-NAS/TV Shows"}, stubPATH)
	if r := runScript(t, env, "", "--generate-only"); r.exit != 0 {
		t.Fatalf("exit=%d stderr=%s", r.exit, r.stderr)
	}
	content := readFile(t, dropin)
	if !strings.Contains(content, `BindReadOnlyPaths="-/mnt/Media-NAS/TV Shows"`) {
		t.Fatalf("space-containing path must be double-quoted:\n%s", content)
	}

	if _, err := exec.LookPath("systemd-analyze"); err != nil {
		t.Skip("systemd-analyze unavailable; quoting asserted structurally above")
	}
	// Build a synthetic merged unit (base service + drop-in appended; systemd
	// tolerates repeated sections) in an isolated SYSTEMD_UNIT_PATH so verify
	// does not drag in real host units, and assert on the absence of the two
	// specific complaints — NOT on exit 0 (an unknown User=/missing ExecStart
	// binary yields unrelated warnings that are not our regression).
	baseUnit := readFile(t, filepath.Join(filepath.Dir(scriptPath(t)), "sakms-node.service"))
	verifyDir := t.TempDir()
	merged := baseUnit + "\n" + content
	if err := os.WriteFile(filepath.Join(verifyDir, "sakms-node.service"), []byte(merged), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("systemd-analyze", "verify", "sakms-node.service")
	cmd.Env = append(os.Environ(), "SYSTEMD_UNIT_PATH="+verifyDir)
	outB, _ := cmd.CombinedOutput()
	out := string(outB)
	if strings.Contains(strings.ToLower(out), "unknown key") {
		t.Errorf("systemd-analyze verify reported an unknown key:\n%s", out)
	}
	if strings.Contains(out, "not absolute") {
		t.Errorf("space-containing path split into a non-absolute token (quoting failed):\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// PT3-3: confirmation + restart + verification (stubbed systemctl/curl)
// ---------------------------------------------------------------------------

// systemctlStub logs each invocation and returns configurable exit codes via
// STUB_* env vars. MainPID is a high, almost-certainly-unused pid so the
// host-side /proc/<pid>/root/etc/os-release containment probe passes.
const systemctlStub = `#!/bin/bash
echo "systemctl $*" >> "$SYSTEMCTL_LOG"
case "$1" in
  daemon-reload) exit ${STUB_RELOAD_RC:-0} ;;
  restart)       exit ${STUB_RESTART_RC:-0} ;;
  is-active)     exit ${STUB_ISACTIVE_RC:-0} ;;
  cat)           echo "# merged unit (stub)"; exit 0 ;;
  show)          echo "${STUB_MAINPID:-999999}"; exit 0 ;;
  *)             exit 0 ;;
esac
`

const curlConnectedStub = `#!/bin/bash
echo '{"state":"connected"}'
exit 0
`

// curlNotConnectedStub never reports "connected", so the bounded function-check
// poll times out — driving the :392 branch.
const curlNotConnectedStub = `#!/bin/bash
echo '{"state":"connecting"}'
exit 0
`

// integrationStubs installs systemctl + curl stubs and returns the PATH= env
// entry that shadows the real binaries. The SYSTEMCTL_LOG path is threaded via
// env by the caller.
func integrationStubs(t *testing.T) string {
	return integrationStubsWithCurl(t, curlConnectedStub)
}

// integrationStubsWithCurl is integrationStubs with a caller-chosen curl body.
func integrationStubsWithCurl(t *testing.T, curlBody string) string {
	return writeStubDir(t, map[string]string{
		"systemctl": systemctlStub,
		"curl":      curlBody,
	})
}

func TestIntegration_SuccessWritesMarker(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "systemctl.log")
	stubPATH := integrationStubs(t)
	// One resolvable root + one down (raw) root, to assert both marker flags.
	base := t.TempDir()
	live := filepath.Join(base, "live")
	if err := os.Mkdir(live, 0o755); err != nil {
		t.Fatal(err)
	}
	down := filepath.Join(base, "down")
	// The resolved (live) root must appear in the sandbox mountinfo for RC-1's
	// positive containment assertion to pass; the down root is resolved==false and
	// excluded from that assertion.
	mountinfo := writeMountinfo(t, []string{resolvePath(t, live)})
	env, dropin, marker := setup(t, []string{live, down},
		stubPATH, "SYSTEMCTL_LOG="+logPath, "SAKMS_CONNECT_TIMEOUT=5", mountinfo)
	r := runScript(t, env, "", "--yes")
	if r.exit != 0 {
		t.Fatalf("expected success exit 0, got %d; stderr=%s", r.exit, r.stderr)
	}
	if _, err := os.Stat(dropin); err != nil {
		t.Errorf("drop-in should exist after a successful apply: %v", err)
	}
	// Marker shape + content.
	var m struct {
		AppliedAt string `json:"appliedAt"`
		Roots     []struct {
			Path     string `json:"path"`
			Resolved bool   `json:"resolved"`
		} `json:"roots"`
	}
	if err := json.Unmarshal([]byte(readFile(t, marker)), &m); err != nil {
		t.Fatalf("marker is not valid JSON: %v", err)
	}
	if m.AppliedAt == "" {
		t.Error("marker appliedAt is empty")
	}
	if len(m.Roots) != 2 {
		t.Fatalf("expected 2 roots in marker, got %d", len(m.Roots))
	}
	byPath := map[string]bool{}
	for _, rt := range m.Roots {
		byPath[rt.Path] = rt.Resolved
	}
	if !byPath[live] {
		t.Errorf("live root %q should be marked resolved=true", live)
	}
	if got, ok := byPath[down]; !ok || got {
		t.Errorf("down root %q should be present with resolved=false", down)
	}
	// Marker mode is 0640.
	fi, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o640 {
		t.Errorf("marker mode = %o, want 0640", fi.Mode().Perm())
	}
	// The restart actually happened.
	if !strings.Contains(readFile(t, logPath), "systemctl restart sakms-node") {
		t.Error("expected systemctl restart to be invoked")
	}
}

func TestIntegration_RestartFailureNonZeroExit(t *testing.T) {
	// RC-2 (:352 branch): a failed post-restart `systemctl restart` must not just
	// exit non-zero — it must RESTORE BASELINE (remove the drop-in, daemon-reload,
	// restart the daemon back onto its base unit). With the stateless stub,
	// STUB_RESTART_RC=1 fails both the original restart and the restore's own
	// restart, so this also exercises the restore-of-restore print-and-exit path
	// asserted separately in TestIntegration_RestoreOfRestoreFailsPrintsAndExits;
	// here we assert the baseline-restore side effects (drop-in gone, exactly one
	// restore attempt — no loop).
	logPath := filepath.Join(t.TempDir(), "systemctl.log")
	stubPATH := integrationStubs(t)
	base := t.TempDir()
	live := filepath.Join(base, "live")
	if err := os.Mkdir(live, 0o755); err != nil {
		t.Fatal(err)
	}
	env, dropin, marker := setup(t, []string{live},
		stubPATH, "SYSTEMCTL_LOG="+logPath, "STUB_RESTART_RC=1")
	r := runScript(t, env, "", "--yes")
	// original :352 restart (1) + single restore restart (1) = 2, no loop.
	assertRestoredBaseline(t, r, dropin, marker, logPath, 2)
}

func TestIntegration_DaemonReloadFailureNonZeroExit(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "systemctl.log")
	stubPATH := integrationStubs(t)
	base := t.TempDir()
	live := filepath.Join(base, "live")
	if err := os.Mkdir(live, 0o755); err != nil {
		t.Fatal(err)
	}
	env, dropin, marker := setup(t, []string{live},
		stubPATH, "SYSTEMCTL_LOG="+logPath, "STUB_RELOAD_RC=1")
	r := runScript(t, env, "", "--yes")
	if r.exit == 0 {
		t.Fatalf("expected non-zero exit when daemon-reload fails")
	}
	// The trial reload fails before confirmation → drop-in is cleaned up.
	if _, err := os.Stat(dropin); err == nil {
		t.Error("drop-in should be removed when the trial daemon-reload fails")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("no marker should be written when daemon-reload fails")
	}
}

func TestIntegration_DeclineRemovesDropinNoRestart(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "systemctl.log")
	stubPATH := integrationStubs(t)
	base := t.TempDir()
	live := filepath.Join(base, "live")
	if err := os.Mkdir(live, 0o755); err != nil {
		t.Fatal(err)
	}
	env, dropin, marker := setup(t, []string{live}, stubPATH, "SYSTEMCTL_LOG="+logPath)
	// No --yes; answer "n" at the confirmation prompt.
	r := runScript(t, env, "n\n")
	if r.exit == 0 {
		t.Fatalf("expected non-zero exit when the operator declines")
	}
	if _, err := os.Stat(dropin); err == nil {
		t.Error("drop-in must be removed when the operator declines")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("no marker should be written when the operator declines")
	}
	if strings.Contains(readFile(t, logPath), "restart") {
		t.Error("systemctl restart must not run when the operator declines")
	}
}

// ---------------------------------------------------------------------------
// RC-2: restore-to-baseline on every post-restart failure branch
// RC-1: positive procfs containment assertion (scoped to resolved==true roots)
// ---------------------------------------------------------------------------

// TestIntegration_ContainmentFailRestoresBaseline drives the :365 branch (the
// PM-1 below-baseline regression) via the RC-4 SAKMS_PROC_ROOT seam: the negative
// containment probe is pointed at a fixture where etc/os-release IS visible, so
// the sandbox looks un-contained. This MUST FAIL if RC-2 is not implemented (a
// bare `die` would leave the drop-in loaded and the daemon below baseline).
func TestIntegration_ContainmentFailRestoresBaseline(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "systemctl.log")
	stubPATH := integrationStubs(t)
	base := t.TempDir()
	live := filepath.Join(base, "live")
	if err := os.Mkdir(live, 0o755); err != nil {
		t.Fatal(err)
	}
	procRoot := writeProcRootWithOsRelease(t)
	env, dropin, marker := setup(t, []string{live},
		stubPATH, "SYSTEMCTL_LOG="+logPath, procRoot, "SAKMS_CONNECT_TIMEOUT=5")
	r := runScript(t, env, "", "--yes")
	if !strings.Contains(r.stderr, "containment check FAILED") {
		t.Errorf("expected a containment-check failure message, got: %s", r.stderr)
	}
	// original restart (1) + restore restart (1) = 2, restore succeeds.
	assertRestoredBaseline(t, r, dropin, marker, logPath, 2)
}

// TestIntegration_NotActiveRestoresBaseline drives the :356 branch: the unit is
// not active after the restart. STUB_ISACTIVE_RC=1 makes `systemctl is-active`
// report inactive.
func TestIntegration_NotActiveRestoresBaseline(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "systemctl.log")
	stubPATH := integrationStubs(t)
	base := t.TempDir()
	live := filepath.Join(base, "live")
	if err := os.Mkdir(live, 0o755); err != nil {
		t.Fatal(err)
	}
	env, dropin, marker := setup(t, []string{live},
		stubPATH, "SYSTEMCTL_LOG="+logPath, "STUB_ISACTIVE_RC=1")
	r := runScript(t, env, "", "--yes")
	if !strings.Contains(r.stderr, "not active after restart") {
		t.Errorf("expected a not-active failure message, got: %s", r.stderr)
	}
	assertRestoredBaseline(t, r, dropin, marker, logPath, 2)
}

// TestIntegration_MainPidLookupFailRestoresBaseline drives the :419-421 branch:
// the post-restart `systemctl show -p MainPID --value` lookup itself fails to
// yield a usable pid (empty or "0"), so the containment check can never even
// start. STUB_MAINPID=0 makes the stub report "0", tripping the script's own
// `[ -n "$main_pid" ] && [ "$main_pid" != "0" ]` guard.
func TestIntegration_MainPidLookupFailRestoresBaseline(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "systemctl.log")
	stubPATH := integrationStubs(t)
	base := t.TempDir()
	live := filepath.Join(base, "live")
	if err := os.Mkdir(live, 0o755); err != nil {
		t.Fatal(err)
	}
	env, dropin, marker := setup(t, []string{live},
		stubPATH, "SYSTEMCTL_LOG="+logPath, "STUB_MAINPID=0")
	r := runScript(t, env, "", "--yes")
	if !strings.Contains(r.stderr, "could not determine") {
		t.Errorf("expected a could-not-determine-MainPID failure message, got: %s", r.stderr)
	}
	assertRestoredBaseline(t, r, dropin, marker, logPath, 2)
}

// TestIntegration_FunctionCheckFailRestoresBaseline drives the :392 branch: the
// daemon comes up active AND contained, but never reports a connected state, so
// the bounded function check times out. This branch is LOWER severity than the
// containment-fail branch (the daemon was contained-and-hardened at failure time
// — stranded at/above baseline, never below it), but RC-2 still restores baseline
// so a failed apply never leaves a non-connecting daemon on the drop-in. It must
// first pass the negative AND positive containment checks, so a valid mountinfo
// fixture with the resolved root is supplied.
func TestIntegration_FunctionCheckFailRestoresBaseline(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "systemctl.log")
	stubPATH := integrationStubsWithCurl(t, curlNotConnectedStub)
	base := t.TempDir()
	live := filepath.Join(base, "live")
	if err := os.Mkdir(live, 0o755); err != nil {
		t.Fatal(err)
	}
	mountinfo := writeMountinfo(t, []string{resolvePath(t, live)})
	env, dropin, marker := setup(t, []string{live},
		stubPATH, "SYSTEMCTL_LOG="+logPath, mountinfo, "SAKMS_CONNECT_TIMEOUT=2")
	r := runScript(t, env, "", "--yes")
	if !strings.Contains(r.stderr, "function check FAILED") {
		t.Errorf("expected a function-check failure message, got: %s", r.stderr)
	}
	assertRestoredBaseline(t, r, dropin, marker, logPath, 2)
}

// TestIntegration_RestoreOfRestoreFailsPrintsAndExits: when the restore's own
// restart ALSO fails, the script prints the exact manual recovery command, exits
// non-zero, and does NOT loop. Scoped to the :352 branch (per the plan's stub
// limitation note): the stateless systemctlStub cannot give the original restart
// and the restore's restart different codes, so STUB_RESTART_RC=1 fails both
// identically — original :352 restart fails → restore fires → restore's restart
// fails the same way → print-and-exit. Coverage is sufficient because the restore
// helper is shared across all four post-restart branches, so proving the
// no-loop/print property once proves it for the shared helper.
func TestIntegration_RestoreOfRestoreFailsPrintsAndExits(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "systemctl.log")
	stubPATH := integrationStubs(t)
	base := t.TempDir()
	live := filepath.Join(base, "live")
	if err := os.Mkdir(live, 0o755); err != nil {
		t.Fatal(err)
	}
	env, dropin, marker := setup(t, []string{live},
		stubPATH, "SYSTEMCTL_LOG="+logPath, "STUB_RESTART_RC=1")
	r := runScript(t, env, "", "--yes")
	if r.exit == 0 {
		t.Fatalf("expected non-zero exit when the restore's own restart fails")
	}
	// The exact manual recovery command must be printed.
	if !strings.Contains(r.stderr, "systemctl daemon-reload; systemctl restart sakms-node") {
		t.Errorf("expected the exact manual recovery command in stderr, got: %s", r.stderr)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("no marker should be written")
	}
	if _, err := os.Stat(dropin); err == nil {
		t.Error("drop-in must be removed even when the restore's restart fails")
	}
	// No loop: exactly the original restart + one restore restart = 2.
	if n := strings.Count(readFile(t, logPath), "systemctl restart sakms-node"); n != 2 {
		t.Errorf("expected exactly 2 restart attempts (no loop), got %d", n)
	}
}

// TestIntegration_PositiveContainmentAssertion exercises RC-1's positive
// assertion via the RC-4 SAKMS_PROC_MOUNTINFO seam.
func TestIntegration_PositiveContainmentAssertion(t *testing.T) {
	newLiveRoots := func(t *testing.T, n int) (base string, roots, resolved []string) {
		base = t.TempDir()
		for i := 0; i < n; i++ {
			p := filepath.Join(base, fmt.Sprintf("root%d", i))
			if err := os.Mkdir(p, 0o755); err != nil {
				t.Fatal(err)
			}
			roots = append(roots, p)
			resolved = append(resolved, resolvePath(t, p))
		}
		return base, roots, resolved
	}

	t.Run("AllResolvedPresentPasses", func(t *testing.T) {
		logPath := filepath.Join(t.TempDir(), "systemctl.log")
		stubPATH := integrationStubs(t)
		_, roots, resolved := newLiveRoots(t, 3)
		mountinfo := writeMountinfo(t, resolved) // all three present
		env, dropin, marker := setup(t, roots,
			stubPATH, "SYSTEMCTL_LOG="+logPath, mountinfo, "SAKMS_CONNECT_TIMEOUT=5")
		r := runScript(t, env, "", "--yes")
		if r.exit != 0 {
			t.Fatalf("expected success when all resolved roots are present; exit=%d stderr=%s", r.exit, r.stderr)
		}
		if _, err := os.Stat(dropin); err != nil {
			t.Errorf("drop-in should exist after a successful apply: %v", err)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Errorf("marker should be written on success: %v", err)
		}
	})

	t.Run("TwoOfThreeFailsAllNotAny", func(t *testing.T) {
		// One resolved root missing from mountinfo => the "all, not any" bar fails.
		logPath := filepath.Join(t.TempDir(), "systemctl.log")
		stubPATH := integrationStubs(t)
		_, roots, resolved := newLiveRoots(t, 3)
		mountinfo := writeMountinfo(t, resolved[:2]) // only 2 of 3 present
		env, dropin, marker := setup(t, roots,
			stubPATH, "SYSTEMCTL_LOG="+logPath, mountinfo, "SAKMS_CONNECT_TIMEOUT=5")
		r := runScript(t, env, "", "--yes")
		if !strings.Contains(r.stderr, "positive containment assertion FAILED") {
			t.Errorf("expected a positive-assertion failure, got: %s", r.stderr)
		}
		assertRestoredBaseline(t, r, dropin, marker, logPath, 2)
	})

	t.Run("PlaceholderDirNoMountEntryFails", func(t *testing.T) {
		// The single resolved root exists on disk (a real dir — the exact
		// placeholder-directory shape systemd leaves for a skipped '-' bind) but
		// has NO mountinfo entry. A bare `[ -e ]` existence check would falsely
		// pass here; the mountinfo/mount-point check correctly fails. This is the
		// case that proves the MECHANISM, not just the wording.
		logPath := filepath.Join(t.TempDir(), "systemctl.log")
		stubPATH := integrationStubs(t)
		_, roots, _ := newLiveRoots(t, 1)
		mountinfo := writeMountinfo(t, nil) // empty mountinfo: root exists but never bound
		env, dropin, marker := setup(t, roots,
			stubPATH, "SYSTEMCTL_LOG="+logPath, mountinfo, "SAKMS_CONNECT_TIMEOUT=5")
		r := runScript(t, env, "", "--yes")
		if !strings.Contains(r.stderr, "positive containment assertion FAILED") {
			t.Errorf("expected a positive-assertion failure for a placeholder-only root, got: %s", r.stderr)
		}
		assertRestoredBaseline(t, r, dropin, marker, logPath, 2)
	})

	t.Run("SpaceContainingResolvedRootPasses", func(t *testing.T) {
		// Mirrors the Go-side TestMediaRootScopes_SpacePathOctalRoundTrip: the
		// resolved root contains a space, the mountinfo fixture carries the
		// \040-escaped form, and the assertion must PASS — proving the shell-side
		// parser octal-unescapes field 5. Without the unescape this false-fails on
		// this deployment's real paths (e.g. /mnt/Media-NAS/TV Shows).
		logPath := filepath.Join(t.TempDir(), "systemctl.log")
		stubPATH := integrationStubs(t)
		base := t.TempDir()
		spaceRoot := filepath.Join(base, "TV Shows")
		if err := os.Mkdir(spaceRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		resolved := resolvePath(t, spaceRoot)
		if !strings.Contains(resolved, " ") {
			t.Fatalf("expected the resolved root to contain a space, got %q", resolved)
		}
		mountinfo := writeMountinfo(t, []string{resolved}) // escapes the space to \040
		env, _, marker := setup(t, []string{spaceRoot},
			stubPATH, "SYSTEMCTL_LOG="+logPath, mountinfo, "SAKMS_CONNECT_TIMEOUT=5")
		r := runScript(t, env, "", "--yes")
		if r.exit != 0 {
			t.Fatalf("space-containing resolved root must pass the positive assertion; exit=%d stderr=%s", r.exit, r.stderr)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Errorf("marker should be written on success: %v", err)
		}
	})
}

// TestIntegration_PositiveAssertionExcludesUnresolvedRoots verifies RC-1's
// scoping: a resolved==false (down-at-validation) root is legitimately absent
// from the namespace and must NOT trip the positive assertion.
func TestIntegration_PositiveAssertionExcludesUnresolvedRoots(t *testing.T) {
	t.Run("DownRootExcluded", func(t *testing.T) {
		// One live (resolved==true) root present in mountinfo + one down
		// (resolved==false) root absent from it => passes: only the resolved root
		// is asserted.
		logPath := filepath.Join(t.TempDir(), "systemctl.log")
		stubPATH := integrationStubs(t)
		base := t.TempDir()
		live := filepath.Join(base, "live")
		if err := os.Mkdir(live, 0o755); err != nil {
			t.Fatal(err)
		}
		down := filepath.Join(base, "down") // never created => resolved==false
		mountinfo := writeMountinfo(t, []string{resolvePath(t, live)})
		env, _, marker := setup(t, []string{live, down},
			stubPATH, "SYSTEMCTL_LOG="+logPath, mountinfo, "SAKMS_CONNECT_TIMEOUT=5")
		r := runScript(t, env, "", "--yes")
		if r.exit != 0 {
			t.Fatalf("a down (resolved==false) root must not trip the positive assertion; exit=%d stderr=%s", r.exit, r.stderr)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Errorf("marker should be written on success: %v", err)
		}
	})

	t.Run("AllDownVacuouslySkipped", func(t *testing.T) {
		// If EVERY root is resolved==false, the positive assertion is vacuously
		// skipped (no mountinfo read at all — the default /proc/<pid>/mountinfo is
		// never opened) and only the negative check runs. Success proves the skip.
		logPath := filepath.Join(t.TempDir(), "systemctl.log")
		stubPATH := integrationStubs(t)
		down := filepath.Join(t.TempDir(), "down") // never created => resolved==false
		env, _, marker := setup(t, []string{down},
			stubPATH, "SYSTEMCTL_LOG="+logPath, "SAKMS_CONNECT_TIMEOUT=5")
		r := runScript(t, env, "", "--yes")
		if r.exit != 0 {
			t.Fatalf("an all-down root set must skip the positive assertion and succeed; exit=%d stderr=%s", r.exit, r.stderr)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Errorf("marker should be written on success: %v", err)
		}
	})
}
