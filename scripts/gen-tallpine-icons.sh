#!/usr/bin/env bash
# Generates the Tallpine app icons from frontend/assets/tallpine/brand/*.svg.
# Needs rsvg-convert, magick, iconutil (macOS). Output: frontend/assets/tallpine/.
# Deterministic: same SVGs -> byte-identical files (no timestamps embedded).
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
brand="$root/frontend/assets/tallpine/brand"
out="${1:-$root/frontend/assets/tallpine}"
mobile_out="${2:-$root/packages/mobile/assets}"
for t in rsvg-convert magick iconutil; do command -v "$t" >/dev/null || { echo "missing $t" >&2; exit 1; }; done
mkdir -p "$out"
work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT

# png <svg> <size> <file>: strip metadata for reproducibility.
png() { rsvg-convert -w "$2" -h "$2" "$1" -o "$work/raw.png"
  magick "$work/raw.png" -strip -define png:exclude-chunk=date,time "$3"; }
# macOS grid: 1024 canvas, 824 tile centered (100px margin), subtle shadow.
# Rendered straight from a wrapper SVG so alpha stays premultiplied (no matte halo).
inner="$(sed -e 's/^<svg[^>]*>//' -e 's/<\/svg>$//' "$brand/tile.svg")"
cat > "$work/mac.svg" <<SVG
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024"><defs><filter id="s" x="-10%" y="-10%" width="120%" height="125%"><feDropShadow dx="0" dy="10" stdDeviation="10" flood-color="#000" flood-opacity=".25"/></filter></defs><g filter="url(#s)"><g transform="translate(100 100) scale(34.3333)">$inner</g></g></svg>
SVG
macicon() { local s=$1
  if [ "$s" -le 32 ]; then
    # Small sizes: no shadow, tile snapped to whole pixels (body keeps the parity of s so the margin is whole).
    local body=$(( s * 824 / 1024 )); [ $(( (s - body) % 2 )) -ne 0 ] && body=$(( body + 1 ))
    local m=$(( (s - body) / 2 ))
    local k; k="$(echo "scale=6; $body/24" | bc)"
    printf '<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%s" viewBox="0 0 %s %s"><g transform="translate(%s %s) scale(%s)">%s</g></svg>\n' \
      "$s" "$s" "$s" "$s" "$m" "$m" "$k" "$inner" > "$work/small.svg"
    rsvg-convert "$work/small.svg" -o "$work/raw.png"
    # Hard 1-bit edge: iconutil lightens the colour of semi-transparent edge pixels (mint fringe on light backgrounds).
    magick "$work/raw.png" -channel A -threshold 50% +channel "$work/raw.png"
  else
    rsvg-convert -w "$s" -h "$s" "$work/mac.svg" -o "$work/raw.png"
  fi
  magick "$work/raw.png" -strip -define png:exclude-chunk=date,time "$2"; }

set_dir="$work/icon.iconset"; mkdir -p "$set_dir"
for s in 16 32 128 256 512; do
  macicon "$s" "$set_dir/icon_${s}x${s}.png"
  macicon $((s*2)) "$set_dir/icon_${s}x${s}@2x.png"
done
iconutil -c icns "$set_dir" -o "$out/icon.icns"
macicon 1024 "$out/icon.png"

# Windows / Linux: full-bleed tile.
for s in 16 24 32 48 64 128 256; do png "$brand/tile.svg" "$s" "$work/w$s.png"; done
magick "$work"/w{16,24,32,48,64,128,256}.png "$out/icon.ico"

# Tray template: the app-icon mark (trunk, chevrons, 7 node dots) tuned for 18pt; @2x is an exact 2x.
png "$brand/tray-mark.svg" 18 "$out/trayIconTemplate.png"
png "$brand/tray-mark.svg" 36 "$out/trayIconTemplate@2x.png"

# Mobile set (Expo): full-bleed tile for stores/iOS, adaptive-icon layers for Android, splash, favicon.
mkdir -p "$mobile_out"
png "$brand/tile.svg" 1024 "$work/tile1024.png"
magick "$work/tile1024.png" -background "#005d3f" -alpha remove -alpha off -strip -define png:exclude-chunk=date,time "$mobile_out/icon.png"
# Android adaptive icon: 108dp canvas, 66dp safe zone -> mark scaled to 61% and centred.
mark_layer() { # <svg> <size> <file>
  rsvg-convert -w 624 -h 624 "$1" -o "$work/m.png"
  magick -size "$2x$2" xc:none "$work/m.png" -gravity center -composite -strip -define png:exclude-chunk=date,time "$3"; }
mark_layer "$brand/mark-ivory.svg" 1024 "$mobile_out/android-icon-foreground.png"
magick -size 1024x1024 "xc:#005d3f" -strip -define png:exclude-chunk=date,time "$mobile_out/android-icon-background.png"
rsvg-convert -w 624 -h 624 "$brand/mark-ivory.svg" -o "$work/m.png"
magick -size 1024x1024 xc:none "$work/m.png" -gravity center -composite -channel RGB -fill white -colorize 100 +channel -strip -define png:exclude-chunk=date,time "$mobile_out/android-icon-monochrome.png"
png "$brand/mark-ivory.svg" 1024 "$mobile_out/splash-icon.png"
png "$brand/tile.svg" 48 "$mobile_out/favicon.png"
echo "wrote icons to $out and $mobile_out"
