#!/usr/bin/env bash
# Refreshes the "blocked in Russia" rule sets in core/rules before a build,
# so every release ships current lists. If a download fails the copies in the
# repository are kept (the build never fails because of this script).
#
# Source: runetfreedom/russia-v2ray-rules-dat (GPL-3.0), built from
# re:filter (MIT), the antifilter community list and v2fly
# domain-list-community (MIT):
#   geosite-ru-blocked.srs, geoip-ru-blocked.srs  - sites blocked in Russia
#   ru-inside.srs - services that block Russian users or are blocked/throttled
#                   (AI services, Spotify, Netflix, YouTube, Discord, ...),
#                   merged from v2fly categories by tools/rules
set -uo pipefail
cd "$(dirname "$0")/.."

RF=https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release/sing-box
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# keep in sync with the "ru-inside" description above
SITES="openai anthropic google-gemini google-deepmind perplexity xai github-copilot category-ai-!cn
  spotify netflix youtube discord telegram twitter meta facebook instagram whatsapp linkedin
  notion canva figma tiktok intel soundcloud patreon medium jetbrains docker hashicorp atlassian"
IPS="telegram facebook twitter netflix"

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

files=""
ok=1
for s in $SITES; do
  if fetch "$RF/rule-set-geosite/geosite-$s.srs" "$TMP/site-$s.srs" 20; then files="$files,$TMP/site-$s.srs"; else ok=0; fi
done
for s in $IPS; do
  if fetch "$RF/rule-set-geoip/geoip-$s.srs" "$TMP/ip-$s.srs" 20; then files="$files,$TMP/ip-$s.srs"; else ok=0; fi
done
# only replace the merged list when every part arrived
if [ "$ok" = 1 ]; then
  go run ./tools/rules -out "$TMP/ru-inside.srs" -srs "${files#,}" && cp "$TMP/ru-inside.srs" core/rules/
fi
ls -la core/rules
exit 0
