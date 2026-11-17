/* 验证「本地有缓存就播本地；没有缓存则先播服务端流，后台补缓存，完成后无缝切本地」。
   站点反爬期间，临时加一个 audioUrl 指向本机 stream 的测试站点（音频真实可播），跑完恢复 sources.json。 */
const http = require('http'); const fs = require('fs'); const path = require('path'); const { spawn } = require('child_process');
const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const PORT = 9288;
const DL = path.resolve('.tmp', 'ptc_downloads');
const SRC = path.resolve('sources.json');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const hj = (p, u) => new Promise((res, rej) => { http.get({ host: '127.0.0.1', port: p, path: u }, (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } }); }).on('error', rej); });
function cdp(u) { return new Promise((resolve, reject) => { const ws = new WebSocket(u); let seq = 0; const pend = new Map(); const events = [];
  ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pend.has(m.id)) { const p = pend.get(m.id); pend.delete(m.id); m.error ? p.reject(new Error(JSON.stringify(m.error))) : p.resolve(m.result); } else if (m.method) events.push({ method: m.method, params: m.params }); });
  ws.addEventListener('open', () => resolve({ ws, events, send: (method, params) => new Promise((rs, rj) => { const id = ++seq; pend.set(id, { resolve: rs, reject: rj }); ws.send(JSON.stringify({ id, method, params: params || {} })); }) }));
  ws.addEventListener('error', reject); }); }

(async () => {
  const backup = fs.readFileSync(SRC);
  const browse = await new Promise((res, rej) => { http.get('http://127.0.0.1:5173/api/browse?dir=@downloads', (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { rej(e); } }); }).on('error', rej); });
  const src = (browse.files || []).find((f) => /\.mp3$/i.test(f.name));
  if (!src) throw new Error('没有可用 mp3');
  const NEW = '先播后缓存测试曲目';
  const newFile = NEW + '.mp3';
  const PROBE_ID = 'ptc_' + Date.now().toString(36);
  let proc = null, P = null, BW = null;
  const wipeTasks = async () => {
    const j = await new Promise((res) => http.get('http://127.0.0.1:5173/api/site/tasks', (r) => { let d = ''; r.on('data', (c) => (d += c)); r.on('end', () => { try { res(JSON.parse(d)); } catch (e) { res({ tasks: [] }); } }); }).on('error', () => res({ tasks: [] })));
    await Promise.all((j.tasks || []).filter((t) => t.site === 'localtest').map((t) => new Promise((res) => http.get('http://127.0.0.1:5173/api/site/task/remove?id=' + encodeURIComponent(t.id), (r) => { r.resume(); r.on('end', res); }).on('error', res))));
  };

  try {
    await wipeTasks();
    const cfg = JSON.parse(backup.toString('utf8'));
    cfg.sites = cfg.sites.filter((s) => s.id !== 'localtest').concat([{
      id: 'localtest', name: '本地测试站点', kind: 'music', enabled: true, lyric: false,
      builtin: false, parser: 'generic',
      searchUrl: 'http://127.0.0.1:5173/', detailUrl: 'http://127.0.0.1:5173/',
      lyricUrl: '', linkPattern: '', base: '',
      audioUrl: 'http://127.0.0.1:5173/api/stream?file=' + encodeURIComponent(src.path),
    }]);
    fs.writeFileSync(SRC, JSON.stringify(cfg, null, 2), 'utf8');
    fs.rmSync(DL, { recursive: true, force: true }); fs.mkdirSync(DL, { recursive: true });
    fs.rmSync(path.resolve('downloads', newFile), { force: true });

    const prof = path.resolve('.tmp', 'profPTC'); fs.rmSync(prof, { recursive: true, force: true });
    proc = spawn(CHROME, ['--headless=new', '--disable-gpu', '--no-first-run', '--mute-audio', '--autoplay-policy=no-user-gesture-required',
      '--remote-debugging-port=' + PORT, '--user-data-dir=' + prof, '--window-size=390,844', 'about:blank'], { stdio: 'ignore', detached: true }); proc.unref();
    let ver = null, list = null;
    for (let i = 0; i < 60; i++) { try { ver = await hj(PORT, '/json/version'); list = await hj(PORT, '/json/list'); if (list.length) break; } catch (e) {} await sleep(400); }
    BW = await cdp(ver.webSocketDebuggerUrl);
    await BW.send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: DL, eventsEnabled: true });
    P = await cdp(list[0].webSocketDebuggerUrl); const send = P.send;
    const ev = async (x) => { const r = await send('Runtime.evaluate', { expression: x, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails).slice(0, 260)); return r.result.value; };
    await send('Page.enable'); await send('Runtime.enable'); await send('Network.enable');
    await send('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 3, mobile: true });
    await send('Page.navigate', { url: 'http://127.0.0.1:5173/' }); await sleep(2200);

    // 一条「服务端尚未缓存」的历史条目：无 path，只有 siteId/itemId
    const hist = [{ id: 'ptc1', name: newFile, kind: 'audio', siteId: 'localtest', itemId: PROBE_ID, path: '',
      tags: { title: NEW, artist: '' }, parsed: { title: NEW, artist: '', episode: 0, year: '' } }];
    await ev(`(() => { localStorage.setItem('mp.history', ${JSON.stringify(JSON.stringify(hist))}); localStorage.removeItem('mp.devsaved'); localStorage.removeItem('mp.autosavedevice'); return 1; })()`);
    await send('Page.reload', { ignoreCache: true }); await sleep(2500);

    // ---- 第一轮：应先播服务端流，不等缓存 ----
    P.events.length = 0;
    await ev(`(() => { document.querySelector('#plist li').click(); return 1; })()`);
    await sleep(320);
    const early = await ev(`(() => { const m = document.querySelector('#media'); return { src: m.getAttribute('src') || '', paused: m.paused, t: Number(m.currentTime.toFixed(2)) }; })()`);
    const earlyPlay = P.events.filter((e) => e.method === 'Network.requestWillBeSent' && /\/api\/site\/play/.test(e.params.request.url)).length;
    console.log('① 点击后 0.32s:', JSON.stringify(early), '| site/play 请求:', earlyPlay);

    // ---- 等后台缓存落地并切源 ----
    let st = null;
    for (let k = 0; k < 25; k++) {
      await sleep(900);
      st = await ev(`(() => { const m = document.querySelector('#media'); return { src: m.getAttribute('src') || '', playing: !m.paused, t: Number(m.currentTime.toFixed(1)), chip: document.querySelector('#ptCache').hidden ? '' : document.querySelector('#ptCacheTxt').textContent }; })()`);
      if (/\/api\/stream\?/.test(st.src) && st.playing && st.t > 0) break;
    }
    console.log('② 缓存完成后:', JSON.stringify(st));
    const svc = fs.existsSync(path.resolve('downloads', newFile));
    const dev = fs.existsSync(DL) ? fs.readdirSync(DL).filter((f) => !f.endsWith('.crdownload')) : [];
    console.log('③ 服务端 downloads:', svc ? newFile + ' (' + Math.round(fs.statSync(path.resolve('downloads', newFile)).size / 1024) + 'KB)' : '无');
    console.log('   设备下载目录:', dev, '| 下载事件:', BW.events.filter((e) => e.method === 'Browser.downloadWillBegin').map((e) => e.params.suggestedFilename));

    // ---- 第二轮：本地已有文件，不该再发起任何站点流请求 ----
    P.events.length = 0;
    await send('Page.reload', { ignoreCache: true }); await sleep(2500);
    await ev(`(() => { document.querySelector('#plist li').click(); return 1; })()`);
    await sleep(1600);
    const again = await ev(`(() => { const m = document.querySelector('#media'); return { src: m.getAttribute('src') || '', playing: !m.paused, t: Number(m.currentTime.toFixed(1)) }; })()`);
    const againPlay = P.events.filter((e) => e.method === 'Network.requestWillBeSent' && /\/api\/site\/play/.test(e.params.request.url)).length;
    const external = P.events.filter((e) => e.method === 'Network.requestWillBeSent' && /^https?:\/\//.test(e.params.request.url) && !/^http:\/\/127\.0\.0\.1:5173\//.test(e.params.request.url)).map((e) => e.params.request.url);
    console.log('④ 再播同一首:', JSON.stringify(again), '| site/play 请求:', againPlay, '| 前端直连外部站点:', external.length);
    const errs = P.events.filter((e) => e.method === 'Runtime.exceptionThrown').map((e) => String(e.params.exceptionDetails.text).slice(0, 100));
    console.log('⑤ 页面异常:', errs.length ? errs : '无');

    const ok = earlyPlay >= 1 && early.src.includes('/api/site/play') && early.paused === false
      && /\/api\/stream\?/.test(st.src) && st.playing && st.t > 0 && svc && dev.length > 0
      && /\/api\/stream\?/.test(again.src) && againPlay === 0 && external.length === 0 && errs.length === 0;
    console.log('RESULT:', ok ? 'PASS 先播后缓存 / 无缝切本地 / 二次播放不再拉站点 / 文件落到设备' : 'FAIL');
  } finally {
    fs.writeFileSync(SRC, backup);
    fs.rmSync(path.resolve('downloads', newFile), { force: true });
    await wipeTasks().catch(() => {});
    if (P) P.ws.close(); if (BW) BW.ws.close(); if (proc) proc.kill();
  }
  process.exit(0);
})().catch((e) => { console.error('FATAL', e); process.exit(1); });
