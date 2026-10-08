"""Generates Vela icons for all platforms (run: python3 tools/make_icons.py)."""
import math
import os
from PIL import Image, ImageDraw, ImageFilter

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
INK = (12, 20, 36)
DEEP = (22, 38, 72)
STAR = (243, 230, 196)
SEA = (89, 208, 176)

# Sail-shaped constellation: mast top, mast foot, clew, plus a bowsprit star.
STARS = [(0.50, 0.17), (0.50, 0.74), (0.80, 0.66), (0.27, 0.79), (0.73, 0.79)]
LINES = [(0, 1), (0, 2), (1, 2), (3, 1), (1, 4)]


def sparkle(draw, cx, cy, r, color):
    pts = []
    for i in range(8):
        ang = math.pi / 4 * i - math.pi / 2
        rr = r if i % 2 == 0 else r * 0.28
        pts.append((cx + rr * math.cos(ang), cy + rr * math.sin(ang)))
    draw.polygon(pts, fill=color)


def app_icon(size=1024, rounded=True, background=True, scale=1.0):
    S = size * 4
    img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    if background:
        bg = Image.new("RGBA", (S, S))
        px = bg.load()
        for y in range(S):
            t = y / S
            c = tuple(int(INK[i] * (1 - t) + DEEP[i] * t) for i in range(3))
            for x in range(S):
                px[x, y] = c + (255,)
        if rounded:
            mask = Image.new("L", (S, S), 0)
            ImageDraw.Draw(mask).rounded_rectangle([0, 0, S - 1, S - 1], radius=int(S * 0.225), fill=255)
            img.paste(bg, (0, 0), mask)
        else:
            img.paste(bg, (0, 0))
    layer = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    d = ImageDraw.Draw(layer)
    off = (1 - scale) / 2

    def P(i):
        x, y = STARS[i]
        return ((off + x * scale) * S, (off + y * scale) * S)

    # filled sail with soft sea-glass glow
    sail = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    ImageDraw.Draw(sail).polygon([P(0), P(1), P(2)], fill=SEA + (70,))
    layer = Image.alpha_composite(layer, sail)
    d = ImageDraw.Draw(layer)
    w = max(2, int(S * 0.012 * scale))
    for a, b in LINES:
        d.line([P(a), P(b)], fill=STAR + (200,), width=w)
    glow = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    gd = ImageDraw.Draw(glow)
    for i in range(len(STARS)):
        r = S * (0.075 if i < 3 else 0.045) * scale
        cx, cy = P(i)
        gd.ellipse([cx - r, cy - r, cx + r, cy + r], fill=STAR + (90,))
    glow = glow.filter(ImageFilter.GaussianBlur(S * 0.02))
    layer = Image.alpha_composite(layer, glow)
    d = ImageDraw.Draw(layer)
    for i in range(len(STARS)):
        r = S * (0.075 if i < 3 else 0.048) * scale
        cx, cy = P(i)
        sparkle(d, cx, cy, r, STAR + (255,))
    img = Image.alpha_composite(img, layer)
    return img.resize((size, size), Image.LANCZOS)


def tray_icon(color, size=64):
    S = size * 4
    img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    d.polygon([(S * 0.46, S * 0.06), (S * 0.46, S * 0.80), (S * 0.92, S * 0.68)], fill=color + (255,))
    d.polygon([(S * 0.38, S * 0.18), (S * 0.38, S * 0.80), (S * 0.10, S * 0.72)], fill=color + (190,))
    d.rounded_rectangle([S * 0.06, S * 0.86, S * 0.94, S * 0.94], radius=S * 0.04, fill=color + (255,))
    return img.resize((size, size), Image.LANCZOS)


def out(*parts):
    p = os.path.join(ROOT, *parts)
    os.makedirs(os.path.dirname(p), exist_ok=True)
    return p


if __name__ == "__main__":
    big = app_icon(1024)
    big.save(out("assets", "icon-1024.png"))
    big.save(out("desktop", "assets", "app.ico"), sizes=[(s, s) for s in (16, 24, 32, 48, 64, 128, 256)])
    tray_icon((154, 166, 186)).save(out("desktop", "assets", "tray-off.ico"), sizes=[(16, 16), (24, 24), (32, 32), (48, 48)])
    tray_icon(SEA).save(out("desktop", "assets", "tray-on.ico"), sizes=[(16, 16), (24, 24), (32, 32), (48, 48)])
    # favicon for the desktop window
    app_icon(128).save(out("desktop", "ui", "icon.png"))
    # iOS: single 1024 icon without transparency or rounding (Apple rounds it)
    app_icon(1024, rounded=False).convert("RGB").save(
        out("ios", "Vela", "Assets.xcassets", "AppIcon.appiconset", "icon-1024.png"))
    # Android adaptive icon foreground (safe zone 66%) + legacy icons
    fg = app_icon(432, background=False, scale=0.62)
    fg.save(out("android", "app", "src", "main", "res", "drawable-nodpi", "ic_launcher_foreground.png"))
    for name, px in {"mdpi": 48, "hdpi": 72, "xhdpi": 96, "xxhdpi": 144, "xxxhdpi": 192}.items():
        app_icon(px).save(out("android", "app", "src", "main", "res", f"mipmap-{name}", "ic_launcher.png"))
    # Play Store listing icon 512x512, no transparency rounding by Google
    app_icon(512, rounded=False).convert("RGB").save(out("assets", "play-store-icon-512.png"))
    # notification icon (white silhouette)
    tray_icon((255, 255, 255), 96).save(out("android", "app", "src", "main", "res", "drawable-nodpi", "ic_stat_vela.png"))
    print("icons generated")
