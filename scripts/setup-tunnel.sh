#!/usr/bin/env bash
#
# One-time setup for a named Cloudflare tunnel, so the raffle lives at a real
# hostname instead of a random trycloudflare.com one.
#
#   cloudflared tunnel login          # do this yourself first — it opens a browser
#   ./scripts/setup-tunnel.sh your-domain.com
#
# Afterwards, `make party HOST=your-domain.com` starts everything.
#
# This is idempotent: run it again and it will reuse the tunnel it already
# made rather than piling up duplicates.
#
set -euo pipefail

HOSTNAME_ARG="${1:-}"
TUNNEL_NAME="${TUNNEL_NAME:-diaper-raffle}"

if [ -z "$HOSTNAME_ARG" ]; then
  echo "usage: $0 <hostname>            e.g. $0 your-domain.com" >&2
  exit 1
fi

if ! command -v cloudflared >/dev/null 2>&1; then
  echo "error: cloudflared is not installed (brew install cloudflared)" >&2
  exit 1
fi

if [ ! -f "$HOME/.cloudflared/cert.pem" ]; then
  cat >&2 <<'MSG'
error: cloudflared is not logged in.

Run this first — it opens a browser and asks which domain to authorise:

    cloudflared tunnel login

Pick your domain from the list, then run this script again.
MSG
  exit 1
fi

echo "==> tunnel"
if cloudflared tunnel info "$TUNNEL_NAME" >/dev/null 2>&1; then
  echo "    '$TUNNEL_NAME' already exists, reusing it"
else
  cloudflared tunnel create "$TUNNEL_NAME"
fi

tunnel_id="$(cloudflared tunnel info "$TUNNEL_NAME" --output json 2>/dev/null \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])' 2>/dev/null || true)"
if [ -z "$tunnel_id" ]; then
  tunnel_id="$(cloudflared tunnel list --output json \
    | python3 -c "
import sys, json
for t in json.load(sys.stdin):
    if t['name'] == '$TUNNEL_NAME':
        print(t['id']); break
")"
fi
echo "    id: $tunnel_id"

echo "==> dns"
# Points the hostname at the tunnel. At the apex this becomes a flattened,
# proxied CNAME, which is why the site gets a Cloudflare certificate without
# anyone touching a certbot.
for host in "$HOSTNAME_ARG" "www.$HOSTNAME_ARG"; do
  if cloudflared tunnel route dns --overwrite-dns "$TUNNEL_NAME" "$host" 2>&1 \
     | sed 's/^/    /'; then
    :
  else
    echo "    could not route $host — check it is in the account you authorised" >&2
  fi
done

cat <<MSG

==> done

    start it with:   make party HOST=$HOSTNAME_ARG
    it will be at:   https://$HOSTNAME_ARG

    The first request after a DNS change can take a minute to propagate.

    There is no password on this. Before you share it widely, consider putting
    Cloudflare Access in front — Zero Trust > Access > Applications, one-time
    PIN to your own email address. Takes about two minutes.

MSG
