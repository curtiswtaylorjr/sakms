#!/bin/bash
# apply-mediaroots.sh — Phase 2 (OS-level namespace containment) activator for
# sakms-node. Root-run, MANUAL, explicit operator action — NEVER invoked from
# an RPM scriptlet (see Decision Driver 3 / Principle 3: an existing
# Phase-1-only install must not have its sandbox silently change or restart
# during a package upgrade transaction).
#
# It reads and VALIDATES the node's operator-declared mediaRoots allowlist from
# config.json, generates a systemd drop-in that empties the daemon's root mount
# namespace (TemporaryFileSystem=/:ro) and re-exposes ONLY the media roots plus
# the minimum a network-TLS client needs, prints the merged effective unit for
# confirmation, restarts the daemon, and verifies FOUR things before declaring
# success: (a) the unit is active, (b) genuine containment (a host path outside
# the bind list is absent from the daemon's namespace, checked host-side via
# procfs), (b2) apply integrity (every realpath-resolved media root actually
# appears as a mount point inside the daemon's namespace, parsed host-side from
# /proc/<pid>/mountinfo — proves the sandbox re-bound what it claimed, not merely
# that it hid one host file), (c) actual function (the daemon reaches its server
# / reports a connected state within a bounded timeout).
#
# FAILURE BEHAVIOR (post-restart): if ANY of the post-restart checks above fails,
# the script does NOT leave the daemon stranded on a loaded-but-unconfirmed (or,
# worse, a below-baseline) drop-in. It RESTORES THE PRE-PHASE-2 BASELINE — removes
# the generated drop-in, daemon-reloads, and restarts the daemon back onto its
# Phase-1 base unit — then exits non-zero. This is attempted exactly ONCE: if the
# restore's own daemon-reload/restart also fails, the script prints the exact
# manual recovery command and exits non-zero WITHOUT looping. (A printed recovery
# command a human may never run is not recovery — the failure path actively
# restores baseline; see Principle 1 in the design review.)
#
# On a successful apply it writes a marker at
# /etc/sakms-node/mediaroots-applied.json (mode 0640, owner root:sakms-node)
# recording exactly which roots were baked into the drop-in and whether each was
# realpath-resolved (mandatory bind) or kept raw (transiently-unresolvable at
# apply time — a down network mount — bound non-mandatory). cmd/sakms-node/
# status.go consumes this marker to compute its per-root three-state
# observability field.
#
# MARKER FILE JSON SHAPE (stable contract with cmd/sakms-node/status.go):
#   {
#     "appliedAt": "2026-07-20T15:04:05Z",          // ISO-8601 UTC
#     "roots": [
#       {"path": "/mnt/Media-NAS/Movies", "resolved": true},   // realpath'd, mandatory bind
#       {"path": "/mnt/Media-NAS/Down",   "resolved": false}   // raw, non-mandatory (was down at apply)
#     ]
#   }
# "path" is the exact value baked into the drop-in's BindReadOnlyPaths= (the
# resolved path when resolved==true, the raw config value when resolved==false).
# status.go keys its mountinfo comparison off this already-resolved list; a
# SYMLINKED root is a known edge case (its resolved path won't string-match the
# raw config value inside the sandbox — direct paths, the common case, match).
#
# Testability env overrides (production defaults shown):
#   SAKMS_NODE_CONFIG      /etc/sakms-node/config.json
#   SAKMS_DROPIN_DIR       /etc/systemd/system/sakms-node.service.d
#   SAKMS_MARKER_FILE      /etc/sakms-node/mediaroots-applied.json
#   SAKMS_REALPATH_TIMEOUT 5   (seconds; bounds realpath/test against a hung mount)
#   SAKMS_CONNECT_TIMEOUT  30  (seconds; bounds the post-restart function check)
#   SAKMS_PROC_ROOT        /proc/<MainPID>/root       (negative containment probe root)
#   SAKMS_PROC_MOUNTINFO   /proc/<MainPID>/mountinfo  (positive containment probe source)
# Flags: --generate-only (validate+generate drop-in, no reload/restart/verify/
#   marker), --yes/-y (non-interactive confirmation), --help.

set -euo pipefail

CONFIG_FILE="${SAKMS_NODE_CONFIG:-/etc/sakms-node/config.json}"
DROPIN_DIR="${SAKMS_DROPIN_DIR:-/etc/systemd/system/sakms-node.service.d}"
DROPIN_FILE="$DROPIN_DIR/mediaroots.conf"
MARKER_FILE="${SAKMS_MARKER_FILE:-/etc/sakms-node/mediaroots-applied.json}"
SERVICE="sakms-node"
RP_TIMEOUT="${SAKMS_REALPATH_TIMEOUT:-5}"
CONNECT_TIMEOUT="${SAKMS_CONNECT_TIMEOUT:-30}"

ASSUME_YES=0
GENERATE_ONLY=0

die() {
	echo "apply-mediaroots: $*" >&2
	exit 1
}

usage() {
	sed -n '2,60p' "$0" | sed 's/^# \{0,1\}//'
	exit 0
}

while [ $# -gt 0 ]; do
	case "$1" in
	-y | --yes) ASSUME_YES=1 ;;
	--generate-only) GENERATE_ONLY=1 ;;
	-h | --help) usage ;;
	*) die "unknown argument: $1 (see --help)" ;;
	esac
	shift
done

command -v python3 >/dev/null 2>&1 ||
	die "python3 is required to parse $CONFIG_FILE"
[ -f "$CONFIG_FILE" ] || die "config not found: $CONFIG_FILE"

# --- Step 1a: read mediaRoots as a NUL-separated list ------------------------
# NUL separation is load-bearing (Pre-mortem #4): a newline-delimited read would
# let an injected embedded-newline mediaRoots value split into two entries, each
# half individually passing validation — defeating the exact injection defense
# this validation exists to provide. python3 emits one NUL-terminated record per
# array element with no transformation; the control-character check below then
# runs on each raw entry (catching any embedded newline as a rejected control
# char). python3 also rejects a missing / non-array / non-string / empty
# mediaRoots up front.
read_roots_py='
import json, sys
try:
    with open(sys.argv[1], "r") as f:
        cfg = json.load(f)
except Exception as e:
    sys.stderr.write("config parse error: %s\n" % e)
    sys.exit(1)
roots = cfg.get("mediaRoots")
if roots is None:
    sys.stderr.write("mediaRoots is not set in config\n")
    sys.exit(1)
if not isinstance(roots, list):
    sys.stderr.write("mediaRoots must be a JSON array\n")
    sys.exit(1)
if len(roots) == 0:
    sys.stderr.write("mediaRoots is empty (refusing to apply a drop-in that binds nothing)\n")
    sys.exit(1)
for r in roots:
    if not isinstance(r, str):
        sys.stderr.write("mediaRoots entries must be strings\n")
        sys.exit(1)
    sys.stdout.write(r)
    sys.stdout.write("\0")
'
# Route NUL-separated output through a temp file, NOT command substitution —
# $(...) strips NUL bytes, which would collapse the whole list. The temp file
# preserves both python3's exit code and the NUL delimiters.
roots_tmp="$(mktemp)"
trap 'rm -f "$roots_tmp"' EXIT
python3 -c "$read_roots_py" "$CONFIG_FILE" >"$roots_tmp" ||
	die "could not read a valid mediaRoots list from $CONFIG_FILE"

RAW_ROOTS=()
while IFS= read -r -d '' entry; do
	RAW_ROOTS+=("$entry")
done <"$roots_tmp"

[ "${#RAW_ROOTS[@]}" -gt 0 ] ||
	die "mediaRoots is empty (refusing to apply a drop-in that binds nothing)"

# --- Step 1a: validate + resolve each entry ----------------------------------
# Parallel arrays: BAKED_PATHS[i] is the exact value emitted into the drop-in;
# BAKED_RESOLVED[i] is 1 (realpath-resolved, mandatory) or 0 (raw, non-mandatory).
BAKED_PATHS=()
BAKED_RESOLVED=()

for entry in "${RAW_ROOTS[@]}"; do
	# Absolute path.
	case "$entry" in
	/*) : ;;
	*) die "mediaRoots entry is not an absolute path: $(printf '%q' "$entry")" ;;
	esac
	# No control characters (also catches embedded newline/tab — Pre-mortem #4).
	case "$entry" in
	*[[:cntrl:]]*) die "mediaRoots entry contains a control character (rejected): $(printf '%q' "$entry")" ;;
	esac
	# No literal '%' — systemd expands %-specifiers at unit load, so a literal
	# '%' is attacker-steerable content once it reaches a root-loaded unit.
	# Reject outright rather than escaping (an escaping bug would reopen exactly
	# this injection class).
	case "$entry" in
	*%*) die "mediaRoots entry contains a literal '%' (rejected — systemd specifier-expansion risk): $entry" ;;
	esac
	# No ':' or '"' (Architect final-review finding, BLOCKING — confirmed
	# empirically). Double-quoting on emit (below) defeats whitespace-splitting
	# but NOT these two: ':' is BindReadOnlyPaths='s source:destination
	# separator (systemd.exec(5)), so an entry like "/etc/shadow:/mnt/x" is
	# emitted verbatim as BindReadOnlyPaths="-/etc/shadow:/mnt/x" and binds
	# /etc/shadow into the sandbox — a direct bypass of this feature's entire
	# containment invariant by the exact compromised-node-process threat it
	# exists to contain. An embedded '"' would similarly break out of the
	# emitted quoting. Reject outright, same posture as '%' — these characters
	# have no legitimate reason to appear in a media-root path, and escaping
	# is exactly the kind of subtle-if-done-wrong fix this validation must not
	# risk getting wrong.
	case "$entry" in
	*:* | *'"'*) die "mediaRoots entry contains a ':' or '\"' (rejected — systemd bind-mount syntax injection risk): $entry" ;;
	esac

	# Canonicalize with a bounded realpath. On success, use the resolved path
	# (full symlink-hardening). On failure we must distinguish ENOENT
	# (transiently unresolvable — e.g. a down network mount — keep raw, bind
	# non-mandatory) from EACCES/ENOTDIR (a real config mistake — hard-reject).
	#
	# NOTE ON MECHANISM (deviation from the plan's literal `test -e` example,
	# for a verified correctness reason): `test -e` returns false IDENTICALLY
	# for ENOENT, ENOTDIR, and EACCES, so it cannot separate the keep-raw case
	# from the hard-reject case the acceptance criteria require. realpath's own
	# errno message (forced to canonical English via LC_ALL=C) is the reliable
	# discriminator: "No such file or directory" → ENOENT (keep raw); anything
	# else (Permission denied / Not a directory / …) → hard-reject. A realpath
	# that HANGS (stale-but-mounted unresponsive share) trips its bounded
	# timeout (exit 124) and is treated as ENOENT — a transient condition must
	# not abort the whole apply (Decision Driver 2).
	rp_err_tmp="$(mktemp)"
	if resolved="$(LC_ALL=C timeout "$RP_TIMEOUT" realpath -e -- "$entry" 2>"$rp_err_tmp")"; then
		rm -f "$rp_err_tmp"
		# Re-validate the RESOLVED value, not only the raw entry (Pre-mortem #4,
		# reached via resolution rather than the raw string): the compromised
		# node process owns /etc/sakms-node and can plant a symlink whose on-disk
		# target NAME contains a newline or '%'. The raw entry is clean and
		# passes above, but realpath then yields the attacker-named target.
		# Double-quoting on emit stops space-splitting but NOT a newline — a
		# newline terminates the directive line and the next line parses as a
		# fresh root-loaded directive. Refuse outright, never sanitize-and-continue.
		case "$resolved" in
		*[[:cntrl:]]* | *%* | *:* | *'"'*) die "resolved path contains a control character, '%', ':', or '\"' (rejected): $(printf '%q' "$resolved")" ;;
		esac
		BAKED_PATHS+=("$resolved")
		BAKED_RESOLVED+=(1)
	else
		rp_rc=$?
		rp_err="$(cat "$rp_err_tmp")"
		rm -f "$rp_err_tmp"
		if [ "$rp_rc" -eq 124 ] || printf '%s' "$rp_err" | grep -q "No such file or directory"; then
			# Genuinely not present (or realpath timed out) → keep raw, non-mandatory.
			BAKED_PATHS+=("$entry")
			BAKED_RESOLVED+=(0)
		else
			# Exists but inaccessible or not a directory — a real mistake, not a
			# transient down-mount. Hard-reject the whole operation.
			die "mediaRoots entry exists but is inaccessible or not a directory (rejected): $entry (${rp_err#realpath: })"
		fi
	fi
done

# --- Step 1b: derive After= mount/automount units via findmnt ----------------
# For each baked path, find its ACTUAL backing mountpoint (media roots in this
# deployment are subdirectories of a mount, not mountpoints themselves) and
# escape THAT — never the root path directly. A path whose backing mount is "/"
# emits no After= line (nothing to order against). Dedupe distinct units.
AFTER_UNITS=()
after_seen=""
for path in "${BAKED_PATHS[@]}"; do
	mp="$(findmnt -no TARGET --target "$path" 2>/dev/null || true)"
	[ -n "$mp" ] || continue
	[ "$mp" = "/" ] && continue
	fstype="$(findmnt -no FSTYPE --target "$path" 2>/dev/null || true)"
	suffix=mount
	[ "$fstype" = "autofs" ] && suffix=automount
	unit="$(systemd-escape -p --suffix="$suffix" "$mp")"
	case "$after_seen" in
	*"|$unit|"*) : ;; # already emitted
	*)
		AFTER_UNITS+=("$unit")
		after_seen="${after_seen}|$unit|"
		;;
	esac
done

# --- Step 1b: generate the drop-in -------------------------------------------
mkdir -p "$DROPIN_DIR"
{
	echo "# Generated by apply-mediaroots.sh — do not edit by hand; re-run the"
	echo "# script after changing mediaRoots in config.json. See the script header"
	echo "# for the full rationale and the systemd#18999 hazard this file avoids."
	echo ""
	echo "[Unit]"
	# After= MUST live under [Unit]. A [Unit] directive found under [Service] is
	# silently ignored by systemd ("Unknown key ... in section 'Service'") — a
	# no-op, not an error. Ordering only (no Requires=): a down/failed mount must
	# not block the daemon from starting (Decision Driver 2).
	if [ "${#AFTER_UNITS[@]}" -gt 0 ]; then
		printf 'After=%s\n' "${AFTER_UNITS[*]}"
	fi
	echo ""
	echo "[Service]"
	# Neutralize the base unit's ProtectSystem=full / ReadWritePaths=/etc/sakms-node
	# BEFORE applying the tmpfs allowlist. ProtectSystem= is a scalar (overridden
	# by direct reassignment to false — NOT an empty value); ReadWritePaths= is a
	# list (reset via empty assignment). DO NOT re-add ProtectSystem=strict/full
	# alongside TemporaryFileSystem= — systemd#18999: the combination silently
	# reintroduces the real host root under the intended empty tmpfs.
	echo "ProtectSystem=false"
	echo "ReadWritePaths="
	echo ""
	echo "TemporaryFileSystem=/:ro"
	echo ""
	# TemporaryFileSystem=/ also blanks /dev and /proc; restore them via these
	# directives (which double as hardening). /proc present here is what
	# status.go's /proc/self/mountinfo check relies on.
	echo "PrivateDevices=yes"
	echo "ProtectProc=invisible"
	echo "PrivateTmp=yes"
	echo ""
	# Minimum a CGO_ENABLED=0 network-TLS client needs with an emptied root:
	# DNS (resolv.conf/hosts/nsswitch), the CA trust store (dir binds, robust to
	# distro path), and its own binary. Considered starting point, not asserted
	# complete — the deferred E2E proves real connectivity under this sandbox.
	echo "BindPaths=/etc/sakms-node"
	echo "BindReadOnlyPaths=/etc/resolv.conf"
	echo "BindReadOnlyPaths=/etc/hosts"
	echo "BindReadOnlyPaths=/etc/nsswitch.conf"
	echo "BindReadOnlyPaths=/etc/pki"
	echo "BindReadOnlyPaths=/etc/ssl"
	echo "BindReadOnlyPaths=/usr/bin/sakms-node"
	echo ""
	# Media-root binds. Every path is double-quoted (systemd.syntax(7) quoting)
	# so a space-containing path (e.g. "/mnt/Media-NAS/TV Shows") does not split
	# into two tokens. The '-' non-mandatory prefix (inside the quotes) is applied
	# to EVERY root deliberately, resolved and unresolved alike — availability-first
	# (Principle 5 / Decision Driver 2): a resolved root whose backing mount later
	# drops must NOT block the daemon from starting. The cost of this choice is
	# owned honestly: a resolved==true root whose mount drops inside the narrow
	# apply-verify window will be absent from the namespace and correctly false-fail
	# RC-1's positive assertion (b2), tearing this apply down to baseline (PM-5,
	# severity MINOR — re-run once the mount is back). RC-1's positive assertion is
	# therefore scoped to resolved==true roots only; a resolved==false root is
	# expected to be absent and is excluded from that check.
	for i in "${!BAKED_PATHS[@]}"; do
		printf 'BindReadOnlyPaths="-%s"\n' "${BAKED_PATHS[$i]}"
	done
	echo ""
	# Tamper-resistance against a compromised process trying to escape via its
	# own mount/namespace syscalls. SystemCallFilter=~@mount is the load-bearing
	# control (not capability-gated, survives a nested user namespace);
	# RestrictNamespaces=~user closes the unshare(CLONE_NEWUSER) bypass at source;
	# NoNewPrivileges makes the seccomp filter non-removable; CapabilityBoundingSet=
	# drops all capabilities (this is a read-only worker).
	echo "NoNewPrivileges=yes"
	echo "RestrictNamespaces=~user"
	echo "SystemCallFilter=~@mount"
	echo "CapabilityBoundingSet="
} >"$DROPIN_FILE"

echo "apply-mediaroots: generated $DROPIN_FILE" >&2

if [ "$GENERATE_ONLY" -eq 1 ]; then
	echo "$DROPIN_FILE"
	exit 0
fi

# --- Step 1c: print merged effective unit + require confirmation -------------
# Trial daemon-reload so `systemctl cat` reflects the new drop-in. This handles
# ONLY the PRE-confirmation aborts (a failed trial reload, a failed `systemctl
# cat`, or the operator declining at the prompt): the daemon has NOT been
# restarted yet, so it is still on its baseline unit, and all this path must do
# is remove the drop-in and reload it away — otherwise the next unrelated restart
# / RPM %systemd_postun_with_restart would silently activate an unconfirmed
# sandbox (violating Principle 3). POST-restart failures are NOT handled here —
# by then the daemon is already running on the drop-in, so they go through
# restore_baseline() below (which additionally restarts the daemon back onto the
# baseline unit), not abort_unapplied().
abort_unapplied() {
	rm -f "$DROPIN_FILE"
	systemctl daemon-reload || true
	die "$1"
}

# restore_baseline is the POST-restart failure path (RC-2). By the time any of
# the four post-restart checks (restart succeeded / active / contained / function)
# can fail, the drop-in is ALREADY loaded and the daemon has ALREADY restarted
# into it — with ProtectSystem=false + ReadWritePaths= already neutralizing the
# Phase-1 hardening. A bare `die` here would strand the daemon on a
# loaded-but-unconfirmed drop-in (and in the containment-fail case, one that is
# strictly BELOW the pre-Phase-2 baseline — see Pre-mortem PM-1). Instead, remove
# the drop-in, daemon-reload, and restart the daemon back onto its Phase-1 base
# unit, returning it to (never below) baseline. Attempt this restore EXACTLY ONCE:
# if the restore's own daemon-reload/restart also fails, print the exact manual
# recovery command and exit non-zero WITHOUT looping (Principle 1 / RC-2
# restore-failure rule).
restore_baseline() {
	echo "apply-mediaroots: $1" >&2
	echo "apply-mediaroots: restoring pre-Phase-2 baseline (removing drop-in, restarting $SERVICE without it)..." >&2
	rm -f "$DROPIN_FILE"
	if systemctl daemon-reload && systemctl restart "$SERVICE"; then
		echo "apply-mediaroots: baseline restored — $SERVICE is running on its Phase-1 base unit with no Phase-2 drop-in." >&2
		exit 1
	fi
	echo "apply-mediaroots: ERROR: automatic restore-to-baseline FAILED." >&2
	echo "apply-mediaroots: recover manually: rm -f \"$DROPIN_FILE\"; systemctl daemon-reload; systemctl restart $SERVICE" >&2
	exit 1
}

systemctl daemon-reload || abort_unapplied "daemon-reload failed"

echo "" >&2
echo "===== merged effective sakms-node unit (with the new drop-in) =====" >&2
systemctl cat "$SERVICE" >&2 || abort_unapplied "could not display merged unit"
echo "===================================================================" >&2
echo "" >&2

if [ "$ASSUME_YES" -ne 1 ]; then
	printf "Apply this Phase-2 containment and restart %s now? [y/N] " "$SERVICE" >&2
	IFS= read -r reply || reply=""
	case "$reply" in
	y | Y | yes | YES) : ;;
	*) abort_unapplied "operator declined — drop-in removed, no change applied" ;;
	esac
fi

# --- Step 1d: apply + verify FOUR things -------------------------------------
# The daemon-reload here is still PRE-restart (daemon on baseline unit), so a
# failure routes through abort_unapplied. Everything from the restart onward is
# POST-restart and routes through restore_baseline (RC-2).
systemctl daemon-reload || abort_unapplied "daemon-reload failed"
systemctl restart "$SERVICE" || restore_baseline "systemctl restart $SERVICE failed"

# (a) active
systemctl is-active --quiet "$SERVICE" ||
	restore_baseline "$SERVICE is not active after restart"

# (b) genuine containment — host-side procfs check (NOT nsenter: the near-empty
# sandbox has no binary to run 'test' with). /etc/os-release exists on the host
# but is not in the bind list, so it must be ABSENT from the daemon's namespace.
# The MainPID lookup is itself post-restart, so a failure to determine it also
# restores baseline (it would otherwise leave the drop-in loaded-but-unconfirmed
# — the PM-3 hole).
main_pid="$(systemctl show "$SERVICE" -p MainPID --value)"
[ -n "$main_pid" ] && [ "$main_pid" != "0" ] ||
	restore_baseline "could not determine $SERVICE MainPID for containment check"
# SAKMS_PROC_ROOT is a testability seam (RC-4): default is the daemon's live
# procfs root; a test points it at a fixture that DOES contain etc/os-release to
# exercise the containment-fail branch.
PROC_ROOT="${SAKMS_PROC_ROOT:-/proc/$main_pid/root}"
if [ -e "$PROC_ROOT/etc/os-release" ]; then
	restore_baseline "containment check FAILED: /etc/os-release is visible inside the sandbox (namespace not contained — see systemd#18999)"
fi

# (b2) apply integrity — positive procfs containment assertion (RC-1). The
# negative check above proves ONE host file is hidden; this proves the sandbox
# actually re-bound what it was supposed to. Assert that EVERY realpath-resolved
# (resolved==true) media root appears as a genuine mount point in the daemon's
# namespace. This is an INTEGRITY check, not a security-enforcement gate: a
# partial re-bind fails SAFE (the daemon ends up MORE contained than intended,
# already visible as namespace_scoped_but_unbound in the status field), but it
# means the apply did not do what it claimed, so we do not bless it — restore
# baseline, same mechanism as the negative check.
#
# Scope + mechanism (both load-bearing):
#   * resolved==false roots (down at validation time) are legitimately absent
#     from the namespace and are EXCLUDED — otherwise this would false-fail on a
#     benign down-mount. If NO root is resolved==true (all down), the assertion is
#     vacuously satisfied (skipped) and only the negative check ran.
#   * A bare `[ -e /proc/$pid/root/<root> ]` existence test is WRONG: under
#     TemporaryFileSystem=/, systemd leaves an empty PLACEHOLDER directory for a
#     skipped '-' non-mandatory bind, so existence passes on a root that never
#     actually bound. We therefore parse /proc/$pid/mountinfo and require a real
#     mount-point ENTRY (a genuinely bound root has one; a placeholder does not).
#   * mountinfo field 5 (the mount point) is octal-escaped by the kernel (space
#     \040, tab \011, newline \012, backslash \134 — proc_pid_mountinfo(5)). The
#     parser MUST octal-unescape it before comparing, because this deployment's
#     media roots routinely contain spaces (e.g. "/mnt/Media-NAS/TV Shows", which
#     the kernel writes as "TV\040Shows"). Without the unescape every apply
#     against a space-containing resolved root would false-fail. This mirrors the
#     Go daemon's own unescapeMountinfo (cmd/sakms-node/mediaroots_scope.go).
# SAKMS_PROC_MOUNTINFO is a testability seam (RC-4): default is the daemon's live
# mountinfo; a test supplies a synthetic file with/without the expected entries.
MOUNTINFO_FILE="${SAKMS_PROC_MOUNTINFO:-/proc/$main_pid/mountinfo}"
expected_resolved=()
for i in "${!BAKED_PATHS[@]}"; do
	[ "${BAKED_RESOLVED[$i]}" = "1" ] || continue
	expected_resolved+=("${BAKED_PATHS[$i]}")
done
if [ "${#expected_resolved[@]}" -gt 0 ]; then
	assert_mountinfo_py='
import re, sys
mountinfo_path = sys.argv[1]
expected = sys.argv[2:]
def unescape(s):
    return re.sub(r"\\([0-7]{3})", lambda m: chr(int(m.group(1), 8)), s)
mountpoints = set()
try:
    with open(mountinfo_path, "r") as f:
        for line in f:
            fields = line.rstrip("\n").split(" ")
            if len(fields) >= 5:
                mountpoints.add(unescape(fields[4]))
except Exception as e:
    sys.stderr.write("could not read %s: %s\n" % (mountinfo_path, e))
    sys.exit(2)
missing = [p for p in expected if p not in mountpoints]
if missing:
    sys.stderr.write("resolved media roots absent from the sandbox mountinfo: %s\n" % ", ".join(missing))
    sys.exit(1)
'
	python3 -c "$assert_mountinfo_py" "$MOUNTINFO_FILE" "${expected_resolved[@]}" ||
		restore_baseline "positive containment assertion FAILED: one or more realpath-resolved media roots did not bind into the sandbox (the apply did not re-expose what it claimed — a bind was skipped, or a mount dropped during apply)"
fi

# (c) actual function — poll the daemon's local status endpoint for a connected
# state within a bounded timeout.
status_port="$(python3 -c '
import json, sys
try:
    with open(sys.argv[1]) as f:
        cfg = json.load(f)
    p = int(cfg.get("statusPort") or 0)
except Exception:
    p = 0
print(p if p > 0 else 7810)
' "$CONFIG_FILE" 2>/dev/null || echo 7810)"

connected=0
deadline=$((SECONDS + CONNECT_TIMEOUT))
while [ "$SECONDS" -lt "$deadline" ]; do
	body="$(curl -fsS --max-time 3 "http://127.0.0.1:${status_port}/status" 2>/dev/null || true)"
	if printf '%s' "$body" | grep -q '"state"[[:space:]]*:[[:space:]]*"connected"'; then
		connected=1
		break
	fi
	sleep 1
done
# Lower severity than the containment-fail branch (this daemon is
# contained-and-hardened at failure time, just not connected — stranded at/above
# baseline, not below it), but still restore baseline so a failed apply never
# leaves a non-connecting daemon running on the drop-in (RC-2).
[ "$connected" -eq 1 ] ||
	restore_baseline "function check FAILED: $SERVICE did not report a connected state within ${CONNECT_TIMEOUT}s (a missing bind may be blocking DNS/TLS — check the bind list)"

# --- Step 8: success → write the marker --------------------------------------
applied_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
marker_py='
import json, sys
applied_at = sys.argv[1]
raw = sys.stdin.read().split("\0")
roots = []
i = 0
while i + 1 < len(raw):
    path = raw[i]
    flag = raw[i + 1]
    roots.append({"path": path, "resolved": flag == "1"})
    i += 2
sys.stdout.write(json.dumps({"appliedAt": applied_at, "roots": roots}, indent=2))
sys.stdout.write("\n")
'
{
	for i in "${!BAKED_PATHS[@]}"; do
		printf '%s\0%s\0' "${BAKED_PATHS[$i]}" "${BAKED_RESOLVED[$i]}"
	done
} | python3 -c "$marker_py" "$applied_at" >"$MARKER_FILE"
chmod 0640 "$MARKER_FILE"
chown root:sakms-node "$MARKER_FILE" 2>/dev/null ||
	echo "apply-mediaroots: warning: could not chown $MARKER_FILE to root:sakms-node" >&2

echo "apply-mediaroots: Phase 2 containment applied and verified for $SERVICE." >&2
echo "apply-mediaroots: marker written to $MARKER_FILE" >&2
