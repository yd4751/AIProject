'use strict';
/* 验证：同一首歌的多个 id（不同上传）是否有的能绕过 code=2 */
const UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36';
const BASE = 'https://www.gequbao.com';
const jar = new Map();
const ck = () => [...jar.entries()].map(([k, v]) => `${k}=${v}`).join('; ');
function grab(res) {
  (res.headers.getSetCookie ? res.headers.getSetCookie() : []).forEach((c) => {
    const kv = c.split(';')[0], i = kv.indexOf('=');
    if (i > 0) jar.set(kv.slice(0, i), kv.slice(i + 1));
  });
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function get(url) {
  const h = { 'User-Agent': UA, 'Accept-Language': 'zh-CN,zh;q=0.9' };
  if (jar.size) h.Cookie = ck();
  const res = await fetch(url, { headers: h, redirect: 'follow' });
  grab(res);
  return { status: res.status, text: await res.text() };
}
function parseAppData(html) {
  const m = html.match(/window\.appData\s*=\s*JSON\.parse\('([\s\S]*?)'\);/);
  if (!m) return null;
  try { return JSON.parse(JSON.parse('"' + m[1].replace(/\\'/g, "'") + '"')); } catch (e) { return null; }
}
async function playUrl(id, playId) {
  const h = {
    'User-Agent': UA,
    'Content-Type': 'application/x-www-form-urlencoded; charset=UTF-8',
    'X-Requested-With': 'XMLHttpRequest',
    Accept: 'application/json, text/javascript, */*; q=0.01',
    Referer: `${BASE}/music/${id}`,
  };
  if (jar.size) h.Cookie = ck();
  const res = await fetch(`${BASE}/member/common-play-url`, {
    method: 'POST', headers: h,
    body: new URLSearchParams({ id: String(playId), purpose: 'play' }).toString(),
    signal: AbortSignal.timeout(15000),
  });
  grab(res);
  const t = await res.text();
  let j = null; try { j = JSON.parse(t); } catch (e) {}
  return { code: j && j.code, url: j && j.data && j.data.url ? String(j.data.url).slice(0, 90) : '', raw: t.slice(0, 120) };
}

(async () => {
  await get(`${BASE}/`);
  const q = process.argv[2] || '七里香';
  const s = await get(`${BASE}/s/${encodeURIComponent(q)}`);
  const re = /<a href="\/music\/(\d+)"[^>]*title="([^"]*)"/g;
  const list = [];
  let m;
  while ((m = re.exec(s.text)) && list.length < 8) list.push({ id: m[1], title: m[2] });
  console.log(`搜索「${q}」共取到 ${list.length} 条\n`);

  for (const it of list) {
    const det = await get(`${BASE}/music/${it.id}`);
    const d = parseAppData(det.text);
    if (!d || !d.play_id) { console.log(it.id, it.title, '→ 无 play_id'); continue; }
    const r = await playUrl(it.id, d.play_id);
    const vip = /is_vip|pay_play|vip_play/.test(det.text) ? ' [页含vip字段]' : '';
    console.log(`${it.id} ${it.title}${vip}\n   code=${r.code} ${r.code === 1 ? 'OK ' + r.url : r.raw}`);
    await sleep(900);
  }
})().catch((e) => console.error('FATAL', e));
