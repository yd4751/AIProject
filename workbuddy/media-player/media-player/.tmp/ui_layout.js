'use strict';
/* 布局检查：默认满屏播放 → 打开信息抽屉 → 搜索后抽屉内结果 */
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
  const prof = path.join(TMP, 'profLayout');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--remote-debugging-port=9224', `--user-data-dir=${prof}`,
    '--window-size=1440,900', '--mute-audio', '--autoplay-policy=no-user-gesture-required',
    'about:blank',
  ], { stdio: 'ignore', detached: true });
  proc.unref();

  let list = null;
  for (let i = 0; i < 50; i++) {
    try { list = await httpJSON(9224, '/json/list'); if (list && list.length) break; } catch (e) { /* wait */ }
    await sleep(400);
  }
  const cdp = await attachCDP(list.find((t) => t.type === 'page'));
  await cdp.send('Page.enable');
  await cdp.send('Runtime.enable');
  await cdp.send('Page.navigate', { url: BASE });
  await sleep(2500);

  const metrics = () => cdp.evaluate(`(() => {
    const p = document.querySelector('.player').getBoundingClientRect();
    const d = document.querySelector('#drawer');
    return JSON.stringify({
      win: innerWidth + 'x' + innerHeight,
      player: Math.round(p.width) + 'x' + Math.round(p.height),
      fill: (p.width * p.height / (innerWidth * innerHeight) * 100).toFixed(1) + '%',
      drawerOpen: d.classList.contains('open'),
      drawerRect: Math.round(d.getBoundingClientRect().left) + '..' + Math.round(d.getBoundingClientRect().right),
      searchbar: !!document.querySelector('.searchbar'),
      navBtns: [...document.querySelectorAll('#navGroup .navbtn')].map(b => b.textContent.trim()),
    });
  })()`);

  console.log('A 默认态:', await metrics());
  await cdp.shot('L1_default.png');

  console.log('\nB 打开信息抽屉');
  await cdp.evaluate(`(() => { document.querySelector('#navGroup .navbtn[data-panel="info"]').click(); return 1 })()`);
  await sleep(700);
  console.log('  ', await metrics());
  await cdp.shot('L2_drawer_info.png');

  console.log('\nC 切到播放列表');
  await cdp.evaluate(`(() => { document.querySelector('#navGroup .navbtn[data-panel="list"]').click(); return 1 })()`);
  await sleep(600);
  console.log('  ', await metrics(), '| title:', await cdp.evaluate(`(() => document.querySelector('#drawerTitle').textContent)()`));
  await cdp.shot('L3_drawer_list.png');

  console.log('\nD 再点同一按钮应收起');
  await cdp.evaluate(`(() => { document.querySelector('#navGroup .navbtn[data-panel="list"]').click(); return 1 })()`);
  await sleep(600);
  console.log('  ', await metrics());

  console.log('\nE 顶栏搜索（回车）');
  await cdp.evaluate(`(() => { const q = document.querySelector('#dbQuery'); q.focus(); q.value = '七里香'; q.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); return 1 })()`);
  await sleep(9000);
  console.log('  ', await metrics());
  console.log('   结果数:', await cdp.evaluate(`(() => document.querySelectorAll('#siteCands .cand').length)`));
  await cdp.shot('L4_search.png');

  console.log('\nF Esc 关闭');
  await cdp.evaluate(`(() => { document.body.focus(); document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })); return 1 })()`);
  await sleep(500);
  console.log('  ', await metrics());

  console.log('\n控制台异常:', cdp.logs.length ? cdp.logs.join('\n') : '（无）');
  cdp.close();
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
