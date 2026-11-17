'use strict';
/* 验证 UI 改造：①资源导入替换打开/媒体库 ②下载进度进列表 ③站点管理占原媒体库位 ④歌词铺满 */
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
  const port = 9241;
  const prof = path.join(TMP, 'profUI2');
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

  log('== 1. 顶栏与主标签页 ==');
  log('   顶栏按钮:', await ev(`(() => JSON.stringify([...document.querySelectorAll('.top-actions .btn')].map(b=>b.textContent.trim())))()`));
  log('   主标签页:', await ev(`(() => JSON.stringify([...document.querySelectorAll('#tabs .tab')].map(b=>b.textContent.trim())))()`));
  log('   媒体库残留:', await ev(`(() => document.querySelector('[data-panel="lib"]') ? '有' : '已移除')()`));

  log('\n== 资源导入弹层 ==');
  await ev(`(() => document.querySelector('#btnImport').click())()`);
  await sleep(1800);
  log('   弹层可见:', await ev(`(() => !document.querySelector('#importPop').hidden)()`));
  log('   本地选项:', await ev(`(() => document.querySelector('#ipLocal').textContent.replace(/\\s+/g,' ').trim())()`));
  log('   下载目录文件:', await ev(`(() => JSON.stringify([...document.querySelectorAll('#ipList li')].map(x=>x.textContent.replace(/\\s+/g,' ').trim().slice(0,42))))()`));
  await shot('ui2_import.png');
  await ev(`(() => document.body.click())()`);
  await sleep(400);

  log('\n== 3. 站点管理（原媒体库位置）==');
  await ev(`(() => { const t=[...document.querySelectorAll('#tabs .tab')].find(x=>/站点/.test(x.textContent)); t.click(); return 1 })()`);
  await sleep(900);
  log('   当前面板:', await ev(`(() => (document.querySelector('.panel.active')||{}).dataset.panel)()`));
  log('   站点列表:', await ev(`(() => JSON.stringify([...document.querySelectorAll('#siteList li')].map(x=>x.textContent.replace(/\\s+/g,' ').trim().slice(0,30))))()`));
  log('   新增按钮可见:', await ev(`(() => !!document.querySelector('#btnSiteAdd').offsetParent)()`));
  await shot('ui2_sites.png');

  log('\n== 2/4. 播放 + 列表内进度 + 歌词铺满 ==');
  await ev(`(() => { const t=[...document.querySelectorAll('#tabs .tab')].find(x=>/信息/.test(x.textContent)); t.click(); const s=document.querySelector('#dbKindSel'); s.value='music'; s.dispatchEvent(new Event('change')); document.querySelector('#dbQuery').value='稻香'; document.querySelector('#btnDbSearch').click(); return 1 })()`);
  await sleep(18000);
  await ev(`(() => { const b=[...document.querySelectorAll('#infoTabs .subtab')].find(x=>/站点资源/.test(x.textContent)); if(b) b.click(); return 1 })()`);
  await sleep(500);
  const clicked = await ev(`(() => { const c=[...document.querySelectorAll('#siteCands .cand')].find(x=>/周杰伦/.test(x.textContent)&&/稻香/.test(x.textContent)); if(!c) return 'nf'; c.click(); return 'ok' })()`);
  log('   点击站点结果:', clicked);

  // 观察列表内进度条变化（下载很快，采样要密）
  let hit = false;
  for (let i = 0; i < 70; i++) {
    await sleep(60);
    const s = await ev(`(() => {
      const li = document.querySelector('#plist li');
      if (!li) return 'no-li';
      const dl = li.querySelector('.pl-dl');
      const bar = li.querySelector('.pl-track i');
      return JSON.stringify({ liHasDl: li.classList.contains('has-dl'), dlHidden: dl ? dl.hidden : null,
        width: bar ? bar.style.width : null, pct: (li.querySelector('.pl-pct')||{}).textContent,
        standalone: !!document.querySelector('#dlBox') });
    })()`);
    if (s.includes('"liHasDl":true')) { log('   进度[' + i + '] 捕获:', s); hit = true; }
  }

  await sleep(5000);
  log('   播放器:', await ev(`(() => { const m=document.querySelector('#media'); return JSON.stringify({ paused:m.paused, t:+m.currentTime.toFixed(1), d:+m.duration.toFixed(1) }) })()`));

  // 歌词铺满检查
  log('   歌词可见:', await ev(`(() => { const b=document.querySelector('#lyricBox'); return b ? !b.hidden : 'no-box' })()`));
  log('   进度条曾出现:', hit);

  await ev(`(() => { const m=document.querySelector('#media'); m.currentTime = 60; return 1 })()`);
  await sleep(1500);
  log('   歌词盒几何:', await ev(`(() => {
    const box=document.querySelector('#lyricBox'), p=document.querySelector('#player');
    if(!box||box.hidden) return 'hidden';
    const a=box.getBoundingClientRect(), b=p.getBoundingClientRect();
    const head=document.querySelector('.lyric-head');
    return JSON.stringify({ covers: Math.round(a.width)>=Math.round(b.width)-2 && Math.round(a.height)>=Math.round(b.height)-2,
      box: [Math.round(a.width),Math.round(a.height)], player: [Math.round(b.width),Math.round(b.height)],
      headOpacity: head ? getComputedStyle(head).opacity : 'n/a',
      activeLine: (document.querySelector('#lyricInner .lyric-line.on')||{}).textContent || '' });
  })()`));
  await shot('ui2_lyric.png');

  log('控制台异常:', logs.length ? logs.slice(0, 5).join(' | ') : '无');
  await send('Browser.close').catch(() => {});
  process.exit(0);
})();
