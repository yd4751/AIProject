"""
生成手写风格签名 PNG（透明背景，可直接插入 Word）
用法:
    python tools/gen-signature.py --name 杨东 --out output/签名-杨东.png
"""
import argparse
import os
import random
from PIL import Image, ImageDraw, ImageFont, ImageFilter, ImageChops

W = '{http://schemas.openxmlformats.org/wordprocessingml/2006/main}'

FONT_CANDIDATES = [
    r'C:\Windows\Fonts\STXINGKA.TTF',   # 华文行楷：最像手写签名
    r'C:\Windows\Fonts\FZSTK.TTF',      # 方正舒体
    r'C:\Windows\Fonts\STKAITI.TTF',    # 华文楷体
    r'C:\Windows\Fonts\simkai.ttf',     # 楷体
]

INK = (28, 32, 40, 255)


def pick_font():
    for p in FONT_CANDIDATES:
        if os.path.exists(p):
            return p
    raise RuntimeError('未找到可用的中文手写字体')


def render_char(ch, font_path, size, seed):
    """单字图层：轻微旋转 + 墨色 + 飞白噪点，制造手写感。"""
    rnd = random.Random(seed)
    pad = int(size * 0.35)
    layer = Image.new('RGBA', (size + pad * 2, size + pad * 2), (0, 0, 0, 0))
    d = ImageDraw.Draw(layer)
    font = ImageFont.truetype(font_path, size)
    d.text((pad, pad - int(size * 0.06)), ch, font=font, fill=INK,
           stroke_width=1, stroke_fill=INK)

    # 极小幅度的随机缩放与旋转，避免两个字形完全平行
    angle = rnd.uniform(-7, 7)
    layer = layer.rotate(angle, resample=Image.Resampling.BICUBIC, expand=False)

    # 飞白：在笔画 alpha 上随机打少量透明小点，并做一次极轻模糊
    alpha = layer.getchannel('A')
    speck = Image.new('L', layer.size, 255)
    sd = ImageDraw.Draw(speck)
    for _ in range(int(size * 0.9)):
        x = rnd.randrange(layer.width)
        y = rnd.randrange(layer.height)
        if alpha.getpixel((x, y)) > 40:
            sd.point((x, y), fill=rnd.randrange(120, 235))
    alpha = ImageChops.multiply(alpha, speck)
    alpha = alpha.filter(ImageFilter.GaussianBlur(0.35))
    layer.putalpha(alpha)
    return layer


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--name', default='杨东')
    ap.add_argument('--out', default='output/signature.png')
    ap.add_argument('--size', type=int, default=190)
    ap.add_argument('--seed', type=int, default=20261106)
    args = ap.parse_args()

    font_path = pick_font()
    rnd = random.Random(args.seed)
    size = args.size
    gap = int(size * 0.90)
    canvas_w = int(size * 1.6) + gap * len(args.name)
    canvas_h = int(size * 1.9)
    canvas = Image.new('RGBA', (canvas_w, canvas_h), (0, 0, 0, 0))

    x = int(size * 0.18)
    for i, ch in enumerate(args.name):
        ch_layer = render_char(ch, font_path, size + rnd.randint(-12, 12), args.seed + i * 97)
        y = int(size * 0.18) + rnd.randint(-8, 8)
        canvas.alpha_composite(ch_layer, (x, y))
        x += gap + rnd.randint(-6, 6)

    # 整体轻微倾斜，像随手写下的
    canvas = canvas.rotate(rnd.uniform(-2.2, 1.2), resample=Image.Resampling.BICUBIC, expand=True)

    bbox = canvas.getchannel('A').getbbox()
    if bbox:
        margin = 12
        bbox = (max(0, bbox[0] - margin), max(0, bbox[1] - margin),
                min(canvas.width, bbox[2] + margin), min(canvas.height, bbox[3] + margin))
        canvas = canvas.crop(bbox)

    os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)
    canvas.save(args.out, 'PNG')
    print('字体:', font_path)
    print('签名:', args.name)
    print('尺寸:', canvas.size)
    print('输出:', os.path.abspath(args.out))


if __name__ == '__main__':
    main()
