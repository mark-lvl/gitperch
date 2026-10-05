#!/usr/bin/env python3
"""Rasterize the actual ANSI View output exported by UPDATE_RENDERS tests.

Optional development dependencies: pillow, pyte. No runtime dependency or demo
mode is added to repodash. Captures use deterministic test fixtures.
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
    cw, ch, pad = 10, 22, 16
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

if __name__ == '__main__':
    for size in ('160x45', '110x35', '78x28', '60x20'):
        width, height = map(int, size.split('x'))
        render(Path('/tmp') / f'repodash-{size}.ansi',
               Path('docs/captures') / f'workspace-{size}.png', width, height)
    if len(sys.argv) == 5:
        render(Path(sys.argv[1]), Path(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4]))
