/* 复现用户场景：历史里只有一条歌曲宝在线条目（path 为空），刷新后点击应能播放 */
const http = require('http');
const fs = require('fs');
const path = require('path');
const { spawn } = require('child_process');
const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const hj = (p, u) => new Promise((res, rej) => {
  http.get({ host: '127.0.0.1', port: p, path: u }, (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } }); }).on('error', rej);
});
(async () => {
  const prof = path.resolve('.tmp', 'profDead9');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', '--mute-audio', '--autoplay-policy=no-user-gesture-required',
    '--remote-debugging-port=9240', '--user-data-dir=' + prof, '--window-size=1440,900', 'about:blank'],
    { stdio: 'ignore', detached: true });
  proc.unref();
  let list = null;
  for (let i = 0; i < 50; i++) { try { list = await hj(9240, '/json/list'); if (list.length) break; } catch (e) {} await sleep(400); }
  const ws = new WebSocket(list[0].webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 0; const pend = new Map();
  ws.onmessage = (ev) => { const m = JSON.parse(ev.data); if (m.id && pend.has(m.id)) { const { resolve, reject } = pend.get(m.id); pend.delete(m.id); m.error ? reject(new Error(JSON.stringify(m.error))) : resolve(m.result); } };
  const send = (method, params) => new Promise((resolve, reject) => { const id = ++seq; pend.set(id, { resolve, reject }); ws.send(JSON.stringify({ id, method, params: params || {} })); });
  const ev = async (x) => { const r = await send('Runtime.evaluate', { expression: x, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error('eval: ' + JSON.stringify(r.exceptionDetails).slice(0, 200)); return r.result.value; };
  await send('Page.enable'); await send('Runtime.enable');
  const U = 'http://127.0.0.1:5173';

  await send('Page.navigate', { url: U }); await sleep(1600);
  // 完全复刻用户浏览器里的历史：一条在线（无 path）、一条空 path 的本地
  await ev(`(() => { localStorage.setItem('mp.history', JSON.stringify([
    { id: 'rmw38bydi4sna', name: '张杰 - 天下.mp3', path: '', kind: 'audio', siteId: 'gequbao', itemId: '13379', remote: true,
      tags: { title: '天下', artist: '张杰', album: '' }, parsed: { title: '天下', artist: '张杰' }, lyric: '', lyricSrc: '歌曲宝 · 歌词', timestamp: Date.now() },
    { id: 'rmw381qn8zqqo', name: '张杰 - 天下.mp3', path: '', kind: 'audio', remote: false,
      tags: { title: '天下', artist: '张杰', album: '' }, parsed: { title: '天下', artist: '张杰' }, lyric: '', lyricSrc: '', timestamp: Date.now() - 5000 }
  ])); return 1 })()`);

  await send('Page.navigate', { url: U }); await sleep(2200);
  const s1 = await ev(`(() => { const li = document.querySelector('#plist li'); return {
    count: document.querySelectorAll('#plist li').length,
    remoteBadge: !!document.querySelector('#plist li .status.remote'),
    localBadge: !!document.querySelector('#plist li .status.local'),
    toast: (document.querySelector('#toast') || {}).textContent || '' } })()`);
  console.log('刷新后:', JSON.stringify(s1));

  const s2 = await ev(`(async () => {
    const li = document.querySelector('#plist li'); li.click();
    await new Promise(r => setTimeout(r, 6000));
    const m = document.querySelector('#media');
    return { src: (m.getAttribute('src') || '').slice(0, 120), readyState: m.readyState,
      paused: m.paused, err: m.error && m.error.code, currentTime: m.currentTime,
      toast: (document.querySelector('#toast') || {}).textContent || '' };
  })()`);
  console.log('点击播放:', JSON.stringify(s2));
  ws.close(); process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
