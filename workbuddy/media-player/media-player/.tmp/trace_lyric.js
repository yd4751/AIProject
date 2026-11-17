'use strict';
/* 诊断：谁在歌词显示后又把它 hidden 掉 */
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
  const port = 9263;
  const prof = path.join(TMP, 'profTR');
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
  let seq = 0; const pending = new Map();
  ws.onmessage = (ev) => {
    const m = JSON.parse(ev.data);
    if (m.id && pending.has(m.id)) { const p = pending.get(m.id); pending.delete(m.id); m.error ? p.reject(new Error(JSON.stringify(m.error))) : p.resolve(m.result); }
    else if (m.method === 'Runtime.consoleAPICalled') {
      const txt = (m.params.args || []).map((a) => a.value || a.description || '').join(' ');
      if (/^TRACE/.test(txt)) console.log('   ', txt);
    }
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
  await send('Page.enable'); await send('Runtime.enable');
  await send('Page.navigate', { url: BASE });
  await sleep(3500);

  // 劫持 hidden，只记录歌词盒的写入与调用栈
  await ev(`(() => {
    const box = document.querySelector('#lyricBox');
    let v = box.hidden;
    Object.defineProperty(box, 'hidden', {
      configurable: true,
      get() { return v; },
      set(nv) {
        if (nv !== v) {
          const st = (new Error().stack || '').split('\\n').slice(2, 5).map(s => s.trim().replace(/^at /, '').slice(0, 60)).join(' <- ');
          console.log('TRACE hidden ' + v + '→' + nv + ' :: ' + st);
          v = nv;
        }
      },
    });
    return 'hooked';
  })()`);

  await ev(`(() => { const s=document.querySelector('#dbKindSel'); s.value='music'; s.dispatchEvent(new Event('change')); document.querySelector('#dbQuery').value='稻香'; document.querySelector('#btnDbSearch').click(); return 1 })()`);
  await sleep(18000);
  await ev(`(() => { const b=[...document.querySelectorAll('#infoTabs .subtab')].find(x=>/站点资源/.test(x.textContent)); if(b) b.click(); return 1 })()`);
  await sleep(400);
  console.log('点击站点结果…');
  await ev(`(() => { const c=[...document.querySelectorAll('#siteCands .cand')].find(x=>/周杰伦/.test(x.textContent)&&/稻香/.test(x.textContent)); if(c) c.click(); return 1 })()`);
  await sleep(12000);

  console.log('最终状态:', await ev(`(() => JSON.stringify({
    hidden: document.querySelector('#lyricBox').hidden,
    lyricOn: document.querySelector('#player').classList.contains('lyric-on'),
    lines: document.querySelectorAll('#lyricInner .lyric-line').length,
  }))()`));
  await send('Browser.close').catch(() => {});
  process.exit(0);
})();
