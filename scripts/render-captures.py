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
                draw.text((px, py+1), c.data, font=bold if c.bold else font, fill=fg)
    target.parent.mkdir(parents=True, exist_ok=True)
    canvas.save(target)

CAPTURES = ['workspace-160x45', 'workspace-110x35', 'workspace-78x28',
            'workspace-60x20', 'palette-110x35', 'details-110x35']

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
