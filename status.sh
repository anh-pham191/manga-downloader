#!/usr/bin/env bash
# status.sh — quick "what's the downloader doing" check.
#
# Usage: ./status.sh <manga-folder-name> [log-file]
# Example: ./status.sh "Hunter x Hunter"
#          ./status.sh Gintama /tmp/gintama.log
set -u

manga="${1:-}"
log="${2:-/tmp/$(echo "${manga}" | tr '[:upper:]' '[:lower:]' | tr ' ' '-').log}"

if [[ -z "$manga" ]]; then
  echo "usage: $0 <manga-folder-name> [log-file]" >&2
  exit 2
fi

root="$HOME/Documents/Manga/$manga"

if pgrep -f "bin/downloader" >/dev/null; then
  state="RUNNING"
else
  state="STOPPED"
fi

done_count=0
total_count=0
if [[ -d "$root" ]]; then
  done_count=$(find "$root" -name .done 2>/dev/null | wc -l | tr -d ' ')
  # Prefer the cached chapter list (true total from the source) over
  # folder count, which only includes chapters dispatched so far.
  if [[ -f "$root/.chapters.json" ]]; then
    total_count=$(grep -c '"URL"' "$root/.chapters.json" 2>/dev/null | tr -d ' ')
  fi
  if [[ "$total_count" -eq 0 ]]; then
    total_count=$(find "$root" -maxdepth 1 -type d -name 'chap-*' 2>/dev/null | wc -l | tr -d ' ')
  fi
fi

cookie_state="cookie ok"
# Only flag expiry when the process has stopped: a live process means
# either the cookie still works, or the binary would already have
# exited.
if [[ "$state" == "STOPPED" && -f "$log" ]] && tail -3 "$log" | grep -q "cloudflare cookie likely expired"; then
  cookie_state="⚠️  cookie expired (refresh and re-run with --resume)"
fi

printf '%s | done %s of %s | %s\n' "$state" "$done_count" "$total_count" "$cookie_state"
if [[ -f "$log" ]]; then
  echo "--- last 3 log lines ---"
  tail -3 "$log"
else
  echo "(log file $log not found)"
fi
