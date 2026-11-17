'use strict';
/* 探测 gequbao code=2（人机验证）的成因：曲目级限制 还是 会话/IP 级风控 */
const UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36';
const BASE = 'https://www.gequbao.com';

function parseAppData(html) {
  const m = html.match(/window\.appData\s*=\s*JSON\.parse\('([\s\S]*?)'\);/);
  if (!m) return null;
  try {
    const inner = JSON.parse('"' + m[1].replace(/\\'/g, "'") + '"');
    return JSON.parse(inner);
  } catch (e) { return null; }
}

const jar = new Map();
function cookieHeader() { return [...jar.entries()].map(([k, v]) => `${k}=${v}`).join('; '); }
function grab(res) {
  const sc = res.headers.getSetCookie ? res.headers.getSetCookie() : [];
  sc.forEach((c) => {
    const kv = c.split(';')[0];
    const i = kv.indexOf('=');
    if (i > 0) jar.set(kv.slice(0, i), kv.slice(i + 1));
  });
}
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function get(url, extra) {
  const headers = Object.assign({ 'User-Agent': UA, 'Accept-Language': 'zh-CN,zh;q=0.9' }, extra || {});
  if (jar.size) headers.Cookie = cookieHeader();
  const res = await fetch(url, { headers, redirect: 'follow' });
  grab(res);
  return { res, text: await res.text() };
}

async function search(q) {
  const { text } = await get(`${BASE}/s/${encodeURIComponent(q)}`);
  const out = [];
  const re = /<a href="\/music\/(\d+)"[^>]*class="[^"]*hover-zoom[^"]*"[^>]*title="([^"]*)"/g;
  const re2 = /<a href="\/music\/(\d+)"[^>]*title="([^"]*)"/g;
  let m;
  const r = re.test(text) ? re : re2;
  r.lastIndex = 0;
  while ((m = r.exec(text)) && out.length < 5) out.push({ id: m[1], title: m[2] });
  return { total: text.length, out };
}

async function playUrl(id, playId, referer) {
  const headers = {
    'User-Agent': UA,
    'Content-Type': 'application/x-www-form-urlencoded; charset=UTF-8',
    'X-Requested-With': 'XMLHttpRequest',
    Accept: 'application/json, text/javascript, */*; q=0.01',
    'Accept-Language': 'zh-CN,zh;q=0.9',
    Referer: referer || `${BASE}/music/${id}`,
  };
  if (jar.size) headers.Cookie = cookieHeader();
  const res = await fetch(`${BASE}/member/common-play-url`, {
    method: 'POST',
    headers,
    body: new URLSearchParams({ id: String(playId), purpose: 'play' }).toString(),
    signal: AbortSignal.timeout(15000),
  });
  grab(res);
  const t = await res.text();
  let j = null; try { j = JSON.parse(t); } catch (e) {}
  return { status: res.status, body: t.slice(0, 260), code: j && j.code, hasUrl: !!(j && j.data && j.data.url) };
}

(async () => {
  const home = await get(`${BASE}/`);
  console.log('1) 首页', home.res.status, '| cookies:', cookieHeader() || '(无)');

  for (const q of ['七里香', '稻香']) {
    console.log('\n==== 搜索《' + q + '》====');
    const s = await search(q);
    console.log('结果:', JSON.stringify(s.out.slice(0, 3)));
    if (!s.out.length) { console.log('无结果, html len =', s.total); continue; }
    const id = s.out[0].id;
    const det = await get(`${BASE}/music/${id}`);
    const d = parseAppData(det.text);
    console.log('详情', det.res.status, '| play_id:', d && String(d.play_id).slice(0, 20), '| cookie 数:', jar.size);
    if (!d || !d.play_id) { console.log('无 play_id'); continue; }

    console.log('--- 连续 3 次 POST ---');
    for (let i = 1; i <= 3; i++) {
      const r = await playUrl(id, d.play_id);
      console.log(`#${i}`, r.status, 'code=', r.code, 'url=', r.hasUrl, '|', r.body);
      await sleep(1000);
    }

    console.log('--- 换 Referer(首页) 再试 ---');
    const r2 = await playUrl(id, d.play_id, `${BASE}/`);
    console.log('home-referer', r2.status, 'code=', r2.code, '|', r2.body);
    await sleep(1000);
  }
})().catch((e) => console.error('FATAL', e));
