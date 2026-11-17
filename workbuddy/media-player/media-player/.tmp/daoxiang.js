'use strict';
/* 回归：歌曲宝搜索「稻香」→ 点击应自动播放 + 启动下载 */
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
  const port = 9231;
  const prof = path.join(TMP, 'profDX');
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
    else if (m.method === 'Runtime.exceptionThrown') logs.push('EXC: ' + ((m.params.exceptionDetails.exception && m.params.exceptionDetails.exception.description) || m.params.exceptionDetails.text || ''));
    else if (m.method === 'Runtime.consoleAPICalled' && m.params.type === 'error') logs.push('ERR: ' + (m.params.args || []).map((a) => a.value || a.description || '').join(' '));
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
  await send('Page.navigate', { url: BASE });
  await sleep(3500);
  const log = (...a) => console.log(a.join(' '));

  // 音乐分类 + 搜索稻香
  await ev(`(() => { const s=document.querySelector('#dbKindSel'); s.value='music'; s.dispatchEvent(new Event('change')); document.querySelector('#dbQuery').value='稻香'; document.querySelector('#btnDbSearch').click(); return 1 })()`);
  await sleep(18000);

  log('豆瓣卡片:', await ev(`(() => JSON.stringify({ title: document.querySelector('#dbTitle').textContent, best: document.querySelector('#dbBest').hidden?'(无)':document.querySelector('#dbBest').textContent }))()`));

  log('站点分组:', await ev(`(() => JSON.stringify([...document.querySelectorAll('#siteCands .cand-group')].map(g=>g.textContent.trim())))()`));

  // 切到站点资源子页，点歌曲宝的第一条稻香
  await ev(`(() => { const b=[...document.querySelectorAll('#infoTabs .subtab')].find(x=>/站点资源/.test(x.textContent)); if(b) b.click(); return 1 })()`);
  await sleep(600);

  log('候选前几条:', await ev(`(() => JSON.stringify([...document.querySelectorAll('#siteCands .cand')].slice(0,6).map(c=>c.textContent.replace(/\\s+/g,' ').trim().slice(0,40))))()`));

  const clicked = await ev(`(() => {
    const cands=[...document.querySelectorAll('#siteCands .cand')];
    const hit=cands.find(c=>/周杰伦/.test(c.textContent) && /稻香/.test(c.textContent));
    if(!hit) return 'not-found';
    hit.click();
    return 'clicked:'+hit.textContent.replace(/\\s+/g,' ').trim().slice(0,40);
  })()`);
  log('点击 →', clicked);

  await sleep(9000);

  log('播放器状态:', await ev(`(() => { const m=document.querySelector('#media'); return JSON.stringify({
    src: m.src ? m.src.slice(0,60) : '(空)',
    paused: m.paused, duration: m.duration, currentTime: m.currentTime,
    err: m.error ? m.error.code : null,
  }) })()`));

  log('播放列表:', await ev(`(() => JSON.stringify([...document.querySelectorAll('#playList li')].map(x=>x.textContent.replace(/\\s+/g,' ').trim().slice(0,36))))()`));

  log('歌词:', await ev(`(() => JSON.stringify({ boxHidden: document.querySelector('#lyricBox').hidden, src: document.querySelector('#lyricSrc').textContent, lines: document.querySelectorAll('#lyricInner .lyric-line').length }))()`));

  for (let i = 0; i < 12; i++) {
    const dl = await ev(`(() => JSON.stringify([...document.querySelectorAll('#dlList li')].map(x=>x.textContent.replace(/\\s+/g,' ').trim().slice(0,60))))()`);
    log('  下载项:', dl);
    if (!/下载中|0%/.test(dl)) break;
    await sleep(2000);
  }

  log('toast:', await ev(`(() => { const t=document.querySelector('#toast'); return t ? t.textContent : '(无)' })()`));
  await shot('daoxiang.png');
  log('控制台异常:', logs.length ? logs.slice(0, 5).join(' | ') : '无');
  await send('Browser.close').catch(() => {});
  process.exit(0);
})();
