'use strict';
// 探测：gequbao 详情 → appData → common-play-url → 音频直链；以及歌词
const UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36';

function parseAppData(html) {
  const m = html.match(/window\.appData\s*=\s*JSON\.parse\('([\s\S]*?)'\);/);
  if (!m) return null;
  // JS 单引号字符串 → 还原 unicode/转义 → JSON
  const inner = JSON.parse('"' + m[1].replace(/'/g, "\\'").replace(/\\'/g, "\\'") + '"');
  return JSON.parse(inner);
}

(async () => {
  const id = process.argv[2] || '4188';
  const detail = await fetch(`https://www.gequbao.com/music/${id}`, {
    headers: { 'User-Agent': UA, 'Accept-Language': 'zh-CN,zh;q=0.9' },
  });
  const html = await detail.text();
  console.log('详情页:', detail.status, 'len', html.length);

  const d = parseAppData(html);
  console.log('appData:', d ? JSON.stringify({
    mp3_id: d.mp3_id, title: d.mp3_title, author: d.mp3_author,
    cover: d.mp3_cover, duration: d.mp3_duration, lrc_is_empty: d.lrc_is_empty,
    play_id: String(d.play_id).slice(0, 40) + '...',
  }, null, 1) : '解析失败');

  // 歌词
  const lrcM = html.match(/<div[^>]+id="content-lrc"[^>]*>([\s\S]*?)<\/div>/i);
  const lrc = lrcM ? lrcM[1].replace(/<br\s*\/?>/gi, '\n').replace(/<[^>]*>/g, '') : '';
  console.log('歌词长度:', lrc.length, '| 前 120 字:', JSON.stringify(lrc.trim().slice(0, 120)));

  if (!d) return;

  // 播放地址
  const body = new URLSearchParams({ id: String(d.play_id), purpose: 'play' });
  const r = await fetch('https://www.gequbao.com/member/common-play-url', {
    method: 'POST',
    headers: {
      'User-Agent': UA,
      'Content-Type': 'application/x-www-form-urlencoded; charset=UTF-8',
      'X-Requested-With': 'XMLHttpRequest',
      Referer: `https://www.gequbao.com/music/${id}`,
      'Accept-Language': 'zh-CN,zh;q=0.9',
      Accept: 'application/json, text/javascript, */*; q=0.01',
    },
    body: body.toString(),
  });
  const txt = await r.text();
  console.log('common-play-url:', r.status, '→', txt.slice(0, 300));

  let url = '';
  try { const j = JSON.parse(txt); if (j.code === 1) url = j.data.url; } catch (e) { /* noop */ }
  if (!url) return;
  console.log('原始 url:', url.slice(0, 160));

  // kuwo antiserver → convert_url3 转换
  let finalUrl = url;
  if (url.includes('antiserver.kuwo.cn')) {
    const u = new URL(url);
    u.searchParams.set('type', 'convert_url3');
    const conv = await fetch(u.href, { headers: { 'User-Agent': UA, Referer: 'https://www.gequbao.com/' } });
    const ct = await conv.text();
    console.log('convert_url3:', conv.status, ct.slice(0, 200));
    try { const cj = JSON.parse(ct); if (cj.code === 200) finalUrl = cj.url; } catch (e) { /* noop */ }
  }
  console.log('最终音频 URL:', finalUrl.slice(0, 160));

  // 实际下载验证
  const head = await fetch(finalUrl, {
    headers: { 'User-Agent': UA, Referer: 'https://www.gequbao.com/', Range: 'bytes=0-2047' },
  });
  const buf = await head.arrayBuffer();
  const head2 = Buffer.from(buf.slice(0, 4));
  console.log('音频响应:', head.status, '| content-type:', head.headers.get('content-type'),
    '| content-length:', head.headers.get('content-length'),
    '| 首字节:', head2.toString('hex'), head2.toString('latin1'));
})();
