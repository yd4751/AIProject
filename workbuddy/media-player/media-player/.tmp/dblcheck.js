'use strict';
/* 排查：双击站点结果是否重复请求/未加入播放列表 */
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');

const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const BASE = 'http://127.0.0.1:5173';
const TMP = path.resolve(__dirname);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function httpJSON(port, p) {
  return new Promise((resolve, reject) => {
    http.get({ host: '127.0.0.1', port, path: p }, (res) => {
      let d = ''; res.on('data', (c) => (d += c));
      res.on('end', () => { try { resolve(JSON.parse(d)); } catch (e) { reject(e); } });
    }).on('error', reject);
  });
}

(async () => {
  const port = 9251;
  const prof = path.join(TMP, 'profDBL');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    `--remote-debugging-port=${port}`, `--user-data-dir=${prof}`,
    '--window-size=1500,950', '--mute-audio', '--autoplay-policy=no-user-gesture-required', 'about:blank',
  ], { stdio: 'ignore', detached: true });
  proc.unref();

  let list = null;
  for (let i = 0; i < 50; i++) {
    try { list = await httpJSON(port, '/json/list'); if (list && list.length) break; } catch (e) { /* wait */ }
    await sleep(400);
  }
  const ws = new WebSocket(list.find((t) => t.type === 'page').webSocketDebuggerUrl);
  await new Promise((r) => { ws.onopen = r; });
  let seq = 0; const pending = new Map(); const reqs = []; const logs = [];
  ws.onmessage = (ev) => {
    const m = JSON.parse(ev.data);
    if (m.id && pending.has(m.id)) { const p = pending.get(m.id); pending.delete(m.id); m.error ? p.reject(new Error(JSON.stringify(m.error))) : p.resolve(m.result); }
    else if (m.method === 'Network.requestWillBeSent') {
      const u = m.params.request.url;
      if (/\/api\/site\/(detail|download|play)/.test(u)) reqs.push(u.replace(/^https?:\/\/[^/]+/, ''));
    } else if (m.method === 'Runtime.exceptionThrown') logs.push('EXC: ' + ((m.params.exceptionDetails.exception && m.params.exceptionDetails.exception.description) || ''));
  };
  const send = (method, params) => new Promise((resolve, reject) => {
    const id = ++seq; pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params: params || {} }));
    setTimeout(() => { if (pending.has(id)) { pending.delete(id); reject(new Error('timeout ' + method)); } }, 90000);
  });
  const ev = async (expr) => {
    const r = await send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error((r.exceptionDetails.exception && r.exceptionDetails.exception.description) || 'eval err');
    return r.result.value;
  };
  await send('Page.enable'); await send('Runtime.enable'); await send('Network.enable');
  await send('Page.navigate', { url: BASE });
  await sleep(3500);
  const log = (...a) => console.log(a.join(' '));

  await ev(`(() => { const s=document.querySelector('#dbKindSel'); s.value='music'; s.dispatchEvent(new Event('change')); document.querySelector('#dbQuery').value='稻香'; document.querySelector('#btnDbSearch').click(); return 1 })()`);
  await sleep(18000);
  await ev(`(() => { const b=[...document.querySelectorAll('#infoTabs .subtab')].find(x=>/站点资源/.test(x.textContent)); if(b) b.click(); return 1 })()`);
  await sleep(400);

  log('双击前列表数:', await ev(`(() => document.querySelectorAll('#plist li').length)()`));
  reqs.length = 0;

  // 模拟真实双击：间隔 60ms 两次 click
  await ev(`(() => {
    const c=[...document.querySelectorAll('#siteCands .cand')].find(x=>/周杰伦/.test(x.textContent)&&/稻香/.test(x.textContent));
    if(!c) return 'nf';
    c.click();
    setTimeout(()=>{ c.click(); }, 60);
    return 'ok';
  })()`);
  await sleep(12000);

  log('双击后列表数:', await ev(`(() => document.querySelectorAll('#plist li').length)()`));
  log('列表内容:', await ev(`(() => JSON.stringify([...document.querySelectorAll('#plist li')].map(x=>x.querySelector('.nm').textContent)))()`));
  log('detail 请求次数:', reqs.filter(u=>u.includes('/detail')).length);
  log('download 请求次数:', reqs.filter(u=>u.includes('/download')).length);
  log('播放器:', await ev(`(() => { const m=document.querySelector('#media'); return JSON.stringify({paused:m.paused, t:+m.currentTime.toFixed(1), d:+m.duration.toFixed(1)}) })()`));
  log('控制台异常:', logs.length ? logs.slice(0,4).join(' | ') : '无');
  await send('Browser.close').catch(() => {});
  process.exit(0);
})();
