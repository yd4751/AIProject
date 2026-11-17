/* 手机端适配检查：多种视口下看是否有横向溢出、控件被裁、顶栏错位 */
const http = require('http');
const fs = require('fs');
const path = require('path');
const { spawn } = require('child_process');
const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const hj = (p, u) => new Promise((res, rej) => {
  http.get({ host: '127.0.0.1', port: p, path: u }, (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } }); }).on('error', rej);
});

const VIEWS = [
  { name: 'M1_iphone', w: 390, h: 844, mobile: true },
  { name: 'M2_small_android', w: 360, h: 640, mobile: true },
  { name: 'M3_landscape', w: 844, h: 390, mobile: true },
];

(async () => {
  const prof = path.resolve('.tmp', 'profMobile');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', '--mute-audio', '--autoplay-policy=no-user-gesture-required',
    '--remote-debugging-port=9245', '--user-data-dir=' + prof, '--window-size=1440,900', 'about:blank'],
    { stdio: 'ignore', detached: true });
  proc.unref();
  let list = null;
  for (let i = 0; i < 60; i++) { try { list = await hj(9245, '/json/list'); if (list.length) break; } catch (e) {} await sleep(400); }
  const ws = new WebSocket(list[0].webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 0; const pend = new Map();
  ws.onmessage = (ev) => { const m = JSON.parse(ev.data); if (m.id && pend.has(m.id)) { const { resolve, reject } = pend.get(m.id); pend.delete(m.id); m.error ? reject(new Error(JSON.stringify(m.error))) : resolve(m.result); } };
  const send = (method, params) => new Promise((resolve, reject) => { const id = ++seq; pend.set(id, { resolve, reject }); ws.send(JSON.stringify({ id, method, params: params || {} })); });
  const ev = async (x) => { const r = await send('Runtime.evaluate', { expression: x, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error('eval err ' + JSON.stringify(r.exceptionDetails).slice(0, 300)); return r.result.value; };
  await send('Page.enable'); await send('Runtime.enable');
  const errors = [];
  ws.addEventListener('message', (e) => {
    const m = JSON.parse(e.data);
    if (m.method === 'Runtime.exceptionThrown') errors.push(JSON.stringify(m.params.exceptionDetails).slice(0, 160));
  });

  for (const v of VIEWS) {
    await send('Emulation.setDeviceMetricsOverride', { width: v.w, height: v.h, deviceScaleFactor: 2, mobile: v.mobile });
    await send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 5 });
    await send('Emulation.setEmulatedMedia', { features: [
      { name: 'hover', value: 'none' }, { name: 'pointer', value: 'coarse' }] });
    await send('Page.navigate', { url: 'http://127.0.0.1:5173' });
    await sleep(1800);

    const info = await ev(`(() => {
      const de = document.documentElement;
      const tb = document.querySelector('.topbar').getBoundingClientRect();
      const pl = document.querySelector('.player').getBoundingClientRect();
      const sb = document.querySelector('.searchbar').getBoundingClientRect();
      const ctr = document.querySelector('.controls').getBoundingClientRect();
      const vis = (sel) => { const el = document.querySelector(sel); if (!el) return 'missing';
        const cs = getComputedStyle(el); return cs.display === 'none' ? 'hidden' : 'shown'; };
      // 控制条里每个按钮是否落在播放区内
      const overflowBtns = [...document.querySelectorAll('.controls .cbtn')].filter(b => {
        if (b.offsetParent === null) return false;   // 自身或祖先被隐藏的不算
        const r = b.getBoundingClientRect();
        return r.width > 0 && (r.right > pl.right + 1 || r.left < pl.left - 1);
      }).map(b => b.id || b.className);
      return {
        vw: de.clientWidth, vh: de.clientHeight,
        hScroll: de.scrollWidth > de.clientWidth + 1,
        topbar: { h: Math.round(tb.height), w: Math.round(tb.width), right: Math.round(tb.right) },
        searchbar: { top: Math.round(sb.top), w: Math.round(sb.width), right: Math.round(sb.right) },
        player: { top: Math.round(pl.top), h: Math.round(pl.height), w: Math.round(pl.width) },
        controls: { h: Math.round(ctr.height) },
        ctrBtns: [...document.querySelectorAll('.controls .cbtn')].filter(b => getComputedStyle(b).display !== 'none').length,
        overflowBtns,
        lyricHeadOpacity: getComputedStyle(document.querySelector('.lyric-head')).opacity,
        vol: vis('.vol-wrap'), shot: vis('#btnShot'), pip: vis('#btnPip'),
        brandText: vis('.brand-text'), importLabelW: Math.round(document.querySelector('#btnImport').getBoundingClientRect().width)
      };
    })()`);
    console.log(v.name, JSON.stringify(info));

    let shot = await send('Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.resolve('.tmp', v.name + '.png'), Buffer.from(shot.data, 'base64'));

    // 打开抽屉看底部弹层
    await ev(`(() => { document.querySelector('.navbtn[data-panel="list"]').click(); return 1 })()`);
    await sleep(500);
    const dr = await ev(`(() => { const d = document.querySelector('#drawer').getBoundingClientRect();
      return { top: Math.round(d.top), h: Math.round(d.height), w: Math.round(d.width), open: document.querySelector('#drawer').classList.contains('open') } })()`);
    console.log(v.name + '_drawer', JSON.stringify(dr));
    shot = await send('Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.resolve('.tmp', v.name + '_drawer.png'), Buffer.from(shot.data, 'base64'));
    await ev(`(() => { document.querySelector('#drawerClose').click(); return 1 })()`);
    await sleep(300);
  }

  console.log('异常:', errors.length ? errors : '无');
  ws.close(); process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
