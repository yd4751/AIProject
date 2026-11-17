'use strict';
/* 深挖 code=2：换 purpose / UA / 参数 / 间隔重试，看是否有可通路径；并检查页面是否真有验证码组件 */
const UA_DESK = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36';
const UA_MOB = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1';
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
async function get(url, ua) {
  const h = { 'User-Agent': ua || UA_DESK, 'Accept-Language': 'zh-CN,zh;q=0.9' };
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
async function post(id, playId, body, ua) {
  const h = {
    'User-Agent': ua || UA_DESK,
    'Content-Type': 'application/x-www-form-urlencoded; charset=UTF-8',
    'X-Requested-With': 'XMLHttpRequest',
    Accept: 'application/json, text/javascript, */*; q=0.01',
    Referer: `${BASE}/music/${id}`,
  };
  if (jar.size) h.Cookie = ck();
  const res = await fetch(`${BASE}/member/common-play-url`, {
    method: 'POST', headers: h, body: new URLSearchParams(body).toString(),
    signal: AbortSignal.timeout(15000),
  });
  grab(res);
  const t = await res.text();
  let j = null; try { j = JSON.parse(t); } catch (e) {}
  return { status: res.status, code: j && j.code, msg: j && j.msg, url: j && j.data && j.data.url ? 1 : 0, ctype: res.headers.get('content-type') };
}

(async () => {
  const id = process.argv[2] || '4194';
  const home = await get(`${BASE}/`);
  const det = await get(`${BASE}/music/${id}`);
  const d = parseAppData(det.text);
  console.log('详情', det.status, '| play_id ok:', !!(d && d.play_id));

  // 页面里是否有验证码组件
  const probeHtml = det.text;
  console.log('页面含 hcaptcha:', /hcaptcha/i.test(probeHtml), '| recaptcha:', /recaptcha/i.test(probeHtml),
    '| turnstile:', /turnstile/i.test(probeHtml), '| site_key 字面量:', /site_key/.test(probeHtml));
  const scriptSrcs = [...probeHtml.matchAll(/<script[^>]+src="([^"]+)"/g)].map((m) => m[1]).slice(0, 12);
  console.log('脚本:', scriptSrcs.join('\n       '));

  const pid = String(d.play_id);
  const cases = [
    ['play 基准', { id: pid, purpose: 'play' }],
    ['download', { id: pid, purpose: 'download' }],
    ['无 purpose', { id: pid }],
    ['带 type=1', { id: pid, purpose: 'play', type: '1' }],
    ['手机 UA', { id: pid, purpose: 'play' }, UA_MOB],
  ];
  for (const [name, body, ua] of cases) {
    const r = await post(id, pid, body, ua);
    console.log(name, '→', r.status, 'code=', r.code, 'url=', r.url, '|', String(r.msg || '').slice(0, 40));
    await sleep(1200);
  }

  console.log('\n--- 冷却 8 秒后再试 ---');
  await sleep(8000);
  const r2 = await post(id, pid, { id: pid, purpose: 'play' });
  console.log('after cooldown → code=', r2.code, r2.msg);

  console.log('\n--- 响应头 ---');
  const res = await fetch(`${BASE}/member/common-play-url`, {
    method: 'POST',
    headers: {
      'User-Agent': UA_DESK, 'Content-Type': 'application/x-www-form-urlencoded; charset=UTF-8',
      'X-Requested-With': 'XMLHttpRequest', Referer: `${BASE}/music/${id}`, Cookie: ck(),
    },
    body: `id=${encodeURIComponent(pid)}&purpose=play`,
  });
  for (const [k, v] of res.headers) if (/captcha|verify|challenge|rate|retry|cookie/i.test(k)) console.log(' ', k, '=', v.slice(0, 120));
})().catch((e) => console.error('FATAL', e));
