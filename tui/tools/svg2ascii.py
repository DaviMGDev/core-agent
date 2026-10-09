#!/usr/bin/env python3
"""svg2ascii.py — render a terminal-grid SVG as a plain-text grid.

For SVGs laid out on a terminal cell grid (8x16 px cells, monospace font at
8 px advance — see chat.wireframe.svg). Thin rects become box-drawing rules,
one-cell fills become blocks, and text lands on its exact cell, so the output
can be read in a terminal or diffed as text.

Plain text has no color: fills, tints and text colors are dropped. Render the
PNG for the full picture.

Usage: tools/svg2ascii.py input.svg [output.txt]
"""

import sys
import xml.etree.ElementTree as ET

CELL_W = 8.0
CELL_H = 16.0
BASELINE_OFF = 12.5  # text baseline sits at row * CELL_H + BASELINE_OFF


def tag(el):
    return el.tag.rsplit("}", 1)[-1]


def num(el, name, default=0.0):
    v = el.get(name)
    return default if v is None else float(v)


def size_of(root):
    w, h = num(root, "width"), num(root, "height")
    if w <= 0 or h <= 0:
        vb = (root.get("viewBox") or "").replace(",", " ").split()
        if len(vb) == 4:
            w, h = float(vb[2]), float(vb[3])
    return w, h


def main():
    if len(sys.argv) < 2 or len(sys.argv) > 3:
        sys.exit("usage: tools/svg2ascii.py input.svg [output.txt]")

    src = sys.argv[1]
    out = sys.argv[2] if len(sys.argv) == 3 else None

    root = ET.parse(src).getroot()
    width, height = size_of(root)
    cols, rows = int(round(width / CELL_W)), int(round(height / CELL_H))
    grid = [[" "] * cols for _ in range(rows)]

    def get(r, c):
        if 0 <= r < rows and 0 <= c < cols:
            return grid[r][c]
        return " "

    def put(r, c, ch):
        if 0 <= r < rows and 0 <= c < cols:
            grid[r][c] = ch

    def h_rule(r, c):
        cur = get(r, c)
        put(r, c, "┼" if cur == "│" else cur if cur not in " ─" else "─")

    def v_rule(r, c):
        cur = get(r, c)
        put(r, c, "┼" if cur == "─" else cur if cur not in " │" else "│")

    for el in root.iter():
        t = tag(el)

        if t == "rect":
            x, y, w, h = num(el, "x"), num(el, "y"), num(el, "width"), num(el, "height")
            if h <= 2 and w >= CELL_W:                      # horizontal rule
                r = int(round(y / CELL_H))
                for c in range(int(round(x / CELL_W)), int(round((x + w) / CELL_W))):
                    h_rule(r, c)
            elif w <= 2 and h >= CELL_H:                    # vertical rule
                c = int(round(x / CELL_W))
                for r in range(int(round(y / CELL_H)), int(round((y + h) / CELL_H))):
                    v_rule(r, c)
            elif w <= CELL_W and h <= CELL_H:               # one-cell fill
                put(int(round(y / CELL_H)), int(round(x / CELL_W)), "▉")

        elif t == "text":
            text = "".join(el.itertext()).strip()
            if not text:
                continue
            c0 = int(round(num(el, "x") / CELL_W))
            r = int(round((num(el, "y") - BASELINE_OFF) / CELL_H))
            for i, ch in enumerate(text):
                if ch != " ":
                    put(r, c0 + i, ch)

    out_text = "\n".join("".join(row).rstrip() for row in grid) + "\n"
    if out:
        with open(out, "w", encoding="utf-8") as fh:
            fh.write(out_text)
        print(f"wrote {out}")
    else:
        sys.stdout.write(out_text)


if __name__ == "__main__":
    main()
