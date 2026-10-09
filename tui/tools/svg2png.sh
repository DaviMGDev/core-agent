#!/usr/bin/env bash
# svg2png.sh — render an SVG to PNG at its intrinsic size.
#
# Usage: tools/svg2png.sh input.svg [output.png]
# Default output: input basename with .png next to it.

set -euo pipefail

in="${1:?usage: svg2png.sh input.svg [output.png]}"
out="${2:-${in%.svg}.png}"

rsvg-convert "$in" -o "$out"
echo "wrote $out"
