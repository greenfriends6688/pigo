#!/usr/bin/env python3
"""Draw the app icon: a round cat with a π tag, on a warm tile.

The icon is drawn at four times its size and scaled down, which is how the
edges come out smooth without a vector source. It writes the PNG every target
uses and the .icns macOS shows in the Dock and Finder.

    python3 scripts/make_icon.py
"""

import os
import subprocess
import tempfile

from PIL import Image, ImageDraw, ImageFilter

SIZE = 1024
SS = 4  # supersampling
W = SIZE * SS


def s(v):
    return int(round(v * SS))


# Palette: a warm aprico tile, cream fur, ink outlines, a pink nose and a
# lavender π tag (the accent the app's dark mode uses for the same marks).
TILE_TOP = (255, 236, 210)
TILE_BOTTOM = (255, 194, 141)
FUR = (255, 248, 239)
FUR_SHADE = (255, 236, 219)
INK = (74, 59, 50)
EAR = (247, 168, 160)
NOSE = (240, 130, 138)
BLUSH = (255, 174, 174)
TAG = (126, 96, 220)
TAG_EDGE = (98, 72, 190)


def tile(draw):
    """The rounded square the icon sits on, with a soft top light."""
    radius = s(228)
    draw.rounded_rectangle([0, 0, W, W], radius=radius, fill=TILE_BOTTOM)
    # A vertical gradient, faked by drawing bands inside the rounded mask.
    grad = Image.new("RGB", (1, W))
    for y in range(W):
        t = y / (W - 1)
        grad.putpixel((0, y), tuple(
            int(TILE_TOP[i] * (1 - t) + TILE_BOTTOM[i] * t) for i in range(3)))
    grad = grad.resize((W, W))
    mask = Image.new("L", (W, W), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, W, W], radius=radius, fill=255)
    return grad, mask


def ear(draw, points, inner):
    draw.polygon(points, fill=FUR)
    draw.line(points + [points[0]], fill=INK, width=s(17), joint="curve")
    draw.polygon(inner, fill=EAR)


def icon():
    img = Image.new("RGBA", (W, W), (0, 0, 0, 0))
    grad, mask = tile(ImageDraw.Draw(img))
    img.paste(grad, (0, 0), mask)
    d = ImageDraw.Draw(img, "RGBA")

    # Ears first, so the head's outline crosses their bases.
    ear(d, [(s(300), s(470)), (s(318), s(300)), (s(470), s(372))],
        [(s(340), s(438)), (s(348), s(348)), (s(438), s(392))])
    ear(d, [(s(724), s(470)), (s(706), s(300)), (s(554), s(372))],
        [(s(684), s(438)), (s(676), s(348)), (s(586), s(392))])

    # Head, a touch wider than tall as a cat's is.
    d.ellipse([s(206), s(336), s(818), s(836)], fill=FUR, outline=INK, width=s(17))

    # Eyes: wide and round, with two highlights each.
    for cx in (s(400), s(624)):
        d.ellipse([cx - s(58), s(486), cx + s(58), s(602)], fill=INK)
        d.ellipse([cx - s(22), s(504), cx + s(8), s(534)], fill=(255, 255, 255))
        d.ellipse([cx + s(20), s(556), cx + s(38), s(574)], fill=(255, 255, 255, 210))

    # Blush.
    for cx in (s(330), s(694)):
        d.ellipse([cx - s(44), s(620), cx + s(44), s(668)], fill=BLUSH + (110,))

    # Nose and mouth.
    d.polygon([(s(486), s(636)), (s(538), s(636)), (s(512), s(662))], fill=NOSE)
    d.arc([s(452), s(646), s(512), s(700)], 20, 160, fill=INK, width=s(12))
    d.arc([s(512), s(646), s(572), s(700)], 20, 160, fill=INK, width=s(12))

    # Whiskers.
    for y0, y1 in ((s(608), s(586)), (s(640), s(646))):
        d.line([(s(300), y0), (s(160), y1)], fill=INK, width=s(10))
        d.line([(s(724), y0), (s(864), y1)], fill=INK, width=s(10))

    # Collar with a π tag: the app's mark, small enough to read as a charm.
    d.rounded_rectangle([s(300), s(806), s(724), s(874)], radius=s(24), fill=FUR, outline=INK, width=s(15))
    tag = [s(452), s(838), s(572), s(958)]
    d.rounded_rectangle(tag, radius=s(30), fill=TAG, outline=TAG_EDGE, width=s(10))
    # π drawn with two strokes: a bar and two legs.
    d.line([(s(474), s(882)), (s(550), s(882))], fill=(255, 255, 255), width=s(14))
    d.line([(s(492), s(882)), (s(486), s(922))], fill=(255, 255, 255), width=s(14))
    d.line([(s(532), s(882)), (s(538), s(922))], fill=(255, 255, 255), width=s(14))

    # A highlight across the tile's top edge, then the shadow beneath it.
    shine = Image.new("RGBA", (W, W), (0, 0, 0, 0))
    ImageDraw.Draw(shine).rounded_rectangle(
        [s(30), s(24), s(994), s(360)], radius=s(200), fill=(255, 255, 255, 46))
    img.alpha_composite(shine.filter(ImageFilter.GaussianBlur(s(18))))

    return img.resize((SIZE, SIZE), Image.LANCZOS)


def main():
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    assets = os.path.join(root, "assets")
    os.makedirs(assets, exist_ok=True)
    img = icon()
    png = os.path.join(assets, "Icon.png")
    img.save(png)

    # An .iconset of every size macOS asks for, then icns through iconutil.
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
