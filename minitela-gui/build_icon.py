#!/usr/bin/env python3
"""Generate a modern app icon for Minitela Go.

Produces:
  - appicon.png  (512x512, used by the tray + desktop shortcut)
  - icon.ico     (for the Windows executable)
"""
import os
from PIL import Image, ImageDraw, ImageFilter

OUT = os.path.dirname(os.path.abspath(__file__))

SIZE = 512


def rounded(draw, xy, radius, fill):
    draw.rounded_rectangle(xy, radius=radius, fill=fill)


def build():
    img = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    # --- Background rounded square with vertical gradient (navy -> deep violet)
    top = (30, 34, 66)
    bottom = (58, 34, 92)
    bg = Image.new("RGBA", (SIZE, SIZE))
    bgd = ImageDraw.Draw(bg)
    for y in range(SIZE):
        t = y / (SIZE - 1)
        r = int(top[0] + (bottom[0] - top[0]) * t)
        g = int(top[1] + (bottom[1] - top[1]) * t)
        b = int(top[2] + (bottom[2] - top[2]) * t)
        bgd.line([(0, y), (SIZE, y)], fill=(r, g, b, 255))

    mask = Image.new("L", (SIZE, SIZE), 0)
    md = ImageDraw.Draw(mask)
    md.rounded_rectangle([24, 24, SIZE - 24, SIZE - 24], radius=120, fill=255)
    img.paste(bg, (0, 0), mask)

    # --- Inner display panel (the mini screen) with glow
    panel = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    pd = ImageDraw.Draw(panel)

    # soft glow behind panel
    glow_r = (0, 200, 255)
    glow = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    gd = ImageDraw.Draw(glow)
    gd.ellipse([150, 150, 362, 362], fill=glow_r + (60,))
    glow = glow.filter(ImageFilter.GaussianBlur(40))
    img = Image.alpha_composite(img, glow)

    # display bezel
    pd.rounded_rectangle([124, 150, 388, 382], radius=46, fill=(16, 18, 32, 255))
    # screen gradient cyan -> blue -> violet
    c1 = (0, 200, 255)
    c2 = (52, 120, 255)
    c3 = (150, 60, 255)
    for y in range(150 + 14, 382 - 14):
        t = (y - (150 + 14)) / (382 - 14 - (150 + 14))
        if t < 0.5:
            t2 = t / 0.5
            r = int(c1[0] + (c2[0] - c1[0]) * t2)
            g = int(c1[1] + (c2[1] - c1[1]) * t2)
            b = int(c1[2] + (c2[2] - c1[2]) * t2)
        else:
            t2 = (t - 0.5) / 0.5
            r = int(c2[0] + (c3[0] - c2[0]) * t2)
            g = int(c2[1] + (c3[1] - c2[1]) * t2)
            b = int(c2[2] + (c3[2] - c2[2]) * t2)
        pd.line([(124 + 14, y), (388 - 14, y)], fill=(r, g, b, 255))

    # shiny diagonal highlight
    hl = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    hd = ImageDraw.Draw(hl)
    hd.polygon([(150, 190), (250, 160), (210, 300), (150, 330)], fill=(255, 255, 255, 26))
    hl = hl.filter(ImageFilter.GaussianBlur(6))
    img = Image.alpha_composite(img, hl)

    # --- Brightness bars at the bottom (mini sliders)
    # three rising bars in white/cyan with rounded caps
    bars = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    bd = ImageDraw.Draw(bars)
    base_x0, base_x1, base_y = 190, 322, 300
    # bar 1 (short)
    bd.rounded_rectangle([base_x0, 260, base_x0 + 34, base_y], radius=14, fill=(200, 255, 255, 230))
    # bar 2 (medium)
    bd.rounded_rectangle([base_x0 + 42, 238, base_x0 + 76, base_y], radius=14, fill=(140, 240, 255, 240))
    # bar 3 (tall)
    bd.rounded_rectangle([base_x0 + 84, 214, base_x0 + 118, base_y], radius=14, fill=(255, 255, 255, 255))
    img = Image.alpha_composite(img, bars)

    # re-draw panel clip over bars so bars sit inside the display nicely
    # (composite bars only within the screen region)
    screen_mask = Image.new("L", (SIZE, SIZE), 0)
    smd = ImageDraw.Draw(screen_mask)
    smd.rounded_rectangle([124 + 14, 150 + 14, 388 - 14, 382 - 14], radius=32, fill=255)
    bars_rgb = Image.composite(bars, Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0)), screen_mask)
    img = Image.alpha_composite(img, bars_rgb)

    # --- Sparkle / highlight dot (a "bright" beam)
    sp = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    sd = ImageDraw.Draw(sp)
    sd.ellipse([330, 215, 360, 245], fill=(255, 255, 255, 200))
    img = Image.alpha_composite(img, sp)

    return img


def main():
    os.makedirs(OUT, exist_ok=True)
    img = build()

    # PNG 512 for tray + shortcut
    png_path = os.path.join(OUT, "appicon.png")
    img.save(png_path)
    print("Wrote", png_path, img.size)

    # ICO 256 for the executable (Wails expects build/windows/icon.ico)
    ico_dir = os.path.join(OUT, "..", "build", "windows")
    os.makedirs(ico_dir, exist_ok=True)
    ico = img.resize((256, 256), Image.LANCZOS)
    ico_path = os.path.join(ico_dir, "icon.ico")
    ico.save(ico_path, format="ICO", sizes=[(256, 256), (128, 128), (64, 64), (48, 48), (32, 32), (16, 16)])
    print("Wrote", ico_path)

    # Also copy appicon.png next to the compiled exe at build time (done in build script)


if __name__ == "__main__":
    main()
