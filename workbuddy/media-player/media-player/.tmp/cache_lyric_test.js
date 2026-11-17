/* 验证：播放时歌词工具栏隐藏；在线播放自动缓存为本地文件并可复播 */
const http = require('http');
const fs = require('fs');
const path = require('path');
const { spawn } = require('child_process');
const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const hj = (p, u) => new Promise((res, rej) => {
  http.get({ host: '127.0.0.1', port: p, path: u }, (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } }); }).on('error', rej);
});
const DL = path.resolve('downloads');

(async () => {
  const prof = path.resolve('.tmp', 'profCache');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', '--mute-audio',
    '--autoplay-policy=no-user-gesture-required', '--remote-debugging-port=9261',
    '--user-data-dir=' + prof, '--window-size=1440,900', 'about:blank'], { stdio: 'ignore', detached: true });
  proc.unref();
  let list = null;
  for (let i = 0; i < 60; i++) { try { list = await hj(9261, '/json/list'); if (list.length) break; } catch (e) {} await sleep(400); }
  const ws = new WebSocket(list[0].webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 0; const pend = new Map();
  ws.onmessage = (ev) => { const m = JSON.parse(ev.data); if (m.id && pend.has(m.id)) { const { resolve, reject } = pend.get(m.id); pend.delete(m.id); m.error ? reject(new Error(JSON.stringify(m.error))) : resolve(m.result); } };
  const send = (method, params) => new Promise((resolve, reject) => { const id = ++seq; pend.set(id, { resolve, reject }); ws.send(JSON.stringify({ id, method, params: params || {} })); });
  const ev = async (x) => { const r = await send('Runtime.evaluate', { expression: x, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails).slice(0, 240)); return r.result.value; };
  const errs = [];
  ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.method === 'Runtime.exceptionThrown') errs.push(String(m.params.exceptionDetails.text).slice(0, 120)); });

  await send('Page.enable'); await send('Runtime.enable');
  await send('Page.navigate', { url: 'http://127.0.0.1:5173/' });
  await sleep(2500);

  const before = fs.readdirSync(DL).length;
  console.log('下载目录原有文件数:', before);

  // 搜一首还没下过的
  await ev(`(() => {
    const i = document.querySelector('#dbQuery');
    i.value = '青花瓷';
    i.dispatchEvent(new Event('input', { bubbles: true }));
    i.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    return 1;
  })()`);
  let n = 0;
  for (let i = 0; i < 30; i++) { await sleep(1000); n = await ev(`document.querySelectorAll('#siteCands .cand').length`); if (n) break; }
  console.log('搜索结果条数:', n);

  await ev(`(() => { const c = document.querySelector('#siteCands .cand'); if (c) c.click(); return 1; })()`);
  await sleep(9000);

  const mid = await ev(`(() => {
    const li = document.querySelector('#plist li');
    const head = document.querySelector('.lyric-head');
    const box = document.querySelector('#lyricBox');
    const m = document.querySelector('#media');
    return {
      playing: document.querySelector('#player').classList.contains('playing'),
      t: Number(m.currentTime.toFixed(1)),
      lyricVisible: !box.hidden,
      headOpacity: getComputedStyle(head).opacity,
      headPointer: getComputedStyle(head).pointerEvents,
      status: li ? li.querySelector('.status').className : '',
      srcTail: (m.currentSrc || m.src).slice(-46),
    };
  })()`);
  console.log('播放中:', JSON.stringify(mid));

  // 等缓存完成（进度条消失 / 状态变 local）
  let cached = null;
  for (let i = 0; i < 30; i++) {
    await sleep(1500);
    cached = await ev(`(() => {
      const li = document.querySelector('#plist li');
      const st = li ? li.querySelector('.status').className : '';
      const dl = li ? li.querySelector('.pl-dl') : null;
      const m = document.querySelector('#media');
      return { status: st, dlHidden: dl ? dl.hidden : null, t: Number(m.currentTime.toFixed(1)), paused: m.paused, srcTail: (m.currentSrc || m.src).slice(-46) };
    })()`);
    if (cached.status.includes('local')) break;
  }
  console.log('缓存完成后:', JSON.stringify(cached));
  console.log('下载目录文件数:', fs.readdirSync(DL).length, '新增:', fs.readdirSync(DL).length - before);

  // 暂停：歌词工具栏应出现
  const paused = await ev(`(async () => {
    document.querySelector('#media').pause();
    await new Promise(r => setTimeout(r, 400));
    const head = document.querySelector('.lyric-head');
    return { opacity: getComputedStyle(head).opacity, pointer: getComputedStyle(head).pointerEvents, playing: document.querySelector('#player').classList.contains('playing') };
  })()`);
  console.log('暂停后工具栏:', JSON.stringify(paused));

  // 刷新：历史恢复后应直接是本地文件
  await send('Page.navigate', { url: 'http://127.0.0.1:5173/' });
  await sleep(3000);
  const restored = await ev(`(async () => {
    const lis = [...document.querySelectorAll('#plist li')];
    const li = lis[0];
    if (!li) return { n: 0 };
    li.click();
    await new Promise(r => setTimeout(r, 2500));
    const m = document.querySelector('#media');
    return { n: lis.length, status: li.querySelector('.status').className, srcTail: (m.currentSrc || m.src).slice(-52), t: Number(m.currentTime.toFixed(1)), paused: m.paused };
  })()`);
  console.log('刷新恢复:', JSON.stringify(restored));
  console.log('异常:', errs.length ? errs : '无');

  ws.close(); proc.kill(); process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
