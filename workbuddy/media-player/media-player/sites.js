'use strict';
/* 可配置的抓取站点：6uq（音乐+歌词）解析器 + 通用 HTML 解析器 */

const fs = require('fs');

const { CONFIG_PATH } = require('./runtime-paths');

const UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36';

/* ------------------------------ 配置读写 ------------------------------ */

function loadConfig() {
  try {
    const j = JSON.parse(fs.readFileSync(CONFIG_PATH, 'utf8'));
    if (j && Array.isArray(j.sites)) return j;
  } catch (err) { /* 读不到就用内嵌默认配置 */ }
  // 单文件 exe 首次启动、或同目录配置被删：回退到打包时内嵌的 sources.json
  try {
    const sea = require('node:sea');
    if (sea.isSea()) {
      const j = JSON.parse(sea.getAsset('sources.json', 'utf8'));
      if (j && Array.isArray(j.sites)) return j;
    }
  } catch (err) { /* 忽略 */ }
  return { sites: [] };
}

function saveConfig(cfg) {
  const j = loadConfig();
  const next = Object.assign({}, j, cfg);
  fs.writeFileSync(CONFIG_PATH, JSON.stringify(next, null, 2), 'utf8');
  return next;
}

let cfgCache = null;
let cfgMtime = 0;
function sites() {
  try {
    const st = fs.statSync(CONFIG_PATH);
    if (!cfgCache || st.mtimeMs !== cfgMtime) {
      cfgCache = loadConfig();
      cfgMtime = st.mtimeMs;
    }
  } catch (err) {
    cfgCache = null;
  }
  if (!cfgCache) cfgCache = loadConfig();
  return cfgCache.sites || [];
}

function getSite(id) {
  return sites().find((s) => s.id === id) || null;
}

/* ------------------------------ 请求 ------------------------------ */

// 会话 cookie：host -> Map(名字 -> 值)。歌曲宝一次下发多条 cookie，
// 且「人机验证」接口的 key 与会话绑定，所以必须完整保存并回传
const cookieJar = new Map();

function cookieHeader(host) {
  const m = cookieJar.get(host);
  return m ? [...m.entries()].map(([k, v]) => `${k}=${v}`).join('; ') : '';
}

function storeCookies(host, res) {
  const list = (res.headers.getSetCookie ? res.headers.getSetCookie() : [])
    .filter(Boolean);
  if (!list.length) {
    const sc = res.headers.get('set-cookie');
    if (sc) list.push(sc);
  }
  if (!list.length) return;
  let m = cookieJar.get(host);
  if (!m) { m = new Map(); cookieJar.set(host, m); }
  list.forEach((c) => {
    const kv = c.split(';')[0];
    const i = kv.indexOf('=');
    if (i > 0) m.set(kv.slice(0, i), kv.slice(i + 1));
  });
}

const REQ_TIMEOUT = 20000; // 站点偶发慢响应，放宽到 20 秒并重试一次，避免误判成「没资源」

async function rawGet(url, extra, timeout) {
  const u = new URL(url);
  const headers = Object.assign({
    'User-Agent': UA,
    Accept: 'text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8',
    'Accept-Language': 'zh-CN,zh;q=0.9,en;q=0.8',
  }, extra || {});
  const ck = cookieHeader(u.host);
  if (ck) headers.Cookie = ck;
  const res = await fetch(url, { headers, signal: AbortSignal.timeout(timeout || REQ_TIMEOUT), redirect: 'follow' });
  storeCookies(u.host, res);
  return res;
}

// 单次取页：网络抖动时重试一次再放弃
async function fetchOnce(url) {
  try {
    return await rawGet(url);
  } catch (err) {
    await new Promise((r) => setTimeout(r, 400));
    return await rawGet(url); // 重试仍失败就抛出去，交给调用方兜底
  }
}

// 部分站点有 WAF：先访问首页拿 cookie 再取数据
async function getPage(url, site) {
  let res;
  let text = '';
  try {
    res = await fetchOnce(url);
    try { text = await res.text(); } catch (err) { text = ''; }
  } catch (err) {
    return { status: 0, text: '', res: null };
  }
  const blocked = res.status === 403 || (res.status === 200 && text.length < 400 && /window\.location\.href/.test(text));
  if (blocked && site && site.base) {
    try { await rawGet(site.base); } catch (err) { /* 忽略 */ }
    try {
      res = await rawGet(url);
      try { text = await res.text(); } catch (err) { text = ''; }
    } catch (err) { /* 保持上一次结果 */ }
  }
  return { status: res.status, text, res };
}

/* ------------------------------ HTML 工具 ------------------------------ */

function stripTags(s) {
  return String(s || '').replace(/<[^>]*>/g, '');
}

function decodeEntities(s) {
  return String(s || '')
    .replace(/&nbsp;/g, ' ')
    .replace(/&/g, '&')
    .replace(/</g, '<')
    .replace(/>/g, '>')
    .replace(/"/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/\\u0026/g, '&')
    .replace(/&middot;/g, '·')
    .replace(/&#(\d+);/g, (m, d) => String.fromCharCode(Number(d)));
}

function clean(s) {
  return decodeEntities(stripTags(s)).replace(/\s+/g, ' ').trim();
}

// 封面等外链统一走 https，否则图片代理会拒收
function httpsUrl(u) {
  return String(u || '').replace(/^http:\/\//i, 'https://');
}

function fill(tpl, vars) {
  return String(tpl || '').replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k]) : m));
}

/* ------------------------------ 6uq ------------------------------ */

function parse6uqSearch(html) {
  const out = [];
  const seen = new Set();
  const re = /<div class="name">\s*<a href="[^"']*?\/play\/([^"'\/]+)\.html"[^>]*>([\s\S]*?)<\/a>/g;
  let m;
  while ((m = re.exec(html))) {
    const id = m[1];
    const raw = clean(m[2]).replace(/\[(MP3_LRC|Mp3_Lrc|MP3|LRC)\]/gi, '').trim();
    const mm = raw.match(/^(.*?)《(.*?)》/);
    const title = (mm ? mm[2] : raw).trim();
    const artist = (mm ? mm[1] : '').trim();
    const key = id + '|' + title;
    if (!title || seen.has(key)) continue;
    seen.add(key);
    out.push({
      id, title, artist, album: '', cover: '', hasLyric: true,
      url: `https://m.6uq.cn/play/${id}.html`,
    });
  }
  return out;
}

function parse6uqDetail(html, id) {
  const dead = /歌曲相关资源已被删除|该歌曲无权播放/.test(html);
  const pageTitle = clean((html.match(/<title>([\s\S]*?)<\/title>/) || [])[1] || '');
  let title = (pageTitle.match(/《([^》]*)》/) || [])[1] || '';
  let artist = clean((html.match(/<div class="name"><a href="\/singer\/[^"]*"[^>]*>([\s\S]*?)<\/a>/) || [])[1] || '');
  if (!title) {
    const h = clean((html.match(/<h1[^>]*>([\s\S]*?)<\/h1>/) || [])[1] || '');
    const mm = h.match(/^(.*?)《(.*?)》/) || h.match(/^(.*?)\s*-\s*(.*)$/);
    if (mm) { artist = artist || (mm[1] || '').trim(); title = (mm[2] || '').trim(); }
    else title = h;
  }
  if (!artist) artist = (pageTitle.match(/^([^《]*)《/) || [])[1] || '';
  const cover = httpsUrl((html.match(/<div class="pic"><a href="\/singer\/[^"]*"[^>]*><img src="([^"]+)"/) || [])[1] || '');
  const album = clean((html.match(/所属专辑：<a href="\/album\/[^"]*"[^>]*>([\s\S]*?)<\/a>/) || [])[1] || '');
  const date = (html.match(/更新时间：([\d-]+)/) || [])[1] || '';
  return {
    id, title, artist, album, cover, date, dead,
    url: `https://m.6uq.cn/play/${id}.html`,
  };
}

/* ------------------------------ gequbao（歌曲宝） ------------------------------ */
/*
 三步取资源（站点改版时需同步调整正则）：
   搜索 GET  /s/{关键词}   列表项 <a href="/music/{id}" class="hover-zoom ..." title="标题 - 歌手">
   详情 GET  /music/{id}   内联 window.appData = JSON.parse('{...}') 提供 play_id / 封面 / 时长；
                           歌词在同页 <div id="content-lrc"> 里
   直链 POST /member/common-play-url  { id: play_id, purpose: 'play' } → data.url
 注意：第三步拿到的 CDN 直链有 Referer 校验，带 gequbao.com 来源会被拒（403），
      所以播放/下载代理不能带 Referer（server.js 的代理默认就不带，保持一致即可）。
*/

const playIdCache = new Map(); // gequbao: 曲目 id -> play_id（换页/重解析音频时用）
// 歌曲宝对部分曲目会要求「人机验证」（code=2），这类不是解析失败，不能笼统报「没有资源」
const needVerify = new Set(); // `${siteId}|${id}`

// 从详情页内联脚本里还原出 window.appData 对象
function parseAppDataBlock(html) {
  const m = html.match(/window\.appData\s*=\s*JSON\.parse\('([\s\S]*?)'\s*\)\s*;/);
  if (!m) return null;
  try {
    // 内层是 JS 字符串字面量：先把转义的单引号还原，再当 JSON 字符串解一层，最后 JSON.parse
    const inner = m[1].replace(/\\'/g, "'");
    return JSON.parse(JSON.parse('"' + inner + '"'));
  } catch (err) {
    return null;
  }
}

function parseGequbaoSearch(html) {
  const out = [];
  const seen = new Set();
  // 列表里有两组同 href 的 <a>：一组带 hover-zoom 展示歌名/歌手，一组是「播放&下载」按钮，这里只取前者
  const re = /<a\s+href="\/music\/(\d+)"\s+class="hover-zoom[^"]*"[\s\S]*?<\/a>/g;
  let m;
  while ((m = re.exec(html))) {
    const id = m[1];
    const block = m[0];
    let title = clean((block.match(/<span class="text-primary font-weight-bold[^"]*"[^>]*>([\s\S]*?)<\/span>/) || [])[1] || '');
    let artist = clean((block.match(/<small class="text-jade[^"]*"[^>]*>([\s\S]*?)<\/small>/) || [])[1] || '');
    // 结构变了就退回 title 属性：形如「晴天 - 周杰伦」
    if (!title || !artist) {
      const tvn = clean((block.match(/title="([^"]*)"/) || [])[1] || '');
      const sep = tvn.indexOf(' - ');
      if (sep > 0) { title = title || tvn.slice(0, sep).trim(); artist = artist || tvn.slice(sep + 3).trim(); }
      else title = title || tvn;
    }
    const key = id + '|' + title;
    if (!title || seen.has(key)) continue;
    seen.add(key);
    out.push({
      id, title, artist, album: '', cover: '', hasLyric: true,
      url: `https://www.gequbao.com/music/${id}`,
    });
  }
  return out;
}

function parseGequbaoDetail(html, id) {
  const d = parseAppDataBlock(html);
  const base = { id, title: '', artist: '', album: '', cover: '', date: '', dead: true, url: `https://www.gequbao.com/music/${id}` };
  if (!d) {
    // 页面存在但没有数据：多半是下架了
    const title = clean((html.match(/<title>([\s\S]*?)<\/title>/) || [])[1] || '').replace(/-\s*MP3.*$/, '').trim();
    return Object.assign({}, base, { title: (title.split('-')[0] || '').trim() });
  }
  const playId = String(d.play_id || '');
  if (playId) playIdCache.set(id, playId);
  return {
    id,
    title: clean(d.mp3_title || d.mp3_name || ''),
    artist: clean(d.mp3_author || ''),
    album: clean(d.mp3_album || ''),
    cover: httpsUrl(d.mp3_cover || ''),
    date: '',
    duration: clean(d.mp3_duration || ''),
    // 没有 play_id 就拿不到直链，视为失效资源
    dead: !playId,
    url: `https://www.gequbao.com/music/${id}`,
  };
}

// 歌词直接嵌在详情页里，不用再单独请求
function extractGequbaoLrc(html) {
  const m = html.match(/<div[^>]+id="content-lrc"[^>]*>([\s\S]*?)<\/div>/i);
  if (!m) return '';
  return m[1]
    .replace(/<br\s*\/?>/gi, '\n')
    .replace(/<[^>]*>/g, '')
    .replace(/&nbsp;/g, ' ')
    .replace(/&/g, '&').replace(/&lt;/g, '<').replace(/&gt;/g, '>')
    .split('\n').map((s) => s.trim()).filter(Boolean).join('\n')
    .trim();
}

// 详情页没抓到时，单独再取一次
async function fetchGequbaoLrc(site, id) {
  if (!site.detailUrl) return '';
  const { status, text } = await getPage(fill(site.detailUrl, { id }), site);
  if (status !== 200 || !text) return '';
  return extractGequbaoLrc(text);
}

// 部分曲目直链指向酷我 antiserver，需换一道转真实地址
async function convertKuwoUrl(url) {
  if (!/antiserver\.kuwo\.cn/i.test(url)) return url;
  try {
    const u = new URL(url);
    u.searchParams.set('type', 'convert_url3');
    const r = await fetch(u.href, { headers: { 'User-Agent': UA }, signal: AbortSignal.timeout(10000) });
    const j = await r.json();
    if (j && Number(j.code) === 200 && j.url) return String(j.url);
  } catch (err) { /* 转换失败就用原地址 */ }
  return url;
}

/*
 歌曲宝对部分曲目要求「人机验证」（POST 返回 code=2）。验证是站内图片验证码：
   1) GET  /api/captcha          → { key, img(base64 dataURL) }
   2) POST /member/common-play-url 携带 { id, purpose:'play', captchaCode:用户输入, key }
 成功后同样返回 code=1 + 直链。captcha 参数即 { captchaCode, key }，由前端弹层收集。
 返回 { url } 成功 / { needCaptcha:true } 要求验证 / { captchaError:true } 验证码不对。
*/
async function resolveGequbaoAudioEx(site, id, captcha) {
  const key = `${site.id}|${id}`;
  const hit = audioCache.get(key);
  if (hit && Date.now() - hit.at < 30 * 60 * 1000) return { url: hit.url };

  // 先确保有 play_id：缓存没有就回详情页取
  let playId = playIdCache.get(id) || '';
  if (!playId) {
    const { status, text } = await getPage(fill(site.detailUrl, { id }), site);
    if (status !== 200 || !text) return {};
    const d = parseAppDataBlock(text);
    if (!d || !d.play_id) return {};
    playId = String(d.play_id);
    playIdCache.set(id, playId);
  }

  const referer = fill(site.detailUrl, { id });
  const host = new URL(site.audioUrl).host;
  const postPlayUrl = async () => {
    const body = { id: playId, purpose: 'play' };
    if (captcha && captcha.captchaCode && captcha.key) {
      body.captchaCode = String(captcha.captchaCode).trim();
      body.key = captcha.key;
    }
    const headers = {
      'User-Agent': UA,
      'Content-Type': 'application/x-www-form-urlencoded; charset=UTF-8',
      'X-Requested-With': 'XMLHttpRequest',
      Accept: 'application/json, text/javascript, */*; q=0.01',
      'Accept-Language': 'zh-CN,zh;q=0.9',
      Referer: referer,
    };
    const ck = cookieHeader(host);
    if (ck) headers.Cookie = ck;
    const res = await fetch(site.audioUrl, {
      method: 'POST',
      headers,
      body: new URLSearchParams(body).toString(),
      signal: AbortSignal.timeout(15000),
    });
    storeCookies(host, res);
    const j = await res.json();
    // code=2 → 需要人机验证；code=3 → 验证码不对。都不是解析失败，单独标记让前端走验证码弹层
    if (j && Number(j.code) === 2) { needVerify.add(key); return { needCaptcha: true }; }
    if (j && Number(j.code) === 3) { needVerify.add(key); return { captchaError: true }; }
    needVerify.delete(key);
    if (j && Number(j.code) === 1 && j.data && j.data.url) return { url: String(j.data.url) };
    return {};
  };

  try {
    let r = await postPlayUrl();
    // 站点偶发返回空，重试一次再判定为无资源（要求验证 / 验证码不对时不重试）
    if (!r.url && !r.needCaptcha && !r.captchaError) {
      await new Promise((s) => setTimeout(s, 500));
      r = await postPlayUrl();
    }
    if (r.url) {
      r.url = await convertKuwoUrl(r.url);
      audioCache.set(key, { url: r.url, at: Date.now() });
    }
    return r;
  } catch (err) {
    return {};
  }
}

/* 取一张站内验证码（与会话 cookie 绑定，key 一起带回给前端，提交时原样传回） */
async function fetchCaptcha(site) {
  const base = site.base || (site.audioUrl || site.detailUrl || '').replace(/\/[^\/]*$/, '');
  if (!base) return { ok: false, error: '站点缺少地址配置' };
  try {
    const host = new URL(base).host;
    const headers = {
      'User-Agent': UA,
      Accept: 'application/json, text/javascript, */*; q=0.01',
      'X-Requested-With': 'XMLHttpRequest',
      Referer: base + '/',
    };
    const ck = cookieHeader(host);
    if (ck) headers.Cookie = ck;
    const res = await fetch(new URL('/api/captcha', base).href + '?' + Math.random(), {
      headers, signal: AbortSignal.timeout(15000),
    });
    storeCookies(host, res);
    const j = await res.json();
    if (j && Number(j.code) === 1 && j.data && j.data.img && j.data.key) {
      return { ok: true, key: String(j.data.key), img: String(j.data.img) };
    }
    return { ok: false, error: '站点未返回验证码' };
  } catch (err) {
    return { ok: false, error: '获取验证码失败：' + (err && err.message || err) };
  }
}

/* 音频直链：站点给的是下载页（通常 302 到 CDN），这里解析出真实地址并短暂缓存 */
const audioCache = new Map(); // siteId|id -> { url, at }
/* 扩展版：返回 { url } / { needCaptcha:true } / { captchaError:true } / {} */
async function resolveAudioEx(site, id, captcha) {
  if (!site.audioUrl) return {};
  // 歌曲宝是 POST 接口换直链，走单独分支
  if (site.audioMethod === 'post') return resolveGequbaoAudioEx(site, id, captcha);
  const url = await resolveGenericAudio(site, id);
  return { url };
}
/* 兼容旧接口：只返回 url 字符串（play/download 代理、下载任务用） */
async function resolveAudio(site, id) {
  const r = await resolveAudioEx(site, id);
  return r.url || '';
}

/* 直链失效（CDN 返回 403/410 等）时清缓存，下一次解析会重新换链接 */
function invalidateAudio(site, id) {
  if (!site || !id) return;
  audioCache.delete(`${site.id}|${id}`);
  playIdCache.delete(String(id));
}

async function resolveGenericAudio(site, id) {
  const key = `${site.id}|${id}`;
  const hit = audioCache.get(key);
  if (hit && Date.now() - hit.at < 30 * 60 * 1000) return hit.url;
  const target = fill(site.audioUrl, { id });
  const u = new URL(target);
  const headers = { 'User-Agent': UA, Accept: 'audio/*,*/*;q=0.8', 'Accept-Language': 'zh-CN,zh;q=0.9' };
  const ck = cookieHeader(u.host);
  if (ck) headers.Cookie = ck;
  try {
    const res = await fetch(target, { headers, redirect: 'manual', signal: AbortSignal.timeout(10000) });
    storeCookies(u.host, res);
    let url = res.headers.get('location') || '';
    if (url && !/^https?:/i.test(url)) url = new URL(url, target).href;
    // 有的站点直接吐音频本体（非跳转）
    if (!url && res.ok && /^audio\//i.test(res.headers.get('content-type') || '')) url = target;
    if (url) audioCache.set(key, { url, at: Date.now() });
    return url;
  } catch (err) {
    return '';
  }
}

/* ------------------------------ 通用 HTML 站点 ------------------------------ */

function genericLinks(html, site) {
  const out = [];
  const seen = new Set();
  const re = /<a\s[^>]*href=["']([^"']+)["'][^>]*>([\s\S]*?)<\/a>/gi;
  let m;
  const pat = site.linkPattern ? new RegExp(site.linkPattern, 'g') : null;
  while ((m = re.exec(html))) {
    let href = m[1];
    const text = clean(m[2]);
    if (!text || text.length < 2) continue;
    let id = '';
    if (pat) {
      pat.lastIndex = 0;
      const p = pat.exec(href);
      if (!p) continue;
      id = p[1] || p[0];
    } else {
      // 没配 linkPattern 时，只保留看起来像详情页的链接
      if (!/\/(play|song|music|subject|detail|info)/i.test(href)) continue;
      id = (href.match(/\/([A-Za-z0-9_-]+)(?:\.html?)?\/?$/) || [])[1] || '';
    }
    if (!id || seen.has(id)) continue;
    seen.add(id);
    try { href = new URL(href, site.detailUrl || site.searchUrl).href; } catch (err) { /* 保持原样 */ }
    out.push({ id, title: text.slice(0, 60), artist: '', album: '', cover: '', hasLyric: !!site.lyricUrl, url: href });
  }
  return out;
}

function genericDetail(html, id, site) {
  const og = (p) => ((html.match(new RegExp(`<meta[^>]+(?:property|name)=["']${p}["'][^>]+content=["']([^"']+)["']`, 'i')) || [])[1]
    || (html.match(new RegExp(`<meta[^>]+content=["']([^"']+)["'][^>]+(?:property|name)=["']${p}["']`, 'i')) || [])[1] || '');
  let title = clean(og('og:title') || (html.match(/<title>([\s\S]*?)<\/title>/) || [])[1] || '');
  title = title.split(/[-|–—]/)[0].trim() || title;
  const cover = httpsUrl(og('og:image') || (html.match(/<img[^>]+src=["'](https?:[^"']+)["']/) || [])[1] || '');
  let artist = clean(og('og:music:artist') || '');
  let album = clean(og('og:music:album') || '');
  return { id, title, artist, album, cover, date: '', dead: false, url: site.detailUrl ? fill(site.detailUrl, { id }) : '' };
}

/* ------------------------------ 对外接口 ------------------------------ */

async function searchSite(site, q) {
  const url = fill(site.searchUrl, { q: encodeURIComponent(q), raw: q });
  const { status, text } = await getPage(url, site);
  // 站点（如歌曲宝）用 404 表示「没搜到」，这是空结果而不是站点故障
  if (status === 404) return { ok: true, items: [] };
  // status === 0 表示请求根本没走通（超时/断网），别显示成「站点返回 0」
  if (status === 0) return { ok: false, error: '连接站点超时，请稍后重试', items: [] };
  if (status === 404) return { ok: true, items: [] }; // 站点用 404 表示无结果
  if (status !== 200 || !text) return { ok: false, error: `站点返回 ${status}`, items: [] };
  let items = [];
  if (site.parser === '6uq') items = parse6uqSearch(text);
  else if (site.parser === 'gequbao') items = parseGequbaoSearch(text);
  else items = genericLinks(text, site);
  // 相关性排序：标题完全命中优先
  const norm = (s) => String(s || '').replace(/\s+/g, '').toLowerCase();
  const nq = norm(q);
  items.sort((a, b) => {
    const sa = (norm(a.title) === nq ? 2 : norm(a.title).includes(nq) ? 1 : 0);
    const sb = (norm(b.title) === nq ? 2 : norm(b.title).includes(nq) ? 1 : 0);
    return sb - sa;
  });
  return { ok: true, items: items.slice(0, 30) };
}

async function fetchLyric(site, id) {
  if (!site.lyricUrl) return '';
  const url = fill(site.lyricUrl, { id });
  const { status, text } = await getPage(url, site);
  if (status !== 200 || !text) return '';
  return text;
}

async function detailSite(site, id, opts) {
  const wantAudio = !opts || opts.audio !== false;
  let info = { id, title: '', artist: '', album: '', cover: '', date: '', dead: false, url: '', audio: '' };
  let html = '';
  if (site.detailUrl) {
    const { status, text } = await getPage(fill(site.detailUrl, { id }), site);
    if (status === 200 && text) {
      html = text;
      if (site.parser === '6uq') info = parse6uqDetail(text, id);
      else if (site.parser === 'gequbao') info = parseGequbaoDetail(text, id);
      else info = genericDetail(text, id, site);
    }
  }
  // 注意：6uq 详情页常标「该歌曲无权播放/资源已删除」，但 down.php 往往还能出直链，
  // 所以不因 dead 放弃，实际有没有资源以解析结果为准。
  let lyric = '';
  // 歌曲宝歌词嵌在详情页里，直接复用已抓到的 HTML，省一次请求
  if (site.parser === 'gequbao') lyric = html ? extractGequbaoLrc(html) : await fetchGequbaoLrc(site, id);
  else if (site.lyric) lyric = await fetchLyric(site, id);
  let audio = '';
  let audioEx = {};
  if (wantAudio) audioEx = await resolveAudioEx(site, id, opts && opts.captcha);
  audio = audioEx.url || '';
  const noAudio = !audio;
  return {
    ok: true,
    info: Object.assign({}, info, { audio }),
    lyric,
    verify: noAudio && needVerify.has(`${site.id}|${id}`) && !audioEx.captchaError,
    // 需要人机验证 / 验证码输入错了 —— 前端据此弹验证码层
    needCaptcha: noAudio && !!audioEx.needCaptcha,
    captchaError: noAudio && !!audioEx.captchaError,
  };
}

module.exports = {
  loadConfig, saveConfig, sites, getSite,
  searchSite, detailSite, fetchLyric, resolveAudio, resolveAudioEx, fetchCaptcha, invalidateAudio, clean,
};
