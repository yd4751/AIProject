#!/usr/bin/env node
/**
 * 把 ip2region_v4.xdb 压缩成网关自用的紧凑 IP 归属地库（ipdb.bin）。
 *
 * 为什么要压缩：xdb 原文件 11.1MB，其中绝大部分是运营商/经纬度等我们不需要的字段，
 * 且相邻段常常属于同一地区却各占一条记录。这里只保留「国家 + 省 + 市」，并把
 * 相邻且地区相同的段合并，结果通常 1MB 左右，可直接内嵌进单文件 exe。
 *
 * 用法：node tools/build-ipdb.cjs <ip2region_v4.xdb> [输出路径]
 *
 * 输出格式（GWIP v1，全部小端）：
 *   magic   4B  'GWI1'
 *   count   u32 段数量
 *   nstr    u16 地区字符串数量
 *   strs    nstr × (u16 len + utf8 bytes)
 *   segs    count × (u32 startIp, u32 endIp, u16 strId)   按 startIp 升序、互不重叠
 */
const fs = require('fs');
const path = require('path');

const XDB_STRUCT_STRIDE = 14;          // startIp(4) + endIp(4) + size(2) + ptr(4)
const FLOOR = 256 + 65536 * 8;

function u16(b, o) { return b.readUInt16LE(o); }
function u32(b, o) { return b.readUInt32LE(o); }

/** 把 xdb 的 region 文本整成给人看的「地区」 */
function displayName(country, region, city) {
  const clean = (s) => (s && s !== 'Reserved' && s !== '0' ? s.trim() : '');
  const c = clean(country), r = clean(region), t = clean(city);

  if (c === '中国' || c === 'China' || c === 'CN') {
    const all = r + t;
    if (/香港/.test(all)) return '中国香港';
    if (/澳门/.test(all)) return '中国澳门';
    if (/台湾/.test(all)) return '中国台湾';
    let out = '';
    if (r) out += r;
    if (t && t !== r) out += (out ? ' ' : '') + t;
    return out || '中国';
  }
  if (!c) return '';
  return c;
}

function main() {
  const src = process.argv[2];
  const out = process.argv[3] || path.join(__dirname, '..', 'ipdb.bin');
  if (!src) {
    console.error('用法: node tools/build-ipdb.cjs <ip2region_v4.xdb> [out.bin]');
    process.exit(1);
  }
  const buf = fs.readFileSync(src);
  const structure = u16(buf, 0);
  const familyId = structure === 2 ? 4 : u16(buf, 16);
  if (structure !== 2 && structure !== 3) throw new Error('不支持的 xdb structure: ' + structure);
  if (familyId !== 4) throw new Error('该文件不是 IPv4 库（family=' + familyId + '），请用 ip2region_v4.xdb');
  console.log('输入: ' + src + '  ' + (buf.length / 1048576).toFixed(2) + 'MB  structure=' + structure);

  const first = u32(buf, 8);
  const last = u32(buf, 12);
  if (first < FLOOR || last < first || (last - first) % XDB_STRUCT_STRIDE !== 0) throw new Error('段索引边界非法');

  const total = (last - first) / XDB_STRUCT_STRIDE + 1;
  console.log('原始段数: ' + total);

  const strIds = new Map();
  const strings = [];
  const segs = [];

  const intern = (s) => {
    let id = strIds.get(s);
    if (id === undefined) {
      id = strings.length;
      strings.push(s);
      strIds.set(s, id);
    }
    return id;
  };

  const decoder = new TextDecoder('utf-8');
  let lastEnd = -1;
  for (let at = first; at <= last; at += XDB_STRUCT_STRIDE) {
    const start = u32(buf, at);
    const end = u32(buf, at + 4);
    const size = u16(buf, at + 8);
    if (size === 0) continue;
    const ptr = u32(buf, at + 10);
    const text = decoder.decode(buf.subarray(ptr, ptr + size));
    const parts = text.split('|');
    const name = displayName(parts[0], parts[1], parts[2]);
    if (!name) continue;
    const id = intern(name);
    // 与上一段首尾相接且地区相同 → 合并（xdb 里这类碎片很多）
    const prev = segs[segs.length - 1];
    if (prev && prev.strId === id && prev.end + 1 === start) {
      prev.end = end;
    } else {
      if (start <= lastEnd) continue;   // 防御：绝不输出重叠段
      segs.push({ start, end, strId: id });
      lastEnd = end;
    }
  }

  console.log('合并后段数: ' + segs.length + '  地区字符串: ' + strings.length);

  const strBytes = strings.map((s) => Buffer.from(s, 'utf8'));
  let strSectionSize = 2;
  strBytes.forEach((b) => { strSectionSize += 2 + b.length; });
  const total2 = 4 + 4 + strSectionSize + segs.length * 10;
  const outBuf = Buffer.alloc(total2);
  let o = 0;
  outBuf.write('GWI1', o, 'latin1'); o += 4;
  outBuf.writeUInt32LE(segs.length, o); o += 4;
  outBuf.writeUInt16LE(strings.length, o); o += 2;
  for (const b of strBytes) {
    outBuf.writeUInt16LE(b.length, o); o += 2;
    b.copy(outBuf, o); o += b.length;
  }
  for (const s of segs) {
    outBuf.writeUInt32LE(s.start, o); o += 4;
    outBuf.writeUInt32LE(s.end, o); o += 4;
    outBuf.writeUInt16LE(s.strId, o); o += 2;
  }
  fs.writeFileSync(out, outBuf);
  console.log('输出: ' + out + '  ' + (outBuf.length / 1024).toFixed(0) + 'KB  (压缩 ' +
    (buf.length / outBuf.length).toFixed(1) + 'x)');
}

main();
