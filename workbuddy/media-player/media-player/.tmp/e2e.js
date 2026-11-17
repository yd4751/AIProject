'use strict';
/* 端到端验证：
   A. 站点搜索 → 点击结果 → 自动播放 + 下载进度 → 完成后切本地文件
   B. 播放本地音乐 → 同目录 .lrc 自动匹配 → 歌词随时间同步高亮
*/
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');

const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
const BASE = 'http://127.0.0.1:5173';
const TMP = path.resolve(__dirname);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function httpJSON(port, urlPath) {
  return new Promise((resolve, reject) => {
    http.get({ host: '127.0.0.1', port, path: urlPath }, (res) => {
      let d = '';
      res.on('data', (c) => (d += c));
      res.on('end', () => { try { resolve(JSON.parse(d)); } catch (e) { reject(e); } });
    }).on('error', reject);
  });
}

async function attachCDP(page) {
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
  let seq = 0;
  const pending = new Map();
  const logs = [];
  ws.onmessage = (ev) => {
    const m = JSON.parse(ev.data);
    if (m.id && pending.has(m.id)) {
      const { resolve, reject } = pending.get(m.id);
      pending.delete(m.id);
      m.error ? reject(new Error(JSON.stringify(m.error))) : resolve(m.result);
    } else if (m.method === 'Log.entryAdded') {
      logs.push(m.params.entry.level + ': ' + m.params.entry.text);
    } else if (m.method === 'Runtime.exceptionThrown') {
      const d = m.params.exceptionDetails;
      logs.push('EXCEPTION: ' + ((d.exception && d.exception.description) || d.text || ''));
    }
  };
  const send = (method, params) => new Promise((resolve, reject) => {
    const id = ++seq;
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params: params || {} }));
    setTimeout(() => { if (pending.has(id)) { pending.delete(id); reject(new Error('timeout: ' + method)); } }, 45000);
  });
  const evaluate = async (expr) => {
    const r = await send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error((r.exceptionDetails.exception && r.exceptionDetails.exception.description) || 'eval error');
    return r.result.value;
  };
  const shot = async (name) => {
    const r = await send('Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(TMP, name), Buffer.from(r.data, 'base64'));
    return name;
  };
  return { ws, send, evaluate, shot, logs, close: () => { try { ws.close(); } catch (e) { /* ignore */ } } };
}

async function launch() {
  const port = 9222;
  const prof = path.join(TMP, 'prof');
  fs.rmSync(prof, { recursive: true, force: true });
  const proc = spawn(CHROME, [
    '--headless=new', '--disable-gpu', '--no-first-run', '--no-default-browser-check',
    `--remote-debugging-port=${port}`, `--user-data-dir=${prof}`,
    '--window-size=1500,940', '--mute-audio', '--autoplay-policy=no-user-gesture-required',
    'about:blank',
  ], { stdio: 'ignore', detached: true });
  proc.unref();
  let list = null;
  for (let i = 0; i < 50; i++) {
    try { list = await httpJSON(port, '/json/list'); if (list && list.length) break; } catch (e) { /* wait */ }
    await sleep(400);
  }
  if (!list || !list.length) throw new Error('Chrome 未就绪');
  const cdp = await attachCDP(list.find((t) => t.type === 'page'));
  await cdp.send('Page.enable');
  await cdp.send('Runtime.enable');
  await cdp.send('Log.enable');
  await cdp.send('Page.navigate', { url: BASE });
  await sleep(3500);
  return { cdp, proc };
}

(async () => {
  const { cdp, proc } = await launch();
  const { evaluate, shot } = cdp;
  const out = [];
  const log = (...a) => { const s = a.join(' '); out.push(s); console.log(s); };

  try {
    /* ---------------- A. 站点结果 → 自动播放 + 下载 ---------------- */
    log('== A1. 初始 & 空列表点播放 ==');
    log('  播放列表:', await evaluate(`(() => JSON.stringify({ n: document.querySelectorAll('#plist li').length }))()`));
    await evaluate(`(() => { document.querySelector('#btnPlay').click(); return 1 })()`);
    await sleep(600);
    log('  toast:', await evaluate(`(() => { const t = document.querySelector('#toast'); return t.hidden ? '(无)' : t.textContent })()`));

    log('\n== A2. 搜索「晴天」（默认全站点）==');
    await evaluate(`(() => { document.querySelector('#dbQuery').value = '晴天'; document.querySelector('#btnDbSearch').click(); return 1 })()`);
    await sleep(11000);
    log('  分组:', await evaluate(`(() => JSON.stringify([...document.querySelectorAll('#dbCands .cand-group')].map(g => g.textContent.trim())))()`));

    log('\n== A3. 点击 6uq 组第一条 ==');
    const clicked = await evaluate(`(() => {
      const kids = [...document.querySelector('#dbCands').children];
      for (let i = 0; i < kids.length; i++) {
        if (kids[i].classList.contains('cand-group') && /爱听|6uq/.test(kids[i].textContent)) {
          const c = kids[i + 1];
          if (c && c.classList.contains('cand')) {
            const nm = c.textContent.replace(/\\s+/g, ' ').trim().slice(0, 24);
            c.click();
            return nm;
          }
        }
      }
      return '未找到 6uq 分组';
    })()`);
    log('  点击:', clicked);
    await sleep(7000);
    log('  播放状态:', await evaluate(`(() => { const m = document.querySelector('#media'); return JSON.stringify({
      title: document.querySelector('#ptTitle').textContent,
      src: decodeURIComponent(m.currentSrc || m.src || '').slice(-46),
      paused: m.paused, duration: Math.round(m.duration || 0), t: Number(m.currentTime || 0).toFixed(2),
      audioMode: document.querySelector('#player').classList.contains('audio-mode'),
      playing: document.querySelector('#player').classList.contains('playing'),
    }) })()`));

    log('\n== A4. 下载进度轮询 ==');
    for (let i = 0; i < 10; i++) {
      const s = await evaluate(`(() => { const li = [...document.querySelectorAll('#dlList .dl-item')];
        return JSON.stringify({ boxHidden: document.querySelector('#dlBox').hidden, items: li.map(x => ({
          nm: (x.querySelector('.dl-nm') || {}).textContent, st: (x.querySelector('.dl-st') || {}).textContent,
          bar: (x.querySelector('.dl-bar i') || {}).style ? x.querySelector('.dl-bar i').style.width : '',
          cls: x.className })) }) })()`);
      log('  ', s);
      if (/已完成/.test(s)) break;
      await sleep(1800);
    }
    log('  下载后 src:', await evaluate(`(() => { const m = document.querySelector('#media'); return decodeURIComponent(m.currentSrc || m.src || '').slice(-50) + ' | paused=' + m.paused })()`));
    log('  歌词:', await evaluate(`(() => JSON.stringify({ hidden: document.querySelector('#lyricBox').hidden, src: document.querySelector('#lyricSrc').textContent, lines: document.querySelectorAll('#lyricInner .lyric-line').length, cur: (document.querySelector('#lyricInner .lyric-line.on') || {}).textContent || '' }))()`));
    await shot('A_site.png');

    /* ---------------- B. 本地歌词同步 ---------------- */
    log('\n== B1. 本地歌词（短音频 wav + 同名 .lrc）==');
    await evaluate(`(() => { document.querySelector('#libDir').value = ${JSON.stringify(TMP.replace(/\\/g, '/'))}; document.querySelector('#btnLibGo').click(); return 1 })()`);
    await sleep(1500);
    log('  目录:', await evaluate(`(() => JSON.stringify([...document.querySelectorAll('#libList li')].map(li => li.textContent.trim().slice(0, 22))))()`));
    await evaluate(`(() => { const li = [...document.querySelectorAll('#libList li')].find(x => /晴天\\.wav/.test(x.textContent)); if (li) li.click(); return 1 })()`);
    await sleep(2000);

    const readLyric = () => evaluate(`(() => { const on = document.querySelector('#lyricInner .lyric-line.on');
      return JSON.stringify({ hidden: document.querySelector('#lyricBox').hidden, src: document.querySelector('#lyricSrc').textContent,
        lines: document.querySelectorAll('#lyricInner .lyric-line').length,
        cur: on ? on.textContent : '(无高亮)', idx: on ? [...document.querySelectorAll('#lyricInner .lyric-line')].indexOf(on) : -1 }) })()`);

    for (const t of [1.2, 3.8]) {
      await evaluate(`(() => { const m = document.querySelector('#media'); m.currentTime = ${t}; return m.currentTime })()`);
      await sleep(800);
      log(`  t=${t}s →`, await readLyric());
    }
    await shot('B1_local_lyric.png');

    /* ---- B2. 长音频多行同步：给已下载的 mp3 造一份跨分钟的歌词 ---- */
    log('\n== B2. 长音频（270s）多行歌词随时间推进 ==');
    const dlDir = path.join(path.dirname(TMP), 'downloads');
    const mp3 = '周杰伦 - 七里香.mp3';
    log('  目标文件:', path.join(dlDir, mp3));
    const lrcPath = path.join(dlDir, mp3.replace(/\.mp3$/, '.lrc'));
    fs.writeFileSync(lrcPath, [
      '[ti:同步测试]', '[ar:测试]',
      '[00:05.00]第一行-五秒', '[00:35.00]第二行-三十五秒',
      '[01:10.00]第三行-一分十秒', '[01:50.00]第四行-一分五十秒',
      '[02:30.00]第五行-二分三十秒',
    ].join('\n'), 'utf8');
    log('  已写本地歌词:', path.basename(lrcPath));

    await evaluate(`(() => { document.querySelector('#libDir').value = ${JSON.stringify(dlDir.replace(/\\/g, '/'))}; document.querySelector('#btnLibGo').click(); return 1 })()`);
    await sleep(1500);
    const pickedMp3 = await evaluate(`(() => { const li = [...document.querySelectorAll('#libList li')].find(x => /七里香\\.mp3/i.test(x.textContent)); if (li) { li.click(); return li.textContent.trim().slice(0, 24); } return 'not-found' })()`);
    log('  载入:', pickedMp3);
    await sleep(2500);
    log('  来源(初始):', await readLyric());

    for (const t of [6, 36, 71, 111, 151]) {
      const real = await evaluate(`(() => { const m = document.querySelector('#media'); m.currentTime = ${t}; return Number(m.currentTime).toFixed(1) })()`);
      await sleep(900);
      log(`  跳到 ${t}s (实际 ${real}s) →`, await readLyric());
    }
    await shot('B2_long_lyric.png');

    log('\n== B2. 控制台 ==');
    log(cdp.logs.length ? cdp.logs.slice(-12).join('\n') : '（无）');
    fs.writeFileSync(path.join(TMP, 'report.txt'), out.join('\n'));
  } catch (err) {
    log('测试异常:', err.message);
  } finally {
    cdp.close();
    await sleep(400);
    try { proc.kill(); } catch (e) { /* ignore */ }
    process.exit(0);
  }
})();
