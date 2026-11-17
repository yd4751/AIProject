'use strict';
/* 冒烟：页面无 JS 异常；搜索七里香 → 点歌曲宝第一条 → 播放/或弹验证码层 */
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
    } else if (m.method === 'Log.entryAdded' && m.params.entry.level === 'error') {
      logs.push(m.params.entry.level + ': ' + m.params.entry.text);
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
  const prof = path.join(TMP, 'profCap');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    '--remote-debugging-port=9223', `--user-data-dir=${prof}`,
    '--window-size=1500,940', '--mute-audio', '--autoplay-policy=no-user-gesture-required',
    'about:blank',
  ], { stdio: 'ignore', detached: true });
  proc.unref();

  let list = null;
  for (let i = 0; i < 50; i++) {
    try { list = await httpJSON(9223, '/json/list'); if (list && list.length) break; } catch (e) { /* wait */ }
    await sleep(400);
  }
  const cdp = await attachCDP(list.find((t) => t.type === 'page'));
  await cdp.send('Page.enable');
  await cdp.send('Runtime.enable');
  await cdp.send('Log.enable');
  await cdp.send('Page.navigate', { url: BASE });
  await sleep(3000);

  // 1) 只勾歌曲宝，搜七里香
  await cdp.evaluate(`(() => {
    const chips = [...document.querySelectorAll('.site-chip')];
    chips.forEach(c => { const on = /歌曲宝/.test(c.textContent); c.classList.toggle('on', on); });
    document.querySelector('#dbQuery').value = '七里香';
    document.querySelector('#btnDbSearch').click();
    return chips.map(c => c.textContent + '=' + c.classList.contains('on')).join(', ');
  })()`);
  await sleep(9000);

  // 2) 点击歌曲宝分组第一条
  const clicked = await cdp.evaluate(`(() => {
    const kids = [...document.querySelector('#siteCands').children];
    for (let i = 0; i < kids.length; i++) {
      if (kids[i].classList.contains('cand-group') && /歌曲宝/.test(kids[i].textContent)) {
        const c = kids[i + 1];
        if (c && c.classList.contains('cand')) { c.click(); return c.textContent.replace(/\\s+/g, ' ').trim().slice(0, 30); }
      }
    }
    return '未找到歌曲宝分组';
  })()`);
  console.log('点击:', clicked);
  await sleep(5000);

  // 3) 状态检查
  const st = await cdp.evaluate(`(() => JSON.stringify({
    capShown: !document.querySelector('#capMask').hidden,
    capImgLoaded: (document.querySelector('#capImg') || {}).src ? String(document.querySelector('#capImg').src).slice(0, 30) : '',
    capInputFocused: document.activeElement === document.querySelector('#capInput'),
    toast: document.querySelector('#toast').hidden ? '(无)' : document.querySelector('#toast').textContent,
    playing: document.querySelector('#player').classList.contains('playing'),
    listN: document.querySelectorAll('#plist li').length,
  }))()`);
  console.log('状态:', st);

  // 4) 若弹了验证码层：截屏留证后取消
  const shown = /"capShown":true/.test(st);
  if (shown) {
    await cdp.shot('cap_popup.png');
    console.log('验证码弹层已弹出，截图 cap_popup.png');
    await cdp.evaluate(`(() => { document.querySelector('#capCancel').click(); return 1 })()`);
    await sleep(600);
    console.log('取消后 toast:', await cdp.evaluate(`(() => document.querySelector('#toast').textContent)()`));
  } else {
    await cdp.shot('cap_direct.png');
    console.log('未弹验证码（会话直通），截图 cap_direct.png');
  }

  console.log('控制台错误:', cdp.logs.length ? cdp.logs.join('\n') : '（无）');
  cdp.close();
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
