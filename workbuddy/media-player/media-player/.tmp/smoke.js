'use strict';
/* 页面冒烟测试：加载页面、检查控制台错误、截图 */
const { spawn } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

const CHROME = 'C:/Program Files/Google/Chrome/Application/chrome.exe';
const PORT = 9222;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function getJSON(url) {
  return new Promise((resolve, reject) => {
    require('node:http').get(url, (res) => {
      let d = '';
      res.on('data', (c) => (d += c));
      res.on('end', () => { try { resolve(JSON.parse(d)); } catch (e) { reject(e); } });
    }).on('error', reject);
  });
}

(async () => {
  const prof = path.join(__dirname, 'prof');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    `--remote-debugging-port=${PORT}`, `--user-data-dir=${prof}`,
    '--window-size=1500,940', '--mute-audio', '--autoplay-policy=no-user-gesture-required',
    'about:blank',
  ], { stdio: 'ignore', detached: true });
  proc.unref();

  let list = null;
  for (let i = 0; i < 40; i++) {
    try { list = await getJSON(`http://127.0.0.1:${PORT}/json/list`); if (list.length) break; } catch (e) { /* wait */ }
    await sleep(400);
  }
  if (!list || !list.length) throw new Error('Chrome 未启动');
  const target = list.find((t) => t.type === 'page');
  const ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });

  let seq = 0;
  const pending = new Map();
  const logs = [];
  ws.onmessage = (ev) => {
    const m = JSON.parse(ev.data);
    if (m.id && pending.has(m.id)) {
      const { resolve, reject } = pending.get(m.id);
      pending.delete(m.id);
      m.error ? reject(new Error(JSON.stringify(m.error))) : resolve(m.result);
    } else if (m.method === 'Log.entryAdded') {
      logs.push(m.params.entry.level + ': ' + m.params.entry.text);
    } else if (m.method === 'Runtime.exceptionThrown') {
      const ex = m.params.exceptionDetails;
      logs.push('EXCEPTION: ' + ((ex.exception && ex.exception.description) || ex.text || ''));
    }
  };
  const send = (method, params) => new Promise((resolve, reject) => {
    const id = ++seq;
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params: params || {} }));
    setTimeout(() => { if (pending.has(id)) { pending.delete(id); reject(new Error('timeout ' + method)); } }, 30000);
  });
  const evaluate = async (expr) => {
    const r = await send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || 'eval error');
    return r.result.value;
  };

  await send('Page.enable');
  await send('Runtime.enable');
  await send('Log.enable');
  await send('Page.navigate', { url: 'http://127.0.0.1:5173/' });
  await sleep(4000);

  const info = await evaluate(`(() => {
    return JSON.stringify({
      title: document.title,
      hasMedia: !!document.querySelector('#media'),
      entries: document.querySelectorAll('#plist li').length,
      dbCardHidden: document.querySelector('#dbCard') ? document.querySelector('#dbCard').hidden : null,
      lyricBoxHidden: document.querySelector('#lyricBox') ? document.querySelector('#lyricBox').hidden : null,
      tabs: [...document.querySelectorAll('#tabs .tab')].map(t => t.textContent.trim()),
      siteChips: [...document.querySelectorAll('#siteRow .site-chip')].map(c => c.textContent.trim().slice(0, 12)),
      dlBoxHidden: document.querySelector('#dlBox') ? document.querySelector('#dlBox').hidden : null,
      skinSel: document.querySelector('#skinSel') ? document.querySelector('#skinSel').children.length : 0,
    });
  })()`);
  console.log('== 页面状态 ==');
  console.log(info);

  await send('Page.captureScreenshot', {}).then((r) => {
    fs.writeFileSync(path.join(__dirname, 'smoke.png'), Buffer.from(r.data, 'base64'));
  });
  console.log('\n== 控制台日志 ==');
  console.log(logs.length ? logs.slice(0, 20).join('\n') : '（无）');

  ws.close();
  try { proc.kill(); } catch (e) { /* ignore */ }
  process.exit(0);
})().catch((e) => { console.error('测试失败:', e.message); process.exit(1); });
