#!/bin/sh
# Runs as root (the image's default USER), fixes up ownership of a
# bind-mounted data dir that will usually belong to whatever host user
# created it — not the container's sakms user — then drops to sakms
# for the real process. Without this, a plain `docker run -v host/path:/data`
# fails on first boot with "unable to open database file", since sqlite
# can't create sakms.db in a directory sakms can't write to.
set -e
chown -R sakms:sakms "${SAKMS_DATA_DIR:-/data}"
exec gosu sakms "$@"
