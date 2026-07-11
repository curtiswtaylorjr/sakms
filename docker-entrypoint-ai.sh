#!/bin/sh
# ai-variant entrypoint (see Dockerfile's "ai" build stage) — same PUID/PGID
# + /data ownership handling as docker-entrypoint.sh, plus starting
# `ollama serve` as a second background process in this same container
# before handing off to sakms.
#
# The model pull runs in the background and this script never waits on it:
# a first-ever pull can take minutes, but server1's sakms-auto-update.py
# health check only allows ~30s (10 retries x 3s) before rolling the deploy
# back — blocking sakms's startup on the pull would fail that check on the
# very first ai-variant deploy. sakms's own AI client is already tolerant of
# Ollama not being ready yet (see mode.buildAIClient's doc comment): a
# request made before the pull finishes just fails per-request, the same as
# if an external Ollama were briefly unreachable.
#
# OLLAMA_MODELS points outside SAKMS_DATA_DIR on purpose — server1's
# sakms-auto-update.py wipe_data() rm -rf's every entry under /data on every
# deploy (pre-alpha data-reset policy, see that script's own header). A model
# cached under /data would re-download its ~1GB on every push-triggered
# deploy instead of being pulled once, ever.
#
# Requires the container to run with an init (`docker run --init` / compose's
# `init: true`): once this script execs into sakms as PID 1 below, the
# backgrounded `ollama serve` process is orphaned and reparented to PID 1 —
# without a real init there, nothing reaps it if it ever exits.
set -e
PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

if [ "$(id -g sakms)" != "$PGID" ]; then
    groupmod -o -g "$PGID" sakms
fi
if [ "$(id -u sakms)" != "$PUID" ]; then
    usermod -o -u "$PUID" sakms
fi

chown -R sakms:sakms "${SAKMS_DATA_DIR:-/data}"
chown -R sakms:sakms "${OLLAMA_MODELS:-/ollama-models}"

gosu sakms ollama serve &

# Give the server a moment to come up before asking it to pull — not a hard
# requirement (the pull below just fails and gets retried next start if the
# server genuinely isn't up), just avoids an instant, noisy failure in the
# common case.
i=0
while [ "$i" -lt 10 ]; do
    if gosu sakms ollama list >/dev/null 2>&1; then
        break
    fi
    i=$((i + 1))
    sleep 1
done

(
    if gosu sakms ollama pull "${SAKMS_BUNDLED_OLLAMA_MODEL:-qwen2.5:1.5b}"; then
        echo "bundled ollama: model ${SAKMS_BUNDLED_OLLAMA_MODEL:-qwen2.5:1.5b} ready"
    else
        echo "bundled ollama: model pull failed, will retry on next container start"
    fi
) &

exec gosu sakms "$@"
