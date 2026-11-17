/* 桌面端回归：点搜索结果后抽屉应保持打开（不遮挡播放区） */
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
  const prof = path.resolve('.tmp', 'profDesk2');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', '--mute-audio',
    '--autoplay-policy=no-user-gesture-required', '--remote-debugging-port=9255',
    '--user-data-dir=' + prof, '--window-size=1440,900', 'about:blank'], { stdio: 'ignore', detached: true });
  proc.unref();
  let list = null;
  for (let i = 0; i < 60; i++) { try { list = await hj(9255, '/json/list'); if (list.length) break; } catch (e) {} await sleep(400); }
  const ws = new WebSocket(list[0].webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 0; const pend = new Map();
  ws.onmessage = (ev) => { const m = JSON.parse(ev.data); if (m.id && pend.has(m.id)) { const { resolve, reject } = pend.get(m.id); pend.delete(m.id); m.error ? reject(new Error(JSON.stringify(m.error))) : resolve(m.result); } };
  const send = (method, params) => new Promise((resolve, reject) => { const id = ++seq; pend.set(id, { resolve, reject }); ws.send(JSON.stringify({ id, method, params: params || {} })); });
  const ev = async (x) => { const r = await send('Runtime.evaluate', { expression: x, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails).slice(0, 240)); return r.result.value; };
  const errs = [];
  ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.method === 'Runtime.exceptionThrown') errs.push(1); });

  await send('Page.enable'); await send('Runtime.enable');
  await send('Page.navigate', { url: 'http://127.0.0.1:5173/' });
  await sleep(2500);
  await ev(`(() => {
    const i = document.querySelector('#dbQuery');
    i.value = '晴天';
    i.dispatchEvent(new Event('input', { bubbles: true }));
    i.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    return 1;
  })()`);
  // 轮询等结果出现，最多 25 秒
  let n = 0;
  for (let i = 0; i < 25; i++) {
    await sleep(1000);
    n = await ev(`document.querySelectorAll('#siteCands .cand').length`);
    if (n) break;
  }
  const before = await ev(`(() => ({ narrow: matchMedia('(max-width: 640px)').matches, open: document.querySelector('#drawer').classList.contains('open'), n: document.querySelectorAll('#siteCands .cand').length }))()`);
  console.log('桌面搜索:', JSON.stringify(before));

  await ev(`(() => { const c = document.querySelector('#siteCands .cand'); if (c) c.click(); return 1; })()`);
  await sleep(9000);
  const after = await ev(`(() => { const m = document.querySelector('#media'); const d = document.querySelector('#drawer').getBoundingClientRect(); return { open: document.querySelector('#drawer').classList.contains('open'), t: Number(m.currentTime.toFixed(1)), paused: m.paused, entries: document.querySelectorAll('#plist li').length, drawerLeft: Math.round(d.left), winW: innerWidth }; })()`);
  console.log('桌面点击后:', JSON.stringify(after));
  console.log('异常:', errs.length ? errs.length : '无');
  ws.close(); proc.kill(); process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
