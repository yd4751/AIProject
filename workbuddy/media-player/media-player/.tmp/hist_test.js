/* 验证刷新后播放历史能重建播放列表：覆盖站点条目 / 服务端路径 / 浏览器本地文件三类 */
const http = require('http');
const fs = require('fs');
const path = require('path');
const { spawn } = require('child_process');

const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function httpJSON(port, p) {
  return new Promise((res, rej) => {
    http.get({ host: '127.0.0.1', port, path: p }, (r) => {
      let d = '';
      r.on('data', (c) => (d += c));
      r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } });
    }).on('error', rej);
  });
}

(async () => {
  const prof = path.resolve('.tmp', 'profHist_' + Date.now().toString(36));
  const proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', '--mute-audio',
    '--remote-debugging-port=9231', '--user-data-dir=' + prof, '--window-size=1440,900', 'about:blank'],
    { stdio: 'ignore', detached: true });
  proc.unref();

  let list = null;
  for (let i = 0; i < 50; i++) {
    try { list = await httpJSON(9231, '/json/list'); if (list.length) break; } catch (e) { /* wait */ }
    await sleep(400);
  }
  const ws = new WebSocket(list[0].webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 0;
  const pend = new Map();
  ws.onmessage = (ev) => {
    const m = JSON.parse(ev.data);
    if (m.id && pend.has(m.id)) {
      const { resolve, reject } = pend.get(m.id);
      pend.delete(m.id);
      if (m.error) reject(new Error(JSON.stringify(m.error))); else resolve(m.result);
    }
  };
  const send = (method, params) => new Promise((resolve, reject) => {
    const id = ++seq; pend.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params: params || {} }));
  });
  const ev = async (expr) => {
    const r = await send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error((r.exceptionDetails.exception && r.exceptionDetails.exception.description) || 'evaluate error');
    return r.result.value;
  };

  await send('Page.enable');
  await send('Runtime.enable');
  const errors = [];
  ws.addEventListener('message', (e) => {
    const m = JSON.parse(e.data);
    if (m.method === 'Runtime.exceptionThrown') errors.push(JSON.stringify(m.params.exceptionDetails).slice(0, 200));
    if (m.method === 'Runtime.consoleAPICalled' && m.params.type === 'error') {
      errors.push(m.params.args.map((a) => a.value || a.description || '').join(' ').slice(0, 200));
    }
  });

  const URL0 = 'http://127.0.0.1:5173';

  // 1) 首次进入，写入三条历史：站点条目、服务端路径、浏览器本地文件（后者刷新后应被跳过）
  await send('Page.navigate', { url: URL0 });
  await sleep(1800);
  await ev(`(() => {
    localStorage.setItem('mp.history', JSON.stringify([
      { id: 'r-last', name: '七里香 - 周杰伦.mp3', path: '', kind: 'audio', remote: true,
        siteId: 'gequbao', itemId: '4194', tags: { title: '七里香', artist: '周杰伦' },
        parsed: { title: '七里香', artist: '周杰伦' }, lyric: '', lyricSrc: '歌曲宝 · 歌词',
        cover: '', info: null, timestamp: Date.now() },
      { id: 's-old', name: '周杰伦 - 七里香.mp3', path: 'E:/Project/workbuddy/media-player/media-player/downloads/周杰伦 - 七里香.mp3',
        kind: 'audio', remote: false, siteId: '', itemId: '', tags: null,
        parsed: { title: '七里香', artist: '周杰伦' }, lyric: '', lyricSrc: '', timestamp: Date.now() - 1000 },
      { id: 'f-gone', name: '本地选的文件.mp3', path: '本地选的文件.mp3', kind: 'audio', remote: false,
        siteId: '', itemId: '', tags: null, parsed: { title: '本地选的文件', artist: '' },
        lyric: '', lyricSrc: '', timestamp: Date.now() - 2000 }
    ]));
    return 1;
  })()`);

  // 2) 刷新（真正的前端重载），检查列表
  await send('Page.navigate', { url: URL0 });
  await sleep(2200);

  const r1 = await ev(`(() => {
    const items = [...document.querySelectorAll('#plist li')].map(li => ({
      name: li.querySelector('.nm') && li.querySelector('.nm').textContent,
      remote: !!li.querySelector('.status.remote'),
      local: !!li.querySelector('.status.local'),
      hist: li.classList.contains('history')
    }));
    return { count: items.length, items,
      info: document.querySelector('#listInfo').textContent,
      toast: (document.querySelector('#toast') || {}).textContent || '' };
  })()`);
  console.log('刷新后列表:', JSON.stringify(r1, null, 1));

  // 3) 依次点本地条目与站点恢复条目，确认都能真正加载
  const r2 = await ev(`(async () => {
    const out = [];
    const items = [...document.querySelectorAll('#plist li')];
    for (let i = 0; i < items.length; i++) {
      items[i].click();
      await new Promise(r => setTimeout(r, i === 0 ? 1500 : 5000));
      const m = document.querySelector('#media');
      out.push({ idx: i, name: document.querySelector('#ptTitle').textContent,
        src: (m.getAttribute('src') || '').slice(0, 80),
        readyState: m.readyState, paused: m.paused, err: m.error && m.error.code,
        cur: !!document.querySelector('#plist li.cur') });
    }
    return out;
  })()`);
  console.log('逐条点击:', JSON.stringify(r2, null, 1));

  // 4) 删掉一条再刷新：不应复活
  await ev(`(() => { const del = document.querySelector('#plist li .del'); del && del.click(); return 1 })()`);
  await sleep(500);
  await send('Page.navigate', { url: URL0 });
  await sleep(2000);
  const r3 = await ev(`(() => ({ count: document.querySelectorAll('#plist li').length,
    info: document.querySelector('#listInfo').textContent }))()`);
  console.log('删除一条后刷新:', JSON.stringify(r3));

  // 5) 清空列表再刷新：应为空且不再提示恢复
  await ev(`(() => { document.querySelector('#btnClearList').click(); return 1 })()`);
  await sleep(500);
  await send('Page.navigate', { url: URL0 });
  await sleep(2000);
  const r4 = await ev(`(() => ({ count: document.querySelectorAll('#plist li').length,
    info: document.querySelector('#listInfo').textContent,
    toast: (document.querySelector('#toast') || {}).textContent || '',
    hist: localStorage.getItem('mp.history') }))()`);
  console.log('清空后刷新:', JSON.stringify(r4));

  console.log('异常:', errors.length ? errors : '无');
  ws.close();
  process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
