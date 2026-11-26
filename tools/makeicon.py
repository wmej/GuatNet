"""生成 guatnet 的多尺寸图标 assets/icon.ico。

画法：蓝色圆角方块 + 白色 WiFi 扇形。纯 Python，无第三方依赖。
用法：py -3.13 tools/makeicon.py
"""
import math
import struct

BLUE = (47, 111, 237)
WHITE = (255, 255, 255)
SIZES = [16, 20, 24, 32, 40, 48, 64, 128, 256]


def rounded_rect_alpha(x, y, size, radius):
    """点到圆角矩形边界的覆盖率（1=完全在内）。"""
    half = size / 2.0
    dx = abs(x - half) - (half - radius)
    dy = abs(y - half) - (half - radius)
    if dx <= 0 and dy <= 0:
        return 1.0
    d = math.hypot(max(dx, 0), max(dy, 0)) - radius
    return max(0.0, min(1.0, -d + 0.5))


def wifi_alpha(x, y, size):
    """三点 WiFi 扇形 + 中心圆点。"""
    cx, cy = size * 0.5, size * 0.74
    px, py = x - cx, y - cy
    dist = math.hypot(px, py)
    stroke = size * 0.085

    # 扇形角度：-135° ~ -45°（向上）
    ang = math.degrees(math.atan2(py, px))  # y 向下为正
    in_arc_angle = -135.0 <= ang <= -45.0

    best = 0.0
    if in_arc_angle:
        for r in (size * 0.40, size * 0.26):
            best = max(best, max(0.0, min(1.0, stroke / 2 - abs(dist - r) + 0.5)))
    # 中心点
    best = max(best, max(0.0, min(1.0, size * 0.065 - dist + 0.5)))
    return best


def render(size):
    ss = 4 if size <= 64 else 2
    n = size * ss
    radius = size * 0.22
    acc = [[[0.0, 0.0, 0.0, 0.0] for _ in range(size)] for _ in range(size)]

    for gy in range(n):
        fy = (gy + 0.5) / ss
        row = acc[int(gy / ss)]
        for gx in range(n):
            fx = (gx + 0.5) / ss
            a_bg = rounded_rect_alpha(fx, fy, size, radius)
            if a_bg <= 0:
                continue
            a_wifi = wifi_alpha(fx, fy, size)
            r = BLUE[0] * (1 - a_wifi) + WHITE[0] * a_wifi
            g = BLUE[1] * (1 - a_wifi) + WHITE[1] * a_wifi
            b = BLUE[2] * (1 - a_wifi) + WHITE[2] * a_wifi
            cell = row[int(gx / ss)]
            cell[0] += r
            cell[1] += g
            cell[2] += b
            cell[3] += a_bg

    k = ss * ss
    pixels = []
    for y in range(size):
        out = []
        for x in range(size):
            r, g, b, a = acc[y][x]
            out.append((int(r / k + 0.5), int(g / k + 0.5), int(b / k + 0.5),
                        int(a / k * 255 + 0.5)))
        pixels.append(out)
    return pixels


def ico_entry(pixels, size):
    xor = bytearray()
    for y in range(size - 1, -1, -1):
        for x in range(size):
            r, g, b, a = pixels[y][x]
            xor += bytes((b, g, r, a))
    mask = bytes(size * 4)
    header = struct.pack('<IiiHHIIiiII', 40, size, size * 2, 1, 32, 0,
                         len(xor) + len(mask), 0, 0, 0, 0)
    return header + bytes(xor) + mask


def main():
    images = [(s, ico_entry(render(s), s)) for s in SIZES]
    blob = struct.pack('<HHH', 0, 1, len(images))
    offset = 6 + 16 * len(images)
    entries = b''
    for size, data in images:
        w = 0 if size >= 256 else size
        entries += struct.pack('<BBBBHHII', w, w, 0, 0, 1, 32, len(data), offset)
        offset += len(data)
    with open('assets/icon.ico', 'wb') as f:
        f.write(blob + entries + b''.join(d for _, d in images))
    print('assets/icon.ico', offset, 'bytes,', len(images), 'sizes')


if __name__ == '__main__':
    main()
