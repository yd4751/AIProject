'use strict';
/* 验证：下载中的条目在播放列表里显示进度条（用 fetch 拦截伪造「下载中」，只为验证 UI 层） */
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

const HOOK = `(() => {
  const orig = window.fetch;
  window.fetch = async function (url, opts) {
    const u = String(url);
    const res = await orig.call(window, url, opts);
    try {
      if (u.indexOf('/api/site/download') >= 0) {
        const j = await res.clone().json();
        if (j && j.task) {
          window.__fakeId = j.task.id;
          j.task.status = 'downloading';
          j.task.percent = 12;
          j.task.received = 400000; j.task.total = 3400000;
        }
        return new Response(JSON.stringify(j), { status: 200, headers: { 'Content-Type': 'application/json' } });
      }
      if (u.indexOf('/api/site/tasks') >= 0 && window.__fakeId) {
        const j = await res.clone().json();
        const base = (j.tasks || []).find(t => t.id === window.__fakeId) || { id: window.__fakeId, name: '测试', total: 3400000 };
        j.tasks = [Object.assign({}, base, { status: 'downloading', percent: 63, received: 2142000, total: 3400000 })];
        return new Response(JSON.stringify(j), { status: 200, headers: { 'Content-Type': 'application/json' } });
      }
    } catch (e) { /* 原样返回 */ }
    return res;
  };
})();`;

(async () => {
  const port = 9247;
  const prof = path.join(TMP, 'profDL');
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
  let seq = 0; const pending = new Map(); const logs = [];
  ws.onmessage = (ev) => {
    const m = JSON.parse(ev.data);
    if (m.id && pending.has(m.id)) { const p = pending.get(m.id); pending.delete(m.id); m.error ? p.reject(new Error(JSON.stringify(m.error))) : p.resolve(m.result); }
    else if (m.method === 'Runtime.exceptionThrown') logs.push('EXC: ' + ((m.params.exceptionDetails.exception && m.params.exceptionDetails.exception.description) || ''));
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
  const shot = async (name) => {
    const r = await send('Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(TMP, name), Buffer.from(r.data, 'base64'));
    return name;
  };

  await send('Page.enable'); await send('Runtime.enable');
  await send('Page.addScriptToEvaluateOnNewDocument', { source: HOOK });
  await send('Page.navigate', { url: BASE });
  await sleep(3500);
  const log = (...a) => console.log(a.join(' '));

  await ev(`(() => { const s=document.querySelector('#dbKindSel'); s.value='music'; s.dispatchEvent(new Event('change')); document.querySelector('#dbQuery').value='稻香'; document.querySelector('#dbBtnSearch')||document.querySelector('#btnDbSearch').click(); return 1 })()`);
  await sleep(18000);
  await ev(`(() => { const b=[...document.querySelectorAll('#infoTabs .subtab')].find(x=>/站点资源/.test(x.textContent)); if(b) b.click(); const t=[...document.querySelectorAll('#tabs .tab')].find(x=>/播放列表/.test(x.textContent)); if(t) t.click(); return 1 })()`);
  await sleep(400);
  await ev(`(() => { const c=[...document.querySelectorAll('#siteCands .cand')].find(x=>/周杰伦/.test(x.textContent)&&/稻香/.test(x.textContent)); if(c) c.click(); return 1 })()`);
  await sleep(5000);

  log('播放列表条目:', await ev(`(() => JSON.stringify([...document.querySelectorAll('#plist li')].map(li => ({
    name: (li.querySelector('.nm')||{}).textContent,
    hasDl: li.classList.contains('has-dl'),
    dlVisible: li.querySelector('.pl-dl') ? !li.querySelector('.pl-dl').hidden : null,
    width: (li.querySelector('.pl-track i')||{}).style ? li.querySelector('.pl-track i').style.width : null,
    pct: (li.querySelector('.pl-pct')||{}).textContent,
    standalone: !!document.querySelector('#dlBox'),
  }))))()`));
  log('控制台异常:', logs.length ? logs.slice(0, 4).join(' | ') : '无');
  await shot('ui2_progress.png');
  await send('Browser.close').catch(() => {});
  process.exit(0);
})();
