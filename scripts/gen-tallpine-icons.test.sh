#!/usr/bin/env bash
# Checks gen-tallpine-icons.sh outputs exist and are deterministic.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
a="$(mktemp -d)"; b="$(mktemp -d)"; ma="$(mktemp -d)"; mb="$(mktemp -d)"; trap 'rm -rf "$a" "$b" "$ma" "$mb"' EXIT
"$here/gen-tallpine-icons.sh" "$a" "$ma" >/dev/null; "$here/gen-tallpine-icons.sh" "$b" "$mb" >/dev/null
for f in icon.icns icon.png icon.ico trayIconTemplate.png trayIconTemplate@2x.png; do
  [ -s "$a/$f" ] || { echo "FAIL missing $f"; exit 1; }
  cmp -s "$a/$f" "$b/$f" || { echo "FAIL nondeterministic $f"; exit 1; }
done
for f in icon.png android-icon-foreground.png android-icon-background.png android-icon-monochrome.png splash-icon.png favicon.png; do
  [ -s "$ma/$f" ] || { echo "FAIL missing mobile $f"; exit 1; }
  cmp -s "$ma/$f" "$mb/$f" || { echo "FAIL nondeterministic mobile $f"; exit 1; }
done
echo PASS
