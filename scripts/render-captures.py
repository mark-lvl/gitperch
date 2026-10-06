#!/usr/bin/env python3
"""Rasterize the actual ANSI View output exported by UPDATE_RENDERS tests.

Optional development dependencies: pillow, pyte. No runtime dependency or demo
mode is added to gitperch. Captures use deterministic test fixtures.
"""
from pathlib import Path
import sys
import pyte
from PIL import Image, ImageDraw, ImageFont

FONT = '/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf'
NAMED = {'black': '#000000', 'red': '#cd0000', 'green': '#00cd00',
         'brown': '#cdcd00', 'blue': '#0000ee', 'magenta': '#cd00cd',
         'cyan': '#00cdcd', 'white': '#e5e5e5'}

def color(value, default):
    if value == 'default':
        return default
    return NAMED.get(value, '#' + value)

QUADRANTS = {0x2596: 'bl', 0x2597: 'br', 0x2598: 'tl', 0x2599: 'tl bl br', 0x259A: 'tl br',
             0x259B: 'tl tr bl', 0x259C: 'tl tr br', 0x259D: 'tr', 0x259E: 'tr bl', 0x259F: 'tr bl br'}
CORNERS = {'tl': (0, 0), 'tr': (.5, 0), 'bl': (0, .5), 'br': (.5, .5)}
BRAILLE = [(0, 0), (0, 1), (0, 2), (1, 0), (1, 1), (1, 2), (0, 3), (1, 3)]

def block_rects(ch):
    """Cell fractions (x0, y0, x1, y1) filled by a U+2580-U+259F block element."""
    o = ord(ch)
    if o == 0x2580:
        return [(0, 0, 1, .5)]
    if 0x2581 <= o <= 0x2588:
        return [(0, 1 - (o - 0x2580) / 8, 1, 1)]
    if 0x2589 <= o <= 0x258F:
        return [(0, 0, (0x2590 - o) / 8, 1)]
    if o == 0x2590:
        return [(.5, 0, 1, 1)]
    if o == 0x2594:
        return [(0, 0, 1, 1 / 8)]
    if o == 0x2595:
        return [(7 / 8, 0, 1, 1)]
    if o in QUADRANTS:
        return [(x, y, x + .5, y + .5) for x, y in (CORNERS[q] for q in QUADRANTS[o].split())]
    return None

def draw_glyph(draw, data, px, py, cw, ch, fg, font):
    """Terminals draw block elements and braille themselves so cells join
    without seams; the font draws everything else."""
    rects = block_rects(data) if len(data) == 1 else None
    if rects:
        for x0, y0, x1, y1 in rects:
            draw.rectangle((px+round(x0*cw), py+round(y0*ch), px+round(x1*cw)-1, py+round(y1*ch)-1), fill=fg)
    elif len(data) == 1 and 0x2800 <= ord(data) <= 0x28FF:
        r = max(1, cw // 6)
        for bit, (col, row) in enumerate(BRAILLE):
            if (ord(data) - 0x2800) >> bit & 1:
                cx, cy = px + (col*2+1)*cw//4, py + (row*2+1)*ch//8
                draw.ellipse((cx-r, cy-r, cx+r, cy+r), fill=fg)
    else:
        draw.text((px, py+1), data, font=font, fill=fg)

def render(source, target, width, height):
    screen = pyte.Screen(width, height)
    pyte.Stream(screen).feed(source.read_text().replace('\n', '\r\n'))
    cw, ch, pad = 10, 20, 16
    font = ImageFont.truetype(FONT, 16)
    bold = ImageFont.truetype(FONT.replace('.ttf', '-Bold.ttf'), 16)
    canvas = Image.new('RGB', (width*cw+2*pad, height*ch+2*pad), '#080808')
    draw = ImageDraw.Draw(canvas)
    for y in range(height):
        for x in range(width):
            c = screen.buffer[y][x]
            fg, bg = color(c.fg, '#d0d0d0'), color(c.bg, '#080808')
            if c.reverse:
                fg, bg = bg, fg
            px, py = pad+x*cw, pad+y*ch
            draw.rectangle((px, py, px+cw, py+ch), fill=bg)
            if c.data.strip():
                draw_glyph(draw, c.data, px, py, cw, ch, fg, bold if c.bold else font)
    target.parent.mkdir(parents=True, exist_ok=True)
    canvas.save(target)

CAPTURES = ['workspace-160x45', 'workspace-110x35', 'workspace-78x28',
            'workspace-60x20', 'palette-110x35', 'details-110x35', 'scanning-80x24']

if __name__ == '__main__':
    if len(sys.argv) == 5:
        render(Path(sys.argv[1]), Path(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4]))
        sys.exit(0)
    if len(sys.argv) != 2:
        sys.exit('usage: render-captures.py ANSI_DIR  (the GITPERCH_ANSI_DIR used by the tests)')
    source_dir = Path(sys.argv[1])
    missing = [n for n in CAPTURES if not (source_dir / f'{n}.ansi').is_file()]
    if missing:
        sys.exit(f'missing ANSI captures in {source_dir}: {", ".join(missing)}')
    target_dir = Path(__file__).resolve().parent.parent / 'docs' / 'captures'
    for name in CAPTURES:
        width, height = map(int, name.rsplit('-', 1)[1].split('x'))
        render(source_dir / f'{name}.ansi', target_dir / f'{name}.png', width, height)
