'use strict';
/* 探测：gequbao common-play-url 返回 code=2（人机验证）时，带 cookie / 换参数是否能过 */
const UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36';

function parseAppData(html) {
  const m = html.match(/window\.appData\s*=\s*JSON\.parse\('([\s\S]*?)'\);/);
  if (!m) return null;
  const inner = JSON.parse('"' + m[1].replace(/\\'/g, "'") + '"');
  return JSON.parse(inner);
}

(async () => {
  const id = process.argv[2] || '4194';
  const jar = [];
  const grab = (res) => {
    const sc = res.headers.getSetCookie ? res.headers.getSetCookie() : [];
    sc.forEach((c) => jar.push(c.split(';')[0]));
  };

  // 1) 先访问首页建立会话
  const home = await fetch('https://www.gequbao.com/', { headers: { 'User-Agent': UA, 'Accept-Language': 'zh-CN,zh;q=0.9' } });
  grab(home);
  await home.text();
  console.log('首页 cookies:', jar.join(' | ') || '(无)');

  // 2) 详情页
  const det = await fetch(`https://www.gequbao.com/music/${id}`, {
    headers: { 'User-Agent': UA, 'Accept-Language': 'zh-CN,zh;q=0.9', Cookie: jar.join('; ') },
  });
  grab(det);
  const html = await det.text();
  const d = parseAppData(html);
  console.log('详情页:', det.status, '| play_id:', d && String(d.play_id).slice(0, 24) + '...');
  console.log('累计 cookies:', jar.join(' | '));

  const common = {
    'User-Agent': UA,
    'Accept-Language': 'zh-CN,zh;q=0.9',
    'X-Requested-With': 'XMLHttpRequest',
    Accept: 'application/json, text/javascript, */*; q=0.01',
    Referer: `https://www.gequbao.com/music/${id}`,
    Cookie: jar.join('; '),
  };

  const cases = [
    ['purpose=play', { id: String(d.play_id), purpose: 'play' }],
    ['purpose=download', { id: String(d.play_id), purpose: 'download' }],
    ['purpose=play + site_key', { id: String(d.play_id), purpose: 'play', site_key: 'site_key' }],
    ['无 purpose', { id: String(d.play_id) }],
  ];

  for (const [name, body] of cases) {
    try {
      const r = await fetch('https://www.gequbao.com/member/common-play-url', {
        method: 'POST',
        headers: Object.assign({ 'Content-Type': 'application/x-www-form-urlencoded; charset=UTF-8' }, common),
        body: new URLSearchParams(body).toString(),
      });
      grab(r);
      const t = await r.text();
      console.log(name, '→', r.status, t.slice(0, 150));
    } catch (e) {
      console.log(name, 'ERR', e.message);
    }
    await new Promise((r) => setTimeout(r, 800));
  }
})();
