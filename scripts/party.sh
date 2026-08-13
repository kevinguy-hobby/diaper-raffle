#!/usr/bin/env bash
#
# Start the raffle and put it on the internet, in one command.
#
# Brings up the server and a Cloudflare tunnel together, prints the public URL
# big enough to read across a room, and shuts both down cleanly on Ctrl-C.
#
#   ./scripts/party.sh                    # temporary trycloudflare.com URL
#   ./scripts/party.sh raffle.example.com # your own hostname, via a named tunnel
#
set -euo pipefail

PORT="${PORT:-8080}"
DB="${DB_PATH:-diaper-raffle.db}"
HOSTNAME_ARG="${1:-}"

cd "$(dirname "$0")/.."

for tool in go cloudflared; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "error: $tool is not installed" >&2
    exit 1
  fi
done

log_dir="$(mktemp -d)"
server_log="$log_dir/server.log"
tunnel_log="$log_dir/tunnel.log"
server_pid=""
tunnel_pid=""

cleanup() {
  echo
  echo "shutting down…"
  # The server traps SIGTERM and finishes any draw already in flight.
  [ -n "$tunnel_pid" ] && kill "$tunnel_pid" 2>/dev/null || true
  [ -n "$server_pid" ] && kill "$server_pid" 2>/dev/null || true
  wait 2>/dev/null || true
  echo "the database is still at $DB — back it up with: make backup"
}
trap cleanup EXIT INT TERM

echo "building…"
go build -o /tmp/raffle-party ./cmd/server

# Braced deliberately: an unbraced $PORT butted up against the multi-byte
# ellipsis gets its continuation bytes read as part of the variable name.
echo "starting the raffle on port ${PORT}…"
DB_PATH="$DB" /tmp/raffle-party -addr ":$PORT" -db "$DB" >"$server_log" 2>&1 &
server_pid=$!

for _ in $(seq 1 40); do
  if curl -fsS "http://localhost:$PORT/api/health" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "the server died on startup:" >&2
    cat "$server_log" >&2
    exit 1
  fi
  sleep 0.25
done

if ! curl -fsS "http://localhost:$PORT/api/health" >/dev/null 2>&1; then
  echo "the server never came up. log:" >&2
  cat "$server_log" >&2
  exit 1
fi

# A tunnel created in the Cloudflare dashboard is driven by a token instead of
# a local certificate. The token is a credential: it lives outside the repo,
# readable only by you, and is never printed.
TOKEN_FILE="${TOKEN_FILE:-$HOME/.config/diaper-raffle/tunnel-token}"

echo "opening the tunnel…"
if [ -n "$HOSTNAME_ARG" ]; then
  if [ -n "${CLOUDFLARE_TUNNEL_TOKEN:-}" ]; then
    token="$CLOUDFLARE_TUNNEL_TOKEN"
  elif [ -f "$TOKEN_FILE" ]; then
    token="$(tr -d '[:space:]' < "$TOKEN_FILE")"
  else
    token=""
  fi

  if [ -n "$token" ]; then
    # Dashboard-managed tunnel. Routing lives in the dashboard under the
    # tunnel's public hostnames, which is also what creates the DNS record.
    cloudflared tunnel run --token "$token" >"$tunnel_log" 2>&1 &
  elif [ -f "$HOME/.cloudflared/cert.pem" ] &&
       cloudflared tunnel info "${TUNNEL_NAME:-diaper-raffle}" >/dev/null 2>&1; then
    # Locally-managed tunnel, set up by scripts/setup-tunnel.sh.
    cloudflared tunnel run --url "http://localhost:${PORT}" \
      "${TUNNEL_NAME:-diaper-raffle}" >"$tunnel_log" 2>&1 &
  else
    echo "error: no way to authenticate the tunnel for $HOSTNAME_ARG." >&2
    echo "       either put the dashboard token in $TOKEN_FILE," >&2
    echo "       or run ./scripts/setup-tunnel.sh $HOSTNAME_ARG." >&2
    exit 1
  fi
  tunnel_pid=$!
  public_url="https://$HOSTNAME_ARG"

  # Wait for the edge connection rather than guessing at a sleep.
  connected=""
  for _ in $(seq 1 60); do
    if grep -qiE "Registered tunnel connection|Connection [a-f0-9-]+ registered" "$tunnel_log" 2>/dev/null; then
      connected=yes
      break
    fi
    if ! kill -0 "$tunnel_pid" 2>/dev/null; then
      echo "the tunnel died:" >&2
      cat "$tunnel_log" >&2
      exit 1
    fi
    sleep 0.5
  done
  if [ -z "$connected" ]; then
    echo "warning: the tunnel has not reported a connection yet. log:" >&2
    tail -20 "$tunnel_log" >&2
  fi
else
  # Quick tunnel: no account needed, random URL, gone when this exits.
  cloudflared tunnel --url "http://localhost:$PORT" >"$tunnel_log" 2>&1 &
  tunnel_pid=$!

  public_url=""
  for _ in $(seq 1 60); do
    public_url="$(grep -Eo 'https://[a-z0-9-]+\.trycloudflare\.com' "$tunnel_log" | head -1 || true)"
    [ -n "$public_url" ] && break
    if ! kill -0 "$tunnel_pid" 2>/dev/null; then
      echo "the tunnel died:" >&2
      cat "$tunnel_log" >&2
      exit 1
    fi
    sleep 0.5
  done

  if [ -z "$public_url" ]; then
    echo "the tunnel never printed a URL. log:" >&2
    cat "$tunnel_log" >&2
    exit 1
  fi
fi

cat <<BANNER

  ┌────────────────────────────────────────────────────────┐

     $public_url

  └────────────────────────────────────────────────────────┘

  Anyone with that link can edit the roster and press the button.
  There is no password. That is fine for an afternoon; do not post it.

  local:   http://localhost:$PORT
  data:    $DB
  logs:    $server_log
           $tunnel_log

  Ctrl-C to stop.

BANNER

wait "$server_pid"
