#!/usr/bin/env bash
#
# Is the raffle actually reachable, and if not, which layer is broken?
#
#   ./scripts/status.sh your-domain.com
#
# Checks each hop in order, because "the site is down" has about six different
# causes and they need different fixes.
#
set -uo pipefail

D="${1:-}"
PORT="${PORT:-8080}"

if [ -z "$D" ]; then
  echo "usage: $0 <hostname>" >&2
  exit 1
fi

ok()   { printf "  \033[32m✓\033[0m %s\n" "$1"; }
bad()  { printf "  \033[31m✗\033[0m %s\n" "$1"; }
warn() { printf "  \033[33m!\033[0m %s\n" "$1"; }

echo
echo "1. the app, locally"
if curl -fsS -m 5 "http://localhost:$PORT/api/health" >/dev/null 2>&1; then
  ok "responding on localhost:$PORT"
else
  bad "not responding on localhost:$PORT"
  echo "      launchctl list | grep diaper-raffle"
  echo "      tail raffle.log"
fi

echo
echo "2. the tunnel connector"
if pgrep -f "cloudflared tunnel run" >/dev/null 2>&1; then
  ok "cloudflared is running"
else
  bad "cloudflared is not running"
  echo "      sudo launchctl list | grep cloudflared"
fi

echo
echo "3. dns"
a="$(dig +short "$D" A 2>/dev/null | grep -E '^[0-9]+\.' | tr '\n' ' ')"
aaaa="$(dig +short "$D" AAAA 2>/dev/null | head -2 | tr '\n' ' ')"
[ -n "$a" ]    && ok "A     $a"    || bad "A     (none) — IPv4-only guests cannot reach this"
[ -n "$aaaa" ] && ok "AAAA  $aaaa" || warn "AAAA  (none)"

echo
echo "4. through cloudflare"
# Cloudflare routes proxied traffic by SNI, so any edge address reaches the
# right zone. This isolates the tunnel from DNS: if this works and step 3 does
# not, the only thing wrong is DNS.
edge="$(dig +short "$D" A 2>/dev/null | grep -E '^[0-9]+\.' | head -1)"
edge="${edge:-172.67.74.226}"
code="$(curl -s -o /dev/null -m 15 --resolve "$D:443:$edge" -w '%{http_code}' "https://$D/api/health" 2>/dev/null)"
case "$code" in
  200) ok  "edge $edge → tunnel → app  (200)" ;;
  502|503) bad "edge reached, but the app behind the tunnel is down ($code)" ;;
  530|1033) bad "cloudflare cannot reach the tunnel ($code)" ;;
  000) bad "no response from edge $edge" ;;
  *)   warn "edge returned $code" ;;
esac

echo
echo "5. the real thing, as a guest sees it"
code="$(curl -s -o /dev/null -m 15 -w '%{http_code}' "https://$D/" 2>/dev/null)"
if [ "$code" = "200" ]; then
  ok "https://$D is live"
else
  bad "https://$D returned ${code:-000}"
  if [ -z "$a" ] && [ "$(curl -s -o /dev/null -m 15 --resolve "$D:443:${edge}" -w '%{http_code}' "https://$D/api/health" 2>/dev/null)" = "200" ]; then
    echo "      everything works except DNS — there is no A record yet."
    echo "      nothing to fix; cloudflare publishes it on its own."
  fi
fi
echo
