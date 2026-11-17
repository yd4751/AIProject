/* 手机端点搜索结果后：抽屉是否自动收起、播放是否真的开始 */
const http = require('http');
const fs = require('fs');
const path = require('path');
const { spawn } = require('child_process');
const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const hj = (p, u) => new Promise((res, rej) => {
  http.get({ host: '127.0.0.1', port: p, path: u }, (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } }); }).on('error', rej);
});

(async () => {
  const prof = path.resolve('.tmp', 'profPick');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', '--mute-audio',
    '--autoplay-policy=no-user-gesture-required', '--remote-debugging-port=9251',
    '--user-data-dir=' + prof, '--window-size=390,844', 'about:blank'],
    { stdio: 'ignore', detached: true });
  proc.unref();
  let list = null;
  for (let i = 0; i < 60; i++) { try { list = await hj(9251, '/json/list'); if (list.length) break; } catch (e) {} await sleep(400); }
  const ws = new WebSocket(list[0].webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 0; const pend = new Map();
  ws.onmessage = (ev) => { const m = JSON.parse(ev.data); if (m.id && pend.has(m.id)) { const { resolve, reject } = pend.get(m.id); pend.delete(m.id); m.error ? reject(new Error(JSON.stringify(m.error))) : resolve(m.result); } };
  const send = (method, params) => new Promise((resolve, reject) => { const id = ++seq; pend.set(id, { resolve, reject }); ws.send(JSON.stringify({ id, method, params: params || {} })); });
  const ev = async (x) => { const r = await send('Runtime.evaluate', { expression: x, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error('eval err ' + JSON.stringify(r.exceptionDetails).slice(0, 300)); return r.result.value; };
  const errs = [];
  ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.method === 'Runtime.exceptionThrown') errs.push(String(m.params.exceptionDetails.exception && m.params.exceptionDetails.exception.description).slice(0, 200)); });

  await send('Page.enable'); await send('Runtime.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
  await send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 5 });
  await send('Page.navigate', { url: 'http://127.0.0.1:5173/' });
  await sleep(2200);

  const vw = await ev('({ w: innerWidth, h: innerHeight, narrow: matchMedia("(max-width: 640px)").matches })');
  console.log('视口:', JSON.stringify(vw));

  // 搜索
  await ev(`(() => {
    const i = document.querySelector('#dbQuery');
    i.value = '七里香';
    i.dispatchEvent(new Event('input', { bubbles: true }));
    i.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    return 1;
  })()`);
  await sleep(9000);

  const before = await ev(`(() => {
    const d = document.querySelector('#drawer');
    const cands = [...document.querySelectorAll('#siteCands .cand')];
    return { open: d.classList.contains('open'), rect: d.getBoundingClientRect().top, cands: cands.length };
  })()`);
  console.log('搜索后:', JSON.stringify(before));

  // 点第一条站点结果
  await ev(`(() => { const c = document.querySelector('#siteCands .cand'); if (!c) return 0; c.click(); return 1; })()`);
  await sleep(350);
  const mid = await ev(`(() => ({ open: document.querySelector('#drawer').classList.contains('open'), cls: document.body.className }))()`);
  console.log('点击后 0.35s:', JSON.stringify(mid));

  await sleep(9000);
  const after = await ev(`(async () => {
    const d = document.querySelector('#drawer');
    const m = document.querySelector('#media');
    const pl = document.querySelector('#player').getBoundingClientRect();
    return {
      open: d.classList.contains('open'),
      drawerVisible: getComputedStyle(d).opacity,
      entries: document.querySelectorAll('#plist li').length,
      cur: !!document.querySelector('#plist li.cur'),
      src: (m.currentSrc || m.src || '').slice(-60),
      t: Number(m.currentTime.toFixed(2)),
      paused: m.paused,
      playerH: Math.round(pl.height),
      winH: innerHeight,
    };
  })()`);
  console.log('点击后 10s:', JSON.stringify(after));

  // 再搜一首，凑出两条列表，验证「在列表里切歌也自动收起」
  await ev(`(() => {
    const i = document.querySelector('#dbQuery');
    i.value = '晴天';
    i.dispatchEvent(new Event('input', { bubbles: true }));
    i.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    return 1;
  })()`);
  await sleep(9000);
  await ev(`(() => { const c = document.querySelector('#siteCands .cand'); if (c) c.click(); return 1; })()`);
  await sleep(9000);

  // 打开列表抽屉点非当前条目：也应自动收起
  const listTry = await ev(`(async () => {
    document.querySelector('#navGroup .navbtn[data-panel="list"]').click();
    await new Promise(r => setTimeout(r, 500));
    const open1 = document.querySelector('#drawer').classList.contains('open');
    const lis = [...document.querySelectorAll('#plist li')];
    const other = lis.find(li => !li.classList.contains('cur'));
    const n = lis.length;
    if (other) other.click();
    await new Promise(r => setTimeout(r, 1500));
    return { n, openAfterOpen: open1, openAfterClick: document.querySelector('#drawer').classList.contains('open'), cur: !!document.querySelector('#plist li.cur') };
  })()`);
  console.log('列表切换:', JSON.stringify(listTry));

  // 点播放区（露出的部分）也应收起
  const tapTry = await ev(`(async () => {
    document.querySelector('#navGroup .navbtn[data-panel="info"]').click();
    await new Promise(r => setTimeout(r, 500));
    const open1 = document.querySelector('#drawer').classList.contains('open');
    const pl = document.querySelector('#player').getBoundingClientRect();
    document.querySelector('#player').click();
    await new Promise(r => setTimeout(r, 500));
    return { open1, openAfterTap: document.querySelector('#drawer').classList.contains('open'), y: Math.round(pl.top) };
  })()`);
  console.log('点播放区:', JSON.stringify(tapTry));

  console.log('异常:', errs.length ? errs : '无');
  ws.close(); proc.kill(); process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
