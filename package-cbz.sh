#!/usr/bin/env bash
# package-cbz.sh — bundle each manga folder into a .cbz next to it.
#
# Usage:
#   ./package-cbz.sh                 # walks ~/Documents/Manga
#   ./package-cbz.sh /path/to/root   # walks a custom root
#
# Behaviour:
#   - For each top-level folder under <root>, produces <root>/<name>.cbz.
#   - If <name>.cbz already exists, runs `zip -ur` to add only new
#     files/chapters. This is the "append" path for ongoing mangas
#     where the downloader has fetched additional chapters since the
#     last package run — no unzip/rezip required.
#   - Excludes downloader bookkeeping files (.done, .chapters.json,
#     .DS_Store) so the resulting archive is just the images.
#
# Why this is safe to re-run:
#   `zip -u` (update mode) treats the existing archive as the source
#   of truth: matching paths are kept, missing paths are added,
#   changed files are replaced. It does NOT unpack-and-repack the
#   whole tree. For a 4 GB archive that would take minutes to rebuild
#   from scratch, an append run typically completes in seconds.
set -euo pipefail

root="${1:-$HOME/Documents/Manga}"

if [[ ! -d "$root" ]]; then
  echo "no such directory: $root" >&2
  exit 2
fi

# Image archives are already JPEG/PNG/WebP — extra deflate is wasted
# CPU and saves under 1%. Use store-only (-0) for speed.
zip_flags=(-r0 -q -X)
exclusions=(-x '*.done' '*.chapters.json' '*.DS_Store')

cd "$root"
shopt -s nullglob

for d in */ ; do
  name="${d%/}"
  out="$root/$name.cbz"

  if [[ -f "$out" ]]; then
    # Append-only update: zip -u adds new entries and replaces
    # changed ones; existing chapters are left untouched.
    echo "→ updating $name.cbz"
    ( cd "$d" && zip -u0 -q -X "$out" -r . "${exclusions[@]}" ) || {
      # zip exits 12 when there is nothing new to add. That is the
      # happy path for a manga with no new chapters since last run.
      status=$?
      if [[ $status -eq 12 ]]; then
        echo "  (no changes)"
      else
        echo "  zip failed with exit $status" >&2
        exit "$status"
      fi
    }
  else
    echo "→ creating $name.cbz"
    ( cd "$d" && zip "${zip_flags[@]}" "$out" . "${exclusions[@]}" )
  fi
done

echo "done."
