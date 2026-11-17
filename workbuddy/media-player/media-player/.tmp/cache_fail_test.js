/* 缓存失败时是否给出可见反馈与重试入口 */
const http = require('http'); const fs = require('fs'); const path = require('path'); const { spawn } = require('child_process');
const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const PORT = 9284;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const hj = (p, u) => new Promise((res, rej) => { http.get({ host: '127.0.0.1', port: p, path: u }, (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } }); }).on('error', rej); });
function cdp(u) { return new Promise((resolve, reject) => { const ws = new WebSocket(u); let seq = 0; const pend = new Map(); const errs = [];
  ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pend.has(m.id)) { const p = pend.get(m.id); pend.delete(m.id); m.error ? p.reject(new Error(JSON.stringify(m.error))) : p.resolve(m.result); } else if (m.method === 'Runtime.exceptionThrown') errs.push(String(m.params.exceptionDetails.text).slice(0, 90)); });
  ws.addEventListener('open', () => resolve({ ws, errs, send: (method, params) => new Promise((rs, rj) => { const id = ++seq; pend.set(id, { resolve: rs, reject: rj }); ws.send(JSON.stringify({ id, method, params: params || {} })); }) }));
  ws.addEventListener('error', reject); }); }

(async () => {
  const prof = path.resolve('.tmp', 'profFail'); fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', '--mute-audio', '--autoplay-policy=no-user-gesture-required',
    '--remote-debugging-port=' + PORT, '--user-data-dir=' + prof, '--window-size=390,844', 'about:blank'], { stdio: 'ignore', detached: true }); proc.unref();
  let list = null; for (let i = 0; i < 60; i++) { try { list = await hj(PORT, '/json/list'); if (list.length) break; } catch (e) {} await sleep(400); }
  const P = await cdp(list[0].webSocketDebuggerUrl); const send = P.send;
  const ev = async (x) => { const r = await send('Runtime.evaluate', { expression: x, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails).slice(0, 240)); return r.result.value; };
  await send('Page.enable'); await send('Runtime.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
  await send('Page.navigate', { url: 'http://127.0.0.1:5173/' }); await sleep(2200);
  const hist = [{ id: 'fail1', name: '无音频测试 - 不存在的曲目', kind: 'audio', siteId: 'gequbao', itemId: 'zzz_no_audio_999', path: '' }];
  await ev(`(() => { localStorage.setItem('mp.history', ${JSON.stringify(JSON.stringify(hist))}); localStorage.removeItem('mp.devsaved'); return 1; })()`);
  await send('Page.reload', { ignoreCache: true }); await sleep(2500);
  console.log('恢复:', await ev(`(() => ({ n: document.querySelectorAll('#plist li').length, name: (document.querySelector('#plist li .nm')||{}).textContent }))()`));
  await ev(`(() => { document.querySelector('#plist li').click(); return 1; })()`);
  let st = null;
  for (let k = 0; k < 18; k++) {
    await sleep(1000);
    st = await ev(`(() => { const c = document.querySelector('#ptCache'); return { hidden: c.hidden, txt: document.querySelector('#ptCacheTxt').textContent, fail: c.classList.contains('fail'), clickable: getComputedStyle(c).pointerEvents }; })()`);
    if (st.fail) break;
  }
  console.log('失败提示:', JSON.stringify(st));
  if (st.fail) {
    await ev(`(() => { document.querySelector('#ptCache').click(); return 1; })()`);
    await sleep(1500);
    console.log('点重试后:', await ev(`(() => ({ txt: document.querySelector('#ptCacheTxt').textContent, toast: document.querySelector('#toast').hidden ? '' : document.querySelector('#toast').textContent }))()`));
  }
  console.log('页面异常:', P.errs.length ? P.errs : '无');
  console.log('RESULT:', st && st.fail && st.clickable === 'auto' ? 'PASS 缓存失败可见且可重试' : 'FAIL');
  P.ws.close(); proc.kill(); process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
