'use strict';
/* 验证 4 项改造：1 歌词滚动 2 豆瓣默认+折叠 3 信息页默认最优 4 站点资源子页 */
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
  const port = 9225;
  const prof = path.join(TMP, 'profUI');
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
  };
  const send = (method, params) => new Promise((resolve, reject) => {
    const id = ++seq; pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params: params || {} }));
    setTimeout(() => { if (pending.has(id)) { pending.delete(id); reject(new Error('timeout ' + method)); } }, 60000);
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
  await send('Page.enable'); await send('Runtime.enable'); await send('Log.enable');
  await send('Page.navigate', { url: BASE });
  await sleep(3500);

  const log = (...a) => console.log(a.join(' '));

  log('== 4. 信息页子页结构 ==');
  log('   子页签:', await ev(`(() => JSON.stringify([...document.querySelectorAll('#infoTabs .subtab')].map(b => b.textContent.trim())))()`));
  log('   主标签页:', await ev(`(() => JSON.stringify([...document.querySelectorAll('#tabs .tab')].map(b => b.textContent.trim())))()`));
  log('   默认激活子页:', await ev(`(() => (document.querySelector('.subpanel.active')||{}).dataset ? document.querySelector('.subpanel.active').dataset.sub : 'none')()`));

  log('\n== 5. 搜索栏分类下拉 ==');
  log('   选项:', await ev(`(() => JSON.stringify([...document.querySelectorAll('#dbKindSel option')].map(o => o.value + ':' + o.textContent)))()`));

  log('\n== 分类过滤站点 ==');
  for (const k of ['all', 'music', 'movie']) {
    await ev(`(() => { const s = document.querySelector('#dbKindSel'); s.value = '${k}'; s.dispatchEvent(new Event('change')); return 1 })()`);
    await sleep(1200);
    log(`   ${k} → 站点:`, await ev(`(() => JSON.stringify([...document.querySelectorAll('#siteRow .site-chip')].map(b => b.textContent.replace(/\\s+/g,' ').trim())))()`));
  }

  log('\n== 2/3. 搜索「晴天」→ 豆瓣最优 + 候选折叠 ==');
  await ev(`(() => { const s=document.querySelector('#dbKindSel'); s.value='music'; s.dispatchEvent(new Event('change')); document.querySelector('#dbQuery').value='晴天'; document.querySelector('#btnDbSearch').click(); return 1 })()`);
  await sleep(16000);
  log('   豆瓣卡片:', await ev(`(() => JSON.stringify({
      hidden: document.querySelector('#dbCard').hidden,
      title: document.querySelector('#dbTitle').textContent,
      sub: document.querySelector('#dbSub').textContent,
      best: document.querySelector('#dbBest').hidden ? '(无)' : document.querySelector('#dbBest').textContent,
      rows: document.querySelectorAll('#dbRows > *').length,
    }))()`));
  log('   折叠候选:', await ev(`(() => { const d = document.querySelector('#dbCands .cands-fold'); return d ? JSON.stringify({ open: d.open, summary: (d.querySelector('summary')||{}).textContent, n: d.querySelectorAll('.cand').length }) : '(无折叠区)' })()`));

  log('\n== 4. 站点资源子页收到结果 ==');
  log('   siteTabN:', await ev(`(() => { const el=document.querySelector('#siteTabN'); return JSON.stringify({n: el.textContent, hidden: el.hidden}) })()`));
  log('   激活子页:', await ev(`(() => document.querySelector('.subpanel.active').dataset.sub)()`));
  log('   分组:', await ev(`(() => JSON.stringify([...document.querySelectorAll('#siteCands .cand-group')].map(g => g.textContent.trim())))()`));

  log('\n== 1. 歌词滚动 ==');
  const clicked = await ev(`(() => {
    const kids = [...document.querySelector('#siteCands').children];
    for (let i = 0; i < kids.length; i++) {
      if (kids[i].classList.contains('cand-group') && /歌曲宝/.test(kids[i].textContent)) {
        const c = kids[i + 1]; if (c && c.classList.contains('cand')) { c.click(); return 'clicked'; }
      }
    }
    return 'no-cand';
  })()`);
  log('   点击站点结果:', clicked);
  await sleep(10000);
  const probe = () => ev(`(() => {
    const inner = document.querySelector('#lyricInner'); const sc = document.querySelector('#lyricScroll');
    const on = document.querySelector('#lyricInner .lyric-line.on');
    const lines = [...document.querySelectorAll('#lyricInner .lyric-line')];
    return JSON.stringify({ tf: inner.style.transform || '(none)', idx: on ? lines.indexOf(on) : -1,
      text: on ? on.textContent.slice(0, 18) : '', n: lines.length, t: Number(document.querySelector('#media').currentTime||0).toFixed(0) });
  })()`);
  log('   载入:', await probe());
  for (const t of [10, 60, 130, 200]) {
    await ev(`(() => { document.querySelector('#media').currentTime = ${t}; return 1 })()`);
    await sleep(1400);
    log(`   t=${t}s →`, await probe());
  }
  log('   截图:', await shot('ui_verify.png'));

  log('\n== 控制台异常 ==');
  log('   ', logs.length ? logs.slice(0, 6).join('\n   ') : '(无)');

  await send('Browser.close').catch(() => {});
  proc.kill();
  setTimeout(() => process.exit(0), 500);
})().catch((e) => { console.error('ERR', e.message); process.exit(1); });
