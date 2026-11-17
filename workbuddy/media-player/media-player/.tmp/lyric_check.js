'use strict';
/* 检查：歌词是否随时间滚动（观察 #lyricInner 的 transform + 当前行） */
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
      let d = '';
      res.on('data', (c) => (d += c));
      res.on('end', () => { try { resolve(JSON.parse(d)); } catch (e) { reject(e); } });
    }).on('error', reject);
  });
}

(async () => {
  const port = 9224;
  const prof = path.join(TMP, 'prof3');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    `--remote-debugging-port=${port}`, `--user-data-dir=${prof}`,
    '--window-size=1500,940', '--mute-audio', '--autoplay-policy=no-user-gesture-required', 'about:blank',
  ], { stdio: 'ignore', detached: true });
  proc.unref();

  let list = null;
  for (let i = 0; i < 50; i++) {
    try { list = await httpJSON(port, '/json/list'); if (list && list.length) break; } catch (e) { /* wait */ }
    await sleep(400);
  }
  const page = list.find((t) => t.type === 'page');
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((r) => { ws.onopen = r; });
  let seq = 0; const pending = new Map(); const logs = [];
  ws.onmessage = (ev) => {
    const m = JSON.parse(ev.data);
    if (m.id && pending.has(m.id)) { const p = pending.get(m.id); pending.delete(m.id); m.error ? p.reject(new Error(JSON.stringify(m.error))) : p.resolve(m.result); }
    else if (m.method === 'Log.entryAdded') logs.push(m.params.entry.level + ': ' + m.params.entry.text);
    else if (m.method === 'Runtime.exceptionThrown') logs.push('EXC: ' + ((m.params.exceptionDetails.exception && m.params.exceptionDetails.exception.description) || m.params.exceptionDetails.text || ''));
  };
  const send = (method, params) => new Promise((resolve, reject) => {
    const id = ++seq; pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params: params || {} }));
    setTimeout(() => { if (pending.has(id)) { pending.delete(id); reject(new Error('timeout')); } }, 45000);
  });
  const ev = async (expr) => {
    const r = await send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error((r.exceptionDetails.exception && r.exceptionDetails.exception.description) || 'eval err');
    return r.result.value;
  };
  await send('Page.enable'); await send('Runtime.enable'); await send('Log.enable');
  await send('Page.navigate', { url: BASE });
  await sleep(3500);

  // 在歌词格子有内容时读取状态
  const probe = () => ev(`(() => {
    const inner = document.querySelector('#lyricInner');
    const scroll = document.querySelector('#lyricScroll');
    const on = document.querySelector('#lyricInner .lyric-line.on');
    const lines = [...document.querySelectorAll('#lyricInner .lyric-line')];
    const m = document.querySelector('#media');
    return JSON.stringify({
      hidden: document.querySelector('#lyricBox').hidden,
      transform: inner.style.transform || '(none)',
      lines: lines.length,
      plist: document.querySelectorAll('#plist li').length,
      ptTitle: document.querySelector('#ptTitle').textContent,
      src: (document.querySelector('#media').src || '').slice(-40),
      curIdx: on ? lines.indexOf(on) : -1,
      curText: on ? on.textContent : '',
      total: lines.length,
      scrollH: scroll.clientHeight, innerH: inner.scrollHeight,
      t: Number(m.currentTime || 0).toFixed(1),
    });
  })()`);

  // 走已验证可行的链路：站点搜索 → 点击条目 → 自动播放并载入歌词
  await ev(`(() => { document.querySelector('#dbQuery').value = '晴天'; document.querySelector('#btnDbSearch').click(); return 1 })()`);
  await sleep(13000);
  console.log('点击:', await ev(`(() => {
    const kids = [...document.querySelector('#dbCands').children];
    for (let i = 0; i < kids.length; i++) {
      if (kids[i].classList.contains('cand-group') && /歌曲宝/.test(kids[i].textContent)) {
        const c = kids[i + 1];
        if (c && c.classList.contains('cand')) { c.click(); return 'clicked'; }
      }
    }
    return 'no-cand';
  })()`));
  await sleep(9000);
  console.log('载入后:', await probe());

  for (const t of [0, 20, 60, 120, 200]) {
    await ev(`(() => { const m = document.querySelector('#media'); m.currentTime = ${t}; return 1 })()`);
    await sleep(1500);
    console.log(`t=${t}s →`, await probe());
  }
  console.log('--- 控制台 ---'); console.log(logs.slice(0,10).join('\n') || '(无)'); await send('Browser.close').catch(() => {});
  proc.kill();
  setTimeout(() => process.exit(0), 500);
})().catch((e) => { console.error('ERR', e.message); process.exit(1); });
