#!/usr/bin/env python3
"""Draw pigo's icon: a pink pig with headphones and a laptop, on the blue
gradient tile Agio's shell uses. Flat shapes keep the .icns small.

    python3 scripts/make_icon.py
"""

import math
import os
import subprocess
import tempfile

from PIL import Image, ImageDraw

SIZE = 1024
SS = 4
W = SIZE * SS

# Palette: the shell's blue-violet, a pink pig, navy hardware, cyan code.
TILE_TOP = (86, 92, 255)
TILE_BOTTOM = (160, 98, 255)
SKIN = (255, 214, 218)
SKIN_DEEP = (247, 176, 188)
SNOUT = (255, 176, 188)
NOSTRIL = (190, 101, 122)
INK = (44, 36, 54)
WHITE = (255, 255, 255)
BLUSH = (255, 158, 172)
NAVY = (42, 42, 73)
NAVY_LIGHT = (61, 61, 101)
CYAN = (107, 227, 255)
CYAN_DIM = (72, 184, 218)


def s(v):
    return int(round(v * SS))


def gradient(size, top, bottom):
    img = Image.new("RGB", (1, size))
    for y in range(size):
        t = y / max(size - 1, 1)
        img.putpixel((0, y), tuple(int(top[i] * (1 - t) + bottom[i] * t) for i in range(3)))
    return img.resize((size, size))


def tile(img):
    mask = Image.new("L", (W, W), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, W, W], radius=s(232), fill=255)
    img.paste(gradient(W, TILE_TOP, TILE_BOTTOM), (0, 0), mask)
    # A soft highlight across the top, kept flat enough to compress well.
    shine = Image.new("RGBA", (W, W), (0, 0, 0, 0))
    ImageDraw.Draw(shine).rounded_rectangle([s(40), s(24), s(984), s(330)], radius=s(210), fill=(255, 255, 255, 30))
    img.alpha_composite(shine)
    return mask


def sparkle(d, cx, cy, r, color):
    d.polygon([(s(cx), s(cy - r)), (s(cx + r * .28), s(cy - r * .28)), (s(cx + r), s(cy)),
               (s(cx + r * .28), s(cy + r * .28)), (s(cx), s(cy + r)),
               (s(cx - r * .28), s(cy + r * .28)), (s(cx - r), s(cy)),
               (s(cx - r * .28), s(cy - r * .28))], fill=color)


def icon():
    img = Image.new("RGBA", (W, W), (0, 0, 0, 0))
    mask = tile(img)
    d = ImageDraw.Draw(img, "RGBA")

    # Background code window, quiet behind the pig.
    d.rounded_rectangle([s(118), s(148), s(454), s(430)], radius=s(26), fill=(28, 30, 72, 190))
    d.rounded_rectangle([s(118), s(148), s(454), s(190)], radius=s(26), fill=(20, 22, 58, 210))
    for i, x in enumerate((150, 180, 210)):
        d.ellipse([s(x), s(162), s(x + 13), s(175)], fill=CYAN_DIM)
    for i, (x, y, w) in enumerate(((150, 225, 78), (150, 258, 200), (150, 291, 150), (150, 324, 230))):
        d.rounded_rectangle([s(x), s(y), s(x + w), s(y + 11)], radius=s(5), fill=(107, 227, 255, 150 if i == 0 else 80))

    # Sparkles.
    sparkle(d, 820, 138, 26, (255, 255, 255, 210))
    sparkle(d, 885, 214, 13, (255, 255, 255, 150))
    sparkle(d, 206, 500, 14, (255, 239, 152, 210))

    # Ears behind the head.
    d.polygon([(s(300), s(430)), (s(330), s(250)), (s(472), s(346))], fill=SKIN)
    d.polygon([(s(724), s(430)), (s(694), s(250)), (s(552), s(346))], fill=SKIN)
    d.polygon([(s(338), s(408)), (s(356), s(300)), (s(448), s(360))], fill=SKIN_DEEP)
    d.polygon([(s(686), s(408)), (s(668), s(300)), (s(576), s(360))], fill=SKIN_DEEP)

    # Head.
    d.ellipse([s(236), s(318), s(788), s(786)], fill=SKIN)

    # Headphone band over the head.
    d.arc([s(286), s(206), s(738), s(604)], 200, 340, fill=NAVY, width=s(44))
    d.arc([s(300), s(220), s(724), s(590)], 200, 340, fill=NAVY_LIGHT, width=s(12))

    # Headphone cups.
    for cx in (292, 732):
        d.ellipse([s(cx - 76), s(412), s(cx + 76), s(580)], fill=NAVY)
        d.ellipse([s(cx - 50), s(438), s(cx + 50), s(554)], fill=NAVY_LIGHT)
        d.arc([s(cx - 38), s(450), s(cx + 38), s(542)], 0, 360, fill=CYAN, width=s(7))
    # A terminal prompt on the right cup.
    d.line([(s(714), s(486)), (s(730), s(500)), (s(714), s(514))], fill=CYAN, width=s(8), joint="curve")
    d.line([(s(738), s(516)), (s(754), s(516))], fill=CYAN, width=s(8))

    # Face: one round eye, one wink, snout and smile.
    d.ellipse([s(398), s(456), s(472), s(530)], fill=INK)
    d.ellipse([s(412), s(464), s(432), s(484)], fill=WHITE)
    d.arc([s(548), s(472), s(644), s(528)], 200, 340, fill=INK, width=s(15))
    d.arc([s(560), s(492), s(632), s(548)], 20, 160, fill=INK, width=s(12))
    d.ellipse([s(472), s(500), s(552), s(572)], fill=SNOUT)
    d.ellipse([s(491), s(522), s(505), s(540)], fill=NOSTRIL)
    d.ellipse([s(519), s(522), s(533), s(540)], fill=NOSTRIL)
    d.arc([s(466), s(566), s(558), s(634)], 20, 160, fill=INK, width=s(10))
    d.ellipse([s(324), s(548), s(390), s(596)], fill=(255, 158, 172, 110))
    d.ellipse([s(634), s(548), s(700), s(596)], fill=(255, 158, 172, 110))

    # Hoodie shoulders behind the laptop.
    d.rounded_rectangle([s(214), s(690), s(810), s(936)], radius=s(150), fill=NAVY)
    d.rounded_rectangle([s(262), s(730), s(762), s(936)], radius=s(110), fill=NAVY_LIGHT)

    # Laptop screen and base.
    d.rounded_rectangle([s(194), s(700), s(830), s(910)], radius=s(34), fill=NAVY)
    d.rounded_rectangle([s(222), s(726), s(802), s(874)], radius=s(18), fill=(24, 26, 58))
    # Code on the screen: >_ and a line.
    d.line([(s(292), s(770)), (s(326), s(800)), (s(292), s(830))], fill=CYAN, width=s(14), joint="curve")
    d.line([(s(348), s(832)), (s(392), s(832))], fill=CYAN, width=s(14))
    d.rounded_rectangle([s(430), s(780), s(650), s(794)], radius=s(7), fill=(107, 227, 255, 130))
    d.rounded_rectangle([s(430), s(814), s(710), s(828)], radius=s(7), fill=(107, 227, 255, 90))
    # Base.
    d.rounded_rectangle([s(158), s(892), s(866), s(948)], radius=s(28), fill=NAVY_LIGHT)
    for x in range(250, 800, 34):
        d.rounded_rectangle([s(x), s(912), s(x + 22), s(922)], radius=s(4), fill=(30, 32, 64))

    # Clip the whole icon to the tile.
    img.putalpha(mask)
    return img.resize((SIZE, SIZE), Image.LANCZOS)


def main():
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    assets = os.path.join(root, "assets")
    os.makedirs(assets, exist_ok=True)
    img = icon()
    png = os.path.join(assets, "Icon.png")
    img.save(png)

    with tempfile.TemporaryDirectory() as tmp:
        iconset = os.path.join(tmp, "AppIcon.iconset")
        os.makedirs(iconset)
        for size in (16, 32, 128, 256, 512):
            img.resize((size, size), Image.LANCZOS).save(
                os.path.join(iconset, f"icon_{size}x{size}.png"))
            img.resize((size * 2, size * 2), Image.LANCZOS).save(
                os.path.join(iconset, f"icon_{size}x{size}@2x.png"))
        icns = os.path.join(assets, "AppIcon.icns")
        subprocess.run(["iconutil", "-c", "icns", iconset, "-o", icns], check=True)
    print("wrote", png, "and", icns)


if __name__ == "__main__":
    main()
