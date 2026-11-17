'use strict';
/**
 * IP 归属地查询（离线，零依赖）。
 *
 * 数据来自 ip2region_v4.xdb，经 tools/build-ipdb.cjs 压缩成「国家 + 省 + 市」的
 * 紧凑二进制（ipdb.bin，约 2MB），可直接内嵌进单文件 exe。
 * 找不到库文件时不影响网关运行，只是归属地显示为「未知」。
 *
 *   const iploc = require('./iploc');
 *   iploc.load();                 // 自动从 exe 内嵌资源 / 同目录 ipdb.bin 加载
 *   iploc.lookup('1.2.3.4')       // → '广东省 深圳市'
 */
const fs = require('fs');
const path = require('path');

const MAGIC = 'GWI1';
const R = { ok: false, src: '', start: null, end: null, strId: null, strings: [] };

/** IPv4 字符串 → uint32；非法返回 -1 */
function ipv4ToInt(ip) {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(ip);
  if (!m) return -1;
  let v = 0;
  for (let i = 1; i <= 4; i++) {
    const n = Number(m[i]);
    if (n > 255) return -1;
    v = (v * 256) + n;
  }
  return v >>> 0;
}

/** 归一化客户端地址：去端口、展开 ::ffff: 映射、识别特殊地址 */
function normalize(raw) {
  let ip = String(raw || '').trim();
  if (!ip) return { kind: 'empty', ip: '' };
  if (ip.startsWith('[')) ip = ip.slice(1, ip.indexOf(']') > 0 ? ip.indexOf(']') : undefined);
  // 1.2.3.4:5678
  if (/^\d{1,3}(\.\d{1,3}){3}:\d+$/.test(ip)) ip = ip.slice(0, ip.lastIndexOf(':'));
  // ::ffff:1.2.3.4 或 ::FFFF:0102:0304
  const mapped = /^::ffff:(\d{1,3}(?:\.\d{1,3}){3})$/i.exec(ip);
  if (mapped) ip = mapped[1];
  if (ip === '::1' || ip === '::' || ip === '0.0.0.0') return { kind: 'local', ip: ip === '::' ? '::' : '::1' };
  if (ip.includes(':')) {
    if (/^f[cd][0-9a-f]{2}:/i.test(ip) || /^fe[89ab][0-9a-f]:/i.test(ip)) return { kind: 'private', ip };
    return { kind: 'ipv6', ip };
  }
  const n = ipv4ToInt(ip);
  if (n < 0) return { kind: 'invalid', ip };
  // 常见保留 / 内网段
  const a = n >>> 24, b = (n >>> 16) & 0xff;
  if (a === 127) return { kind: 'local', ip };
  if (a === 10) return { kind: 'private', ip };
  if (a === 172 && b >= 16 && b <= 31) return { kind: 'private', ip };
  if (a === 192 && b === 168) return { kind: 'private', ip };
  if (a === 169 && b === 254) return { kind: 'private', ip };
  if (a === 100 && b >= 64 && b <= 127) return { kind: 'private', ip };
  if (a >= 224) return { kind: 'reserved', ip };
  return { kind: 'public', ip, value: n };
}

/**
 * 加载归属地库。source 可以是 Buffer（exe 内嵌资源）或文件路径；
 * 省略时依次尝试：同目录 ipdb.bin → 内嵌资源。
 */
function load(source) {
  const candidates = [];
  if (Buffer.isBuffer(source)) candidates.push({ buf: source, src: 'memory' });
  else {
    if (typeof source === 'string') candidates.push({ file: source });
    candidates.push({ file: path.join(__dirname, 'ipdb.bin') });
  }
  for (const c of candidates) {
    let buf = c.buf;
    if (!buf) {
      try { buf = fs.readFileSync(c.file); } catch (e) { continue; }
    }
    try {
      if (buf.length < 10 || buf.toString('latin1', 0, 4) !== MAGIC) continue;
      const count = buf.readUInt32LE(4);
      const nstr = buf.readUInt16LE(8);
      let o = 10;
      const strings = new Array(nstr);
      for (let i = 0; i < nstr; i++) {
        const len = buf.readUInt16LE(o); o += 2;
        strings[i] = buf.toString('utf8', o, o + len); o += len;
      }
      if (count === 0) continue;
      const start = new Uint32Array(count);
      const end = new Uint32Array(count);
      const strId = new Uint16Array(count);
      for (let i = 0; i < count; i++) {
        start[i] = buf.readUInt32LE(o);
        end[i] = buf.readUInt32LE(o + 4);
        strId[i] = buf.readUInt16LE(o + 8);
        o += 10;
      }
      R.ok = true;
      R.src = c.src || c.file;
      R.start = start; R.end = end; R.strId = strId; R.strings = strings;
      return true;
    } catch (e) { /* 换下一个候选 */ }
  }
  R.ok = false;
  return false;
}

/** uint32 → 二分找到所在段 → 地区名 */
function searchIpv4(n) {
  let lo = 0, hi = R.start.length - 1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (n < R.start[mid]) hi = mid - 1;
    else if (n > R.end[mid]) lo = mid + 1;
    else return R.strings[R.strId[mid]] || '';
  }
  return '';
}

/**
 * 查询 IP 归属地。
 * 返回 { ip, region, kind }：region 为 '' 表示未知，kind 为 public/private/local/ipv6 等。
 */
function lookup(raw) {
  const n = normalize(raw);
  if (n.kind === 'local') return { ip: n.ip, region: '本机', kind: 'local' };
  if (n.kind === 'private') return { ip: n.ip, region: '内网', kind: 'private' };
  if (n.kind === 'reserved') return { ip: n.ip, region: '保留地址', kind: 'reserved' };
  if (n.kind === 'ipv6') return { ip: n.ip, region: 'IPv6', kind: 'ipv6' };
  if (n.kind !== 'public') return { ip: n.ip || String(raw || ''), region: '', kind: n.kind };
  if (!R.ok) return { ip: n.ip, region: '', kind: 'public' };
  const region = searchIpv4(n.value);
  return { ip: n.ip, region: region || '未知', kind: 'public' };
}

/** 便捷方法：只要地区字符串 */
function regionOf(ip) { return lookup(ip).region; }

function ready() { return R.ok; }
function source() { return R.src; }
function count() { return R.ok ? R.start.length : 0; }

module.exports = { load, lookup, regionOf, ready, source, count, normalize, ipv4ToInt };
