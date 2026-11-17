/* 截图：播放中（歌词层）工具栏应不可见；暂停后出现 */
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
  const prof = path.resolve('.tmp', 'profShot2');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', '--mute-audio',
    '--autoplay-policy=no-user-gesture-required', '--remote-debugging-port=9263',
    '--user-data-dir=' + prof, '--window-size=1440,900', 'about:blank'], { stdio: 'ignore', detached: true });
  proc.unref();
  let list = null;
  for (let i = 0; i < 60; i++) { try { list = await hj(9263, '/json/list'); if (list.length) break; } catch (e) {} await sleep(400); }
  const ws = new WebSocket(list[0].webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 0; const pend = new Map();
  ws.onmessage = (ev) => { const m = JSON.parse(ev.data); if (m.id && pend.has(m.id)) { const { resolve, reject } = pend.get(m.id); pend.delete(m.id); m.error ? reject(new Error(JSON.stringify(m.error))) : resolve(m.result); } };
  const send = (method, params) => new Promise((resolve, reject) => { const id = ++seq; pend.set(id, { resolve, reject }); ws.send(JSON.stringify({ id, method, params: params || {} })); });
  const ev = async (x) => { const r = await send('Runtime.evaluate', { expression: x, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails).slice(0, 200)); return r.result.value; };
  await send('Page.enable'); await send('Runtime.enable');

  async function shot(name) {
    const r = await send('Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.resolve('.tmp', name), Buffer.from(r.data, 'base64'));
  }

  for (const v of [{ n: 'C_desktop', w: 1440, h: 900, mobile: false }, { n: 'C_mobile', w: 390, h: 844, mobile: true }]) {
    await send('Emulation.setDeviceMetricsOverride', { width: v.w, height: v.h, deviceScaleFactor: 1, mobile: v.mobile });
    await send('Page.navigate', { url: 'http://127.0.0.1:5173/' });
    await sleep(2500);
    // 搜一首并播放（历史为空，用搜索结果驱动）
    await ev(`(() => {
      const i = document.querySelector('#dbQuery');
      i.value = '青花瓷';
      i.dispatchEvent(new Event('input', { bubbles: true }));
      i.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
      return 1;
    })()`);
    for (let k = 0; k < 25; k++) { await sleep(1000); if (await ev(`document.querySelectorAll('#siteCands .cand').length`)) break; }
    await ev(`(() => { const c = document.querySelector('#siteCands .cand'); if (c) c.click(); return 1; })()`);
    await sleep(9000);
    const st = await ev(`(() => {
      const box = document.querySelector('#lyricBox');
      const head = document.querySelector('.lyric-head');
      return { boxHidden: box.hidden, opacity: getComputedStyle(head).opacity, playing: document.querySelector('#player').classList.contains('playing'), lines: document.querySelectorAll('.lyric-line').length };
    })()`);
    console.log(v.n, '播放中:', JSON.stringify(st));
    await shot(v.n + '_playing.png');
    // 暂停
    await ev(`(async () => { document.querySelector('#media').pause(); await new Promise(r => setTimeout(r, 500)); return 1; })()`);
    await shot(v.n + '_paused.png');
  }
  ws.close(); proc.kill(); process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
