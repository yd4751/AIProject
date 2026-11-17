'use strict';
/* UI 冒烟：确认「歌曲宝」站点出现在界面上，且搜索 → 点击 → 自动播放/下载/歌词链路正常 */
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
  const port = 9223;
  const prof = path.join(TMP, 'prof2');
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
  const log = (...a) => console.log(a.join(' '));

  try {
    log('== 1. 站点列表里是否有「歌曲宝」==');
    log('   ', await evaluate(`(() => {
      const txt = document.querySelector('#siteList') ? document.querySelector('#siteList').textContent : document.body.textContent;
      return JSON.stringify(txt.replace(/\\s+/g,' ').slice(0, 240));
    })()`));

    log('\n== 2. 搜索「晴天」==');
    await evaluate(`(() => { document.querySelector('#dbQuery').value = '晴天'; document.querySelector('#btnDbSearch').click(); return 1 })()`);
    await sleep(13000);
    log('   分组:', await evaluate(`(() => JSON.stringify([...document.querySelectorAll('#dbCands .cand-group')].map(g => g.textContent.trim())))()`));

    log('\n== 3. 点「歌曲宝」分组第一条 ==');
    const clicked = await evaluate(`(() => {
      const kids = [...document.querySelector('#dbCands').children];
      for (let i = 0; i < kids.length; i++) {
        if (kids[i].classList.contains('cand-group') && /歌曲宝|gequbao/.test(kids[i].textContent)) {
          const c = kids[i + 1];
          if (c && c.classList.contains('cand')) {
            const nm = c.textContent.replace(/\\s+/g, ' ').trim().slice(0, 30);
            c.click();
            return nm;
          }
        }
      }
      return '未找到歌曲宝分组';
    })()`);
    log('   点击:', clicked);

    // 等下载/播放
    for (let i = 0; i < 14; i++) {
      await sleep(2500);
      const st = await evaluate(`(() => { const m = document.querySelector('#media'); return JSON.stringify({
        title: document.querySelector('#ptTitle').textContent,
        playing: m && !m.paused && m.currentTime > 0,
        t: m ? Number(m.currentTime || 0).toFixed(2) : '-',
        dur: m ? Math.round(m.duration || 0) : 0,
        lyric: document.querySelectorAll('#lyricInner .lyric-line').length,
      }) })()`);
      log(`   +${(i + 1) * 2.5}s`, st);
      const o = JSON.parse(st);
      if (o.playing && o.lyric > 0) break;
    }

    log('\n== 4. 下载任务面板 ==');
    log('   ', await evaluate(`(() => JSON.stringify([...document.querySelectorAll('#dlList li')].map(li => li.textContent.replace(/\\s+/g,' ').trim().slice(0,60))))()`));

    await sleep(8000);
    log('\n== 5. 最终状态 + 截图 ==');
    log('   ', await evaluate(`(() => { const m = document.querySelector('#media'); return JSON.stringify({
        title: document.querySelector('#ptTitle').textContent,
        t: m ? Number(m.currentTime||0).toFixed(1) : '-', paused: m ? m.paused : null,
        lyricOn: (document.querySelector('#lyricInner .lyric-line.on')||{}).textContent || '',
        dl: [...document.querySelectorAll('#dlList li')].map(li => li.textContent.replace(/\\s+/g,' ').trim().slice(0,50)),
      }) })()`));
    log('   截图:', await shot('gequbao_ui.png'));

    const errs = cdp.logs.filter((l) => /^error|^EXCEPTION/.test(l));
    log('\n== 6. 控制台错误 ==');
    log('   ', errs.length ? errs.slice(0, 8).join('\n    ') : '(无)');
  } catch (err) {
    console.error('FAILED:', err.message);
  } finally {
    try { await cdp.send('Browser.close'); } catch (e) { /* ignore */ }
    try { proc.kill(); } catch (e) { /* ignore */ }
    cdp.close();
    setTimeout(() => process.exit(0), 800);
  }
})();
