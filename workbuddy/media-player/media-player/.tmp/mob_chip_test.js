/* 手机端：缓存提示可见 + 列表里「存到本机」按钮 */
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
  const prof = path.resolve('.tmp', 'profMobChip');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', '--mute-audio',
    '--autoplay-policy=no-user-gesture-required', '--remote-debugging-port=9273',
    '--user-data-dir=' + prof, '--window-size=390,844', 'about:blank'], { stdio: 'ignore', detached: true });
  proc.unref();
  let list = null;
  for (let i = 0; i < 60; i++) { try { list = await hj(9273, '/json/list'); if (list.length) break; } catch (e) {} await sleep(400); }
  const ws = new WebSocket(list[0].webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 0; const pend = new Map();
  ws.onmessage = (ev) => { const m = JSON.parse(ev.data); if (m.id && pend.has(m.id)) { const { resolve, reject } = pend.get(m.id); pend.delete(m.id); m.error ? reject(new Error(JSON.stringify(m.error))) : resolve(m.result); } };
  const send = (method, params) => new Promise((resolve, reject) => { const id = ++seq; pend.set(id, { resolve, reject }); ws.send(JSON.stringify({ id, method, params: params || {} })); });
  const ev = async (x) => { const r = await send('Runtime.evaluate', { expression: x, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails).slice(0, 240)); return r.result.value; };
  const errs = [];
  ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.method === 'Runtime.exceptionThrown') errs.push(String(m.params.exceptionDetails.text).slice(0, 120)); });

  await send('Page.enable'); await send('Runtime.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
  await send('Page.navigate', { url: 'http://127.0.0.1:5173/' });
  await sleep(2500);

  await ev(`(() => {
    const i = document.querySelector('#dbQuery');
    i.value = '简单爱';
    i.dispatchEvent(new Event('input', { bubbles: true }));
    i.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    return 1;
  })()`);
  for (let k = 0; k < 25; k++) { await sleep(1000); if (await ev(`document.querySelectorAll('#siteCands .cand').length`)) break; }
  await ev(`(() => { const c = document.querySelector('#siteCands .cand'); if (c) c.click(); return 1; })()`);

  // 采样 chip 状态
  const samples = [];
  for (let k = 0; k < 24; k++) {
    await sleep(700);
    samples.push(await ev(`(() => { const c = document.querySelector('#ptCache'); return { hidden: c.hidden, txt: document.querySelector('#ptCacheTxt').textContent, done: c.classList.contains('done'), drawerOpen: document.querySelector('#drawer').classList.contains('open') }; })()`));
  }
  console.log('chip 采样:', JSON.stringify(samples.filter((s, i) => i % 3 === 0 || s.done)));

  // 打开列表：本地条目 + 存到本机按钮
  const info = await ev(`(async () => {
    document.querySelector('#navGroup .navbtn[data-panel="list"]').click();
    await new Promise(r => setTimeout(r, 600));
    const li = document.querySelector('#plist li');
    const btn = li ? li.querySelector('.savebtn') : null;
    let href = '';
    if (btn) {
      const orig = HTMLAnchorElement.prototype.click;
      HTMLAnchorElement.prototype.click = function () { href = this.href; };
      btn.click();
      HTMLAnchorElement.prototype.click = orig;
    }
    return { status: li.querySelector('.status').className, hasSave: !!btn, href: href, cur: li.classList.contains('cur') };
  })()`);
  console.log('列表条目:', JSON.stringify(info));
  console.log('异常:', errs.length ? errs : '无');
  ws.close(); proc.kill(); process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
