/* 验证「本地缓存优先 + 前端文件只从服务端拿」：
   A) 服务端已有缓存的 remote 条目 -> 直接播 /api/stream，零 /api/site/play 请求
   B) 服务端没有缓存的 remote 条目 -> 先播服务端流式代理 /api/site/play，后台同时补缓存
   C) 站点搜索点结果（命中已有缓存）-> 立即开播本地文件、不弹验证码、抽屉收起
   全程浏览器不直连任何外部站点（upstream = 0） */
const http = require('http'); const fs = require('fs'); const path = require('path'); const { spawn } = require('child_process');
const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const PORT = 9285;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const hj = (p, u) => new Promise((res, rej) => { http.get({ host: '127.0.0.1', port: p, path: u }, (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } }); }).on('error', rej); });
function cdp(u) { return new Promise((resolve, reject) => { const ws = new WebSocket(u); let seq = 0; const pend = new Map(); const events = [];
  ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pend.has(m.id)) { const p = pend.get(m.id); pend.delete(m.id); m.error ? p.reject(new Error(JSON.stringify(m.error))) : p.resolve(m.result); } else if (m.method) events.push({ method: m.method, params: m.params }); });
  ws.addEventListener('open', () => resolve({ ws, events, send: (method, params) => new Promise((rs, rj) => { const id = ++seq; pend.set(id, { resolve: rs, reject: rj }); ws.send(JSON.stringify({ id, method, params: params || {} })); }) }));
  ws.addEventListener('error', reject); }); }

(async () => {
  const browse = await new Promise((res, rej) => { http.get('http://127.0.0.1:5173/api/browse?dir=@downloads', (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } }); }).on('error', rej); });
  const cached = (browse.files || []).find((f) => /\.mp3$/i.test(f.name));
  if (!cached) throw new Error('下载目录没有 mp3');
  console.log('已缓存测试曲目:', cached.name);

  const prof = path.resolve('.tmp', 'profServerOnly'); fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', '--mute-audio', '--autoplay-policy=no-user-gesture-required',
    '--remote-debugging-port=' + PORT, '--user-data-dir=' + prof, '--window-size=390,844', 'about:blank'], { stdio: 'ignore', detached: true }); proc.unref();
  let list = null; for (let i = 0; i < 60; i++) { try { list = await hj(PORT, '/json/list'); if (list.length) break; } catch (e) {} await sleep(400); }
  const P = await cdp(list[0].webSocketDebuggerUrl); const send = P.send;
  const ev = async (x) => { const r = await send('Runtime.evaluate', { expression: x, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails).slice(0, 260)); return r.result.value; };
  const stat = () => {
    const urls = P.events.filter((e) => e.method === 'Network.requestWillBeSent').map((e) => e.params.request.url);
    return {
      play: urls.filter((u) => /\/api\/site\/play/.test(u)).length,
      stream: urls.filter((u) => /\/api\/stream\?/.test(u)).length,
      upstream: urls.filter((u) => !u.startsWith('http://127.0.0.1:5173') && !u.startsWith('data:') && !u.startsWith('about:') && !u.startsWith('blob:')),
    };
  };
  await send('Page.enable'); await send('Runtime.enable'); await send('Network.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
  await send('Page.navigate', { url: 'http://127.0.0.1:5173/' }); await sleep(2200);

  const hist = [
    { id: 'soA', name: cached.name, kind: 'audio', siteId: 'gequbao', itemId: 'so_cached_1', path: '' },
    { id: 'soB', name: '服务端无缓存测试曲目 - 不存在的歌', kind: 'audio', siteId: 'gequbao', itemId: 'so_none_999', path: '' },
  ];
  await ev(`(() => { localStorage.setItem('mp.history', ${JSON.stringify(JSON.stringify(hist))}); localStorage.removeItem('mp.devsaved'); localStorage.removeItem('mp.autosavedevice'); return 1; })()`);
  await send('Page.reload', { ignoreCache: true }); await sleep(2600);

  // A：命中本地缓存，不应发起站点流
  P.events.length = 0;
  await ev(`(() => { const li = [...document.querySelectorAll('#plist li')].find(x => x.textContent.includes(${JSON.stringify(cached.name.slice(0, 6))})); (li || document.querySelector('#plist li')).click(); return 1; })()`);
  await sleep(2600);
  const a = await ev(`(() => { const m = document.querySelector('#media'); return { src: m.getAttribute('src') || '', playing: !m.paused, t: Number(m.currentTime.toFixed(1)), title: document.querySelector('#ptTitle').textContent }; })()`);
  const sa = stat();
  console.log('A 已有缓存:', JSON.stringify(a), '| site/play:', sa.play, '| 外部直连:', sa.upstream.length);

  // B：没有缓存，应先播服务端流式代理，后台补缓存（当前站点要人机验证，会给出可见失败提示）
  P.events.length = 0;
  await ev(`(() => { const li = [...document.querySelectorAll('#plist li')].find(x => x.textContent.includes('服务端无缓存')); if (li) li.click(); return 1; })()`);
  await sleep(400);
  const b0 = await ev(`(() => { const m = document.querySelector('#media'); return { src: m.getAttribute('src') || '', paused: m.paused }; })()`);
  let b = null;
  for (let k = 0; k < 14; k++) { await sleep(1000); b = await ev(`(() => { const m = document.querySelector('#media'); const c = document.querySelector('#ptCache'); return { src: m.getAttribute('src') || '', t: Number(m.currentTime.toFixed(1)), chip: c.hidden ? '' : document.querySelector('#ptCacheTxt').textContent }; })()`); if (b.chip.includes('失败')) break; }
  const sb = stat();
  console.log('B 点击后 0.4s:', JSON.stringify(b0));
  console.log('B 无缓存:', JSON.stringify(b), '| site/play:', sb.play, '| 外部直连:', sb.upstream.length);

  // C：站点搜索点结果，已有缓存的青花瓷应立刻播本地文件
  P.events.length = 0;
  await ev(`(() => { const i = document.querySelector('#dbQuery'); i.value = '青花瓷'; i.dispatchEvent(new Event('input', { bubbles: true })); i.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); return 1; })()`);
  let n = 0; for (let k = 0; k < 20; k++) { await sleep(1000); n = await ev(`document.querySelectorAll('#siteCands .cand').length`); if (n) break; }
  if (n) { await ev(`(() => { document.querySelector('#siteCands .cand').click(); return 1; })()`); await sleep(4000); }
  const c = await ev(`(() => { const m = document.querySelector('#media'); return { cands: ${n}, listN: document.querySelectorAll('#plist li').length, src: m.getAttribute('src') || '', playing: !m.paused, cap: !document.querySelector('#capMask').hidden, drawer: document.querySelector('#drawer').classList.contains('open') }; })()`);
  const sc = stat();
  console.log('C 搜索点结果:', JSON.stringify(c), '| site/play:', sc.play, '| 外部直连:', sc.upstream.length);

  // D：站点搜索点结果 —— 不等详情抓取，立即开播（命中本地缓存或走服务端流都算）
  P.events.length = 0;
  await ev(`(() => { const i = document.querySelector('#dbQuery'); i.value = '告白气球'; i.dispatchEvent(new Event('input', { bubbles: true })); i.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); return 1; })()`);
  let n2 = 0; for (let k = 0; k < 20; k++) { await sleep(1000); n2 = await ev(`(() => [...document.querySelectorAll('#siteCands .cand')].filter(x => x.textContent.includes('告白气球')).length)()`); if (n2) break; }
  if (n2) { await ev(`(() => { const c = [...document.querySelectorAll('#siteCands .cand')].find(x => x.textContent.includes('告白气球')); c.click(); return 1; })()`); await sleep(400); }
  const d = await ev(`(() => { const m = document.querySelector('#media'); return { src: m.getAttribute('src') || '', paused: m.paused, listN: document.querySelectorAll('#plist li').length }; })()`);
  const sd = stat();
  console.log('D 搜索结果 0.4s:', JSON.stringify(d), '| 候选项:', n2, '| site/play:', sd.play, '| 外部直连:', sd.upstream.length);

  const errs = P.events.filter((e) => e.method === 'Runtime.exceptionThrown').map((e) => String(e.params.exceptionDetails.text).slice(0, 100));
  console.log('页面异常:', errs.length ? errs : '无');
  const ok = /\/api\/stream\?/.test(a.src) && a.playing && a.t > 0 && sa.play === 0
    && /\/api\/site\/play/.test(b0.src) && !b0.paused && sb.play >= 1
    && /\/api\/stream\?/.test(c.src) && sc.play === 0 && !c.cap && !c.drawer && c.listN >= 3
    && n2 > 0 && d.src !== '' && !d.paused && d.listN >= 4
    && (sd.play >= 1 || /\/api\/stream\?/.test(d.src))
    && !sa.upstream.length && !sb.upstream.length && !sc.upstream.length && !sd.upstream.length && errs.length === 0;
  console.log('RESULT:', ok ? 'PASS 本地缓存优先 / 无缓存先播服务端流 / 前端零外部直连' : 'FAIL');
  P.ws.close(); proc.kill(); process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
