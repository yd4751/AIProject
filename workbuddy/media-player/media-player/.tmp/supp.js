const http = require('http');
const path = require('path');
const fs = require('fs');
const { spawn } = require('child_process');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const hj = (p, u) => new Promise((res, rej) => {
  http.get({ host: '127.0.0.1', port: p, path: u }, (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } }); }).on('error', rej);
});
(async () => {
  const prof = path.resolve('.tmp', 'profSup');
  fs.rmSync(prof, { recursive: true, force: true });
  const p = spawn('C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
    ['--headless=new', '--disable-gpu', '--no-first-run', '--remote-debugging-port=9246', '--user-data-dir=' + prof, 'about:blank'],
    { stdio: 'ignore', detached: true });
  p.unref();
  let l = null;
  for (let i = 0; i < 50; i++) { try { l = await hj(9246, '/json/list'); if (l.length) break; } catch (e) {} await sleep(300); }
  const ws = new WebSocket(l[0].webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 0; const pend = new Map();
  ws.onmessage = (e) => { const m = JSON.parse(e.data); if (m.id && pend.has(m.id)) { const { resolve, reject } = pend.get(m.id); pend.delete(m.id); m.error ? reject(new Error(JSON.stringify(m.error))) : resolve(m.result); } };
  const send = (m, pm) => new Promise((resolve, reject) => { const id = ++seq; pend.set(id, { resolve, reject }); ws.send(JSON.stringify({ id, method: m, params: pm || {} })); });
  await send('Runtime.enable');
  const expr = `JSON.stringify({
    dvh: CSS.supports('height', '88dvh'),
    minDvhPct: CSS.supports('height', 'min(88dvh, 100%)'),
    minDvhVh: CSS.supports('height', 'min(88dvh, 84vh)'),
    ver: navigator.userAgent
  })`;
  const r = await send('Runtime.evaluate', { expression: expr, returnByValue: true });
  console.log(r.result.value);
  ws.close(); process.exit(0);
})().catch((e) => { console.error(e); process.exit(1); });
