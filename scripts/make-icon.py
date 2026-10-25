#!/usr/bin/env python3
"""Generate the adrop launcher icon set (PNG only). Requires Pillow.

Usage: python3 scripts/make-icon.py OUTDIR
Then copy OUTDIR/android/mipmap-*/ic_launcher.png and drawable-*/ic_notification.png
into app/android/app/src/main/res/
and OUTDIR/linux/*.png, icon-512.png into packaging/icons/.
"""
import math
import os
import sys
from PIL import Image, ImageDraw

OUT = sys.argv[1] if len(sys.argv) > 1 else "icon-out"

BG = "#2B3A8F"      # indigo background
DROP = "#FFFFFF"    # white drop
ACCENT = "#FF8A3D"  # orange arrow

SS = 4096  # supersampled canvas


def master():
    s = SS
    img = Image.new("RGBA", (s, s), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    # full-bleed rounded square
    d.rounded_rectangle([0, 0, s - 1, s - 1], radius=int(s * 0.22), fill=BG)

    # teardrop: circle of radius r, tip above at distance k*r from centre
    r = s * 0.25
    k = 1.7
    total_h = r + k * r
    cx = s / 2
    top = (s - total_h) / 2
    tip = (cx, top)
    cy = top + k * r
    alpha = math.acos(1 / k)  # tangent angle measured from the up direction
    pts = [tip]
    n = 720
    # walk around the circle from right tangent point, under the bottom, to left
    start = -math.pi / 2 + alpha  # screen angle (y down): -pi/2 is straight up
    end = -math.pi / 2 - alpha + 2 * math.pi
    for i in range(n + 1):
        a = start + (end - start) * i / n
        pts.append((cx + r * math.cos(a), cy + r * math.sin(a)))
    d.polygon(pts, fill=DROP)

    # two opposing arrows (exchange between own devices) inside the round part
    t = r * 0.28            # shaft thickness
    hw = r * 0.68           # arrow-head width (perpendicular)
    hl = r * 0.44           # arrow-head length
    half = r * 0.64         # half length of each arrow
    gap = r * 0.34          # vertical offset of each arrow from circle centre
    ac = cy + r * 0.04

    def arrow(y, direction):
        x0, x1 = cx - half, cx + half
        if direction > 0:   # points right
            d.rectangle([x0, y - t / 2, x1 - hl + 1, y + t / 2], fill=ACCENT)
            d.polygon([(x1 - hl, y - hw / 2), (x1, y), (x1 - hl, y + hw / 2)], fill=ACCENT)
        else:               # points left
            d.rectangle([x0 + hl - 1, y - t / 2, x1, y + t / 2], fill=ACCENT)
            d.polygon([(x0 + hl, y - hw / 2), (x0, y), (x0 + hl, y + hw / 2)], fill=ACCENT)

    arrow(ac - gap, +1)
    arrow(ac + gap, -1)
    return img


def save(img, size, rel):
    path = os.path.join(OUT, rel)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    img.resize((size, size), Image.LANCZOS).save(path, optimize=True)


def preview(img):
    sizes = [1024, 192, 96, 48]
    pad = 48
    w = sum(sizes) + pad * (len(sizes) + 1)
    rowh = 1024 + pad * 2
    out = Image.new("RGB", (w, rowh * 2), "#F2F2F2")
    ImageDraw.Draw(out).rectangle([0, rowh, w, rowh * 2], fill="#121212")
    for row in range(2):
        x = pad
        for sz in sizes:
            ic = img.resize((sz, sz), Image.LANCZOS)
            y = row * rowh + (rowh - sz) // 2
            out.paste(ic, (x, y), ic)
            x += sz + pad
    out.save(os.path.join(OUT, "preview.png"), optimize=True)
    # zoomed small-size check: 48 and 96 px renders scaled up 4x, no smoothing
    z = Image.new("RGB", (48 * 4 + 96 * 4 + 3 * pad, 96 * 4 + 2 * pad), "#121212")
    for i, sz in enumerate((48, 96)):
        ic = img.resize((sz, sz), Image.LANCZOS).resize((sz * 4, sz * 4), Image.NEAREST)
        z.paste(ic, (pad + i * (48 * 4 + pad), pad), ic)
    z.save(os.path.join(OUT, "preview-zoom.png"))


def main():
    img = master()
    save(img, 1024, "icon-1024.png")
    save(img, 512, "icon-512.png")
    for name, sz in [("mdpi", 48), ("hdpi", 72), ("xhdpi", 96),
                     ("xxhdpi", 144), ("xxxhdpi", 192)]:
        save(img, sz, f"android/mipmap-{name}/ic_launcher.png")
    save(img, 256, "linux/adrop-256.png")
    save(img, 128, "linux/adrop-128.png")
    preview(img)
    notification(img)


def notification(img):
    """Android's status-bar icon: the drop as a white silhouette (the system
    uses only the alpha channel), arrows cut out."""
    r, g, b, a = img.split()
    drop = Image.eval(Image.merge("RGB", (r, g, b)).convert("L"), lambda v: 255 if v > 200 else 0)
    sil = Image.new("RGBA", img.size, (255, 255, 255, 0))
    sil.putalpha(drop)
    for name, sz in [("mdpi", 24), ("hdpi", 36), ("xhdpi", 48), ("xxhdpi", 72), ("xxxhdpi", 96)]:
        save(sil, sz, f"android/drawable-{name}/ic_notification.png")


if __name__ == "__main__":
    main()
