// 生成 tabBar 图标（无第三方依赖，直接写 PNG）
const fs = require('fs');
const path = require('path');
const zlib = require('zlib');

const OUT_DIR = path.join(__dirname, '..', 'miniprogram', 'assets', 'icons');
const SIZE = 81;
const INACTIVE = [154, 162, 172];
const ACTIVE = [23, 105, 224];

function crc32(buffer) {
    let crc = 0xffffffff;
    for (let i = 0; i < buffer.length; i += 1) {
        crc ^= buffer[i];
        for (let k = 0; k < 8; k += 1) {
            crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0);
        }
    }
    return (crc ^ 0xffffffff) >>> 0;
}

function chunk(type, data) {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length, 0);
    const body = Buffer.concat([Buffer.from(type, 'ascii'), data]);
    const crc = Buffer.alloc(4);
    crc.writeUInt32BE(crc32(body), 0);
    return Buffer.concat([len, body, crc]);
}

function toPng(rgba) {
    const ihdr = Buffer.alloc(13);
    ihdr.writeUInt32BE(SIZE, 0);
    ihdr.writeUInt32BE(SIZE, 4);
    ihdr[8] = 8;   // bit depth
    ihdr[9] = 6;   // RGBA
    ihdr[10] = 0;
    ihdr[11] = 0;
    ihdr[12] = 0;
    const raw = Buffer.alloc(SIZE * (1 + SIZE * 4));
    for (let y = 0; y < SIZE; y += 1) {
        raw[y * (1 + SIZE * 4)] = 0;
        rgba.copy(raw, y * (1 + SIZE * 4) + 1, y * SIZE * 4, (y + 1) * SIZE * 4);
    }
    return Buffer.concat([
        Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
        chunk('IHDR', ihdr),
        chunk('IDAT', zlib.deflateSync(raw, { level: 9 })),
        chunk('IEND', Buffer.alloc(0)),
    ]);
}

function globe(x, y) {
    const dx = x - 40.5;
    const dy = y - 40.5;
    const r = Math.sqrt(dx * dx + dy * dy);
    if (Math.abs(r - 27) < 1.9) return true;                       // 外圆
    if (r < 26 && Math.abs(dy) < 1.7) return true;                  // 赤道
    const ellipse = Math.sqrt((dx / 11) ** 2 + (dy / 27) ** 2);
    if (Math.abs(ellipse - 1) < 0.075) return true;                 // 经线
    if (r < 26 && Math.abs(Math.abs(dy) - 14) < 1.5) return true;  // 纬线
    return false;
}

function gear(x, y) {
    const dx = x - 40.5;
    const dy = y - 40.5;
    const r = Math.sqrt(dx * dx + dy * dy);
    if (r < 7) return false;                                        // 轴孔
    if (r >= 11 && r <= 22) return true;                            // 齿环
    if (r > 22 && r <= 29) {
        const step = Math.PI / 4;
        const angle = Math.atan2(dy, dx);
        const delta = Math.abs(angle - Math.round(angle / step) * step);
        if (delta < 0.24) return true;                              // 8 个齿
    }
    return false;
}

function render(shape, color) {
    const rgba = Buffer.alloc(SIZE * SIZE * 4);
    const samples = [-0.333, 0, 0.333];
    for (let y = 0; y < SIZE; y += 1) {
        for (let x = 0; x < SIZE; x += 1) {
            let hits = 0;
            for (let sy = 0; sy < 3; sy += 1) {
                for (let sx = 0; sx < 3; sx += 1) {
                    if (shape(x + 0.5 + samples[sx], y + 0.5 + samples[sy])) hits += 1;
                }
            }
            const offset = (y * SIZE + x) * 4;
            rgba[offset] = color[0];
            rgba[offset + 1] = color[1];
            rgba[offset + 2] = color[2];
            rgba[offset + 3] = Math.round((hits / 9) * 255);
        }
    }
    return toPng(rgba);
}

if (!fs.existsSync(OUT_DIR)) fs.mkdirSync(OUT_DIR, { recursive: true });
fs.writeFileSync(path.join(OUT_DIR, 'tab-site.png'), render(globe, INACTIVE));
fs.writeFileSync(path.join(OUT_DIR, 'tab-site-active.png'), render(globe, ACTIVE));
fs.writeFileSync(path.join(OUT_DIR, 'tab-settings.png'), render(gear, INACTIVE));
fs.writeFileSync(path.join(OUT_DIR, 'tab-settings-active.png'), render(gear, ACTIVE));
console.log('icons written to ' + OUT_DIR);
