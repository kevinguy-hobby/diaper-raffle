#!/usr/bin/env bash
#
# Set the shared password for the raffle.
#
#   ./scripts/set-password.sh            # prompts, twice, without echoing
#   ./scripts/set-password.sh --clear    # removes it, making the site open
#
# The password is typed at the prompt and piped straight into the binary on
# stdin. It never appears as a command-line argument, so it stays out of shell
# history and out of `ps` output. Only a PBKDF2 hash is stored.
#
set -euo pipefail

cd "$(dirname "$0")/.."

DB="${DB_PATH:-diaper-raffle.db}"
BINARY="${BINARY:-./raffle}"
AGENT="com.kevinnguyen.diaper-raffle"

if [ ! -x "$BINARY" ]; then
  echo "building…"
  CGO_ENABLED=0 go build -trimpath -o "$BINARY" ./cmd/server
fi

if [ "${1:-}" = "--clear" ]; then
  "$BINARY" -db "$DB" -clear-password
else
  # read -s keeps the terminal from echoing what is typed.
  printf 'New password: ' >&2
  read -rs password
  printf '\n' >&2

  printf 'Again to confirm: ' >&2
  read -rs confirm
  printf '\n' >&2

  if [ "$password" != "$confirm" ]; then
    echo "those do not match; nothing changed" >&2
    exit 1
  fi
  if [ -z "$password" ]; then
    echo "the password cannot be empty; nothing changed" >&2
    exit 1
  fi
  if [ ${#password} -lt 4 ]; then
    echo "warning: that is very short — anyone who guesses the domain gets a few tries" >&2
  fi

  printf '%s' "$password" | "$BINARY" -db "$DB" -set-password
  unset password confirm
fi

# The running server reads the password on every request, so it picks this up
# without a restart. Restart anyway if the agent is loaded, so a stale binary
# is never what is serving.
if launchctl list 2>/dev/null | grep -q "$AGENT"; then
  echo "restarting the app…" >&2
  launchctl kickstart -k "gui/$(id -u)/$AGENT" 2>/dev/null || true
fi

echo "done" >&2
