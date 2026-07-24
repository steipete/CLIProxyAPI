#!/usr/bin/env bash
# Deploy a CLIProxyAPI commit to the local cliproxyapi-candidate service.
#
# Usage: deploy-candidate.sh <git-sha> <version>
#   e.g. deploy-candidate.sh 752757b431ed8f2f0c5ca4da7e1904996db40cab 7.2.93-steipete.8
#
# Builds from a pristine clone of the fork at <git-sha>, installs a versioned
# binary, flips the systemd user service, and health-checks with a real
# authenticated /v1/messages request. Any failure rolls back to the previous
# ExecStart and restarts. Layout matches the gorillaclaw conventions:
#   sources   ~/.local/src/cliproxyapi/<sha>
#   binaries  ~/.local/lib/cliproxyapi/<version>-<sha8>/cli-proxy-api
#   service   ~/.config/systemd/user/cliproxyapi-candidate.service
#   config    ~/.config/cliproxyapi-candidate/config.yaml
set -euo pipefail

REPO_URL="https://github.com/steipete/CLIProxyAPI.git"
SERVICE=cliproxyapi-candidate
UNIT="$HOME/.config/systemd/user/$SERVICE.service"
CONFIG="$HOME/.config/cliproxyapi-candidate/config.yaml"

SHA=${1:?usage: deploy-candidate.sh <git-sha> <version>}
VER=${2:?usage: deploy-candidate.sh <git-sha> <version>}
[[ $SHA =~ ^[0-9a-f]{40}$ ]] || { echo "need a full 40-char SHA" >&2; exit 1; }

SRC="$HOME/.local/src/cliproxyapi/$SHA"
LIB="$HOME/.local/lib/cliproxyapi/$VER-${SHA:0:8}"

if [ ! -f "$SRC/go.mod" ]; then
  rm -rf "$SRC.tmp"
  git clone --quiet "$REPO_URL" "$SRC.tmp"
  git -C "$SRC.tmp" checkout --quiet "$SHA"
  rm -rf "$SRC" && mv "$SRC.tmp" "$SRC"
fi

cd "$SRC"
echo "running focused tests..."
go test ./internal/runtime/executor/... ./internal/translator/... >/dev/null
mkdir -p "$LIB"
echo "building $VER..."
go build -trimpath \
  -ldflags "-s -w -X main.Version=$VER -X main.Commit=$SHA -X main.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o "$LIB/cli-proxy-api" ./cmd/server

PREV_EXEC=$(grep "^ExecStart=" "$UNIT")
cp "$UNIT" "$UNIT.bak-$(date -u +%Y%m%dT%H%M%SZ)"

rollback() {
  echo "DEPLOY FAILED — rolling back to previous binary" >&2
  sed -i "s|^ExecStart=.*|$PREV_EXEC|" "$UNIT"
  systemctl --user daemon-reload
  systemctl --user restart "$SERVICE"
  exit 1
}

sed -i "s|^ExecStart=.*|ExecStart=$LIB/cli-proxy-api -config $CONFIG -local-model|" "$UNIT"
systemctl --user daemon-reload
systemctl --user restart "$SERVICE"
sleep 3

systemctl --user is-active --quiet "$SERVICE" || rollback
journalctl --user -u "$SERVICE" --since "30 seconds ago" --no-pager | grep -q "Version: $VER" || rollback

# Real request through the freshly flipped service; key never printed.
KEY=$(python3 -c "
import yaml,sys
print((yaml.safe_load(open('$CONFIG')).get('api-keys') or [''])[0])")
CODE=$(curl -s --max-time 60 -o /dev/null -w "%{http_code}" \
  http://127.0.0.1:18081/v1/messages?beta=true \
  -H "x-api-key: $KEY" -H "content-type: application/json" \
  -d '{"model":"claude-fable-5","max_tokens":16,"messages":[{"role":"user","content":"Reply with exactly: OK"}]}')
[ "$CODE" = "200" ] || rollback

echo "deployed $VER ($SHA) — health check passed (HTTP $CODE)"
