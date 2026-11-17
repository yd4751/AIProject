'use strict';

/**
 * 本地影音播放器 —— 轻量后端（零依赖）
 *  1) 静态资源托管
 *  2) 豆瓣信息代理（搜索 / 详情 / 图片），解决 CORS 与图片防盗链
 *  3) 本地媒体库浏览 + 支持 HTTP Range 的流式播放
 */

const http = require('node:http');
const fs = require('node:fs');
const fsp = fs.promises;
const path = require('node:path');
const os = require('node:os');
const { spawn } = require('node:child_process');
const { Readable } = require('node:stream');

const SITES = require('./sites');
// 监听端口、绑定地址与所有数据路径统一由 config.json 提供
const {
  APP_DIR, PUBLIC_DIR, DOWNLOAD_DIR, CONFIG_PATH, CONFIG_FILE,
  MEDIA_DIR, PORT, HOST, config, isSea,
} = require('./runtime-paths');

const DEFAULT_MEDIA_DIR = MEDIA_DIR;

const UA_DESKTOP =
  'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36';
const UA_MOBILE =
  'Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1';

const MEDIA_EXT = new Set([
  '.mp4', '.m4v', '.webm', '.mkv', '.mov', '.avi', '.wmv', '.flv', '.ogv', '.mpg', '.mpeg', '.ts', '.3gp',
  '.mp3', '.m4a', '.wav', '.flac', '.aac', '.opus', '.ogg', '.oga', '.aiff', '.aif',
]);

const STREAM_MIME = {
  '.mp4': 'video/mp4', '.m4v': 'video/mp4', '.webm': 'video/webm', '.mkv': 'video/x-matroska',
  '.mov': 'video/quicktime', '.avi': 'video/x-msvideo', '.wmv': 'video/x-ms-wmv', '.flv': 'video/x-flv',
  '.ogv': 'video/ogg', '.ogg': 'audio/ogg', '.oga': 'audio/ogg', '.mpg': 'video/mpeg', '.mpeg': 'video/mpeg',
  '.ts': 'video/mp2t', '.3gp': 'video/3gpp', '.mp3': 'audio/mpeg', '.m4a': 'audio/mp4', '.wav': 'audio/wav',
  '.flac': 'audio/flac', '.aac': 'audio/aac', '.opus': 'audio/ogg', '.aiff': 'audio/aiff', '.aif': 'audio/aiff',
};

const STATIC_MIME = {
  '.html': 'text/html; charset=utf-8', '.htm': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8', '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8', '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml', '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg',
  '.gif': 'image/gif', '.webp': 'image/webp', '.ico': 'image/x-icon', '.woff2': 'font/woff2',
  '.txt': 'text/plain; charset=utf-8', '.map': 'application/json; charset=utf-8',
};

/* ---------------------------------- 工具 ---------------------------------- */

function sendJSON(res, data, status = 200) {
  const body = JSON.stringify(data);
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Content-Length': Buffer.byteLength(body),
    'Cache-Control': 'no-store',
  });
  res.end(body);
}

function norm(s) {
  return String(s || '')
    .replace(/[\u200b-\u200f\u202a-\u202e\ufeff]/g, '')
    .replace(/[\s\u3000]+/g, '')
    .replace(/[·・.,_\-—–!！?？:：'"“”‘’()（）\[\]【】]/g, '')
    .toLowerCase();
}

function stripMarks(s) {
  return String(s || '').replace(/[\u200b-\u200f\u202a-\u202e\ufeff]/g, '').trim();
}

async function upstream(url, { ua = UA_DESKTOP, referer = '', timeout = 9000 } = {}) {
  const headers = {
    'User-Agent': ua,
    Accept: 'application/json,text/plain,text/html,*/*',
    'Accept-Language': 'zh-CN,zh;q=0.9,en;q=0.8',
  };
  if (referer) headers.Referer = referer;
  return fetch(url, { headers, signal: AbortSignal.timeout(timeout), redirect: 'follow' });
}

const cache = new Map();
async function cached(key, ttl, fn) {
  const hit = cache.get(key);
  if (hit && Date.now() - hit.t < ttl) return hit.v;
  const v = await fn();
  cache.set(key, { t: Date.now(), v });
  if (cache.size > 300) {
    for (const [k, item] of cache) if (Date.now() - item.t > ttl) cache.delete(k);
  }
  return v;
}

/* -------------------------------- 豆瓣接口 -------------------------------- */

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/* 搜索页 HTML 里内嵌的 window.__DATA__（电影/音乐都可用，这是目前唯一稳定的音乐检索入口） */
function parseSearchData(text) {
  const i = text.indexOf('window.__DATA__');
  if (i < 0) return { items: [], error: '' };
  const s = text.indexOf('{', i);
  if (s < 0) return { items: [], error: '' };
  let depth = 0; let end = -1;
  for (let p = s; p < text.length; p++) {
    const c = text[p];
    if (c === '{') depth++;
    else if (c === '}') { depth--; if (depth === 0) { end = p; break; } }
  }
  if (end < 0) return { items: [], error: '' };
  try {
    const j = JSON.parse(text.slice(s, end + 1));
    return {
      items: Array.isArray(j.items) ? j.items : [],
      error: String(j.error_info || '').trim(),
    };
  } catch (err) {
    return { items: [], error: '' };
  }
}

function fromSearchItem(it, kind) {
  const url = String(it.url || '');
  const isMusic = kind === 'music' || /music\.douban\.com/.test(url);
  let title = stripMarks(it.title);
  let original = '';
  const seg = title.split(/\s*\/\s*/);
  if (seg.length > 1) { title = seg[0].trim(); original = seg.slice(1).join(' / ').trim(); }
  let year = '';
  const ym = title.match(/[（(]\s*(\d{4})\s*[)）]\s*$/);
  if (ym) { year = ym[1]; title = title.replace(/[（(]\s*\d{4}\s*[)）]\s*$/, '').trim(); }
  const abs = String(it.abstract || '').split('/').map((x) => x.trim()).filter(Boolean);
  // 音乐：艺人 / 发行日期 / 载体类型 / 介质 / 流派
  if (!year && isMusic && abs[1]) {
    const y = abs[1].match(/(\d{4})/);
    if (y) year = y[1];
  }
  const rating = it.rating && Number(it.rating.value)
    ? { value: Number(it.rating.value), count: Number(it.rating.count || 0), stars: Number(it.rating.star_count || Math.round(it.rating.value / 2)) }
    : null;
  return {
    id: String(it.id || ''),
    title,
    original,
    year,
    cover: String(it.cover_url || ''),
    kind: isMusic ? 'music' : 'movie',
    url,
    abstract: abs.join(' / '),
    rating,
  };
}

let lastSearchError = ''; // 最近一次搜索页返回的错误信息（多为限流），供接口回传前端
async function doubanSearchPage(query, kind) {
  const q = String(query || '').trim();
  if (!q) return [];
  const path = kind === 'music' ? 'music' : 'movie';
  const key = `p:${path}:${q}`;
  return cached(key, 10 * 60 * 1000, async () => {
    const url = `https://search.douban.com/${path}/subject_search?search_text=${encodeURIComponent(q)}`;
    try {
      let res = await upstream(url, { timeout: 9000 });
      if (!res.ok) return [];
      let parsed = parseSearchData(await res.text());
      // 豆瓣对搜索页有频率限制（error_info: 搜索访问太频繁），退避后重试一次
      if (!parsed.items.length && /频繁|too many|captcha|验证/i.test(parsed.error)) {
        await sleep(1500);
        res = await upstream(url, { timeout: 9000 });
        if (res.ok) parsed = parseSearchData(await res.text());
      }
      lastSearchError = parsed.error || '';
      return parsed.items.map((it) => fromSearchItem(it, kind));
    } catch (err) {
      return [];
    }
  }).then((list) => {
    if (!list.length) cache.delete(key); // 空结果（多为限流）不缓存，方便重试
    return list;
  });
}

async function doubanSuggest(query, kind) {
  const q = String(query || '').trim();
  if (!q) return [];
  const key = `s:${kind}:${q}`;
  return cached(key, 10 * 60 * 1000, async () => {
    const host = kind === 'music' ? 'https://music.douban.com' : 'https://movie.douban.com';
    const url = `${host}/j/subject_suggest?q=${encodeURIComponent(q)}`;
    try {
      const res = await upstream(url, { timeout: 8000 });
      if (!res.ok) return [];
      const data = JSON.parse(await res.text());
      if (!Array.isArray(data)) return [];
      return data
        .map((it) => ({
          id: String(it.id || ''),
          title: stripMarks(it.title),
          original: stripMarks(it.sub_title || it.original_title || ''),
          year: String(it.year || ''),
          cover: String(it.img || it.pic || ''),
          kind: kind === 'music' ? 'music' : it.type === 'tv' ? 'tv' : 'movie',
          url: String(it.url || '').replace(/\?suggest=.*$/, ''),
        }))
        .filter((it) => it.id && it.title);
    } catch (err) {
      return [];
    }
  });
}

function fromRexxar(j, id, kind) {
  const pic = j.pic || {};
  return {
    id: String(id),
    kind: kind || (j.is_tv ? 'tv' : 'movie'),
    title: stripMarks(j.title),
    originalTitle: stripMarks(j.original_title || ''),
    year: String(j.year || ''),
    subtitle: stripMarks(j.card_subtitle || ''),
    rating: j.rating && j.rating.value
      ? { value: Number(j.rating.value), count: Number(j.rating.count || 0), stars: Number(j.rating.star_count || Math.round(j.rating.value / 2)) }
      : null,
    genres: Array.isArray(j.genres) ? j.genres.filter(Boolean) : [],
    duration: Array.isArray(j.durations) ? j.durations.join(' / ') : '',
    pubdate: Array.isArray(j.pubdate) ? j.pubdate.join(' / ') : '',
    region: Array.isArray(j.countries) ? j.countries.join(' / ') : '',
    languages: Array.isArray(j.languages) ? j.languages.join(' / ') : '',
    directors: Array.isArray(j.directors) ? j.directors.map((d) => stripMarks(d.name)).filter(Boolean) : [],
    actors: Array.isArray(j.actors) ? j.actors.map((a) => stripMarks(a.name)).filter(Boolean).slice(0, 10) : [],
    intro: String(j.intro || '').trim(),
    aka: Array.isArray(j.aka) ? j.aka.map(stripMarks).filter(Boolean).slice(0, 4) : [],
    episodes: Number(j.episodes_count || 0),
    cover: String(pic.normal || pic.large || j.cover_url || ''),
    url: String(j.url || `https://movie.douban.com/subject/${id}/`),
    // 音乐条目字段
    singer: Array.isArray(j.singer) ? j.singer.map((s) => stripMarks(s.name)).filter(Boolean) : [],
    publisher: Array.isArray(j.publisher) ? j.publisher.map(stripMarks).filter(Boolean) : [],
    tracks: Array.isArray(j.songs) ? j.songs.length : Number(j.track_count || 0),
    source: 'douban',
  };
}

/* 详情接口取不到时，用搜索结果本身拼一个可用的卡片（有封面、评分、链接） */
function fromItemCard(it) {
  return {
    id: String(it.id),
    kind: it.kind || 'movie',
    title: it.title,
    originalTitle: it.original,
    year: it.year,
    subtitle: it.abstract || [it.original, it.year].filter(Boolean).join(' / '),
    rating: it.rating || null,
    genres: [],
    duration: '',
    pubdate: it.year || '',
    region: '',
    languages: '',
    directors: [],
    actors: [],
    intro: '',
    aka: [],
    episodes: 0,
    cover: it.cover,
    url: it.url || (it.kind === 'music'
      ? `https://music.douban.com/subject/${it.id}/`
      : `https://movie.douban.com/subject/${it.id}/`),
    singer: [],
    publisher: [],
    tracks: 0,
    source: 'douban-search',
  };
}

function fromAbstract(j, id, kind) {
  const raw = stripMarks(j.title || '');
  const title = raw.replace(/\s*[（(]\s*\d{4}\s*[)）]\s*$/, '').trim();
  const rate = Number(j.rate || 0);
  return {
    id: String(id),
    kind: kind || (j.is_tv ? 'tv' : 'movie'),
    title,
    originalTitle: '',
    year: String(j.release_year || (raw.match(/(\d{4})/) || [])[1] || ''),
    subtitle: [j.release_year, j.region, (j.types || []).join(' ')].filter(Boolean).join(' / '),
    rating: rate ? { value: rate, count: 0, stars: Math.round(rate / 2) } : null,
    genres: Array.isArray(j.types) ? j.types.filter(Boolean) : [],
    duration: String(j.duration || ''),
    pubdate: '',
    region: String(j.region || ''),
    languages: '',
    directors: Array.isArray(j.directors) ? j.directors.map(stripMarks) : [],
    actors: Array.isArray(j.actors) ? j.actors.map(stripMarks).slice(0, 10) : [],
    intro: j.short_comment && j.short_comment.content ? `短评：${j.short_comment.content}` : '',
    aka: [],
    episodes: Number(j.episodes_count || 0),
    cover: '',
    url: String(j.url || `https://movie.douban.com/subject/${id}/`),
    source: 'douban',
  };
}

async function doubanSubject(id, kind) {
  const key = `d:${kind}:${id}`;
  return cached(key, 60 * 60 * 1000, async () => {
    // 音乐走独立端点，影视走 movie / tv
    const rexxarKind = kind === 'music' ? 'music' : kind === 'tv' ? 'tv' : 'movie';
    const refererHost = kind === 'music' ? 'https://m.douban.com/music/subject/' : 'https://m.douban.com/movie/subject/';
    try {
      const res = await upstream(`https://m.douban.com/rexxar/api/v2/${rexxarKind}/${id}`, {
        ua: UA_MOBILE,
        referer: `${refererHost}${id}/`,
        timeout: 9000,
      });
      if (res.ok) {
        const j = await res.json();
        if (j && j.title) return fromRexxar(j, id, kind);
      }
    } catch (err) {
      /* 继续走备选接口 */
    }
    if (kind === 'music') return null;
    try {
      const res2 = await upstream(`https://movie.douban.com/j/subject_abstract?subject_id=${id}`, { timeout: 8000 });
      if (res2.ok) {
        const j2 = await res2.json();
        if (j2 && j2.subject) return fromAbstract(j2.subject, id, kind);
      }
    } catch (err) {
      /* 忽略 */
    }
    return null;
  });
}

function scoreMatch(item, title, year) {
  const a = norm(item.title);
  const b = norm(title);
  let score = 0;
  let primary = 0;
  if (a && b) {
    if (a === b) primary = 60;              // 主名完全命中
    else if (a.includes(b) || b.includes(a)) primary = 30; // 主名包含（多为「XX 纪念版」这类再版）
    else if (a.startsWith(b.slice(0, 3))) primary = 5;
  }
  score += primary;
  if (item.original) {
    const o = norm(item.original);
    // 「原名」只是本名的扩展（如「七里香 (2023星外星版)」）时没有额外信息量，不计分
    const trivial = o === a || o.startsWith(a) || a.startsWith(o);
    // 主名没命中时，原名命中才值钱；主名只是包含时，原名命中只做小幅加分，避免再版压过原版
    if (o === b) score += primary === 60 ? 0 : primary ? 20 : 40;
    else if (!trivial && primary !== 60 && o && (o.includes(b) || b.includes(o))) score += 15;
  }
  if (year && item.year && String(item.year) === String(year)) score += 25;
  if (item.cover) score += 1;
  // 同分时优先选评价人数更多的条目（例如「七里香」应选经典原版而非冷门再版）
  if (item.rating && Number(item.rating.count)) {
    score += Math.min(12, Math.log10(Number(item.rating.count) + 1) * 2);
  } else if (item.rating && item.rating.value) score += 4;
  return score;
}

/* 单个类型的检索：搜索页优先，旧建议接口兜底，再退一步用分隔符后的片段重试 */
async function searchByKind(title, kind) {
  let items = (await doubanSearchPage(title, kind)) || [];
  if (!items.length) items = (await doubanSuggest(title, kind)) || [];
  if (!items.length) {
    const seg = title.split(/\s+[-–—]\s+/);
    const alt = seg.length > 1 ? seg[seg.length - 1].trim() : '';
    if (alt && alt !== title) items = (await doubanSearchPage(alt, kind)) || [];
  }
  return items;
}

async function handleLookup(params, res) {
  const title = String(params.get('title') || '').trim();
  const year = String(params.get('year') || '').trim();
  const rawKind = String(params.get('kind') || 'movie');
  const kind = rawKind === 'all' ? 'all' : rawKind === 'music' ? 'music' : 'movie';
  // kind=all 时，用 prefer 指定当前文件该优先匹配哪一类（音频→音乐，视频→影视）
  const prefer = String(params.get('prefer') || '') === 'music' ? 'music'
    : String(params.get('prefer') || '') === 'movie' ? 'movie' : '';
  if (!title) return sendJSON(res, { ok: false, error: '缺少标题' }, 400);

  let items = [];
  if (kind === 'all') {
    const [mv, mu] = await Promise.all([searchByKind(title, 'movie'), searchByKind(title, 'music')]);
    items = [...mv, ...mu];
  } else {
    items = await searchByKind(title, kind);
    if (!items.length && kind === 'music') {
      // 音乐站搜不到时，退回影视站（例如影视原声常被归到电影条目）
      items = await searchByKind(title, 'movie');
    }
  }

  const ranked = items
    .map((it) => ({ it, score: scoreMatch(it, title, year) }))
    .sort((x, y) => y.score - x.score)
    .map((x) => x.it);

  // 自动选中的条目：优先在 prefer 指定的类别里挑，否则取全局最高分
  const pool = prefer ? ranked.filter((it) => (prefer === 'music' ? it.kind === 'music' : it.kind !== 'music')) : ranked;
  const best = (pool[0] || ranked[0]);
  let subject = best ? await doubanSubject(best.id, best.kind) : null;
  if (!subject && best) subject = fromItemCard(best);

  const movieUrl = `https://search.douban.com/movie/subject_search?search_text=${encodeURIComponent(title)}`;
  const musicUrl = `https://search.douban.com/music/subject_search?search_text=${encodeURIComponent(title)}`;
  // 混合检索时两类各出 6 条，便于前端分组展示
  const outItems = kind === 'all'
    ? [
      ...ranked.filter((it) => it.kind !== 'music').slice(0, 6),
      ...ranked.filter((it) => it.kind === 'music').slice(0, 6),
    ]
    : ranked.slice(0, 10);

  sendJSON(res, {
    ok: true,
    query: { title, year, kind, prefer },
    items: outItems,
    subject,
    notice: items.length ? '' : lastSearchError,
    fallbackUrl: kind === 'music' ? musicUrl : movieUrl,
    fallbackMovieUrl: movieUrl,
    fallbackMusicUrl: musicUrl,
  });
}

async function handleSubject(params, res) {
  const id = String(params.get('id') || '').trim();
  const kind = String(params.get('kind') || 'movie');
  if (!/^\d+$/.test(id)) return sendJSON(res, { ok: false, error: '条目 ID 不合法' }, 400);
  let subject = await doubanSubject(id, kind);
  if (!subject && kind === 'tv') subject = await doubanSubject(id, 'movie');
  sendJSON(res, { ok: !!subject, subject });
}

/* --------------------------- 可配置抓取站点 --------------------------- */

function handleSitesList(res) {
  // 注意：这里返回的字段会原样回到前端，前端任何一次「启用切换/删除/编辑保存」都会把整个数组
  // 回写到 sources.json。所以新增字段必须同步加到这里，否则会被静默抹掉（歌曲宝的 audioUrl 曾因此失效）。
  const list = SITES.sites().map((s) => ({
    id: s.id, name: s.name, kind: s.kind, enabled: s.enabled !== false,
    lyric: !!s.lyric, builtin: !!s.builtin, parser: s.parser,
    searchUrl: s.searchUrl || '', detailUrl: s.detailUrl || '', lyricUrl: s.lyricUrl || '',
    linkPattern: s.linkPattern || '', base: s.base || '',
    audioUrl: s.audioUrl || '', audioMethod: s.audioMethod || '',
  }));
  sendJSON(res, { ok: true, sites: list });
}

// 站点回写保护：前端只认识表单里的字段，若整份覆盖会丢掉 sources.json 里的高级配置
// （audioUrl / audioMethod / builtin / parser 等）。这里以磁盘上的现有配置为基底做合并。
function mergeSiteConfig(incoming) {
  const existing = SITES.sites();
  const byId = new Map(existing.map((s) => [s.id, s]));
  return incoming.map((s) => {
    const prev = byId.get(s.id);
    if (!prev) return s;
    const merged = Object.assign({}, prev);
    Object.keys(s).forEach((k) => {
      // 空字符串视为「前端没填」，保留原值，避免误清
      if (s[k] === '' || s[k] == null) return;
      merged[k] = s[k];
    });
    return merged;
  });
}

async function handleSiteSearch(params, res) {
  const id = String(params.get('site') || '').trim();
  const q = String(params.get('q') || '').trim();
  const site = SITES.getSite(id);
  if (!site) return sendJSON(res, { ok: false, error: '站点不存在', items: [] }, 404);
  if (!q) return sendJSON(res, { ok: false, error: '缺少关键词', items: [] }, 400);

  if (site.parser === 'douban') {
    const kind = site.kind === 'music' ? 'music' : 'movie';
    const items = await searchByKind(q, kind);
    return sendJSON(res, {
      ok: true,
      items: items.slice(0, 20).map((it) => ({
        id: it.id, title: it.title, artist: (it.abstract || '').split(' / ')[0] || '',
        album: '', cover: it.cover || '', hasLyric: false, kind: it.kind, url: it.url,
      })),
    });
  }
  const r = await SITES.searchSite(site, q);
  sendJSON(res, Object.assign({ site: site.id, kind: site.kind }, r));
}

async function handleSiteDetail(params, res) {
  const id = String(params.get('site') || '').trim();
  const sid = String(params.get('id') || '').trim();
  const site = SITES.getSite(id);
  if (!site || !sid) return sendJSON(res, { ok: false, error: '参数不完整' }, 400);

  if (site.parser === 'douban') {
    const kind = site.kind === 'music' ? 'music' : 'movie';
    let subject = await doubanSubject(sid, kind);
    if (!subject && kind === 'movie') subject = await doubanSubject(sid, 'tv');
    return sendJSON(res, { ok: !!subject, subject, lyric: '', source: site.name });
  }
  const captchaCode = String(params.get('captcha') || '').trim();
  const captchaKey = String(params.get('captchaKey') || '').trim();
  const r = await SITES.detailSite(site, sid, captchaCode && captchaKey
    ? { captcha: { captchaCode, key: captchaKey } } : undefined);
  const info = r.info || {};
  const subject = {
    id: String(info.id || sid),
    kind: site.kind === 'music' ? 'music' : 'movie',
    title: info.title || '',
    originalTitle: '',
    year: (String(info.date || '').match(/(\d{4})/) || [])[1] || '',
    subtitle: [info.artist, info.album, info.date].filter(Boolean).join(' / '),
    rating: null,
    genres: [],
    duration: '',
    pubdate: info.date || '',
    region: '',
    languages: '',
    directors: [],
    actors: [],
    intro: '',
    aka: [],
    episodes: 0,
    cover: info.cover || '',
    url: info.url || '',
    singer: info.artist ? [info.artist] : [],
    publisher: [],
    tracks: 0,
    album: info.album || '',
    source: site.name,
  };
  sendJSON(res, {
    ok: true, subject, lyric: r.lyric || '', dead: !!info.dead, source: site.name,
    audio: info.audio || '',
    // 站点要求人机验证（不是解析失败），前端据此给不同提示
    verify: !!r.verify,
    // needCaptcha: 要弹验证码；captchaError: 用户输入的验证码不对（刷新重输）
    needCaptcha: !!r.needCaptcha,
    captchaError: !!r.captchaError,
  });
}

/* 取一张站点人机验证码（如歌曲宝的图片验证码），前端弹层展示 */
async function handleSiteCaptcha(params, res) {
  const id = String(params.get('site') || '').trim();
  const site = SITES.getSite(id);
  if (!site) return sendJSON(res, { ok: false, error: '站点不存在' }, 400);
  const r = await SITES.fetchCaptcha(site);
  sendJSON(res, r);
}

/* --------------------- 在线资源：直链 / 流式播放 / 后台下载 --------------------- */

// DOWNLOAD_DIR 由 runtime-paths 提供（exe 下指向 exe 同目录的 downloads/）
const downloadTasks = new Map();

function safeName(s, fallback) {
  const v = String(s || '').replace(/[\\/:*?"<>|\r\n\t]+/g, '_').replace(/\s+/g, ' ').trim();
  return (v || fallback || 'track').slice(0, 80);
}

async function ensureDownloadDir() {
  try { await fsp.mkdir(DOWNLOAD_DIR, { recursive: true }); } catch (err) { /* 忽略 */ }
}

/* 直链查询（一般不用，主要给调试） */
async function handleSiteAudio(params, res) {
  const id = String(params.get('site') || '').trim();
  const sid = String(params.get('id') || '').trim();
  const site = SITES.getSite(id);
  if (!site || !sid) return sendJSON(res, { ok: false, error: '参数不完整' }, 400);
  const url = await SITES.resolveAudio(site, sid);
  sendJSON(res, { ok: !!url, url });
}

/* 流式代理远端音频，支持 Range —— 让浏览器可以边下边播、随意拖动 */
async function handleSitePlay(params, req, res) {
  const id = String(params.get('site') || '').trim();
  const sid = String(params.get('id') || '').trim();
  const site = SITES.getSite(id);
  if (!site || !sid) {
    res.writeHead(400, { 'Content-Type': 'text/plain; charset=utf-8' });
    return res.end('参数不完整');
  }
  // fresh=1：前端发现播放失败后要求重新换一次直链（丢弃可能已过期的缓存）
  if (params.get('fresh') === '1') SITES.invalidateAudio(site, sid);

  const upstreamHeaders = { 'User-Agent': UA_DESKTOP, Accept: 'audio/*,*/*;q=0.8' };
  if (req.headers.range) upstreamHeaders.Range = req.headers.range;

  // CDN 直链带时效，缓存里的地址可能已经 403/410：失效就清缓存重换一次
  let lastStatus = 0;
  for (let attempt = 0; attempt < 2; attempt++) {
    const target = await SITES.resolveAudio(site, sid);
    if (!target) {
      res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' });
      return res.end('该站点没有可播放的音频资源');
    }
    // 注意：超时只用于「建立连接」，不能覆盖整个传输过程——否则播满超时的歌会把服务打崩
    let up = null;
    const ac = new AbortController();
    const timer = setTimeout(() => ac.abort(), 20000);
    try {
      up = await fetch(target, { headers: upstreamHeaders, signal: ac.signal });
    } catch (err) {
      up = null;
    } finally {
      clearTimeout(timer);
    }

    if (up && (up.ok || up.status === 206)) {
      const out = {
        'Content-Type': up.headers.get('content-type') || 'audio/mpeg',
        'Accept-Ranges': 'bytes',
        'Cache-Control': 'public, max-age=3600',
      };
      if (up.headers.get('content-length')) out['Content-Length'] = up.headers.get('content-length');
      if (up.headers.get('content-range')) out['Content-Range'] = up.headers.get('content-range');
      res.writeHead(up.status, out);
      if (req.method === 'HEAD') { try { up.body.cancel(); } catch (e) { /* 忽略 */ } return res.end(); }
      const src = Readable.fromWeb(up.body);
      src.on('error', () => { try { res.destroy(); } catch (e) { /* 忽略 */ } });
      res.on('close', () => { try { src.destroy(); } catch (e) { /* 忽略 */ } });
      src.pipe(res);
      return;
    }

    lastStatus = up ? up.status : 0;
    // 上游拒绝/没有响应体：丢弃这条直链，清缓存后再试一次
    if (up && up.body) { try { up.body.cancel(); } catch (e) { /* 忽略 */ } }
    SITES.invalidateAudio(site, sid);
  }

  res.writeHead(lastStatus ? 502 : 504, { 'Content-Type': 'text/plain; charset=utf-8' });
  res.end(lastStatus ? '站点资源已失效（上游 ' + lastStatus + '），请重新搜索该曲目' : '连接站点超时，请稍后重试');
}

/* 后台下载到 downloads/ 目录，前端轮询进度 */
async function handleSiteDownload(params, res) {
  const id = String(params.get('site') || '').trim();
  const sid = String(params.get('id') || '').trim();
  const site = SITES.getSite(id);
  if (!site || !sid) return sendJSON(res, { ok: false, error: '参数不完整' }, 400);

  const title = String(params.get('title') || '').trim();
  const artist = String(params.get('artist') || '').trim();
  const key = `${site.id}|${sid}`;
  for (const t of downloadTasks.values()) {
    if (t.key === key) return sendJSON(res, { ok: true, task: publicTask(t), reused: true });
  }

  const task = {
    id: 't' + Date.now().toString(36) + Math.random().toString(36).slice(2, 6),
    key, site: site.id, siteName: site.name,
    title: title || '未命名', artist,
    total: 0, received: 0, status: 'pending', error: '', file: '', name: '', startedAt: Date.now(),
  };
  downloadTasks.set(task.id, task);
  sendJSON(res, { ok: true, task: publicTask(task) });

  // 异步执行，先回响应
  runDownload(site, sid, task).catch((err) => {
    task.status = 'error';
    task.error = String(err && err.message || err);
  });
}

function publicTask(t) {
  const name = t.name || [t.artist, t.title].filter(Boolean).join(' - ') || t.key;
  return {
    id: t.id, key: t.key, site: t.site, siteName: t.siteName,
    title: t.title, artist: t.artist, cover: t.cover || '', name,
    total: t.total, received: t.received, status: t.status, error: t.error,
    file: t.file, percent: t.total ? Math.min(100, Math.round((t.received / t.total) * 100)) : (t.status === 'done' ? 100 : 0),
  };
}

async function runDownload(site, sid, task) {
  const target = await SITES.resolveAudio(site, sid);
  if (!target) {
    task.status = 'error';
    task.error = '没有可下载的音频资源';
    return;
  }
  await ensureDownloadDir();
  let ext = (path.extname(new URL(target).pathname) || '').toLowerCase();
  if (!/^\.(mp3|m4a|flac|wav|ogg|aac|wma|ape)$/.test(ext)) ext = '.mp3';
  const baseName = safeName([task.artist, task.title].filter(Boolean).join(' - '), task.key.replace(/\|/g, '_'));
  let file = path.join(DOWNLOAD_DIR, baseName + ext);
  let tmpPath = '';
  
  // 检查文件是否已存在
  try {
    await fsp.access(file);
    // 文件已存在，直接使用现有文件
    task.status = 'done';
    task.file = file;
    task.name = path.basename(file);
    task.received = (await fsp.stat(file)).size;
    task.total = task.received;
    return;
  } catch (err) {
    // 文件不存在，继续下载逻辑
    let n = 1;
    while (true) {
      try { await fsp.access(file); n++; file = path.join(DOWNLOAD_DIR, `${baseName} (${n})${ext}`); }
      catch (err) { break; }
    }
    // 注意：tmp 必须在函数作用域声明，写在 catch 块里外面取不到（原来就踩了这个坑，导致下载必失败）
    tmpPath = file + '.part';
    task.file = file;
    task.name = path.basename(file);
  }

  // 超时同样只作用于建立连接阶段，大文件慢慢下不能被中断
  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(), 30000);
  let up;
  try {
    up = await fetch(target, { headers: { 'User-Agent': UA_DESKTOP, Accept: '*/*' }, signal: ac.signal });
  } finally {
    clearTimeout(timer);
  }
  if (!up.ok) {
    task.status = 'error';
    task.error = '下载失败：' + up.status;
    return;
  }
  task.total = Number(up.headers.get('content-length') || 0);
  task.status = 'downloading';

  const ws = fs.createWriteStream(tmpPath);
  const reader = up.body.getReader();
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      ws.write(Buffer.from(value));
      task.received += value.byteLength;
    }
  } finally {
    try { await reader.cancel(); } catch (err) { /* 忽略 */ }
    await new Promise((r) => ws.end(r));
  }
  await fsp.rename(tmpPath, file);
  task.status = 'done';
  task.received = task.total || task.received;
  
  // 歌词自动下载和保存
  if (site.lyric && task.title) {
    try {
      // 获取歌词
      let lyricUrl = site.lyricUrl || site.detailUrl;
      if (lyricUrl && /\{id\}/.test(lyricUrl)) {
        lyricUrl = lyricUrl.replace(/{id}/g, sid);
      }
      
      if (lyricUrl) {
        const lyricRes = await upstream(lyricUrl, { timeout: 8000 });
        if (lyricRes.ok) {
          const lyricText = await lyricRes.text();
          if (lyricText && lyricText.length > 10) {
            // 保存歌词文件
            const lrcPath = file.replace(/\.[^/.]+$/, '.lrc');
            await fsp.writeFile(lrcPath, lyricText, 'utf8');
            task.lyric = lrcPath;
          }
        }
      }
    } catch (err) {
      console.error('歌词下载失败:', err);
    }
  }
}

function handleDownloadTasks(res) {
  sendJSON(res, { ok: true, tasks: [...downloadTasks.values()].map(publicTask).reverse() });
}

/* 在文件管理器里定位已下载的文件 */
async function handleOpenFile(params, res) {
  const p = String(params.get('path') || '').trim();
  if (!p) return sendJSON(res, { ok: false, error: '缺少路径' }, 400);
  const full = path.resolve(p);
  try {
    await fsp.access(full);
  } catch (err) {
    return sendJSON(res, { ok: false, error: '文件不存在' }, 404);
  }
  try {
    if (process.platform === 'win32') spawn('explorer', ['/select,', full], { detached: true, stdio: 'ignore' });
    else if (process.platform === 'darwin') spawn('open', ['-R', full], { detached: true, stdio: 'ignore' });
    else spawn('xdg-open', [path.dirname(full)], { detached: true, stdio: 'ignore' });
    sendJSON(res, { ok: true, path: full });
  } catch (err) {
    sendJSON(res, { ok: false, error: String(err.message || err) });
  }
}

async function handleTaskRemove(params, res) {
  const id = String(params.get('id') || '').trim();
  const t = downloadTasks.get(id);
  if (!t) return sendJSON(res, { ok: false, error: '任务不存在' }, 404);
  downloadTasks.delete(id);
  sendJSON(res, { ok: true });
}

/* 在文件管理器里定位已下载的文件 */
async function handleOpenFile(params, res) {
  const target = String(params.get('path') || '').trim();
  if (!target) return sendJSON(res, { ok: false, error: '缺少路径' }, 400);
  try {
    await fsp.access(target);
  } catch (err) {
    return sendJSON(res, { ok: false, error: '文件不存在' }, 404);
  }
  const abs = path.resolve(target);
  try {
    if (process.platform === 'win32') spawn('explorer', ['/select,', abs], { detached: true, stdio: 'ignore' }).unref();
    else if (process.platform === 'darwin') spawn('open', ['-R', abs], { detached: true, stdio: 'ignore' }).unref();
    else spawn('xdg-open', [path.dirname(abs)], { detached: true, stdio: 'ignore' }).unref();
    sendJSON(res, { ok: true, path: abs });
  } catch (err) {
    sendJSON(res, { ok: false, error: String(err.message || err) });
  }
}

/* ------------------------------ 本地歌词 ------------------------------ */

function lrcCandidates(file) {
  const dir = path.dirname(file);
  const base = path.basename(file, path.extname(file));
  const list = [path.join(dir, base + '.lrc')];
  // 「艺人 - 标题」这类文件名，也试一下标题单独命名的歌词
  const m = base.split(/\s+[-–—]\s+/);
  if (m.length > 1) list.push(path.join(dir, m[m.length - 1] + '.lrc'));
  list.push(path.join(dir, 'lrc', base + '.lrc'));
  return list;
}

async function handleLrcLocal(params, res) {
  const file = String(params.get('path') || '').trim();
  if (!file) return sendJSON(res, { ok: false, lyric: '' });
  let stat = null;
  try { stat = fs.statSync(file); } catch (err) { /* 忽略 */ }
  if (!stat) return sendJSON(res, { ok: false, lyric: '', error: '文件不存在' });

  for (const c of lrcCandidates(file)) {
    try {
      if (fs.statSync(c).isFile()) {
        return sendJSON(res, { ok: true, lyric: fs.readFileSync(c, 'utf8'), path: c });
      }
    } catch (err) { /* 继续找 */ }
  }
  // 目录内模糊匹配：歌词文件名与媒体名互相包含
  const dir = path.dirname(file);
  const base = path.basename(file, path.extname(file)).toLowerCase();
  try {
    const files = fs.readdirSync(dir).filter((f) => /\.lrc$/i.test(f));
    for (const f of files.slice(0, 400)) {
      const nb = path.basename(f, path.extname(f)).toLowerCase();
      if (nb && (base.includes(nb) || nb.includes(base))) {
        return sendJSON(res, { ok: true, lyric: fs.readFileSync(path.join(dir, f), 'utf8'), path: path.join(dir, f) });
      }
    }
  } catch (err) { /* 忽略 */ }
  sendJSON(res, { ok: false, lyric: '' });
}

async function handleLrcSave(body, res) {
  const file = String(body.path || '').trim();
  const content = String(body.content || '');
  if (!file || !content) return sendJSON(res, { ok: false, error: '参数不完整' }, 400);
  try {
    if (!fs.statSync(file).isFile()) return sendJSON(res, { ok: false, error: '媒体文件不存在' }, 400);
  } catch (err) {
    return sendJSON(res, { ok: false, error: '媒体文件不存在' }, 400);
  }
  const target = lrcCandidates(file)[0];
  try {
    fs.writeFileSync(target, content, 'utf8');
    sendJSON(res, { ok: true, path: target });
  } catch (err) {
    sendJSON(res, { ok: false, error: '写入失败：' + err.message }, 500);
  }
}

/* ------------------------------- 图片代理 -------------------------------- */

const PLACEHOLDER_SVG = Buffer.from(
  '<svg xmlns="http://www.w3.org/2000/svg" width="240" height="240" viewBox="0 0 240 240">'
  + '<rect width="240" height="240" fill="#1c2430"/>'
  + '<circle cx="120" cy="104" r="52" fill="none" stroke="rgba(255,255,255,.22)" stroke-width="6"/>'
  + '<circle cx="120" cy="104" r="16" fill="rgba(255,255,255,.22)"/>'
  + '<rect x="72" y="182" width="96" height="8" rx="4" fill="rgba(255,255,255,.16)"/>'
  + '</svg>', 'utf8'
);

async function handleImage(params, res) {
  const target = String(params.get('url') || '');
  let u;
  try {
    u = new URL(target);
  } catch (err) {
    return sendJSON(res, { ok: false, error: '图片地址不合法' }, 400);
  }
  // 只代理 https 图片：豆瓣图床之外，也放行第三方站点（如音乐站封面）的 CDN
  if (u.protocol !== 'https:') {
    return sendJSON(res, { ok: false, error: '仅允许代理 https 图片' }, 400);
  }
  const referer = /doubanio\.com$/.test(u.hostname) ? 'https://movie.douban.com/' : `${u.protocol}//${u.hostname}/`;
  // 豆瓣图床偶发超时/限流，重试一次；仍失败则返回占位图，避免界面破图
  let upstreamRes = null;
  for (let i = 0; i < 2 && !upstreamRes; i++) {
    try {
      upstreamRes = await fetch(u.href, {
        headers: { 'User-Agent': UA_DESKTOP, Referer: referer, Accept: 'image/avif,image/webp,image/*,*/*' },
        signal: AbortSignal.timeout(12000),
      });
      if (!upstreamRes.ok) upstreamRes = null;
    } catch (err) {
      upstreamRes = null;
      if (i === 0) await new Promise((r) => setTimeout(r, 800));
    }
  }
  const ctype = upstreamRes ? (upstreamRes.headers.get('content-type') || '') : '';
  if (!upstreamRes || !upstreamRes.body || (ctype && !/^image\//.test(ctype))) {
    res.writeHead(200, { 'Content-Type': 'image/svg+xml', 'Cache-Control': 'no-store' });
    return res.end(PLACEHOLDER_SVG);
  }
  res.writeHead(200, {
    'Content-Type': ctype || 'image/jpeg',
    'Cache-Control': 'public, max-age=86400',
  });
  const { Readable } = require('node:stream');
  const src = Readable.fromWeb(upstreamRes.body);
  src.on('error', () => { try { res.destroy(); } catch (e) { /* 忽略 */ } });
  src.pipe(res);
}

/* ------------------------------- 媒体库 ---------------------------------- */

function defaultMediaDir() {
  if (DEFAULT_MEDIA_DIR) return DEFAULT_MEDIA_DIR;
  const home = os.homedir();
  for (const name of ['Videos', 'Music', 'Documents']) {
    const p = path.join(home, name);
    if (fs.existsSync(p)) return p;
  }
  return home;
}

async function handleBrowse(params, res) {
  const raw = String(params.get('dir') || '').trim();
  // @downloads 是「资源导入」用的快捷方式：直接列下载目录，前端不必知道绝对路径
  let dir = raw === '@downloads' ? DOWNLOAD_DIR : (raw || defaultMediaDir());
  dir = path.resolve(dir);
  let stat;
  try {
    stat = await fsp.stat(dir);
  } catch (err) {
    return sendJSON(res, { ok: false, error: '目录不存在', dir });
  }
  if (stat.isFile()) dir = path.dirname(dir);

  try {
    const entries = await fsp.readdir(dir, { withFileTypes: true });
    const files = [];
    const dirs = [];
    for (const e of entries) {
      if (e.name.startsWith('.')) continue;
      const full = path.join(dir, e.name);
      if (e.isDirectory()) {
        dirs.push({ name: e.name, path: full });
        continue;
      }
      const ext = path.extname(e.name).toLowerCase();
      if (!MEDIA_EXT.has(ext)) continue;
      try {
        const s = await fsp.stat(full);
        files.push({ name: e.name, path: full, size: s.size, ext: ext.slice(1), mtime: s.mtimeMs });
      } catch (err) {
        /* 忽略不可访问的文件 */
      }
    }
    const byName = (a, b) => a.name.localeCompare(b.name, 'zh-Hans-CN', { numeric: true });
    dirs.sort(byName);
    files.sort(byName);
    sendJSON(res, { ok: true, dir, parent: path.dirname(dir), dirs, files });
  } catch (err) {
    sendJSON(res, { ok: false, error: String(err.message || err) });
  }
}

async function handleStream(params, req, res) {
  const file = String(params.get('file') || '');
  if (!file) {
    res.writeHead(400, { 'Content-Type': 'text/plain; charset=utf-8' });
    return res.end('缺少 file 参数');
  }
  const full = path.resolve(file);
  let stat;
  try {
    stat = await fsp.stat(full);
  } catch (err) {
    res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' });
    return res.end('文件不存在');
  }
  if (!stat.isFile()) {
    res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' });
    return res.end('不是文件');
  }

  const type = STREAM_MIME[path.extname(full).toLowerCase()] || 'application/octet-stream';
  const total = stat.size;
  const range = req.headers.range;
  // download=1：给「存到本机」用，浏览器会弹出保存而不是内联播放
  const dl = /\b1\b/.test(String(params.get('download') || ''));
  const dlHeaders = dl
    ? { 'Content-Disposition': `attachment; filename*=UTF-8''${encodeURIComponent(path.basename(full))}` }
    : {};

  if (range) {
    const m = /bytes=(\d*)-(\d*)/.exec(range);
    if (m) {
      let start = m[1] ? Number(m[1]) : 0;
      let end = m[2] ? Number(m[2]) : total - 1;
      if (Number.isNaN(start) || start < 0) start = 0;
      if (Number.isNaN(end) || end > total - 1) end = total - 1;
      if (start > end) {
        res.writeHead(416, { 'Content-Range': `bytes */${total}` });
        return res.end();
      }
      res.writeHead(206, {
        'Content-Type': type,
        'Content-Length': end - start + 1,
        'Accept-Ranges': 'bytes',
        'Content-Range': `bytes ${start}-${end}/${total}`,
        'Cache-Control': 'no-store',
        ...dlHeaders,
      });
      fs.createReadStream(full, { start, end }).on('error', () => res.end()).pipe(res);
      return;
    }
  }

  res.writeHead(200, {
    'Content-Type': type,
    'Content-Length': total,
    'Accept-Ranges': 'bytes',
    'Cache-Control': 'no-store',
    ...dlHeaders,
  });
  fs.createReadStream(full).on('error', () => res.end()).pipe(res);
}

/* ------------------------------- 静态资源 -------------------------------- */

// 单文件 exe：public/ 已内嵌，磁盘上没有该文件时直接从 SEA assets 读
function embeddedAsset(rel) {
  if (!isSea()) return null;
  try {
    const key = 'public/' + rel.replace(/^[/\\]+/, '').replace(/\\/g, '/');
    return Buffer.from(require('node:sea').getAsset(key));
  } catch (err) { return null; }
}

function serveStatic(req, res, pathname) {
  let rel = decodeURIComponent(pathname);
  if (rel === '/' || rel === '') rel = '/index.html';
  const filePath = path.join(PUBLIC_DIR, path.normalize(rel).replace(/^([/\\])+/, ''));
  if (!filePath.startsWith(PUBLIC_DIR)) {
    res.writeHead(403, { 'Content-Type': 'text/plain; charset=utf-8' });
    return res.end('403');
  }
  fs.stat(filePath, (err, stat) => {
    if (err || !stat.isFile()) {
      const buf = embeddedAsset(rel);
      if (buf) {
        const type = STATIC_MIME[path.extname(rel).toLowerCase()] || 'application/octet-stream';
        res.writeHead(200, { 'Content-Type': type, 'Content-Length': buf.length, 'Cache-Control': 'no-cache' });
        return res.end(buf);
      }
      res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' });
      return res.end('404 Not Found');
    }
    const type = STATIC_MIME[path.extname(filePath).toLowerCase()] || 'application/octet-stream';
    res.writeHead(200, { 'Content-Type': type, 'Content-Length': stat.size, 'Cache-Control': 'no-cache' });
    fs.createReadStream(filePath).on('error', () => res.end()).pipe(res);
  });
}

/* --------------------------------- 路由 ---------------------------------- */

/* 资源导入：把本地文件流式写入下载目录（作为缓存，之后可从「已下载」里加入播放列表）
   用 application/octet-stream 直传，避免解析 multipart 的开销和依赖 */
async function handleImport(params, req, res) {
  const raw = String(params.get('name') || '').trim();
  const base = path.basename(raw).replace(/[\\/:*?"<>|\x00-\x1f]/g, '_').trim();
  if (!base) return sendJSON(res, { ok: false, error: '缺少文件名' }, 400);
  let file = '';
  try {
    await fsp.mkdir(DOWNLOAD_DIR, { recursive: true });
    const ext = path.extname(base);
    const stem = base.slice(0, base.length - ext.length) || 'import';
    file = path.join(DOWNLOAD_DIR, base);
    // 重名就加序号，不覆盖已有文件
    for (let n = 1; ; n++) {
      try {
        await fsp.access(file);
        file = path.join(DOWNLOAD_DIR, `${stem} (${n})${ext}`);
      } catch (err) { break; }
    }
    let received = 0;
    req.on('data', (c) => { received += c.length; });
    await new Promise((resolve, reject) => {
      const ws = fs.createWriteStream(file);
      req.pipe(ws);
      ws.on('finish', resolve);
      ws.on('error', reject);
      req.on('error', reject);
      req.on('aborted', () => reject(new Error('上传中断')));
    });
    if (!received) {
      try { await fsp.unlink(file); } catch (err) { /* 忽略 */ }
      return sendJSON(res, { ok: false, error: '没有收到文件内容' }, 400);
    }
    sendJSON(res, { ok: true, file, name: path.basename(file), size: received });
  } catch (err) {
    try { if (file) await fsp.unlink(file); } catch (e) { /* 忽略 */ }
    sendJSON(res, { ok: false, error: '导入失败：' + err.message }, 500);
  }
}

function readBody(req, limit = 1024 * 1024) {  return new Promise((resolve, reject) => {
    let data = '';
    req.on('data', (c) => {
      data += c;
      if (data.length > limit) { reject(new Error('请求体过大')); req.destroy(); }
    });
    req.on('end', () => {
      try { resolve(data ? JSON.parse(data) : {}); } catch (err) { reject(new Error('JSON 解析失败')); }
    });
    req.on('error', reject);
  });
}

const server = http.createServer(async (req, res) => {
  const reqUrl = new URL(req.url, `http://${req.headers.host || 'localhost'}`);
  const pathname = reqUrl.pathname;
  const isApi = pathname.startsWith('/api/');

  if (req.method !== 'GET' && req.method !== 'HEAD' && !(isApi && req.method === 'POST')) {
    res.writeHead(405, { 'Content-Type': 'text/plain; charset=utf-8' });
    return res.end('405');
  }

  try {
    if (pathname === '/api/douban/lookup') return await handleLookup(reqUrl.searchParams, res);
    if (pathname === '/api/douban/subject') return await handleSubject(reqUrl.searchParams, res);

    // 可配置抓取站点
    if (pathname === '/api/sites') return handleSitesList(res);
    if (pathname === '/api/sites/save' && req.method === 'POST') {
      const body = await readBody(req);
      if (!Array.isArray(body.sites)) return sendJSON(res, { ok: false, error: 'sites 必须是数组' }, 400);
      SITES.saveConfig({ sites: mergeSiteConfig(body.sites) });
      return handleSitesList(res);
    }
    if (pathname === '/api/site/search') return await handleSiteSearch(reqUrl.searchParams, res);
    if (pathname === '/api/site/detail') return await handleSiteDetail(reqUrl.searchParams, res);
    if (pathname === '/api/site/captcha') return await handleSiteCaptcha(reqUrl.searchParams, res);
    if (pathname === '/api/site/audio') return await handleSiteAudio(reqUrl.searchParams, res);
    // 服务端流式代理：解析站点直链后在服务端转发给浏览器，前端只请求本机 /api/*
    // 命中 downloads/ 缓存时前端会改播 /api/stream，不会走到这里
    if (pathname === '/api/site/play') return await handleSitePlay(reqUrl.searchParams, req, res);
    if (pathname === '/api/site/download') return await handleSiteDownload(reqUrl.searchParams, res);
    if (pathname === '/api/site/tasks') return handleDownloadTasks(res);
    if (pathname === '/api/site/task/remove') return await handleTaskRemove(reqUrl.searchParams, res);
    if (pathname === '/api/import' && req.method === 'POST') return await handleImport(reqUrl.searchParams, req, res);
    if (pathname === '/api/open') return await handleOpenFile(reqUrl.searchParams, res);

    // 本地歌词
    if (pathname === '/api/lrc/local') return await handleLrcLocal(reqUrl.searchParams, res);
    if (pathname === '/api/lrc/save' && req.method === 'POST') {
      return await handleLrcSave(await readBody(req), res);
    }

    if (pathname === '/api/img') return await handleImage(reqUrl.searchParams, res);
    if (pathname === '/api/browse') return await handleBrowse(reqUrl.searchParams, res);
    if (pathname === '/api/stream') return await handleStream(reqUrl.searchParams, req, res);
    if (pathname === '/favicon.ico') {
      res.writeHead(204);
      return res.end();
    }
    return serveStatic(req, res, pathname);
  } catch (err) {
    console.error('[error]', pathname, err.message);
    res.writeHead(500, { 'Content-Type': 'text/plain; charset=utf-8' });
    res.end('500 服务器内部错误');
  }
});

// 兜底：任何未捕获异常都只记录、不让整个播放服务挂掉
process.on('uncaughtException', (err) => {
  console.error('[uncaught]', err && err.message || err);
});
process.on('unhandledRejection', (err) => {
  console.error('[unhandled]', err && err.message || err);
});

// 首次运行：按配置先建好目录，再释放可编辑的配置文件（exe 下用内嵌默认值）
// 每一步独立容错，避免某一步失败（如父目录不存在）连累后续步骤
function releaseFile(file, content, label) {
  try {
    if (fs.existsSync(file)) return;
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, content, 'utf8');
  } catch (err) {
    console.error('[warn] 无法写入' + label + '：' + err.message);
  }
}
try { fs.mkdirSync(APP_DIR, { recursive: true }); } catch (err) { /* 忽略 */ }
try { fs.mkdirSync(DOWNLOAD_DIR, { recursive: true }); }
catch (err) { console.error('[warn] 无法创建下载目录：' + err.message); }
releaseFile(CONFIG_FILE, JSON.stringify(config, null, 2) + '\n', ' config.json');
if (isSea()) {
  let embedded = '';
  try { embedded = require('node:sea').getAsset('sources.json', 'utf8'); } catch (err) { /* 忽略 */ }
  if (embedded) releaseFile(CONFIG_PATH, embedded, '站点配置');
}

let listenPort = PORT;
let portRetries = 0;
server.on('error', (err) => {
  if (err.code === 'EADDRINUSE' && portRetries < 8) {
    portRetries++;
    console.log(`  端口 ${listenPort} 已被占用，尝试 ${listenPort + 1} …`);
    listenPort++;
    setTimeout(() => server.listen(listenPort, HOST), 120);
    return;
  }
  console.error('启动失败：' + err.message);
  process.exitCode = 1;
});

server.listen(listenPort, HOST, () => {
  console.log('');
  console.log('  影音播放器已启动');
  console.log(`  地址: http://${HOST}:${listenPort}`);
  console.log(`  本机浏览器: http://localhost:${listenPort}`);
  const lan = Object.values(os.networkInterfaces()).flat()
    .find((x) => x && x.family === 'IPv4' && !x.internal);
  if (lan) console.log(`  手机（同一 WiFi）: http://${lan.address}:${listenPort}`);
  console.log(`  配置文件: ${CONFIG_FILE}`);
  console.log(`  数据目录: ${APP_DIR}`);
  console.log(`  媒体库默认目录: ${defaultMediaDir()}`);
  console.log('  停止: Ctrl + C');
  console.log('');
});

process.on('SIGINT', () => {
  console.log('\n已停止');
  process.exit(0);
});
