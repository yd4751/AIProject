/* 手机端视口下：点结果播放 → 后台缓存是否真的发生 + 列表里能否看到 */
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
  const prof = path.resolve('.tmp', 'profMobCache');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', '--mute-audio',
    '--autoplay-policy=no-user-gesture-required', '--remote-debugging-port=9271',
    '--user-data-dir=' + prof, '--window-size=390,844', 'about:blank'], { stdio: 'ignore', detached: true });
  proc.unref();
  let list = null;
  for (let i = 0; i < 60; i++) { try { list = await hj(9271, '/json/list'); if (list.length) break; } catch (e) {} await sleep(400); }
  const ws = new WebSocket(list[0].webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 0; const pend = new Map();
  ws.onmessage = (ev) => { const m = JSON.parse(ev.data); if (m.id && pend.has(m.id)) { const { resolve, reject } = pend.get(m.id); pend.delete(m.id); m.error ? reject(new Error(JSON.stringify(m.error))) : resolve(m.result); } };
  const send = (method, params) => new Promise((resolve, reject) => { const id = ++seq; pend.set(id, { resolve, reject }); ws.send(JSON.stringify({ id, method, params: params || {} })); });
  const ev = async (x) => { const r = await send('Runtime.evaluate', { expression: x, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails).slice(0, 240)); return r.result.value; };
  const errs = [];
  ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.method === 'Runtime.exceptionThrown') errs.push(String(m.params.exceptionDetails.text).slice(0, 120)); });

  await send('Page.enable'); await send('Runtime.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
  await send('Page.navigate', { url: 'http://127.0.0.1:5173/' });
  await sleep(2500);

  await ev(`(() => {
    const i = document.querySelector('#dbQuery');
    i.value = '搁浅';
    i.dispatchEvent(new Event('input', { bubbles: true }));
    i.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    return 1;
  })()`);
  for (let k = 0; k < 25; k++) { await sleep(1000); if (await ev(`document.querySelectorAll('#siteCands .cand').length`)) break; }
  await ev(`(() => { const c = document.querySelector('#siteCands .cand'); if (c) c.click(); return 1; })()`);
  await sleep(4000);

  // 播放 4 秒后（抽屉已自动收起）：任务在服务端是否进行
  const t1 = await hj(5173, '/api/site/tasks');
  console.log('点击 4s 后任务:', JSON.stringify((t1.tasks || []).slice(0, 2).map((t) => ({ key: t.key, status: t.status, pct: t.percent, err: t.error }))));
  const playing = await ev(`(() => { const m = document.querySelector('#media'); return { playing: document.querySelector('#player').classList.contains('playing'), t: Number(m.currentTime.toFixed(1)), drawerOpen: document.querySelector('#drawer').classList.contains('open') }; })()`);
  console.log('播放状态:', JSON.stringify(playing));

  // 打开列表抽屉看条目状态与进度
  const listState = await ev(`(async () => {
    document.querySelector('#navGroup .navbtn[data-panel="list"]').click();
    await new Promise(r => setTimeout(r, 600));
    const li = document.querySelector('#plist li');
    if (!li) return { none: true };
    return {
      status: li.querySelector('.status').className,
      dl: li.querySelector('.pl-dl') ? li.querySelector('.pl-dl').hidden : null,
      pct: li.querySelector('.pl-pct') ? li.querySelector('.pl-pct').textContent : '',
    };
  })()`);
  console.log('列表抽屉打开:', JSON.stringify(listState));

  // 等待缓存完成
  let final = null;
  for (let i = 0; i < 40; i++) {
    await sleep(1500);
    final = await ev(`(() => { const li = document.querySelector('#plist li'); const m = document.querySelector('#media'); return { status: li.querySelector('.status').className, t: Number(m.currentTime.toFixed(1)), paused: m.paused, src: (m.currentSrc || m.src).slice(-50) }; })()`);
    if (final.status.includes('local')) break;
  }
  console.log('最终条目:', JSON.stringify(final));
  const t2 = await hj(5173, '/api/site/tasks');
  console.log('任务终态:', JSON.stringify((t2.tasks || []).slice(0, 2).map((t) => ({ key: t.key, status: t.status, pct: t.percent, err: t.error }))));
  console.log('搁浅文件:', fs.readdirSync(path.resolve('downloads')).filter((f) => f.includes('搁浅')));
  console.log('异常:', errs.length ? errs : '无');
  ws.close(); proc.kill(); process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
