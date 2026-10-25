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
from PIL import Image, ImageDraw, ImageFilter

OUT = sys.argv[1] if len(sys.argv) > 1 else "icon-out"

BG_TOP = (18, 150, 163)   # teal, lit from above
BG_BOT = (0, 82, 94)      # deep teal, as the very first adrop icon
INK = (255, 255, 255)

SS = 4096  # supersampled canvas
W = SS * 0.088  # stroke width


def stroke(d, pts, fill, w=W):
    for (x0, y0), (x1, y1) in zip(pts, pts[1:]):
        d.line([(x0, y0), (x1, y1)], fill=fill, width=int(w))
    for x, y in pts:
        d.ellipse([x - w / 2, y - w / 2, x + w / 2, y + w / 2], fill=fill)


def glyph(d, fill):
    """A download arrow dropping into a tray: a file arriving on the device."""
    s = SS
    P = lambda x, y: (x * s, y * s)
    stroke(d, [P(0.5, 0.22), P(0.5, 0.58)], fill)
    stroke(d, [P(0.34, 0.43), P(0.5, 0.60), P(0.66, 0.43)], fill)
    stroke(d, [P(0.27, 0.64), P(0.27, 0.76), P(0.73, 0.76), P(0.73, 0.64)], fill)


def master():
    s = SS
    bg = Image.new("RGBA", (s, s))
    bd = ImageDraw.Draw(bg)
    for y in range(s):
        t = y / (s - 1)
        bd.line([(0, y), (s, y)], fill=tuple(int(BG_TOP[i] + (BG_BOT[i] - BG_TOP[i]) * t) for i in range(3)) + (255,))
    mask = Image.new("L", (s, s), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, s - 1, s - 1], radius=int(s * 0.22), fill=255)
    img = Image.new("RGBA", (s, s), (0, 0, 0, 0))
    img.paste(bg, (0, 0), mask)

    # soft shadow under the glyph
    sh = Image.new("RGBA", (s, s), (0, 0, 0, 0))
    glyph(ImageDraw.Draw(sh), (0, 40, 48, 110))
    sh = sh.filter(ImageFilter.GaussianBlur(s * 0.012))
    off = Image.new("RGBA", (s, s), (0, 0, 0, 0))
    off.paste(sh, (0, int(s * 0.012)))
    img = Image.alpha_composite(img, off)
    glyph(ImageDraw.Draw(img), INK + (255,))
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
    """Android's status-bar icon: the glyph as a white silhouette (the system
    uses only the alpha channel)."""
    sil = Image.new("RGBA", img.size, (0, 0, 0, 0))
    glyph(ImageDraw.Draw(sil), (255, 255, 255, 255))
    for name, sz in [("mdpi", 24), ("hdpi", 36), ("xhdpi", 48), ("xxhdpi", 72), ("xxxhdpi", 96)]:
        save(sil, sz, f"android/drawable-{name}/ic_notification.png")


if __name__ == "__main__":
    main()
