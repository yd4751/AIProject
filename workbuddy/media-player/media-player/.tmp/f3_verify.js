'use strict';
/* 验证三项修复：①双击只处理一次 ②歌词不遮挡标题栏/控制条 ③资源导入上传到下载目录 */
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
  const port = 9257;
  const prof = path.join(TMP, 'profF3');
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
  let seq = 0; const pending = new Map(); const reqs = []; const logs = [];
  ws.onmessage = (ev) => {
    const m = JSON.parse(ev.data);
    if (m.id && pending.has(m.id)) { const p = pending.get(m.id); pending.delete(m.id); m.error ? p.reject(new Error(JSON.stringify(m.error))) : p.resolve(m.result); }
    else if (m.method === 'Network.requestWillBeSent') {
      const u = m.params.request.url;
      if (/\/api\/(site\/(detail|download)|import)/.test(u)) reqs.push(u.replace(/^https?:\/\/[^/]+/, '').slice(0, 70));
    } else if (m.method === 'Runtime.exceptionThrown') logs.push('EXC: ' + ((m.params.exceptionDetails.exception && m.params.exceptionDetails.exception.description) || ''));
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
  const shot = async (n) => {
    const r = await send('Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(TMP, n), Buffer.from(r.data, 'base64'));
  };
  await send('Page.enable'); await send('Runtime.enable'); await send('Network.enable');
  await send('Page.navigate', { url: BASE });
  await sleep(3500);
  const log = (...a) => console.log(a.join(' '));

  log('== 1. 双击站点结果 ==');
  await ev(`(() => { const s=document.querySelector('#dbKindSel'); s.value='music'; s.dispatchEvent(new Event('change')); document.querySelector('#dbQuery').value='稻香'; document.querySelector('#btnDbSearch').click(); return 1 })()`);
  await sleep(18000);
  await ev(`(() => { const b=[...document.querySelectorAll('#infoTabs .subtab')].find(x=>/站点资源/.test(x.textContent)); if(b) b.click(); return 1 })()`);
  await sleep(400);
  reqs.length = 0;
  await ev(`(() => {
    const c=[...document.querySelectorAll('#siteCands .cand')].find(x=>/周杰伦/.test(x.textContent)&&/稻香/.test(x.textContent));
    if(!c) return 'nf';
    c.click(); setTimeout(()=>c.click(), 50); setTimeout(()=>c.click(), 110);
    return 'ok';
  })()`);
  await sleep(14000);
  log('   detail 请求:', reqs.filter((u) => u.includes('/detail')).length, '| download 请求:', reqs.filter((u) => u.includes('/download')).length);
  log('   列表条目:', await ev(`(() => JSON.stringify([...document.querySelectorAll('#plist li')].map(x=>x.querySelector('.nm').textContent)))()`));
  log('   播放器:', await ev(`(() => { const m=document.querySelector('#media'); return JSON.stringify({paused:m.paused,t:+m.currentTime.toFixed(1),d:+m.duration.toFixed(1)}) })()`));

  log('\n== 2. 歌词不遮挡控件 ==');
  log('   歌词状态:', await ev(`(() => JSON.stringify({
    boxHidden: document.querySelector('#lyricBox').hidden,
    lines: document.querySelectorAll('#lyricInner .lyric-line').length,
    src: document.querySelector('#lyricSrc').textContent,
    audioMode: document.querySelector('#player').classList.contains('audio-mode'),
    lyricOn: document.querySelector('#player').classList.contains('lyric-on'),
  }))()`))
  log('   ', await ev(`(() => {
    const p=document.querySelector('#player');
    const box=document.querySelector('#lyricBox');
    const ctl=document.querySelector('#controls');
    const top=document.querySelector('#player-top') || document.querySelector('.player-top');
    if(!box || box.hidden) return '歌词未显示';
    const pb=p.getBoundingClientRect(), bb=box.getBoundingClientRect();
    const cr=ctl.getBoundingClientRect(), tr=top.getBoundingClientRect();
    const scroll=document.querySelector('#lyricScroll').getBoundingClientRect();
    // 真实命中测试：控制条中心点和标题栏中心点最上层元素
    const hitCtl=document.elementFromPoint(cr.left+cr.width/2, cr.top+cr.height/2);
    const hitTop=document.elementFromPoint(tr.left+60, tr.top+tr.height/2);
    return JSON.stringify({
      boxCoversPlayer: Math.round(bb.width)>=Math.round(pb.width)-2 && Math.round(bb.height)>=Math.round(pb.height)-2,
      lyricArea: [Math.round(scroll.top-bb.top), Math.round(bb.bottom-scroll.bottom)],
      ctlZ: getComputedStyle(ctl).zIndex,
      boxZ: getComputedStyle(box).zIndex,
      ctlHit: hitCtl ? (hitCtl.id || hitCtl.className || hitCtl.tagName) : 'none',
      topHit: hitTop ? (hitTop.id || hitTop.className || hitTop.tagName) : 'none',
      ctlInLyric: box.contains(hitCtl) && !ctl.contains(hitCtl),
    });
  })()`));
  // 模拟鼠标移入让控制条出现
  await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 700, y: 500 });
  await sleep(400);
  await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 700, y: 860 });
  await sleep(500);
  await shot('f3_lyric.png');
  log('   控制条状态:', await ev(`(() => { const c=document.querySelector('#controls'); const r=c.getBoundingClientRect(); const hit=document.elementFromPoint(r.left+r.width/2, r.top+r.height/2); return JSON.stringify({opacity: getComputedStyle(c).opacity, hit: hit ? (hit.id||hit.className||hit.tagName).toString().slice(0,30) : 'none', inLyric: hit && document.querySelector('#lyricBox').contains(hit) && !c.contains(hit)}) })()`));

  log('\n== 3. 资源导入（上传到下载目录）==');
  await ev(`(() => { const t=[...document.querySelectorAll('#tabs .tab')].find(x=>/信息/.test(x.textContent)); if(t)t.click(); document.querySelector('#btnImport').click(); return 1 })()`);
  await sleep(1500);
  log('   面板文案:', await ev(`(() => document.querySelector('#ipLocal').textContent.replace(/\\s+/g,' ').trim())()`));
  const before = await ev(`(() => document.querySelectorAll('#ipList li').length)()`);
  // 通过 DataTransfer 注入文件到 input 并触发 change（模拟选择本地文件）
  await ev(`(async () => {
    const res = await fetch('/api/stream?file=' + encodeURIComponent('C:/Windows/Media/Alarm02.wav')).catch(()=>null);
    const blob = res ? await res.blob() : new Blob([new Uint8Array(2048)], {type:'audio/wav'});
    const f = new File([blob], '浏览器导入测试.wav', {type:'audio/wav'});
    const dt = new DataTransfer(); dt.items.add(f);
    const inp = document.querySelector('#fileInput');
    inp.files = dt.files;
    inp.dispatchEvent(new Event('change'));
    return 'sent';
  })()`);
  await sleep(4000);
  log('   列表条数:', before, '→', await ev(`(() => document.querySelectorAll('#ipList li').length)()`));
  log('   新导入项:', await ev(`(() => JSON.stringify([...document.querySelectorAll('#ipList li')].map(x=>x.textContent.replace(/\\s+/g,' ').trim()).slice(0,3)))()`));
  log('   toast:', await ev(`(() => { const t=document.querySelector('#toast'); return t&&!t.hidden ? t.textContent : '(无)' })()`));
  log('   播放列表未被动添加:', await ev(`(() => document.querySelectorAll('#plist li').length)()`));
  await shot('f3_import.png');

  log('控制台异常:', logs.length ? logs.slice(0, 4).join(' | ') : '无');
  await send('Browser.close').catch(() => {});
  process.exit(0);
})();
