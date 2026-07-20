#!/bin/bash
# Post-install script for sakms-node RPM.
# Writes /etc/sakms-node/config.json with an empty apiKey (triggers pairing
# mode on first start), then enables and starts the systemd service.
# Server URL is read from SAKMS_SERVER_URL env if set; otherwise the user
# is prompted interactively (only when a terminal is actually attached — see
# below). Node name works the same way via SAKMS_NODE_NAME, defaulting to the
# machine's hostname.

set -euo pipefail

CONFIG_DIR=/etc/sakms-node
CONFIG_FILE="$CONFIG_DIR/config.json"

# Only write config on a fresh install (no existing config.json).
if [ ! -f "$CONFIG_FILE" ]; then
    if [ -n "${SAKMS_SERVER_URL:-}" ]; then
        SERVER_URL="$SAKMS_SERVER_URL"
    elif [ -t 0 ]; then
        read -r -p "sakms server URL (e.g. https://sakms.example.com): " SERVER_URL
    else
        # No env var and no TTY (e.g. a non-interactive/automated install):
        # `read` would fail here anyway, but silently and before any output,
        # leaving a half-installed system with no config and no clear error.
        # serverUrl has no safe default, so fail loudly instead.
        echo "sakms-node: no interactive terminal and SAKMS_SERVER_URL not set" \
             "— for a non-interactive install, set SAKMS_SERVER_URL" \
             "(and optionally SAKMS_NODE_NAME) as environment variables" >&2
        exit 1
    fi
    DEFAULT_NODE_NAME="$(hostname)"
    if [ -n "${SAKMS_NODE_NAME:-}" ]; then
        NODE_NAME="$SAKMS_NODE_NAME"
    elif [ -t 0 ]; then
        read -r -p "Node name [$DEFAULT_NODE_NAME]: " NODE_NAME
        NODE_NAME="${NODE_NAME:-$DEFAULT_NODE_NAME}"
    else
        # nodeName has a safe default, so a non-interactive install can
        # proceed without SAKMS_NODE_NAME instead of failing.
        NODE_NAME="$DEFAULT_NODE_NAME"
    fi
    mkdir -p "$CONFIG_DIR"
    cat > "$CONFIG_FILE" <<JSON
{
  "serverUrl": "$SERVER_URL",
  "apiKey": "",
  "nodeName": "$NODE_NAME"
}
JSON
    chmod 600 "$CONFIG_FILE"
    # Security-hardening addendum: the daemon runs as the dedicated
    # sakms-node system user (see sakms-node.service), not root — the
    # config directory/file must be owned by that user or the daemon can't
    # read/rewrite its own config on the very first start.
    chown -R sakms-node:sakms-node "$CONFIG_DIR"
fi

systemctl enable --now sakms-node.service
