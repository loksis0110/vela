#!/usr/bin/env bash
# Refreshes the "blocked in Russia" rule sets in core/rules before a build,
# so every release ships current lists. If a download fails the copies in the
# repository are kept (the build never fails because of this script).
#
# Sources:
#   runetfreedom/russia-v2ray-rules-dat: re:filter + community lists
#     (geosite-ru-blocked.srs, geoip-ru-blocked.srs)
#   itdoginfo/allow-domains: services blocked or geo-restricted for Russia
#     (Russia/inside-raw.lst) and subnets of Telegram, Meta, X and Discord,
#     compiled into ru-inside.srs
set -uo pipefail
cd "$(dirname "$0")/.."

RF=https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/sing-box
IT=https://raw.githubusercontent.com/itdoginfo/allow-domains/main
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

fetch() { # url dest min_bytes
  if curl -fsSL --retry 3 --max-time 120 -o "$2" "$1" && [ "$(wc -c < "$2")" -ge "$3" ]; then
    return 0
  fi
  echo "update-rules: could not download $1, keeping the old list" >&2
  return 1
}

fetch "$RF/rule-set-geosite/geosite-ru-blocked.srs" "$TMP/geosite-ru-blocked.srs" 100000 &&
  cp "$TMP/geosite-ru-blocked.srs" core/rules/
fetch "$RF/rule-set-geoip/geoip-ru-blocked.srs" "$TMP/geoip-ru-blocked.srs" 20000 &&
  cp "$TMP/geoip-ru-blocked.srs" core/rules/

ok=1
fetch "$IT/Russia/inside-raw.lst" "$TMP/inside.lst" 5000 || ok=0
cidrs=""
for s in telegram meta twitter discord; do
  for v in IPv4 IPv6; do
    if fetch "$IT/Subnets/$v/$s.lst" "$TMP/$v-$s.lst" 10; then
      cidrs="$cidrs,$TMP/$v-$s.lst"
    fi
  done
done
if [ "$ok" = 1 ]; then
  go run ./tools/rules -out "$TMP/ru-inside.srs" -domains "$TMP/inside.lst" -cidrs "${cidrs#,}" &&
    cp "$TMP/ru-inside.srs" core/rules/
fi
ls -la core/rules
exit 0
