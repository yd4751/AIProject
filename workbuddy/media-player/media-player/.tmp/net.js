'use strict';
/* 抓取页面中的非 2xx 请求 */
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');
const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const getJSON = (port, p) => new Promise((res, rej) => {
  http.get({ host: '127.0.0.1', port, path: p }, (r) => {
    let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } });
  }).on('error', rej);
});

(async () => {
  const port = 9334;
  const prof = path.join(__dirname, 'prof2');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', `--remote-debugging-port=${port}`, `--user-data-dir=${prof}`, '--window-size=1400,900', '--mute-audio', 'about:blank'], { stdio: 'ignore', detached: true });
  proc.unref();
  let list = null;
  for (let i = 0; i < 50; i++) { try { list = await getJSON(port, '/json/list'); if (list && list.length) break; } catch (e) {} await sleep(400); }
  const page = list.find((t) => t.type === 'page');
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 0; const pending = new Map(); const bad = [];
  ws.onmessage = (ev) => {
    const m = JSON.parse(ev.data);
    if (m.id && pending.has(m.id)) { const p = pending.get(m.id); pending.delete(m.id); m.error ? p.reject(new Error('x')) : p.resolve(m.result); return; }
    if (m.method === 'Network.responseReceived') {
      const { response } = m.params;
      if (response.status >= 400) bad.push(response.status + ' ' + response.url);
    }
  };
  const send = (method, params) => new Promise((resolve, reject) => { const id = ++seq; pending.set(id, { resolve, reject }); ws.send(JSON.stringify({ id, method, params: params || {} })); });
  await send('Network.enable');
  await send('Page.enable');
  await send('Runtime.enable');
  await send('Page.navigate', { url: 'http://127.0.0.1:5173/' });
  await sleep(3000);
  const ev = async (expr) => (await send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true })).result;

  await ev(`(() => { document.querySelector('#dbQuery').value='晴天'; document.querySelector('#btnDbSearch').click(); return 1 })()`);
  await sleep(12000);
  console.log('== 搜索阶段非 2xx ==');
  console.log(bad.join('\n') || '（无）');

  bad.length = 0;
  await ev(`(() => { const kids=[...document.querySelector('#dbCands').children];
    for(let i=0;i<kids.length;i++){ if(kids[i].classList.contains('cand-group') && /爱听|6uq/.test(kids[i].textContent)){ const c=kids[i+1]; if(c){c.click();return 1} } }
    return 0 })()`);
  await sleep(15000);
  console.log('\n== 点击播放/下载阶段非 2xx ==');
  console.log(bad.join('\n') || '（无）');

  ws.close(); try { proc.kill(); } catch (e) {}
  process.exit(0);
})().catch((e) => { console.error('失败', e.message); process.exit(1); });
