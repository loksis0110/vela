#!/usr/bin/env bash
# Turns the important lines of a failed build log into a GitHub annotation,
# so failures are readable through the API without downloading logs.
LOG="$1"
[ -f "$LOG" ] || exit 0
LINES=$(grep -E "error|Error|ERROR|e: |FAILURE|What went wrong|failed|cannot|undefined|unresolved|Unresolved|not found|No such|expected|warning: unable" "$LOG" \
  | grep -vE "^\s*$|deprecat|--no-daemon" | tail -n 70)
[ -z "$LINES" ] && LINES=$(tail -n 60 "$LOG")
MSG=$(printf '%s' "$LINES" | sed 's/%/%25/g' | awk '{printf "%s%%0A", $0}' | cut -c1-60000)
echo "::error title=build log::${MSG}"
