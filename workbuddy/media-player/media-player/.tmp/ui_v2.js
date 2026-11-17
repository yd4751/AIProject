'use strict';
/* 验证：品牌「小鹅」、无重新匹配、导入直连文件选择、换肤下拉 */
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');

const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const BASE = 'http://127.0.0.1:5173';
const TMP = path.resolve(__dirname);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function httpJSON(port, urlPath) {
  return new Promise((resolve, reject) => {
    http.get({ host: '127.0.0.1', port, path: urlPath }, (res) => {
      let d = '';
      res.on('data', (c) => (d += c));
      res.on('end', () => { try { resolve(JSON.parse(d)); } catch (e) { reject(e); } });
    }).on('error', reject);
  });
}

async function attachCDP(page) {
  const ws = new WebSocket(page.webSocketDebuggerUrl);
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
    } else if (m.method === 'Runtime.exceptionThrown') {
      const d = m.params.exceptionDetails;
      logs.push('EXCEPTION: ' + ((d.exception && d.exception.description) || d.text || ''));
    }
  };
  const send = (method, params) => new Promise((resolve, reject) => {
    const id = ++seq;
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params: params || {} }));
    setTimeout(() => { if (pending.has(id)) { pending.delete(id); reject(new Error('timeout: ' + method)); } }, 45000);
  });
  const evaluate = async (expr) => {
    const r = await send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error((r.exceptionDetails.exception && r.exceptionDetails.exception.description) || 'eval error');
    return r.result.value;
  };
  const shot = async (name) => {
    const r = await send('Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(TMP, name), Buffer.from(r.data, 'base64'));
    return name;
  };
  return { send, evaluate, shot, logs, close: () => { try { ws.close(); } catch (e) { /* ignore */ } } };
}

(async () => {
  const prof = path.join(TMP, 'profV2');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--remote-debugging-port=9225', `--user-data-dir=${prof}`,
    '--window-size=1440,900', '--mute-audio', '--autoplay-policy=no-user-gesture-required',
    'about:blank',
  ], { stdio: 'ignore', detached: true });
  proc.unref();

  let list = null;
  for (let i = 0; i < 50; i++) {
    try { list = await httpJSON(9225, '/json/list'); if (list && list.length) break; } catch (e) { /* wait */ }
    await sleep(400);
  }
  const cdp = await attachCDP(list.find((t) => t.type === 'page'));
  await cdp.send('Page.enable');
  await cdp.send('Runtime.enable');
  await cdp.send('Page.navigate', { url: BASE });
  await sleep(2500);

  console.log('1) 品牌:', await cdp.evaluate(`(() => JSON.stringify({
    title: document.title,
    name: document.querySelector('.brand-text').firstChild.textContent.trim(),
    sub: document.querySelector('.brand-text em').textContent,
    icon: document.querySelector('.brand-penguin').getAttribute('src'),
    iconLoaded: document.querySelector('.brand-penguin').naturalWidth > 0,
    retryBtn: !!document.querySelector('#btnDbRetry'),
    importPop: !!document.querySelector('#importPop'),
  }))()`));

  console.log('\n2) 导入按钮直接弹文件框');
  await cdp.evaluate(`(() => { window.__fc = 0; document.querySelector('#fileInput').addEventListener('click', () => window.__fc++); return 1 })()`);
  await cdp.evaluate(`(() => { document.querySelector('#btnImport').click(); return 1 })()`);
  await sleep(500);
  console.log('   fileInput 被点击次数:', await cdp.evaluate(`(() => window.__fc)()`));

  console.log('\n3) 换肤下拉');
  console.log('   初始:', await cdp.evaluate(`(() => JSON.stringify({
    skin: document.documentElement.dataset.skin,
    label: document.querySelector('#skinCur').textContent,
    menuHidden: document.querySelector('#skinMenu').hidden,
    items: document.querySelectorAll('#skinMenu .skin-item').length,
  }))()`));
  await cdp.evaluate(`(() => { document.querySelector('#skinBtn').click(); return 1 })()`);
  await sleep(400);
  console.log('   点开:', await cdp.evaluate(`(() => JSON.stringify({
    menuHidden: document.querySelector('#skinMenu').hidden,
    expanded: document.querySelector('#skinBtn').getAttribute('aria-expanded'),
    active: (document.querySelector('#skinMenu .skin-item.active') || {}).textContent,
  }))()`));
  await cdp.shot('V1_skin_menu.png');

  await cdp.evaluate(`(() => { [...document.querySelectorAll('#skinMenu .skin-item')].find(b => b.dataset.skinId === 'sakura').click(); return 1 })()`);
  await sleep(600);
  console.log('   选「樱」后:', await cdp.evaluate(`(() => JSON.stringify({
    skin: document.documentElement.dataset.skin,
    label: document.querySelector('#skinCur').textContent,
    menuHidden: document.querySelector('#skinMenu').hidden,
    active: (document.querySelector('#skinMenu .skin-item.active') || {}).textContent,
    saved: localStorage.getItem('mp.skin'),
  }))()`));
  await cdp.shot('V2_skin_applied.png');

  console.log('\n4) 点外部关闭菜单');
  await cdp.evaluate(`(() => { document.querySelector('#skinBtn').click(); return 1 })()`);
  await sleep(300);
  await cdp.evaluate(`(() => { document.querySelector('.player').click(); return 1 })()`);
  await sleep(300);
  console.log('   menu hidden:', await cdp.evaluate(`(() => document.querySelector('#skinMenu').hidden)`));

  // 恢复默认皮肤
  await cdp.evaluate(`(() => { localStorage.setItem('mp.skin','midnight'); return 1 })()`);

  console.log('\n控制台异常:', cdp.logs.length ? cdp.logs.join('\n') : '（无）');
  cdp.close();
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
