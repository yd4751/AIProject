'use strict';
/**
 * 独立鉴权网关（不改现有 server.js）
 *
 *   浏览器 / 小程序 ──▶ 本网关（微信白名单鉴权）──▶ 多个后端服务（按路径/域名转发）
 *
 * 能力：
 *   1) 多转发：config.json 的 upstreams 数组，按最长路径前缀 + 可选 Host 选路，支持路径重写；
 *      管理端可在线增改删 / 启停，写回 config.json 并实时生效（手工改文件也热加载）
 *   2) 微信白名单：whitelist.json 热加载；只走微信登录（个人主体小程序码 / 开放平台网站应用 / 服务号），
 *      不提供固定口令等任何绕过通道；非白名单用户明确提示「无权访问」并回显 openid
 *   3) 授权记录：records.json 记录成功与失败（含 openid / unionid / 错误码 / IP / UA），
 *      失败同时写入 whitelist.pending 并打印控制台，便于手动加入白名单
 *   4) 管理端：config.json 可配置管理员账号（自动生成随机初始密码、明文自动转 scrypt 哈希），
 *      独立会话，可查看授权记录、放行 / 停用 / 删除白名单用户、在线管理转发服务
 *   5) 授权通过后统一进入服务列表页 /__auth/apps，列表来自实时路由表，点击卡片跳转到对应服务
 *
 * 启动：node auth-gateway/gateway.js
 */
const http = require('node:http');
const net = require('node:net');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const QRCode = require('qrcode');
const LOGIN_PAGE = require('./login-page');
const DEVICE_LOGIN_PAGE = require('./device-login-page');
const ADMIN_PAGE = require('./admin-page');
const ADMIN_PAGE_FILE = path.join(__dirname, 'admin-page.js');
/* 源码运行时每次请求重新读取管理端页面：改完 admin-page.js 刷新浏览器即生效，不用重启网关。
   单文件 exe 内没有独立文件，自动回退到打包内嵌版本。 */
function adminPageHTML() {
  try {
    if (fs.existsSync(ADMIN_PAGE_FILE)) {
      const src = fs.readFileSync(ADMIN_PAGE_FILE, 'utf8');
      if (src.includes('module.exports')) {
        const mod = { exports: {} };
        new Function('module', 'exports', src)(mod, mod.exports);
        if (typeof mod.exports === 'string' && mod.exports.length > 1000) return mod.exports;
      }
    }
  } catch (err) { /* 读取 / 解析失败则用内嵌版本 */ }
  return ADMIN_PAGE;
}

const PREFIX = '/__auth/';
const COOKIE = 'mp_gate';
const ADMIN_COOKIE = 'mp_admin';
// 记住「当前正在访问哪个子路径服务」，用于兜底转发后端页面里写死的 /assets、/favicon.ico 等绝对请求
const STICKY_COOKIE = 'gw_svc';
const REWRITE_MAX = 8 * 1024 * 1024;   // 参与路径改写的 HTML/CSS 体积上限
const TICKET_TTL = 5 * 60 * 1000;      // 二维码 5 分钟有效
const SESSION_MAX = 30 * 24 * 3600;    // 会话最长 30 天
const RECORD_MAX = 500;                // 授权记录最多保留 500 条

/* --------------------------------- 配置 ---------------------------------- */

function isSea() {
  try { return !!require('node:sea').isSea(); } catch (err) { return false; }
}
const ROOT = isSea() ? path.dirname(process.execPath) : __dirname;
// exe 单独用 gateway-config.json，避免与同目录的 media-player.exe 共用（并覆盖）服务端 config.json
const CONFIG_FILE = path.join(ROOT, isSea() ? 'gateway-config.json' : 'config.json');

function embeddedBuffer(key) {
  if (!isSea()) return null;
  try {
    const a = require('node:sea').getAsset(key);
    return Buffer.isBuffer(a) ? a : Buffer.from(a);
  } catch (err) { return null; }
}
function embeddedJSON(key) {
  if (!isSea()) return null;
  try { return JSON.parse(require('node:sea').getAsset(key, 'utf8')); } catch (err) { return null; }
}
function readJSON(file) {
  try {
    const j = JSON.parse(fs.readFileSync(file, 'utf8'));
    return (j && typeof j === 'object' && !Array.isArray(j)) ? j : null;
  } catch (err) { return null; }
}

// SQLite 存储层：除「启动端口 / 监听地址」外的所有数据（转发服务、管理员、白名单、设备、授权记录、其余配置项）全部入库。
const Store = require('./store');
Store.init(ROOT);
const DB_FILE = Store.dbPath();
const IN_MEMORY_DB = Store.isInMemory();

// IP 归属地：离线库（ip2region 压缩而来，约 2MB），优先用 exe 内嵌资源，其次同目录 ipdb.bin。
// 加载失败不影响网关运行，只是记录里的「地区」显示为空。
const iploc = require('./iploc');
const IPDB_FILE = path.join(ROOT, 'ipdb.bin');
const IPDB_EMBED = embeddedBuffer('ipdb.bin');
if (!iploc.load(IPDB_EMBED || IPDB_FILE)) {
  console.log('[warn] 未找到 IP 归属地库 ipdb.bin，访问记录中的「地区」将为空（其余功能不受影响）');
}

const DEFAULTS = {
  port: 5173, host: '0.0.0.0',
  upstream: { host: '127.0.0.1', port: 6001 },
  upstreams: [],
  // loginMode=device 时完全屏蔽微信登录（微信代码保留，改回 wechat 即恢复）
  enabled: true, mode: 'auto', loginMode: 'wechat', publicBaseUrl: '', lanBaseUrl: '',
  whitelistFile: 'whitelist.json', recordFile: 'records.json',
  sessionHours: 168, adminSessionHours: 12,
  secret: '', allowTokenInQuery: true, corsOrigins: [], landing: '',
  // portal = 访问根路径显示服务列表（默认）；proxy = 直接转发到挂在 / 的后端
  rootMode: 'portal', admins: [],
  wechat: { mpAppId: '', mpAppSecret: '', openAppId: '', openAppSecret: '',
    miniAppId: '', miniAppSecret: '', miniPage: 'pages/gate/index', miniEnv: 'release',
    apiBase: 'https://api.weixin.qq.com', authorizeBase: 'https://open.weixin.qq.com' },
};

// 首次运行：把同目录旧的 gateway-config.json / whitelist.json / records.json（若有）迁进 SQLite；
// 其中 secret 一并迁入，已授权的设备机器码不会失效。仅端口 / 监听地址保留在 config.json。
const DISK_CFG = readJSON(CONFIG_FILE) || embeddedJSON('config.json') || {};
function legacyJSONFile(name) {
  const p = path.join(ROOT, name);
  try { return JSON.parse(fs.readFileSync(p, 'utf8')); } catch (err) { return null; }
}
const MIGRATED = Store.seedFrom({
  cfg: DISK_CFG,
  whitelist: legacyJSONFile('whitelist.json') || embeddedJSON('whitelist.json') || null,
  records: legacyJSONFile('records.json') || null,
});

// CFG：运行期内存配置。端口 / 监听地址来自 config.json；其余配置项来自 SQLite（迁移时已从旧配置导入）。
let CFG = Object.assign({}, DEFAULTS, Store.getConfig());
CFG.port = Number(DISK_CFG.port) > 0 ? Number(DISK_CFG.port) : (CFG.port || DEFAULTS.port);
CFG.host = DISK_CFG.host || CFG.host || DEFAULTS.host;
CFG.wechat = Object.assign({}, DEFAULTS.wechat, CFG.wechat || {});
CFG.corsOrigins = Array.isArray(CFG.corsOrigins) ? CFG.corsOrigins : [];
CFG.upstreams = [];                 // 运行期以 store 为准
CFG.secret = Store.getSecret() || '';

// 配置写回：端口 / 监听地址写 config.json；其余配置项写回 SQLite（kv 表）。
function persistConfig(patch) {
  try {
    const j = readJSON(CONFIG_FILE) || {};
    let jsonChanged = false;
    if (patch.port !== undefined) { CFG.port = Number(patch.port) > 0 ? Number(patch.port) : CFG.port; j.port = CFG.port; jsonChanged = true; }
    if (patch.host !== undefined) { CFG.host = String(patch.host || DEFAULTS.host); j.host = CFG.host; jsonChanged = true; }
    if (jsonChanged) fs.writeFileSync(CONFIG_FILE, JSON.stringify(j, null, 2) + '\n', 'utf8');
  } catch (err) { console.error('[warn] 无法写回 config.json：' + err.message); }
  const moved = {};
  for (const k of Object.keys(patch)) if (Store.MOVED_KEYS.includes(k)) moved[k] = patch[k];
  if (Object.keys(moved).length) { Object.assign(CFG, moved); Store.setConfig(moved); }
}
function randomPassword(n = 8) {
  const abc = '23456789abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ';
  let s = '';
  const b = crypto.randomBytes(n);
  for (let i = 0; i < n; i++) s += abc[b[i] % abc.length];
  return s;
}
function hashPassword(pw, salt) {
  return crypto.scryptSync(String(pw), salt, 32).toString('hex');
}

// 若 config.json 不存在（极少），写一个最小化文件，方便查看/修改端口与监听地址
if (!fs.existsSync(CONFIG_FILE)) {
  try {
    fs.writeFileSync(CONFIG_FILE, JSON.stringify({
      _note: '启动端口 / 监听地址在此修改；其余配置已存入同目录 gateway.db（管理端可在线修改）',
      port: CFG.port, host: CFG.host,
    }, null, 2) + '\n', 'utf8');
  } catch (err) { /* 忽略 */ }
}

const initialPasswords = MIGRATED.initialPasswords || [];   // 首次生成的随机管理员初始密码（仅打印一次）
const generated = [];                                       // 历史兼容占位，现已无运行期自动生成的配置项
const ADMINS = Store.listAdmins();                          // 管理员账号（来自 SQLite，改密码时同步库）

const LISTEN_PORT = Number(CFG.port) > 0 ? Number(CFG.port) : DEFAULTS.port;
// Chromium / undici 会直接拒绝这些「不安全端口」，配置了也打不开页面，启动时给个明确提示
const BAD_PORT_LIST = new Set([
  1, 7, 9, 11, 13, 15, 17, 19, 20, 21, 22, 23, 25, 37, 42, 43, 53, 69, 77, 79, 87, 95,
  101, 102, 103, 104, 109, 110, 111, 113, 115, 117, 119, 123, 135, 137, 139, 143, 161, 179,
  389, 427, 465, 512, 513, 514, 515, 526, 530, 531, 532, 540, 548, 554, 556, 563, 587, 601,
  636, 989, 990, 993, 995, 1719, 1720, 1723, 2049, 3659, 4045, 5060, 5061, 6000, 6566,
  6665, 6666, 6667, 6668, 6669, 6697, 10080,
]);
const BAD_PORT = BAD_PORT_LIST.has(LISTEN_PORT);
let SESSION_SEC = Math.min(SESSION_MAX, Math.max(600, Number(CFG.sessionHours || 168) * 3600));
let ADMIN_SESSION_SEC = Math.min(SESSION_MAX, Math.max(600, Number(CFG.adminSessionHours || 12) * 3600));
let AUTH_ON = CFG.enabled !== false;
let TOKEN_QUERY = CFG.allowTokenInQuery !== false;

function stripSlash(s) { return String(s || '').replace(/\/+$/, ''); }
function lanIP() {
  const list = Object.values(os.networkInterfaces()).flat();
  const x = list.find((i) => i && i.family === 'IPv4' && !i.internal);
  return x ? x.address : '';
}
function localBase() {
  if (CFG.lanBaseUrl) return stripSlash(CFG.lanBaseUrl);
  if (CFG.publicBaseUrl) return stripSlash(CFG.publicBaseUrl);
  return 'http://' + (lanIP() || '127.0.0.1') + ':' + LISTEN_PORT;
}

/** 登录方式：loginMode=device 走设备登录（微信入口全关）；否则按凭证自动选微信方式。 */
function decideMode() {
  const login = String(CFG.loginMode || 'wechat').toLowerCase();
  if (login === 'device' || login === 'machine') return 'device';
  const w = CFG.wechat;
  const has = (a, b) => !!(String(a || '').trim() && String(b || '').trim());
  const open = has(w.openAppId, w.openAppSecret) && !!CFG.publicBaseUrl;
  const mini = has(w.miniAppId, w.miniAppSecret);
  const mp = has(w.mpAppId, w.mpAppSecret) && !!CFG.publicBaseUrl;
  const want = String(CFG.mode || 'auto').toLowerCase();
  if (want === 'wechat-open' && open) return 'wechat-open';
  if (want === 'wechat-mini' && mini) return 'wechat-mini';
  if (want === 'wechat-mp' && mp) return 'wechat-mp';
  if (want !== 'auto') {
    console.log('  [warn] 指定的登录方式 ' + want + ' 缺少凭证或 publicBaseUrl，已按可用凭证自动选择');
  }
  // 个人开发者优先走小程序码：个人主体小程序 0 元、无需营业执照/公章/开发者认证，
  // 也不要求 publicBaseUrl；open（网站应用）与 mp（服务号）都对企业主体有资质要求。
  if (mini) return 'wechat-mini';
  if (open) return 'wechat-open';
  if (mp) return 'wechat-mp';
  return 'none';
}
let MODE, MODE_LABEL, WECHAT_OK, WECHAT_PATHS;
/** 根据当前 CFG 重新计算登录方式 / 会话时长；配置页改动后调用即可即时生效，无需重启 */
function recomputeMode() {
  MODE = decideMode();
  MODE_LABEL = {
    none: '未配置微信登录', device: '设备登录（机器码）',
    'wechat-mp': '微信服务号网页授权扫码',
    'wechat-open': '微信开放平台扫码登录', 'wechat-mini': '微信小程序码扫码',
  }[MODE];
  WECHAT_OK = MODE !== 'none' && MODE !== 'device';
  // 设备登录模式下这些微信入口一律关闭，避免绕过设备白名单直接拿 token
  WECHAT_PATHS = new Set(['/__auth/qr', '/__auth/wxacode', '/__auth/poll',
    '/__auth/wx/callback', '/__auth/mp-login']);
  SESSION_SEC = Math.min(SESSION_MAX, Math.max(600, Number(CFG.sessionHours || 168) * 3600));
  ADMIN_SESSION_SEC = Math.min(SESSION_MAX, Math.max(600, Number(CFG.adminSessionHours || 12) * 3600));
  // 这两个开关同样允许在「系统配置」页在线切换，改完即时生效
  AUTH_ON = CFG.enabled !== false;
  TOKEN_QUERY = CFG.allowTokenInQuery !== false;
}
recomputeMode();

/* ------------------------------ 多转发路由表 ------------------------------ */

function normPrefix(p) {
  let s = String(p || '/').trim();
  if (!s || s === '/') return '/';
  if (!s.startsWith('/')) s = '/' + s;
  return s.replace(/\/+$/, '');
}
function newRouteId() { return 'r_' + crypto.randomBytes(5).toString('hex'); }
function bool(v, def) {
  if (v === undefined || v === null || v === '') return def;
  return !(v === false || v === 0 || v === 'false' || v === '0' || v === 'no');
}
function hostList(v) {
  const arr = Array.isArray(v) ? v : String(v || '').split(/[,\s，]+/);
  return arr.map((h) => String(h).trim().toLowerCase()).filter(Boolean);
}
/** 转发服务来自 SQLite（store 为运行期真相）：每次读取并补 id */
function configRoutes() {
  const raw = Store.listRoutes();
  const list = raw.map((u) => {
    const o = Object.assign({}, u && typeof u === 'object' ? u : {});
    if (!o.id) o.id = newRouteId();
    o.hosts = hostList(o.hosts);
    return o;
  });
  CFG.upstreams = list;
  return list;
}
/**
 * 路由分两类：
 *  - 独立端口路由（listenPort > 0）：该端口上所有请求都以「原路径 + /」转发，零改写，服务以为自己就在根上
 *  - 主端口路径路由（listenPort = 0）：沿用 /demo、/media 这类路径前缀（老玩法）
 */
let PORT_ROUTES = new Map();
function normalizeUpstreams() {
  const all = configRoutes().map((u, i) => ({
    id: u.id,
    name: String(u.name || ('服务 ' + (i + 1))),
    listenPort: Number(u.listenPort) || 0,
    prefix: normPrefix(u.prefix),
    host: String(u.host || '127.0.0.1').trim(),
    port: Number(u.port) || 6001,
    stripPrefix: !!u.stripPrefix,
    hosts: hostList(u.hosts),
    enabled: u.enabled !== false,
  })).filter((u) => u.enabled);
  PORT_ROUTES = new Map();
  all.filter((u) => u.listenPort).forEach((u) => {
    // 独立端口上不保留任何前缀概念，等价于「服务默认根路径」
    PORT_ROUTES.set(u.listenPort, Object.assign({}, u, { prefix: '/', stripPrefix: false }));
  });
  return all.filter((u) => !u.listenPort)
    .sort((a, b) => b.prefix.length - a.prefix.length);   // 最长前缀优先
}
let UPSTREAMS = normalizeUpstreams();
let DEFAULT_UP = UPSTREAMS.find((u) => u.prefix === '/') || UPSTREAMS[UPSTREAMS.length - 1] || null;
/** 该对外端口上挂的服务（没有则 null） */
function routeForPort(port) {
  return PORT_ROUTES.get(Number(port)) || null;
}
/** 主端口之外的所有已分配端口 */
function assignedPorts() {
  return Array.from(PORT_ROUTES.keys());
}
/** 管理端在线改动后实时刷新内存路由表（无需重启） */
function refreshUpstreams() {
  UPSTREAMS = normalizeUpstreams();
  DEFAULT_UP = UPSTREAMS.find((u) => u.prefix === '/') || UPSTREAMS[UPSTREAMS.length - 1] || null;
}
/** 选路前同步最新路由表（来自 SQLite，开销极小） */
function syncRoutesFromDisk() { refreshUpstreams(); }
/** 校验管理端提交的单条转发服务 */
function validateRoute(body, existing) {
  const name = String(body.name || '').trim();
  const listenPort = body.listenPort === '' || body.listenPort === undefined || body.listenPort === null
    ? 0 : Number(body.listenPort);
  const prefix = listenPort ? '/' : normPrefix(body.prefix);   // 独立端口必为根路径
  const host = String(body.host || '').trim();
  const port = Number(body.port);
  if (!name) return { error: '服务名不能为空' };
  if (listenPort) {
    if (!Number.isInteger(listenPort) || listenPort < 1 || listenPort > 65535) {
      return { error: '对外端口需为 1-65535 的整数' };
    }
    if (listenPort === LISTEN_PORT) {
      return { error: '对外端口不能与网关主端口（' + LISTEN_PORT + '）相同' };
    }
    if (port === listenPort) return { error: '对外端口不能与后端端口相同' };
  }
  if (prefix !== '/' && (prefix.startsWith('/__auth') || prefix.includes('..')
    || !/^\/[A-Za-z0-9_\-/]*$/.test(prefix))) {
    return { error: '路径前缀不合法（/__auth 为网关保留路径）' };
  }
  if (!host) return { error: '目标主机不能为空' };
  if (!Number.isInteger(port) || port < 1 || port > 65535) return { error: '端口需为 1-65535 的整数' };
  const route = {
    id: String(body.id || (existing && existing.id) || newRouteId()),
    name, prefix, host, port, listenPort,
    stripPrefix: listenPort ? false : bool(body.stripPrefix, false),
    hosts: hostList(body.hosts),
    enabled: bool(body.enabled, true),
  };
  if (listenPort) {
    const clash = configRoutes().find((u) => u.id !== route.id && Number(u.listenPort) === listenPort);
    if (clash) return { error: '对外端口 ' + listenPort + ' 已被「' + String(clash.name || '') + '」占用' };
  } else {
    const dup = configRoutes().find((u) => u.id !== route.id && !Number(u.listenPort)
      && normPrefix(u.prefix) === prefix
      && !hostList(u.hosts).length && !route.hosts.length);
    if (dup) return { error: '已有相同路径前缀的通用服务：' + String(dup.name || dup.prefix) };
  }
  return { route };
}
function writeRoutes(list) {
  Store.replaceRoutes(list);            // 落库，重启后依然有效
  refreshUpstreams();                   // 内存立即生效，无需重启
  syncListeners();                      // 端口增删立即绑定 / 释放
  return list;
}
function pickUpstream(req, pathname) {
  syncRoutesFromDisk();
  const host = String(req.headers.host || '').split(':')[0].toLowerCase();
  let fallback = null, hostFallback = null;
  for (const u of UPSTREAMS) {
    if (u.hosts.length && !u.hosts.includes(host)) continue;
    if (u.prefix === '/') {
      // 绑定了 Host 的兜底路由优先于通用兜底路由
      if (u.hosts.length) { if (!hostFallback) hostFallback = u; }
      else if (!fallback) fallback = u;
      continue;
    }
    if (pathname === u.prefix || pathname.startsWith(u.prefix + '/')) return u;
  }
  return hostFallback || fallback || DEFAULT_UP;
}
function upstreamPath(reqUrl, u) {
  let p = reqUrl.pathname;
  // 只有真的带该前缀时才截断（兜底转发的 /assets/x 这类绝对请求本身没有前缀）
  if (u.stripPrefix && u.prefix !== '/' && (p === u.prefix || p.startsWith(u.prefix + '/'))) {
    p = p.slice(u.prefix.length) || '/';
    if (!p.startsWith('/')) p = '/' + p;
  }
  return p + (reqUrl.search || '');
}
/**
 * 子路径适配（后端零改动）：服务挂在 /media 下、但后端只认 /。
 * 返回改写基准（'' 表示该服务不需要改写）。
 */
function subpathOf(u) {
  return u && u.stripPrefix && u.prefix !== '/' ? u.prefix : '';
}
/** 只匹配「显式前缀」路由（不含挂在 / 的兜底路由） */
function matchByPrefix(req, pathname) {
  syncRoutesFromDisk();
  const host = String(req.headers.host || '').split(':')[0].toLowerCase();
  for (const u of UPSTREAMS) {
    if (u.hosts.length && !u.hosts.includes(host)) continue;
    if (u.prefix === '/') continue;
    if (pathname === u.prefix || pathname.startsWith(u.prefix + '/')) return u;
  }
  return null;
}
/** 读取「上次访问的子路径服务」，用于接管 /assets/xxx、/favicon.ico 这类绝对请求 */
function stickyRoute(req) {
  const raw = String(req.headers.cookie || '');
  const m = new RegExp('(?:^|;\\s*)' + STICKY_COOKIE + '=([^;]+)').exec(raw);
  if (!m) return null;
  const id = decodeURIComponent(m[1]);
  syncRoutesFromDisk();
  const u = UPSTREAMS.find((x) => x.id === id);
  return u && subpathOf(u) ? u : null;
}
/** 把绝对路径补上子路径前缀（已经是该前缀的保持原样） */
function withBase(val, base) {
  if (!base || !val || val[0] !== '/' || val[1] === '/') return val;
  if (val === base || val.startsWith(base + '/') || val.startsWith(base + '?')
    || val.startsWith(base + '#')) return val;
  return base + val;
}
/** 改写 HTML：注入 <base>（修相对路径）+ 改写写死的 /xxx（修绝对路径） */
function rewriteHTML(html, base) {
  let out = String(html);
  if (!/<base[\s>]/i.test(out)) {
    const tag = '<base href="' + base + '/">';
    if (/<head[^>]*>/i.test(out)) out = out.replace(/<head[^>]*>/i, (m) => m + tag);
    else if (/<html[^>]*>/i.test(out)) out = out.replace(/<html[^>]*>/i, (m) => m + tag);
    else out = tag + out;
  }
  // src="" / href="" / action="" / poster="" / data-src="" 等属性
  out = out.replace(/(\s(?:src|href|action|poster|data-src|data-href)=)(["'])([^"']*)(["'])/gi,
    (m, attr, q, val, q2) => attr + q + withBase(val.trim(), base) + q2);
  // srcset="a.jpg 1x, /b.jpg 2x"
  out = out.replace(/(\ssrcset=)(["'])([^"']*)(["'])/gi, (m, attr, q, val, q2) =>
    attr + q + val.replace(/(^|,\s*)\/(?!\/)/g, (mm, sep) => sep + base + '/') + q2);
  // 内联 style / <style> 里的 url(/xxx)
  out = out.replace(/url\(\s*(['"]?)(\/[^'")]*)(['"]?)\s*\)/gi,
    (m, q1, val, q2) => 'url(' + q1 + withBase(val, base) + q2 + ')');
  return out;
}
/** 改写 CSS 文件里的 url(/xxx) 与 @import "/xxx" */
function rewriteCSS(css, base) {
  return String(css)
    .replace(/url\(\s*(['"]?)(\/[^'")]*)(['"]?)\s*\)/gi,
      (m, q1, val, q2) => 'url(' + q1 + withBase(val, base) + q2 + ')')
    .replace(/(@import\s+(?:url\(\s*)?)(['"])(\/[^'"]*)(['"])/gi,
      (m, head, q1, val, q2) => head + q1 + withBase(val, base) + q2);
}
/** 给响应追加「当前服务」标记（不动后端自己的 Set-Cookie） */
function withStickyCookie(setCookie, routeId) {
  const list = (Array.isArray(setCookie) ? setCookie : (setCookie ? [setCookie] : []))
    .filter((c) => !String(c).trim().startsWith(STICKY_COOKIE + '='));
  list.push(STICKY_COOKIE + '=' + encodeURIComponent(routeId)
    + '; Path=/; SameSite=Lax; Max-Age=' + SESSION_MAX);
  return list;
}
function publicRoutes() {
  syncRoutesFromDisk();
  return configRoutes().filter((u) => u.enabled !== false).map((u) => {
    const lp = Number(u.listenPort) || 0;
    const p = lp ? '/' : normPrefix(u.prefix);
    return { id: u.id, name: String(u.name || '未命名服务'), prefix: p, listenPort: lp,
      url: p === '/' ? '/' : p + '/', hostBound: hostList(u.hosts).length > 0 };
  });
}
function landingUrl() {
  if (CFG.landing) return String(CFG.landing);
  return PREFIX + 'apps';   // 授权通过后统一进入「服务列表」，点击卡片跳转
}

/* --------------------------------- 工具 ---------------------------------- */

function sendJSON(res, data, status = 200, headers = {}) {
  const body = JSON.stringify(data);
  res.writeHead(status, Object.assign({
    'Content-Type': 'application/json; charset=utf-8',
    'Content-Length': Buffer.byteLength(body),
    'Cache-Control': 'no-store',
  }, headers));
  res.end(body);
}
function sendHTML(res, html, status = 200, headers = {}) {
  const body = Buffer.from(String(html));
  res.writeHead(status, Object.assign({
    'Content-Type': 'text/html; charset=utf-8',
    'Content-Length': body.length,
    'Cache-Control': 'no-store',
  }, headers));
  res.end(body);
}
function readBody(req, limit = 64 * 1024) {
  return new Promise((resolve, reject) => {
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
function corsHeaders(req) {
  const list = CFG.corsOrigins;
  if (!list.length) return {};
  const origin = String(req.headers.origin || '');
  const allow = list.includes('*') || (origin && list.includes(origin));
  if (!allow) return {};
  return {
    'Access-Control-Allow-Origin': list.includes('*') ? '*' : origin,
    'Access-Control-Allow-Headers': 'Authorization, Content-Type, X-Auth-Token, X-Admin-Token',
    'Access-Control-Allow-Methods': 'GET, POST, HEAD, OPTIONS',
    'Access-Control-Allow-Credentials': 'true',
    'Vary': 'Origin',
  };
}
function corsWrap(req, res) {
  const h = corsHeaders(req);
  if (!Object.keys(h).length) return;
  const orig = res.writeHead.bind(res);
  res.writeHead = function (code, headers) { return orig(code, Object.assign({}, headers || {}, h)); };
}
function escapeHTML(s) {
  return String(s || '').replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);
}
/** 取客户端真实 IP：仅当直连对端是内网/回环（说明前面有反向代理）时才采信转发头，
 *  避免网关直接暴露在公网时被伪造的 X-Forwarded-For 误导归属地。 */
function clientIP(req) {
  const raw = String(req.socket.remoteAddress || '');
  const direct = iploc.normalize(raw);
  const h = req.headers || {};
  const cand = String(h['cf-connecting-ip'] || h['x-real-ip']
    || String(h['x-forwarded-for'] || '').split(',')[0] || '').trim();
  if (cand && (direct.kind === 'local' || direct.kind === 'private')) {
    const viaHeader = iploc.normalize(cand);
    if (viaHeader.kind !== 'invalid' && viaHeader.kind !== 'empty' && viaHeader.ip) return viaHeader.ip;
  }
  return direct.ip || raw;
}
function reqInfo(req) {
  const ip = clientIP(req);
  return {
    ip,
    region: ip ? iploc.regionOf(ip) : '',
    ua: String(req.headers['user-agent'] || '').slice(0, 200),
  };
}

/* ------------------------------ 白名单（热加载） ------------------------------ */

let wlCache = null;
function whitelist() {
  if (!wlCache) {
    try {
      wlCache = {
        users: Store.listWlUsers(),
        pending: Store.listPending(),
        devices: Store.listDevices(),
        devicePending: Store.listDevicePending(),
      };
    } catch (err) {
      wlCache = { users: [], pending: [], devices: [], devicePending: [] };
    }
  }
  return wlCache;
}
function saveWhitelist(wl) {
  try {
    Store.saveWhitelistCollections(
      wl.users || [], wl.pending || [], wl.devices || [], wl.devicePending || []);
  } catch (err) { console.error('[warn] 写入白名单失败：' + err.message); }
  wlCache = null;   // 下次读取从 SQLite 重新载入
}
function matchUser(openid, unionid) {
  return whitelist().users.find((u) => {
    if (!u || u.enabled === false || !u.openid) return false;
    if (openid && u.openid === openid) return true;
    if (unionid && u.unionid && u.unionid === unionid) return true;
    if (unionid && u.openid === unionid) return true;   // 白名单里直接填 unionid 也认
    return false;
  }) || null;
}
/** 未授权用户记入 pending，管理员在管理端就能复制 openid 放行 */
function recordPending(openid, unionid, src) {
  if (!openid) return;
  try {
    const wl = whitelist();
    if (wl.users.some((u) => u && u.openid === openid)) return;
    const hit = wl.pending.find((p) => p && p.openid === openid);
    if (hit) { hit.at = new Date().toISOString(); hit.src = src; }
    else wl.pending.unshift({ openid, unionid: unionid || '', src, at: new Date().toISOString() });
    wl.pending = wl.pending.slice(0, 50);
    saveWhitelist(wl);
  } catch (err) { /* 忽略 */ }
}
function removePending(openid) {
  try {
    const wl = whitelist();
    const before = wl.pending.length;
    wl.pending = wl.pending.filter((p) => !p || p.openid !== openid);
    if (wl.pending.length !== before) saveWhitelist(wl);
  } catch (err) { /* 忽略 */ }
}

/* --------------------------- 设备登录 / 机器码 ---------------------------
   浏览器拿不到硬件序列号，所以机器码 = 持久设备 ID + 稳定设备指纹，
   在服务端用 CFG.secret 加盐 HMAC 生成：同一台设备每次算出来一致，
   别人只知道机器码也无法反推 ID/指纹来伪造。数据已存入 SQLite（gateway.db）。 */
const DEV_COOKIE = 'mp_dev';
function machineCode(id, fp) {
  const h = crypto.createHmac('sha256', String(CFG.secret))
    .update('device|' + String(id) + '|' + String(fp)).digest('hex').toUpperCase();
  return 'MC-' + (h.slice(0, 16).match(/.{4}/g) || []).join('-');
}
function deviceCookie(id) {
  return DEV_COOKIE + '=' + encodeURIComponent(id) + '; Path=/; SameSite=Lax; Max-Age=31536000';
}
function safeNext(v) {
  const s = String(v || '').trim();
  if (s && s.startsWith('/') && !s.startsWith('//') && !s.includes('\\')) return s;
  return landingUrl();
}
function matchDevice(code) {
  const c = String(code || '').trim().toUpperCase();
  if (!c) return null;
  return whitelist().devices.find((d) => d && d.enabled !== false && d.code === c) || null;
}
/** 未授权设备记入待授权列表；同一机器码 30 秒内只刷新一次，避免登录页轮询刷屏 */
function pendingDevice(code, meta) {
  try {
    const wl = whitelist();
    if (wl.devices.some((d) => d && d.code === code)) return;
    const hit = wl.devicePending.find((p) => p && p.code === code);
    const now = Date.now();
    if (hit) {
      if (now - (hit.ts || 0) < 30000) return;
      hit.at = new Date().toISOString(); hit.ts = now;
      Object.assign(hit, meta || {});
    } else {
      wl.devicePending.unshift(Object.assign({ code, at: new Date().toISOString(), ts: now }, meta || {}));
      wl.devicePending = wl.devicePending.slice(0, 50);
      console.log('[待授权设备] ' + code + ' ' + String((meta && meta.label) || '').slice(0, 80));
    }
    saveWhitelist(wl);
  } catch (err) { /* 只读目录时忽略 */ }
}
function touchDevice(code, meta) {
  try {
    const wl = whitelist();
    const d = wl.devices.find((x) => x && x.code === code);
    if (!d) return;
    d.lastSeen = new Date().toISOString();
    Object.assign(d, meta || {});
    saveWhitelist(wl);
  } catch (err) { /* 忽略 */ }
}
const devFailLog = new Map();
function shouldLogDevice(code) {
  const now = Date.now();
  if (now - (devFailLog.get(code) || 0) < 30000) return false;
  devFailLog.set(code, now);
  if (devFailLog.size > 500) devFailLog.clear();
  return true;
}

/* ------------------------------- 授权记录 -------------------------------- */

let recCache = null;
function readRecords() {
  if (!recCache) {
    try { recCache = Store.listRecords(); } catch (err) { recCache = []; }
  }
  return recCache;
}
/** 统一记录所有授权事件；失败时同步 pending 并打印控制台，方便手动加白名单 */
function addRecord(rec) {
  const ip = String(rec.ip || '');
  const e = {
    at: new Date().toISOString(),
    type: rec.type === 'success' ? 'success' : 'failure',
    action: String(rec.action || ''),
    src: String(rec.src || MODE),
    openid: String(rec.openid || ''),
    unionid: String(rec.unionid || ''),
    device: String(rec.device || ''),
    name: String(rec.name || ''),
    reason: String(rec.reason || ''),
    message: String(rec.message || ''),
    errcode: rec.errcode === undefined || rec.errcode === null ? '' : rec.errcode,
    errmsg: String(rec.errmsg || ''),
    ip,
    region: String(rec.region || '') || (ip ? iploc.regionOf(ip) : ''),
    ua: String(rec.ua || ''),
    ticket: String(rec.ticket || ''),
  };
  try {
    Store.insertRecord(e);
  } catch (err) { console.error('[warn] 无法写入授权记录：' + err.message); }
  recCache = null;   // 下次读取从 SQLite 重新载入（保证最新记录置顶）
  if (e.type === 'failure') {
    console.log('[授权失败] ' + e.at + ' ' + e.reason + ' openid=' + (e.openid || '-')
      + ' device=' + (e.device || '-') + ' src=' + e.src + ' ' + (e.message || e.errmsg || ''));
    if (e.openid && e.reason === 'not_whitelisted') recordPending(e.openid, e.unionid, e.src);
  } else {
    console.log('[授权成功] ' + e.at + ' ' + (e.name || e.openid || '-') + ' src=' + e.src);
  }
  return e;
}
/** 就地更新某条授权记录（放行未授权记录时使用，避免新增重复记录） */
function updateRecord(id, patch) {
  try { Store.updateRecord(id, patch); } catch (err) { console.error('[warn] 更新授权记录失败 ' + id + '：' + err.message); }
  recCache = null;
}
/** 微信身份 → 白名单判定（含记录），返回身份对象或 null */
function authorizeWechat(openid, unionid, src, ctx) {
  const c = ctx || {};
  const u = matchUser(openid, unionid);
  if (!u) {
    addRecord(Object.assign({}, c, { type: 'failure', action: c.action || 'login', src,
      openid, unionid, reason: 'not_whitelisted', message: '该微信用户不在白名单中，无权访问' }));
    return null;
  }
  addRecord(Object.assign({}, c, { type: 'success', action: c.action || 'login', src,
    openid, unionid, name: u.name, reason: 'ok', message: '微信身份校验通过' }));
  return { sub: String(openid || unionid), unionid: String(unionid || ''),
    name: String(u.name || '微信用户'), src };
}

/* -------------------------------- 会话 token -------------------------------- */

function sign(b64) {
  return crypto.createHmac('sha256', String(CFG.secret)).update(b64).digest('base64url');
}
function seal(payload) {
  const b = Buffer.from(JSON.stringify(payload)).toString('base64url');
  return b + '.' + sign(b);
}
function unseal(tok) {
  if (!tok || typeof tok !== 'string') return null;
  const i = tok.lastIndexOf('.');
  if (i < 1) return null;
  const b = tok.slice(0, i), sig = tok.slice(i + 1);
  const expect = sign(b);
  if (sig.length !== expect.length || !crypto.timingSafeEqual(Buffer.from(sig), Buffer.from(expect))) return null;
  try { return JSON.parse(Buffer.from(b, 'base64url').toString('utf8')); } catch (err) { return null; }
}
function issueToken(user) {
  const now = Math.floor(Date.now() / 1000);
  return seal({ sub: user.sub, unionid: user.unionid || '', name: user.name || '',
    src: user.src || MODE, role: 'user', iat: now, exp: now + SESSION_SEC });
}
function verifyToken(tok) {
  const p = unseal(tok);
  if (!p || p.role !== 'user' || !p.sub || !p.exp || p.exp < Math.floor(Date.now() / 1000)) return null;
  // 每次请求复检白名单：移除后已签发的 token 立即失效；设备模式只认设备白名单，
  // 切换登录模式时旧模式签发的 token 也会立即失效
  if (MODE === 'device') {
    if (p.src !== 'device' || !matchDevice(p.sub)) return null;
  } else if (!matchUser(p.sub, p.unionid)) return null;
  return p;
}
function issueAdminToken(username) {
  const now = Math.floor(Date.now() / 1000);
  return seal({ sub: 'admin:' + username, name: username, role: 'admin',
    iat: now, exp: now + ADMIN_SESSION_SEC });
}
function verifyAdminToken(tok) {
  const p = unseal(tok);
  if (!p || p.role !== 'admin' || !p.exp || p.exp < Math.floor(Date.now() / 1000)) return null;
  const name = String(p.name || '');
  if (!ADMINS.some((a) => a.username === name)) return null;   // 删除管理员后旧会话立即失效
  return p;
}
function parseCookies(req) {
  const out = {};
  String(req.headers.cookie || '').split(';').forEach((part) => {
    const i = part.indexOf('=');
    if (i > 0) out[part.slice(0, i).trim()] = decodeURIComponent(part.slice(i + 1).trim());
  });
  return out;
}
function tokenFrom(req, params) {
  const auth = String(req.headers.authorization || '');
  if (/^Bearer\s+/i.test(auth)) return auth.replace(/^Bearer\s+/i, '').trim();
  if (req.headers['x-auth-token']) return String(req.headers['x-auth-token']);
  const ck = parseCookies(req)[COOKIE];
  if (ck) return ck;
  if (TOKEN_QUERY && params && params.get('token')) return params.get('token');
  return '';
}
function adminTokenFrom(req, params) {
  const auth = String(req.headers.authorization || '');
  if (/^Bearer\s+/i.test(auth)) return auth.replace(/^Bearer\s+/i, '').trim();
  if (req.headers['x-admin-token']) return String(req.headers['x-admin-token']);
  const ck = parseCookies(req)[ADMIN_COOKIE];
  if (ck) return ck;
  return (params && params.get('admin_token')) || '';
}
function cookieHeader(tok) {
  return COOKIE + '=' + encodeURIComponent(tok) + '; Path=/; HttpOnly; SameSite=Lax; Max-Age=' + SESSION_SEC;
}
function adminCookieHeader(tok) {
  return ADMIN_COOKIE + '=' + encodeURIComponent(tok) + '; Path=/; HttpOnly; SameSite=Lax; Max-Age=' + ADMIN_SESSION_SEC;
}
function cookieClear(name) {
  return name + '=; Path=/; HttpOnly; SameSite=Lax; Max-Age=0';
}

/* --------------------------------- 扫码票据 -------------------------------- */

const tickets = new Map();
function newTicket() {
  const id = crypto.randomBytes(16).toString('hex');   // 32 字符，正好符合小程序码 scene 限制
  tickets.set(id, { id, status: 'pending', createdAt: Date.now(), expiresAt: Date.now() + TICKET_TTL,
    token: '', user: null, reason: '', code: '', openid: '', unionid: '', scannedAt: 0 });
  return id;
}
function grantTicket(t, user) {
  if (!t) return false;
  t.status = 'ok'; t.token = issueToken(user);
  t.user = { name: user.name, sub: user.sub }; t.reason = ''; t.code = '';
  return true;
}
function denyTicket(t, reason, extra) {
  if (!t) return;
  t.status = 'denied'; t.reason = reason;
  Object.assign(t, extra || {});
}
setInterval(() => {
  const now = Date.now();
  for (const [k, t] of tickets) if (t.expiresAt < now) tickets.delete(k);
}, 30 * 1000).unref();

/* 管理端登录防爆破：同一 IP 连续错 5 次锁 60 秒 */
const loginFails = new Map();
function locked(ip) {
  const r = loginFails.get(ip);
  return !!(r && r.n >= 5 && Date.now() - r.at < 60000);
}
function markFail(ip) {
  const r = loginFails.get(ip);
  loginFails.set(ip, { n: r && Date.now() - r.at < 60000 ? r.n + 1 : 1, at: Date.now() });
}
function clearFail(ip) { loginFails.delete(ip); }

/* -------------------------------- 微信 API -------------------------------- */

function wxURL(p) {
  return stripSlash(CFG.wechat.apiBase || DEFAULTS.wechat.apiBase) + p;
}
async function wxJSON(url) {
  const r = await fetch(url, { signal: AbortSignal.timeout(9000) });
  return await r.json();
}
function jscode2session(code) {
  const w = CFG.wechat;
  return wxJSON(wxURL('/sns/jscode2session?appid=' + encodeURIComponent(w.miniAppId)
    + '&secret=' + encodeURIComponent(w.miniAppSecret)
    + '&js_code=' + encodeURIComponent(code) + '&grant_type=authorization_code'));
}
let miniToken = { value: '', exp: 0 };
async function getMiniAccessToken() {
  if (miniToken.value && miniToken.exp > Date.now() + 60000) return miniToken.value;
  const w = CFG.wechat;
  const j = await wxJSON(wxURL('/cgi-bin/token?grant_type=client_credential&appid='
    + encodeURIComponent(w.miniAppId) + '&secret=' + encodeURIComponent(w.miniAppSecret)));
  if (!j.access_token) throw new Error(j.errmsg || ('微信返回 ' + j.errcode));
  miniToken = { value: j.access_token, exp: Date.now() + (Number(j.expires_in) || 7200) * 1000 };
  return miniToken.value;
}
function wxAuthorizeURL(ticket) {
  const w = CFG.wechat;
  const redirect = encodeURIComponent(stripSlash(CFG.publicBaseUrl) + PREFIX + 'wx/callback');
  const tail = '&response_type=code&state=' + encodeURIComponent(ticket) + '#wechat_redirect';
  const base = stripSlash(w.authorizeBase || DEFAULTS.wechat.authorizeBase);
  if (MODE === 'wechat-open') {
    return base + '/connect/qrconnect?appid=' + encodeURIComponent(w.openAppId)
      + '&redirect_uri=' + redirect + '&scope=snsapi_login' + tail;
  }
  return base + '/connect/oauth2/authorize?appid=' + encodeURIComponent(w.mpAppId)
    + '&redirect_uri=' + redirect + '&scope=snsapi_base' + tail;
}

/* ------------------------------- 认证路由页面 ------------------------------- */

const LOGO_SVG = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48" width="48" height="48">'
  + '<rect width="48" height="48" rx="12" fill="#4c8dff"/>'
  + '<path d="M20 12v20.5a5.5 5.5 0 1 1-3-4.9V17l14-3v13.5a5.5 5.5 0 1 1-3-4.9V11z" fill="#fff"/>'
  + '</svg>';
const PAGE_CSS = 'body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;'
  + 'background:#0d1117;color:#e6edf3;font:15px/1.7 -apple-system,"PingFang SC","Microsoft YaHei",sans-serif}'
  + '.c{width:min(90vw,420px);background:#151b23;border:1px solid #263140;border-radius:16px;'
  + 'padding:30px 24px;text-align:center}.d{width:52px;height:52px;border-radius:50%;margin:0 auto 16px;'
  + 'display:flex;align-items:center;justify-content:center;font-size:26px;background:'
  + 'rgba(61,220,151,.14);color:#3ddc97}h1{font-size:17px;margin:0 0 8px}p{color:#8b98a9;font-size:13px;margin:0}';

function pageHTML(title, inner) {
  return '<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">'
    + '<meta name="viewport" content="width=device-width,initial-scale=1"><title>' + escapeHTML(title) + '</title>'
    + '<style>' + PAGE_CSS + '</style></head><body><div class="c">' + inner + '</div></body></html>';
}
function resultPage(ok, msg) {
  return pageHTML('验证结果', '<div class="d" style="' + (ok ? '' :
    'background:rgba(255,123,123,.14);color:#ff7b7b') + '">' + (ok ? '✓' : '×') + '</div>'
    + '<h1>' + (ok ? '验证通过' : '验证未通过') + '</h1><p>' + escapeHTML(msg) + '</p>');
}
/** 非白名单专用：明确「无权访问」+ 回显 openid 便于复制给管理员 */
function denyPage(openid, unionid, msg) {
  const copy = openid ? '<div style="margin:18px 0 6px;text-align:left">'
    + '<div style="color:#8b98a9;font-size:11.5px;margin-bottom:5px">你的微信 openid（复制给管理员即可放行）</div>'
    + '<code id="oid" style="display:block;word-break:break-all;background:#0d1117;border:1px solid #263140;'
    + 'border-radius:8px;padding:9px 10px;font-size:12px;color:#9ecbff">' + escapeHTML(openid) + '</code>'
    + (unionid ? '<div style="color:#5f6c7d;font-size:11px;margin-top:5px">unionid: ' + escapeHTML(unionid) + '</div>' : '')
    + '<button id="cp" style="margin-top:9px;width:100%;background:#4c8dff;color:#fff;border:0;'
    + 'border-radius:8px;padding:9px;font-size:13px;cursor:pointer">复制 openid</button></div>' : '';
  const js = openid ? '<script>document.getElementById("cp").onclick=function(){'
    + 'var t=document.getElementById("oid").textContent;'
    + 'navigator.clipboard&&navigator.clipboard.writeText(t);'
    + 'this.textContent="已复制";}<\/script>' : '';
  return '<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">'
    + '<meta name="viewport" content="width=device-width,initial-scale=1"><title>无权访问</title>'
    + '<style>' + PAGE_CSS + '</style></head><body><div class="c">'
    + '<div class="d" style="background:rgba(255,123,123,.14);color:#ff7b7b">×</div>'
    + '<h1>无权访问</h1><p>' + escapeHTML(msg) + '</p>' + copy
    + '<p style="margin-top:14px;font-size:11.5px;color:#5f6c7d">本次授权失败已记录，管理员可在网关管理端一键放行。</p>'
    + '</div>' + js + '</body></html>';
}
/** 后端可达性探测：TCP 连一下即可，不真正转发；用于列表里把没启动的服务置灰，以及 502 时给友好降级页 */
function probeUpstream(u, timeoutMs = 800) {
  return new Promise((resolve) => {
    if (!u || !u.host || !u.port) return resolve(false);
    let done = false;
    const sock = net.connect(Number(u.port), String(u.host));
    const finish = (ok) => { if (done) return; done = true; try { sock.destroy(); } catch (e) {} resolve(ok); };
    sock.setTimeout(timeoutMs);
    sock.on('connect', () => finish(true));
    sock.on('timeout', () => finish(false));
    sock.on('error', () => finish(false));
  });
}
/** 当前请求的「协议 + 主机名」（不带端口），用于拼装其它端口的访问地址 */
function reqOrigin(req) {
  const proto = String(req.headers['x-forwarded-proto'] || '').split(',')[0].trim()
    || ((req.socket && req.socket.encrypted) ? 'https' : 'http');
  const host = String(req.headers.host || 'localhost').split(':')[0] || 'localhost';
  return proto + '://' + host;
}
/** 某个转发服务后端没起来时给的友好页（替代裸 502 文本），带返回列表入口 */
function serviceDownPage(u) {
  const name = (u && u.name) || '该服务';
  const addr = u ? (u.host + ':' + u.port) : '';
  return pageHTML('服务未启动', '<div class="d" style="background:rgba(255,180,84,.14);color:#ffb454">!</div>'
    + '<h1>服务未启动</h1>'
    + '<p>「' + escapeHTML(name) + '」的后端当前没有运行' + (addr ? '（' + escapeHTML(addr) + '）' : '') + '。</p>'
    + '<p style="margin-top:8px">请在对应机器启动该服务，或到「网关管理端 → 转发服务」核对地址与端口。</p>'
    + (u && u.listenPort ? '<p style="margin-top:6px;font-size:12px;color:#5f6c7d">入口地址：'
      + escapeHTML(String(u.listenPort)) + ' 端口</p>' : '')
    + '<p class="links" style="margin-top:18px"><a href="/__auth/apps" style="color:#7aa7ff;font-size:13px">返回服务列表</a></p>');
}
/** 授权通过后的服务列表：从实时路由表生成，逐个探测后端，未启动的置灰不可点 */
async function appsPage(req) {
  const routes = publicRoutes();
  const origin = req ? reqOrigin(req) : '';
  const probed = await Promise.all(routes.map(async (r) => {
    const u = r.listenPort ? routeForPort(r.listenPort) : (UPSTREAMS.find((x) => x && x.id === r.id) || null);
    const online = u ? await probeUpstream(u) : false;
    return Object.assign({}, r, { online });
  }));
  const offline = probed.length - probed.filter((r) => r.online).length;
  const css = '<style>.c{width:min(92vw,520px);text-align:left}'
    + '.d{width:44px;height:44px;font-size:22px}h1,p{text-align:center}'
    + '.svc{display:flex;align-items:center;gap:10px;background:#0d1117;border:1px solid #263140;'
    + 'border-radius:10px;padding:13px 14px;margin:9px 0;color:#e6edf3;text-decoration:none}'
    + '.svc:hover{border-color:#4c8dff}'
    + '.svc.off{opacity:.5;border-style:dashed;cursor:not-allowed}'
    + '.svc.off:hover{border-color:#263140}'
    + '.svc-n{font-size:14px;font-weight:600;flex:1}'
    + '.svc-p{color:#5f6c7d;font-size:11.5px;white-space:nowrap}'
    + '.svc-go{color:#7aa7ff;font-size:12px;white-space:nowrap}'
    + '.badge{color:#ffb454;font-size:11px;white-space:nowrap;border:1px solid rgba(255,180,84,.4);'
    + 'border-radius:6px;padding:2px 7px;background:rgba(255,180,84,.08)}'
    + '.svc-empty{color:#8b98a9;font-size:13px;text-align:center;padding:18px 4px}'
    + '.links{text-align:center;margin-top:18px}</style>';
  const items = probed.length
    ? probed.map((r) => {
        // 独立端口服务：直接给绝对地址（换端口就是换服务，路径永远是 /）
        // 路径前缀服务：站内相对地址；挂在 / 的用显式参数，避免点卡片又弹回门户
        const href = r.listenPort
          ? origin + ':' + r.listenPort + '/'
          : (r.prefix === '/' ? '/?__gw_go=' + encodeURIComponent(r.id) : r.url);
        const label = r.listenPort ? ('端口 ' + r.listenPort) : r.prefix;
        if (r.online) {
          return '<a class="svc" href="' + escapeHTML(href) + '">'
            + '<span class="svc-n">' + escapeHTML(r.name) + '</span>'
            + '<span class="svc-p">' + escapeHTML(label) + (r.hostBound ? ' · 限定域名' : '') + '</span>'
            + '<span class="svc-go">进入 →</span></a>';
        }
        return '<div class="svc off" title="后端未启动，暂不可访问">'
          + '<span class="svc-n">' + escapeHTML(r.name) + '</span>'
          + '<span class="svc-p">' + escapeHTML(label) + (r.hostBound ? ' · 限定域名' : '') + '</span>'
          + '<span class="badge">未启动</span></div>';
      }).join('')
    : '<div class="svc-empty">暂无可用服务，请管理员到「网关管理端 → 转发服务」添加。</div>';
  const okTitle = MODE === 'device' ? '设备已通过授权' : '已通过微信身份验证';
  const tip = offline > 0 ? ('，' + offline + ' 个未启动已置灰') : '';
  return pageHTML('服务入口', css + '<div class="d">✓</div><h1>' + okTitle + '</h1>'
    + '<p style="margin-bottom:16px">共 ' + probed.length + ' 个服务' + tip + '，点击即可进入</p>' + items
    + '<p class="links"><a href="/__auth/apps" style="color:#7aa7ff;font-size:12px">刷新状态</a>'
    + ' · <a href="/__auth/admin" style="color:#7aa7ff;font-size:12px">网关管理端</a>'
    + ' · <a href="/__auth/logout" style="color:#7aa7ff;font-size:12px">退出登录</a></p>'
    + '<p style="margin-top:14px;font-size:11px;color:#5f6c7d">已进入某个服务后，访问根路径 <b style="color:#8b98a9">/</b> 或 <b style="color:#8b98a9">/__auth/apps</b> 可随时回到本列表切换其它服务</p>');
}

/* -------------------------------- 认证路由 -------------------------------- */

async function handleAuth(req, res, pathname, params) {
  const method = req.method;
  const cors = corsHeaders(req);
  const info = reqInfo(req);

  // 设备登录模式下微信入口全部关闭：不签发二维码、不处理回调、不接受小程序 code
  if (MODE === 'device' && WECHAT_PATHS.has(pathname)) {
    return sendJSON(res, { ok: false, code: 'WECHAT_DISABLED',
      error: '当前为设备登录模式，微信登录已停用' }, 403, cors);
  }

  if (pathname === '/__auth/login') {
    if (!AUTH_ON || verifyToken(tokenFrom(req, params))) {
      res.writeHead(302, Object.assign({ Location: landingUrl() }, cors)); return res.end();
    }
    return sendHTML(res, MODE === 'device' ? DEVICE_LOGIN_PAGE : LOGIN_PAGE, 200, cors);
  }

  /* 设备登录：浏览器自动上报设备 ID + 指纹 → 服务端算机器码 → 查设备白名单。
     已授权直接签发会话；未授权回显机器码并记入待授权，管理员放行后前端自动进入。 */
  if (pathname === '/__auth/device/login' && method === 'POST') {
    if (MODE !== 'device') return sendJSON(res, { ok: false, error: '当前不是设备登录模式' }, 404, cors);
    let body = {};
    try { body = await readBody(req); } catch (err) {
      return sendJSON(res, { ok: false, error: err.message }, 400, cors);
    }
    const id = String(parseCookies(req)[DEV_COOKIE] || body.id || '').trim().slice(0, 128);
    if (!id) return sendJSON(res, { ok: false, error: '缺少设备标识' }, 400, cors);
    const fp = String(body.fp || '').slice(0, 4096);
    const mc = machineCode(id, fp);
    const label = String(body.label || '').slice(0, 160);
    const meta = { label: label, ua: info.ua, ip: info.ip };
    const cookies = [deviceCookie(id)];
    const dev = matchDevice(mc);
    if (!dev) {
      pendingDevice(mc, meta);
      if (shouldLogDevice(mc)) {
        addRecord(Object.assign({}, info, { type: 'failure', action: 'device-login', src: 'device',
          device: mc, name: label.slice(0, 40), reason: 'not_whitelisted',
          message: '设备未授权：' + mc }));
      }
      return sendJSON(res, { ok: false, code: 'NOT_AUTHORIZED',
        error: '无权访问：该设备未获授权', machineCode: mc, device: mc, label: label,
        hint: '把机器码发给管理员，在管理端「设备白名单」放行后本页会自动进入' }, 403,
      Object.assign({ 'Set-Cookie': cookies }, cors));
    }
    const tok = issueToken({ sub: mc, unionid: '', name: dev.name || label || '已授权设备', src: 'device' });
    touchDevice(mc, meta);
    addRecord(Object.assign({}, info, { type: 'success', action: 'device-login', src: 'device',
      device: mc, name: dev.name || label, reason: 'ok', message: '设备校验通过：' + mc }));
    cookies.push(cookieHeader(tok));
    return sendJSON(res, { ok: true, token: tok, machineCode: mc, name: dev.name || label,
      next: safeNext(body.next), expiresIn: SESSION_SEC }, 200,
    Object.assign({ 'Set-Cookie': cookies }, cors));
  }
  if (pathname === '/__auth/logo') {
    const buf = Buffer.from(LOGO_SVG);
    res.writeHead(200, Object.assign({ 'Content-Type': 'image/svg+xml', 'Content-Length': buf.length,
      'Cache-Control': 'public, max-age=86400' }, cors));
    return res.end(buf);
  }
  if (pathname === '/__auth/apps') {
    if (AUTH_ON && !verifyToken(tokenFrom(req, params))) {
      res.writeHead(302, Object.assign({ Location: PREFIX + 'login' }, cors)); return res.end();
    }
    return sendHTML(res, await appsPage(req), 200, cors);
  }

  // 登录状态（免鉴权，供页面判断）
  if (pathname === '/__auth/status') {
    const p = verifyToken(tokenFrom(req, params));
    return sendJSON(res, { enabled: AUTH_ON, mode: MODE, modeLabel: MODE_LABEL,
      loginMode: String(CFG.loginMode || 'wechat'), deviceMode: MODE === 'device',
      wechatConfigured: WECHAT_OK, loggedIn: !!p,
      user: p ? { name: p.name, sub: p.sub, src: p.src } : null,
      routes: publicRoutes(), landing: landingUrl(), adminUrl: PREFIX + 'admin' }, 200, cors);
  }

  // 生成扫码票据与二维码
  if (pathname === '/__auth/qr' && method === 'GET') {
    if (!WECHAT_OK) {
      return sendJSON(res, { error: '未配置微信登录凭证：请在管理端「系统配置」页填 miniAppId / miniAppSecret（个人主体小程序即可），保存即时生效',
        hint: '个人开发者用小程序码：0 元、无需公章与企业认证、无需 publicBaseUrl；企业也可用开放平台网站应用或服务号（需 publicBaseUrl）',
        mode: 'none', configured: false }, 503, cors);
    }
    const id = newTicket();
    const out = { ticket: id, mode: MODE, modeLabel: MODE_LABEL, expiresIn: TICKET_TTL / 1000,
      qrSvg: null, imageUrl: null, link: '', scanText: '', hint: '', configured: true };
    try {
      if (MODE === 'wechat-mini') {
        out.imageUrl = PREFIX + 'wxacode?ticket=' + encodeURIComponent(id);
        out.scanText = '请用微信扫一扫小程序码';
        out.hint = '扫码后在小程序内完成验证，本页会自动进入';
      } else {
        const link = wxAuthorizeURL(id);
        out.link = link;
        out.scanText = '请用微信扫一扫登录';
        out.hint = MODE === 'wechat-open'
          ? '在手机上确认登录后，本页会自动进入'
          : '在微信内打开并确认授权后，本页会自动进入';
        out.qrSvg = await QRCode.toString(link, {
          type: 'svg', width: 236, margin: 1, errorCorrectionLevel: 'M',
          color: { dark: '#0d1117', light: '#ffffff' },
        });
      }
      return sendJSON(res, out, 200, cors);
    } catch (err) {
      return sendJSON(res, { error: '二维码生成失败：' + err.message }, 500, cors);
    }
  }

  // 小程序码（wechat-mini 模式）
  if (pathname === '/__auth/wxacode' && method === 'GET') {
    if (MODE !== 'wechat-mini') return sendJSON(res, { error: '当前不是小程序码模式' }, 404, cors);
    try {
      const token = await getMiniAccessToken();
      const w = CFG.wechat;
      const r = await fetch(wxURL('/wxa/getwxacodeunlimit?access_token=' + encodeURIComponent(token)), {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ scene: String(params.get('ticket') || '').slice(0, 32),
          page: w.miniPage || 'pages/gate/index', width: 280, check_path: false,
          env_version: w.miniEnv || 'release' }),
        signal: AbortSignal.timeout(9000),
      });
      const buf = Buffer.from(await r.arrayBuffer());
      if (String(r.headers.get('content-type') || '').includes('json')) {
        return sendJSON(res, { error: '小程序码生成失败：' + buf.toString('utf8').slice(0, 160) }, 502, cors);
      }
      res.writeHead(200, Object.assign({ 'Content-Type': 'image/png', 'Content-Length': buf.length,
        'Cache-Control': 'no-store' }, cors));
      return res.end(buf);
    } catch (err) {
      return sendJSON(res, { error: '小程序码生成失败：' + err.message }, 502, cors);
    }
  }

  // 网页轮询扫码结果；成功时种下会话 cookie
  if (pathname === '/__auth/poll' && method === 'GET') {
    const t = tickets.get(String(params.get('ticket') || ''));
    if (!t || t.expiresAt < Date.now()) return sendJSON(res, { status: 'expired' }, 200, cors);
    if (t.status === 'ok') {
      return sendJSON(res, { status: 'ok', token: t.token, user: t.user, next: landingUrl(),
        expiresIn: SESSION_SEC }, 200, Object.assign({ 'Set-Cookie': cookieHeader(t.token) }, cors));
    }
    if (t.status === 'denied') {
      return sendJSON(res, { status: 'denied', code: t.code || 'DENIED', reason: t.reason,
        openid: t.openid || '', unionid: t.unionid || '' }, 200, cors);
    }
    return sendJSON(res, { status: t.status === 'scanned' ? 'scanned' : 'pending' }, 200, cors);
  }

  // 微信网页授权回调（开放平台网站应用 / 服务号）
  if (pathname === '/__auth/wx/callback' && method === 'GET') {
    const ticketId = String(params.get('state') || '');
    const t = tickets.get(ticketId);
    if (!t || t.expiresAt < Date.now()) {
      addRecord(Object.assign({}, info, { type: 'failure', action: 'login', src: MODE,
        reason: 'expired', ticket: ticketId, message: '扫码票据已过期' }));
      return sendHTML(res, resultPage(false, '二维码已过期，请回到电脑重新扫码'));
    }
    t.scannedAt = Date.now(); t.status = 'scanned';
    const code = String(params.get('code') || '');
    if (!code) {
      const msg = '微信授权被取消';
      addRecord(Object.assign({}, info, { type: 'failure', action: 'login', src: MODE,
        reason: 'cancelled', ticket: ticketId, message: msg }));
      denyTicket(t, msg, { code: 'CANCELLED' });
      return sendHTML(res, resultPage(false, msg));
    }
    const w = CFG.wechat;
    const appid = MODE === 'wechat-open' ? w.openAppId : w.mpAppId;
    const secret = MODE === 'wechat-open' ? w.openAppSecret : w.mpAppSecret;
    try {
      const j = await wxJSON(wxURL('/sns/oauth2/access_token?appid=' + encodeURIComponent(appid)
        + '&secret=' + encodeURIComponent(secret) + '&code=' + encodeURIComponent(code)
        + '&grant_type=authorization_code'));
      if (!j.openid) {
        const msg = '微信授权失败：' + (j.errmsg || j.errcode || '未知错误');
        addRecord(Object.assign({}, info, { type: 'failure', action: 'login', src: MODE,
          reason: 'wechat_api_error', ticket: ticketId, errcode: j.errcode, errmsg: j.errmsg || '', message: msg }));
        denyTicket(t, msg, { code: 'WX_API_ERROR' });
        return sendHTML(res, resultPage(false, msg));
      }
      const user = authorizeWechat(j.openid, j.unionid || '', MODE,
        Object.assign({}, info, { action: 'login', ticket: ticketId }));
      if (!user) {
        const msg = '该微信账号未获授权，请联系管理员把 openid 加入白名单';
        denyTicket(t, '无权访问：' + msg, { code: 'NOT_IN_WHITELIST',
          openid: j.openid, unionid: j.unionid || '' });
        return sendHTML(res, denyPage(j.openid, j.unionid || '', msg));
      }
      grantTicket(t, user);
      return sendHTML(res, resultPage(true, '已授权「' + user.name + '」，请回到电脑继续'));
    } catch (err) {
      const msg = '微信接口请求失败：' + err.message;
      addRecord(Object.assign({}, info, { type: 'failure', action: 'login', src: MODE,
        reason: 'network_error', ticket: ticketId, message: msg }));
      denyTicket(t, msg, { code: 'NETWORK_ERROR' });
      return sendHTML(res, resultPage(false, msg));
    }
  }

  // 小程序端：wx.login 的 code → 白名单校验 → 签发 token
  // 带 ticket 时（从小程序码 scene 进来）同时确认网页端扫码登录
  if (pathname === '/__auth/mp-login' && method === 'POST') {
    let body = {};
    try { body = await readBody(req); } catch (err) { /* 忽略 */ }
    const base = Object.assign({}, info, { action: 'mp-login', src: 'wechat-mini',
      ticket: String(body.ticket || '') });
    const code = String(body.code || '');
    if (!code) {
      addRecord(Object.assign({}, base, { type: 'failure', reason: 'missing_code', message: '缺少 wx.login 的 code' }));
      return sendJSON(res, { ok: false, code: 'MISSING_CODE', error: '缺少 code' }, 400, cors);
    }
    const w = CFG.wechat;
    if (!w.miniAppId || !w.miniAppSecret) {
      addRecord(Object.assign({}, base, { type: 'failure', reason: 'not_configured',
        message: '未配置小程序 AppID/AppSecret' }));
      return sendJSON(res, { ok: false, code: 'NOT_CONFIGURED',
        error: '未配置小程序 AppID/AppSecret（config.json → wechat）' }, 503, cors);
    }
    try {
      const j = await jscode2session(code);
      if (!j.openid) {
        const msg = '微信校验失败：' + (j.errmsg || j.errcode || '');
        addRecord(Object.assign({}, base, { type: 'failure', reason: 'wechat_api_error',
          errcode: j.errcode, errmsg: j.errmsg || '', message: msg }));
        return sendJSON(res, { ok: false, code: 'WX_API_ERROR', error: msg, openid: '' }, 401, cors);
      }
      const user = authorizeWechat(j.openid, j.unionid || '', 'wechat-mini', base);
      if (!user) {
        return sendJSON(res, { ok: false, code: 'NOT_IN_WHITELIST', error: '无权访问：该微信账号未获授权',
          openid: j.openid, unionid: j.unionid || '',
          hint: '把 openid 发给我们，或在网关管理端「授权记录」里一键放行' }, 403, cors);
      }
      const tok = issueToken(user);
      if (body.ticket) grantTicket(tickets.get(String(body.ticket)), user);
      const root = stripSlash(CFG.publicBaseUrl) || localBase();
      return sendJSON(res, { ok: true, token: tok, expiresIn: SESSION_SEC,
        user: { name: user.name, sub: user.sub, unionid: user.unionid },
        webUrl: root + '/?token=' + encodeURIComponent(tok) }, 200,
      Object.assign({ 'Set-Cookie': cookieHeader(tok) }, cors));
    } catch (err) {
      const msg = '微信接口请求失败：' + err.message;
      addRecord(Object.assign({}, base, { type: 'failure', reason: 'network_error', message: msg }));
      return sendJSON(res, { ok: false, code: 'NETWORK_ERROR', error: msg }, 502, cors);
    }
  }

  if (pathname === '/__auth/logout') {
    res.writeHead(302, Object.assign({ Location: PREFIX + 'login', 'Set-Cookie': cookieClear(COOKIE) }, cors));
    return res.end();
  }

  // 兼容旧接口：白名单读取 / 保存（需管理员或已登录微信用户）
  const compatOK = verifyAdminToken(adminTokenFrom(req, params)) || verifyToken(tokenFrom(req, params));
  if (pathname === '/__auth/whitelist') {
    if (!compatOK) return sendJSON(res, { error: 'unauthorized' }, 401, cors);
    const wl = whitelist();
    return sendJSON(res, { users: wl.users, pending: wl.pending, file: DB_FILE }, 200, cors);
  }
  if (pathname === '/__auth/whitelist/save' && method === 'POST') {
    if (!compatOK) return sendJSON(res, { error: 'unauthorized' }, 401, cors);
    let body = {};
    try { body = await readBody(req, 256 * 1024); } catch (err) {
      return sendJSON(res, { error: err.message }, 400, cors);
    }
    if (!Array.isArray(body.users)) return sendJSON(res, { error: 'users 必须是数组' }, 400, cors);
    try {
      const wl = whitelist();
      wl.users = body.users.map((u) => ({ openid: String(u.openid || '').trim(),
        unionid: String(u.unionid || '').trim(), name: String(u.name || '').trim(),
        enabled: u.enabled !== false })).filter((u) => u.openid);
      if (Array.isArray(body.pending)) wl.pending = body.pending.slice(0, 50);
      saveWhitelist(wl);
      return sendJSON(res, { ok: true, count: wl.users.length }, 200, cors);
    } catch (err) {
      return sendJSON(res, { error: err.message }, 500, cors);
    }
  }

  return sendJSON(res, { error: 'not found' }, 404, cors);
}

/* ------------------------------- 网关管理端 -------------------------------- */

function adminGuard(req, res, params, cors) {
  const p = verifyAdminToken(adminTokenFrom(req, params));
  if (p) return p;
  sendJSON(res, { error: 'unauthorized', code: 'ADMIN_REQUIRED' }, 401, cors);
  return null;
}
function adminStats() {
  const wl = whitelist();
  const recs = readRecords();
  const day = new Date(); day.setHours(0, 0, 0, 0);
  return {
    users: wl.users.length,
    activeUsers: wl.users.filter((u) => u && u.enabled !== false).length,
    pending: wl.pending.length,
    devices: (wl.devices || []).length,
    activeDevices: (wl.devices || []).filter((d) => d && d.enabled !== false).length,
    devicePending: (wl.devicePending || []).length,
    records: recs.length,
    failures: recs.filter((r) => r.type === 'failure').length,
    successes: recs.filter((r) => r.type === 'success').length,
    todaySuccess: recs.filter((r) => r.type === 'success' && new Date(r.at) >= day).length,
  };
}
function routeJSON(u) {
  const p = normPrefix(u.prefix || '/');
  const lp = Number(u.listenPort) || 0;
  return { id: u.id, name: String(u.name || ''), prefix: p, listenPort: lp,
    host: String(u.host || '127.0.0.1'), port: Number(u.port) || 6001,
    stripPrefix: !!u.stripPrefix, hosts: hostList(u.hosts), enabled: u.enabled !== false,
    url: lp ? '/' : (p === '/' ? '/' : p + '/') };
}
function adminRoutesPayload() {
  syncRoutesFromDisk();
  return { ok: true, routes: configRoutes().map(routeJSON), landing: landingUrl(), file: DB_FILE };
}
async function handleAdmin(req, res, pathname, params) {
  const method = req.method;
  const cors = corsHeaders(req);
  const info = reqInfo(req);

  if (pathname === '/__auth/admin') return sendHTML(res, adminPageHTML(), 200, cors);

  if (pathname === '/__auth/admin/login' && method === 'POST') {
    const ip = info.ip;
    if (locked(ip)) return sendJSON(res, { error: '尝试次数过多，请 1 分钟后再试' }, 429, cors);
    let body = {};
    try { body = await readBody(req); } catch (err) { /* 空体当失败 */ }
    const username = String(body.username || '').trim();
    const pw = String(body.password || '');
    const acc = ADMINS.find((a) => a.username === username);
    let ok = false;
    if (acc && pw) {
      const got = Buffer.from(hashPassword(pw, acc.salt), 'hex');
      const want = Buffer.from(acc.passwordHash, 'hex');
      ok = got.length === want.length && crypto.timingSafeEqual(got, want);
    }
    if (!ok) {
      markFail(ip);
      addRecord(Object.assign({}, info, { type: 'failure', action: 'admin-login', src: 'admin',
        name: username, reason: 'bad_password', message: '管理员账号或密码错误' }));
      return sendJSON(res, { ok: false, error: '账号或密码不正确' }, 401, cors);
    }
    clearFail(ip);
    addRecord(Object.assign({}, info, { type: 'success', action: 'admin-login', src: 'admin',
      name: username, reason: 'ok', message: '管理员登录成功' }));
    const tok = issueAdminToken(username);
    return sendJSON(res, { ok: true, next: PREFIX + 'admin', username }, 200,
      Object.assign({ 'Set-Cookie': adminCookieHeader(tok) }, cors));
  }
  if (pathname === '/__auth/admin/logout') {
    res.writeHead(302, Object.assign({ Location: PREFIX + 'admin',
      'Set-Cookie': cookieClear(ADMIN_COOKIE) }, cors));
    return res.end();
  }

  if (!pathname.startsWith('/__auth/admin/api/')) return sendJSON(res, { error: 'not found' }, 404, cors);
  const admin = adminGuard(req, res, params, cors);
  if (!admin) return;
  // 兼容历史页面拼出的 /api//records 这类双斜杠路径（去掉前导斜杠再匹配）
  const api = pathname.slice('/__auth/admin/api/'.length).replace(/^\/+/, '');

  if (api === 'me' && method === 'GET') {
    return sendJSON(res, { loggedIn: true, username: admin.name,
      admins: ADMINS.map((a) => a.username), mode: MODE, modeLabel: MODE_LABEL,
      loginMode: String(CFG.loginMode || 'wechat'), deviceMode: MODE === 'device',
      wechatConfigured: WECHAT_OK, authEnabled: AUTH_ON, port: LISTEN_PORT,
      routes: publicRoutes(), files: { config: CONFIG_FILE, db: DB_FILE },
      ipdb: { ready: iploc.ready(), count: iploc.count(), embedded: !!IPDB_EMBED,
        source: IPDB_EMBED ? 'exe 内嵌' : IPDB_FILE } }, 200, cors);
  }
  if (api === 'overview' && method === 'GET') {
    return sendJSON(res, Object.assign({ ok: true }, adminStats()), 200, cors);
  }
  if (api === 'records' && method === 'GET') {
    const type = String(params.get('type') || 'all');
    const q = String(params.get('q') || '').toLowerCase();
    // 升级前写入的历史记录没有 region：读取时按 IP 现算补上（不改库、不污染缓存）
    let list = readRecords().map((r) => (r.region || !r.ip
      ? r : Object.assign({}, r, { region: iploc.regionOf(r.ip) })));
    if (type === 'failure' || type === 'success') list = list.filter((r) => r.type === type);
    if (q) list = list.filter((r) => [r.openid, r.name, r.reason, r.message, r.src, r.action, r.ip, r.region]
      .some((v) => String(v || '').toLowerCase().includes(q)));
    return sendJSON(res, { ok: true, total: list.length, records: list.slice(0, Number(params.get('limit')) || 200) }, 200, cors);
  }
  if (api === 'records/clear' && method === 'POST') {
    saveRecords([]);
    addRecord(Object.assign({}, info, { type: 'success', action: 'admin-clear', src: 'admin',
      name: admin.name, reason: 'ok', message: '清空授权记录' }));
    return sendJSON(res, { ok: true }, 200, cors);
  }
  if (api === 'config' && method === 'GET') {
    const w = CFG.wechat || {};
    const mask = (v) => (v ? '********' : '');
    return sendJSON(res, { ok: true,
      port: CFG.port, host: CFG.host,
      mode: CFG.mode, loginMode: CFG.loginMode,
      publicBaseUrl: CFG.publicBaseUrl, lanBaseUrl: CFG.lanBaseUrl,
      sessionHours: CFG.sessionHours, adminSessionHours: CFG.adminSessionHours,
      allowTokenInQuery: CFG.allowTokenInQuery !== false,
      corsOrigins: (CFG.corsOrigins || []).join(','),
      landing: CFG.landing,
      rootMode: CFG.rootMode === 'proxy' ? 'proxy' : 'portal',
      wechat: {
        mpAppId: w.mpAppId || '', mpAppSecret: mask(w.mpAppSecret),
        openAppId: w.openAppId || '', openAppSecret: mask(w.openAppSecret),
        miniAppId: w.miniAppId || '', miniAppSecret: mask(w.miniAppSecret),
        miniPage: w.miniPage || '', miniEnv: w.miniEnv || 'release',
      },
    }, 200, cors);
  }
  if (api === 'config' && method === 'POST') {
    let body = {};
    try { body = await readBody(req, 64 * 1024); } catch (err) { return sendJSON(res, { error: err.message }, 400, cors); }
    const patch = {};
    ['mode', 'loginMode', 'publicBaseUrl', 'lanBaseUrl', 'landing'].forEach((k) => {
      if (body[k] !== undefined) patch[k] = String(body[k]);
    });
    if (body.sessionHours !== undefined) { const n = Number(body.sessionHours); if (Number.isFinite(n) && n > 0) patch.sessionHours = n; }
    if (body.adminSessionHours !== undefined) { const n = Number(body.adminSessionHours); if (Number.isFinite(n) && n > 0) patch.adminSessionHours = n; }
    if (body.allowTokenInQuery !== undefined) patch.allowTokenInQuery = !!body.allowTokenInQuery;
    if (body.rootMode !== undefined) patch.rootMode = body.rootMode === 'proxy' ? 'proxy' : 'portal';
    if (body.corsOrigins !== undefined) patch.corsOrigins = hostList(String(body.corsOrigins || ''));
    const wechat = Object.assign({}, CFG.wechat);
    ['mpAppId', 'openAppId', 'miniAppId', 'miniPage', 'miniEnv'].forEach((k) => { if (body[k] !== undefined) wechat[k] = String(body[k]); });
    const MASK = '********';
    ['mpAppSecret', 'openAppSecret', 'miniAppSecret'].forEach((k) => {
      const v = body[k];
      if (v !== undefined && v !== '' && v !== MASK) wechat[k] = String(v);
    });
    patch.wechat = wechat;
    persistConfig(patch);
    recomputeMode();   // 即时生效：登录方式 / 会话时长等无需重启
    addRecord(Object.assign({}, info, { type: 'success', action: 'admin-config', src: 'admin',
      name: admin.name, reason: 'ok', message: '修改系统配置' }));
    return sendJSON(res, { ok: true }, 200, cors);
  }
  if (api === 'whitelist' && method === 'GET') {
    const wl = whitelist();
    return sendJSON(res, { ok: true, users: wl.users, pending: wl.pending, file: DB_FILE }, 200, cors);
  }
  if (api === 'whitelist/save' && method === 'POST') {
    let body = {};
    try { body = await readBody(req, 256 * 1024); } catch (err) {
      return sendJSON(res, { error: err.message }, 400, cors);
    }
    if (!Array.isArray(body.users)) return sendJSON(res, { error: 'users 必须是数组' }, 400, cors);
    const wl = whitelist();
    wl.users = body.users.map((u) => ({ openid: String(u.openid || '').trim(),
      unionid: String(u.unionid || '').trim(), name: String(u.name || '').trim(),
      enabled: u.enabled !== false })).filter((u) => u.openid);
    saveWhitelist(wl);
    addRecord(Object.assign({}, info, { type: 'success', action: 'admin-save', src: 'admin',
      name: admin.name, reason: 'ok', message: '保存白名单，共 ' + wl.users.length + ' 人' }));
    return sendJSON(res, { ok: true, count: wl.users.length }, 200, cors);
  }
  if (api === 'allow' && method === 'POST') {
    let body = {};
    try { body = await readBody(req); } catch (err) { return sendJSON(res, { error: err.message }, 400, cors); }
    const openid = String(body.openid || '').trim();
    if (!openid) return sendJSON(res, { error: '缺少 openid' }, 400, cors);
    const wl = whitelist();
    const hit = wl.users.find((u) => u && u.openid === openid);
    const wasEnabled = !!(hit && hit.enabled);
    if (hit) { hit.enabled = true; hit.name = String(body.name || hit.name || '微信用户'); }
    else wl.users.push({ openid, unionid: String(body.unionid || '').trim(),
      name: String(body.name || '微信用户').trim(), enabled: true });
    wl.pending = wl.pending.filter((p) => !p || p.openid !== openid);
    saveWhitelist(wl);
    // 放行：更新该 openid 最近一条「未授权」记录，而不是新增一条成功记录
    const now = new Date().toISOString();
    const failId = Store.findUnresolvedFailure({ openid });
    if (failId) {
      updateRecord(failId, { resolved: 1, resolvedBy: admin.name, resolvedAt: now,
        resolvedNote: '管理员手动放行', reason: 'approved',
        message: '管理员 ' + admin.name + ' 已放行 ' + openid });
    } else if (!wasEnabled) {
      // 无未授权记录（如直接预置白名单），仍记一条放行动作
      addRecord(Object.assign({}, info, { type: 'success', action: 'admin-allow', src: 'admin',
        name: admin.name, openid, reason: 'ok', message: '手动放行微信用户 ' + openid }));
    }
    return sendJSON(res, { ok: true, count: wl.users.length }, 200, cors);
  }
  if (api === 'revoke' && method === 'POST') {
    let body = {};
    try { body = await readBody(req); } catch (err) { return sendJSON(res, { error: err.message }, 400, cors); }
    const openid = String(body.openid || '').trim();
    if (!openid) return sendJSON(res, { error: '缺少 openid' }, 400, cors);
    const wl = whitelist();
    const before = wl.users.length;
    wl.users = body.remove === true
      ? wl.users.filter((u) => !u || u.openid !== openid)
      : wl.users.map((u) => (u && u.openid === openid ? Object.assign({}, u, { enabled: false }) : u));
    saveWhitelist(wl);
    addRecord(Object.assign({}, info, { type: 'success', action: 'admin-revoke', src: 'admin',
      name: admin.name, openid, reason: 'ok',
      message: (body.remove === true ? '删除' : '停用') + '白名单用户 ' + openid }));
    return sendJSON(res, { ok: true, removed: before - wl.users.length }, 200, cors);
  }
  /* ------------------------- 设备白名单（机器码） ------------------------- */
  if (api === 'devices' && method === 'GET') {
    const wl = whitelist();
    return sendJSON(res, { ok: true, devices: wl.devices, pending: wl.devicePending,
      file: DB_FILE }, 200, cors);
  }
  if (api === 'devices/allow' && method === 'POST') {
    let body = {};
    try { body = await readBody(req); } catch (err) { return sendJSON(res, { error: err.message }, 400, cors); }
    const code = String(body.code || body.machineCode || body.device || '').trim().toUpperCase();
    if (!/^MC-[0-9A-F]{4}(-[0-9A-F]{4}){3}$/.test(code)) {
      return sendJSON(res, { error: '机器码格式不正确（应形如 MC-XXXX-XXXX-XXXX-XXXX）' }, 400, cors);
    }
    const wl = whitelist();
    const hit = wl.devices.find((d) => d && d.code === code);
    const wasEnabled = !!(hit && hit.enabled);
    // 不传 name 时保留原备注（「启用」按钮走同一接口，不该把备注冲掉）
    const name = String(body.name || body.label || '').trim().slice(0, 80)
      || (hit && hit.name) || '已授权设备';
    if (hit) { hit.enabled = true; hit.name = name; }
    else wl.devices.push({ code: code, name: name, enabled: true,
      firstSeen: new Date().toISOString(), lastSeen: '', ip: '', ua: '' });
    wl.devicePending = wl.devicePending.filter((p) => !p || p.code !== code);
    saveWhitelist(wl);
    // 放行：更新该机器码最近一条「未授权」记录，而不是新增一条成功记录
    const now = new Date().toISOString();
    const failId = Store.findUnresolvedFailure({ device: code });
    if (failId) {
      updateRecord(failId, { resolved: 1, resolvedBy: admin.name, resolvedAt: now,
        resolvedNote: '管理员放行设备', reason: 'approved',
        message: '管理员 ' + admin.name + ' 已放行设备 ' + code + '（' + name + '）' });
    } else if (!wasEnabled) {
      addRecord(Object.assign({}, info, { type: 'success', action: 'admin-device-allow', src: 'admin',
        name: admin.name, device: code, reason: 'ok', message: '放行设备 ' + code + '（' + name + '）' }));
    }
    return sendJSON(res, { ok: true, code: code, devices: wl.devices.length }, 200, cors);
  }
  if (api === 'devices/revoke' && method === 'POST') {
    let body = {};
    try { body = await readBody(req); } catch (err) { return sendJSON(res, { error: err.message }, 400, cors); }
    const code = String(body.code || '').trim().toUpperCase();
    if (!code) return sendJSON(res, { error: '缺少机器码' }, 400, cors);
    const wl = whitelist();
    const before = wl.devices.length;
    wl.devices = body.remove === true
      ? wl.devices.filter((d) => !d || d.code !== code)
      : wl.devices.map((d) => (d && d.code === code ? Object.assign({}, d, { enabled: false }) : d));
    saveWhitelist(wl);
    addRecord(Object.assign({}, info, { type: 'success', action: 'admin-device-revoke', src: 'admin',
      name: admin.name, device: code, reason: 'ok',
      message: (body.remove === true ? '删除' : '停用') + '设备 ' + code }));
    return sendJSON(res, { ok: true, removed: before - wl.devices.length }, 200, cors);
  }
  if (api === 'devices/rename' && method === 'POST') {
    let body = {};
    try { body = await readBody(req); } catch (err) { return sendJSON(res, { error: err.message }, 400, cors); }
    const code = String(body.code || '').trim().toUpperCase();
    const wl = whitelist();
    const hit = wl.devices.find((d) => d && d.code === code);
    if (!hit) return sendJSON(res, { error: '设备不存在' }, 404, cors);
    hit.name = String(body.name || '').trim().slice(0, 80) || hit.name;
    saveWhitelist(wl);
    addRecord(Object.assign({}, info, { type: 'success', action: 'admin-device-rename', src: 'admin',
      name: admin.name, device: code, reason: 'ok', message: '设备备注改为 ' + hit.name }));
    return sendJSON(res, { ok: true }, 200, cors);
  }
  if (api === 'routes' && method === 'GET') {
    return sendJSON(res, adminRoutesPayload(), 200, cors);
  }
  /* 转发服务在线增改删：写回 config.json + 立即重建内存路由表，无需重启 */
  if (api === 'routes/save' && method === 'POST') {
    let body = {};
    try { body = await readBody(req, 64 * 1024); } catch (err) {
      return sendJSON(res, { error: err.message }, 400, cors);
    }
    const list = configRoutes().slice();
    const idx = body.id ? list.findIndex((u) => u.id === String(body.id)) : -1;
    if (body.id && idx < 0) return sendJSON(res, { error: '要修改的服务不存在，请刷新后重试' }, 404, cors);
    const checked = validateRoute(body, idx >= 0 ? list[idx] : null);
    if (checked.error) return sendJSON(res, { error: checked.error }, 400, cors);
    const route = Object.assign({}, idx >= 0 ? list[idx] : {}, checked.route);
    if (idx >= 0) list[idx] = route; else list.push(route);
    writeRoutes(list);
    addRecord(Object.assign({}, info, { type: 'success', action: 'admin-route-save', src: 'admin',
      name: admin.name, reason: 'ok',
      message: (idx >= 0 ? '修改' : '新增') + '转发服务 ' + route.name + '（' + route.prefix
        + ' → ' + route.host + ':' + route.port + '）' }));
    return sendJSON(res, Object.assign(adminRoutesPayload(), { id: route.id }), 200, cors);
  }
  if (api === 'routes/delete' && method === 'POST') {
    let body = {};
    try { body = await readBody(req); } catch (err) { return sendJSON(res, { error: err.message }, 400, cors); }
    const id = String(body.id || '').trim();
    const list = configRoutes().slice();
    const hit = list.find((u) => u.id === id);
    if (!hit) return sendJSON(res, { error: '服务不存在或已删除' }, 404, cors);
    writeRoutes(list.filter((u) => u.id !== id));
    addRecord(Object.assign({}, info, { type: 'success', action: 'admin-route-delete', src: 'admin',
      name: admin.name, reason: 'ok', message: '删除转发服务 ' + String(hit.name || hit.prefix) }));
    return sendJSON(res, adminRoutesPayload(), 200, cors);
  }
  if (api === 'routes/toggle' && method === 'POST') {
    let body = {};
    try { body = await readBody(req); } catch (err) { return sendJSON(res, { error: err.message }, 400, cors); }
    const id = String(body.id || '').trim();
    const list = configRoutes().slice();
    const hit = list.find((u) => u.id === id);
    if (!hit) return sendJSON(res, { error: '服务不存在' }, 404, cors);
    hit.enabled = body.enabled !== false;
    writeRoutes(list);
    addRecord(Object.assign({}, info, { type: 'success', action: 'admin-route-toggle', src: 'admin',
      name: admin.name, reason: 'ok',
      message: (hit.enabled ? '启用' : '停用') + '转发服务 ' + String(hit.name || hit.prefix) }));
    return sendJSON(res, adminRoutesPayload(), 200, cors);
  }
  if (api === 'password' && method === 'POST') {
    let body = {};
    try { body = await readBody(req); } catch (err) { return sendJSON(res, { error: err.message }, 400, cors); }
    const acc = ADMINS.find((a) => a.username === admin.name);
    if (!acc) return sendJSON(res, { error: '管理员账号已不存在' }, 404, cors);
    const oldPw = String(body.oldPassword || ''), newPw = String(body.newPassword || '');
    if (!oldPw || !newPw) return sendJSON(res, { error: '请填写当前密码与新密码' }, 400, cors);
    const got = Buffer.from(hashPassword(oldPw, acc.salt), 'hex');
    const want = Buffer.from(acc.passwordHash, 'hex');
    if (got.length !== want.length || !crypto.timingSafeEqual(got, want)) {
      addRecord(Object.assign({}, info, { type: 'failure', action: 'admin-password', src: 'admin',
        name: admin.name, reason: 'bad_old_password', message: '修改密码时当前密码不正确' }));
      return sendJSON(res, { error: '当前密码不正确' }, 401, cors);
    }
    if (newPw.length < 8) return sendJSON(res, { error: '新密码至少 8 位' }, 400, cors);
    acc.salt = crypto.randomBytes(16).toString('hex');
    acc.passwordHash = hashPassword(newPw, acc.salt);
    Store.saveAdmins(ADMINS);
    addRecord(Object.assign({}, info, { type: 'success', action: 'admin-password', src: 'admin',
      name: admin.name, reason: 'ok', message: '修改管理员密码' }));
    return sendJSON(res, { ok: true }, 200, cors);
  }
  return sendJSON(res, { error: 'not found' }, 404, cors);
}
function saveRecords(list) {
  try {
    Store.clearRecords();
    (list || []).forEach((r) => Store.insertRecord(Object.assign({ resolved: 0 }, r)));
  } catch (err) { console.error('[warn] 无法写入授权记录：' + err.message); }
  recCache = null;
}

/* ------------------------------ 透明转发后端 ------------------------------- */

function proxyHeaders(req, identity, u) {
  const headers = Object.assign({}, req.headers);
  delete headers.authorization;         // 网关会话 token 不透给后端
  delete headers['x-auth-token'];
  delete headers['x-admin-token'];
  // 去掉浏览器的条件请求头：否则后端 304 会让手机继续拿缓存里的旧版 HTML
  // （旧的 <base href>/改写路径），换前缀或改独立端口后就全 404。
  delete headers['if-none-match'];
  delete headers['if-modified-since'];
  delete headers['if-unmodified-since'];
  delete headers['if-match'];
  delete headers['if-range'];
  const ck = String(headers.cookie || '').split(';')
    .filter((p) => p.trim() && !p.trim().startsWith(COOKIE + '=') && !p.trim().startsWith(ADMIN_COOKIE + '=')
      && !p.trim().startsWith(STICKY_COOKIE + '='))
    .join(';').trim();
  if (ck) headers.cookie = ck; else delete headers.cookie;
  if (identity) {
    headers['x-auth-user'] = encodeURIComponent(identity.name || identity.sub || '');
    headers['x-auth-openid'] = encodeURIComponent(String(identity.sub || ''));
    headers['x-auth-unionid'] = encodeURIComponent(String(identity.unionid || ''));
  }
  headers['x-gateway-route'] = encodeURIComponent(u.name);
  return headers;
}
/** 页面/样式强制不缓存：典型受害场景是手机缓存了旧版（带 base href / 改写路径）的 HTML，
 *  服务改成独立端口后旧缓存仍按 /static/... 之类去请求，导致只有手机端 404。 */
function noStorePageHeaders(hdrs, ct) {
  if (!/text\/html|text\/css/i.test(ct)) return;
  hdrs['cache-control'] = 'no-store, must-revalidate, max-age=0';
  delete hdrs['etag'];
  delete hdrs['last-modified'];
  delete hdrs['expires'];
  hdrs['pragma'] = 'no-cache';
}
function proxy(req, res, identity, u, reqUrl) {
  const headers = proxyHeaders(req, identity, u);
  const base = subpathOf(u);
  if (base) delete headers['accept-encoding'];   // 要在网关里改写，必须拿到明文
  const p = http.request({
    host: u.host, port: u.port, method: req.method, path: upstreamPath(reqUrl, u), headers,
  }, (r) => {
    const hdrs = Object.assign({}, r.headers, corsHeaders(req));
    if (base) {
      // 后端的 /login 之类重定向补上前缀，否则跳出子路径
      const loc = hdrs.location;
      if (loc && typeof loc === 'string' && loc.startsWith('/') && loc[1] !== '/') {
        hdrs.location = withBase(loc, base);
      }
      hdrs['set-cookie'] = withStickyCookie(hdrs['set-cookie'], u.id);
    }
    const ct = String(hdrs['content-type'] || '').toLowerCase();
    const enc = String(hdrs['content-encoding'] || '').toLowerCase();
    noStorePageHeaders(hdrs, ct);                // 页面/样式一律 no-store，避免手机复用旧缓存
    const needRewrite = !!base && req.method === 'GET'
      && !(r.statusCode === 204 || r.statusCode === 304)
      && !/gzip|br|deflate/.test(enc)          // 上游若强行压缩就不改写，避免解压出错
      && (ct.includes('text/html') || ct.includes('text/css'));
    if (!needRewrite) {
      res.writeHead(r.statusCode, hdrs);
      r.pipe(res);
      return;
    }
    const chunks = [];
    let size = 0, tooBig = false;
    r.on('data', (c) => {
      size += c.length;
      if (size > REWRITE_MAX) tooBig = true;
      chunks.push(c);
    });
    r.on('end', () => {
      let body = Buffer.concat(chunks).toString('utf8');
      if (!tooBig) body = ct.includes('text/css') ? rewriteCSS(body, base) : rewriteHTML(body, base);
      else console.error('[warn] 响应超过 ' + (REWRITE_MAX / 1048576) + 'MB，未做子路径改写：' + reqUrl.pathname);
      if (!res.headersSent) {
        const out = Buffer.from(body, 'utf8');
        delete hdrs['content-length'];
        delete hdrs['transfer-encoding'];
        delete hdrs['content-encoding'];
        hdrs['content-length'] = out.length;
        res.writeHead(r.statusCode, hdrs);
        res.end(out);
      } else res.end();
    });
    r.on('error', () => { if (!res.headersSent) res.end(); });
  });
  p.on('error', (err) => {
    console.error('[proxy]', u.name, req.method, req.url, err.message);
    if (!res.headersSent) sendHTML(res, serviceDownPage(u), 502, corsHeaders(req));
    else res.end();
  });
  req.pipe(p);
}
/** WebSocket / Upgrade 也按同一套白名单鉴权与转发规则处理 */
function proxyUpgrade(req, socket, head, reqUrl, forced) {
  const identity = AUTH_ON ? verifyToken(tokenFrom(req, reqUrl.searchParams)) : { sub: 'disabled', name: '' };
  if (!identity) { socket.end('HTTP/1.1 401 Unauthorized\r\n\r\n'); return; }
  const u = forced || matchByPrefix(req, reqUrl.pathname)
    || (reqUrl.pathname !== '/' ? stickyRoute(req) : null)
    || pickUpstream(req, reqUrl.pathname);
  if (!u) { socket.end('HTTP/1.1 503 Service Unavailable\r\n\r\n'); return; }
  const headers = proxyHeaders(req, identity, u);
  headers.connection = 'Upgrade';
  headers.upgrade = String(req.headers.upgrade || 'websocket');
  const conn = net.connect(u.port, u.host, () => {
    let raw = req.method + ' ' + upstreamPath(reqUrl, u) + ' HTTP/1.1\r\n';
    Object.keys(headers).forEach((k) => {
      const v = headers[k];
      if (Array.isArray(v)) v.forEach((x) => { raw += k + ': ' + x + '\r\n'; });
      else if (v !== undefined && v !== null) raw += k + ': ' + v + '\r\n';
    });
    conn.write(raw + '\r\n');
    if (head && head.length) conn.write(head);
    conn.pipe(socket);
    socket.pipe(conn);
  });
  conn.on('error', () => socket.destroy());
  socket.on('error', () => conn.destroy());
}

/* --------------------------------- 入口 ---------------------------------- */

/** 端口未分配（服务刚被删除 / 停用时可能命中） */
function portUnassignedPage(port) {
  return pageHTML('端口未分配', '<div class="d" style="background:rgba(255,180,84,.14);color:#ffb454">!</div>'
    + '<h1>该端口暂无服务</h1>'
    + '<p>网关的 ' + escapeHTML(String(port)) + ' 端口当前没有分配转发服务。</p>'
    + '<p style="margin-top:8px">可能是服务被删除或停用了，请到「网关管理端 → 转发服务」确认。</p>'
    + '<p class="links" style="margin-top:18px"><a href="/__auth/apps" style="color:#7aa7ff;font-size:13px">返回服务列表</a></p>');
}

async function handleRequest(req, res) {
  corsWrap(req, res);
  let reqUrl;
  try { reqUrl = new URL(req.url, 'http://' + (req.headers.host || 'localhost')); }
  catch (err) { res.writeHead(400); return res.end('400'); }
  const pathname = reqUrl.pathname;
  const params = reqUrl.searchParams;
  const localPort = Number((req.socket && req.socket.localPort) || LISTEN_PORT);
  const isMainPort = localPort === LISTEN_PORT;
  const portRoute = isMainPort ? null : routeForPort(localPort);

  if (CFG.corsOrigins.length && req.method === 'OPTIONS') {
    res.writeHead(204); return res.end();
  }

  try {
    // 登录 / 管理端在每个端口上都可用，这样任何端口上会话过期都能就地重新登录
    if (pathname.startsWith('/__auth/admin')) return await handleAdmin(req, res, pathname, params);
    if (pathname.startsWith(PREFIX)) return await handleAuth(req, res, pathname, params);

    const identity = AUTH_ON ? verifyToken(tokenFrom(req, params)) : { sub: 'disabled', name: '' };

    // 小程序 web-view：?token= 换 cookie，并立刻把 token 从地址栏抹掉，避免留在历史记录里
    if (TOKEN_QUERY && params.get('token') && identity) {
      const queryToken = params.get('token');
      params.delete('token');
      res.writeHead(302, Object.assign({
        Location: pathname + (params.toString() ? '?' + params.toString() : ''),
        'Set-Cookie': cookieHeader(queryToken),
        'Cache-Control': 'no-store',
      }, corsHeaders(req)));
      return res.end();
    }

    // ── 独立端口模式：整站转发，路径原样不动，服务看到的就是自己根下的地址 ──
    if (!isMainPort) {
      if (!portRoute) return sendHTML(res, portUnassignedPage(localPort), 404, corsHeaders(req));
      if (!identity) {
        if (pathname.includes('/api/') || pathname === '/api' || pathname.endsWith('/api')) {
          return sendJSON(res, { error: 'unauthorized', code: 'AUTH_REQUIRED',
            loginUrl: PREFIX + 'login' }, 401, corsHeaders(req));
        }
        res.writeHead(302, Object.assign({
          Location: PREFIX + 'login?next=' + encodeURIComponent(req.url),
          'Cache-Control': 'no-store',
        }, corsHeaders(req)));
        return res.end();
      }
      return proxy(req, res, identity, portRoute, reqUrl);
    }

    // ── 主端口：管理端 / 门户 / 路径前缀转发 ──
    if (identity) {
      if (pathname === '/' && CFG.rootMode !== 'proxy') {
        const goId = String(params.get('__gw_go') || '');
        if (goId) {
          syncRoutesFromDisk();            // 显式入口按最新路由表解析
          const direct = UPSTREAMS.find((x) => x && x.id === goId && x.prefix === '/');
          if (direct) {
            params.delete('__gw_go');
            return proxy(req, res, identity, direct, reqUrl);
          }
        }
        return sendHTML(res, await appsPage(req), 200, corsHeaders(req));
      }
      const byPrefix = matchByPrefix(req, pathname);
      // 子路径服务补斜杠：/media → /media/，否则页面里的相对路径会解析到根上
      if (byPrefix && pathname === byPrefix.prefix
        && (req.method === 'GET' || req.method === 'HEAD')) {
        res.writeHead(302, Object.assign({
          Location: byPrefix.prefix + '/' + (reqUrl.search || ''),
          'Cache-Control': 'no-store',
        }, corsHeaders(req)));
        return res.end();
      }
      // 顺序很关键：显式前缀 → 上次访问的子路径服务（接管 /assets、/favicon.ico 等绝对请求）→ 兜底
      const u = byPrefix
        || (pathname !== '/' ? stickyRoute(req) : null)
        || pickUpstream(req, pathname);
      if (!u) {
        return sendJSON(res, { error: '暂无可用的转发服务，请在网关管理端「转发服务」中添加',
          code: 'NO_UPSTREAM' }, 503, corsHeaders(req));
      }
      return proxy(req, res, identity, u, reqUrl);
    }

    if (pathname.includes('/api/') || pathname === '/api' || pathname.endsWith('/api')) {
      return sendJSON(res, { error: 'unauthorized', code: 'AUTH_REQUIRED',
        loginUrl: PREFIX + 'login' }, 401, corsHeaders(req));
    }
    res.writeHead(302, Object.assign({
      Location: PREFIX + 'login?next=' + encodeURIComponent(req.url),
      'Cache-Control': 'no-store',
    }, corsHeaders(req)));
    res.end();
  } catch (err) {
    console.error('[error]', pathname, err.message);
    if (!res.headersSent) res.writeHead(500, { 'Content-Type': 'text/plain; charset=utf-8' });
    res.end('500 网关内部错误');
  }
}

function handleUpgrade(req, socket, head) {
  const reqUrl = new URL(req.url, 'http://' + (req.headers.host || 'localhost'));
  const localPort = Number((req.socket && req.socket.localPort) || LISTEN_PORT);
  const forced = localPort === LISTEN_PORT ? null : routeForPort(localPort);
  proxyUpgrade(req, socket, head, reqUrl, forced);
}

/** 端口 → 监听实例（独立端口服务各占一个端口） */
const listeners = new Map();

/** 让实际监听的端口集合与路由表保持一致：新增即绑定、删除即释放 */
function syncListeners() {
  const want = new Set(assignedPorts());
  Array.from(listeners.keys()).forEach((port) => {
    if (want.has(port)) return;
    try { listeners.get(port).close(); } catch (err) { /* 已关闭 */ }
    listeners.delete(port);
    console.log('  [端口] ' + port + ' 已释放');
  });
  want.forEach((port) => {
    if (listeners.has(port)) return;
    const srv = http.createServer(handleRequest);
    srv.on('upgrade', (req, socket, head) => {
      try { handleUpgrade(req, socket, head); } catch (err) { socket.destroy(); }
    });
    srv.on('clientError', (err, socket) => {
      if (socket.writable) socket.end('HTTP/1.1 400 Bad Request\r\n\r\n');
    });
    srv.on('error', (err) => {
      console.error('  [端口] ' + port + ' 监听失败：' + err.message
        + (err.code === 'EADDRINUSE' ? '（该端口已被其它程序占用）' : ''));
      if (listeners.get(port) === srv) listeners.delete(port);
      try { srv.close(); } catch (e) { /* ignore */ }
    });
    srv.listen(port, String(CFG.host || '0.0.0.0'), () => {
      const r = routeForPort(port);
      console.log('  [端口] ' + port + ' → ' + (r ? r.name + '  后端 ' + r.host + ':' + r.port : '(未分配)'));
    });
    listeners.set(port, srv);
  });
}

const server = http.createServer(handleRequest);

server.on('upgrade', (req, socket, head) => {
  try { handleUpgrade(req, socket, head); }
  catch (err) { socket.destroy(); }
});
server.on('clientError', (err, socket) => {
  if (socket.writable) socket.end('HTTP/1.1 400 Bad Request\r\n\r\n');
});
process.on('uncaughtException', (err) => console.error('[uncaught]', err && err.message || err));
process.on('unhandledRejection', (err) => console.error('[unhandled]', err && err.message || err));

// 数据全部存入 SQLite（gateway.db），无需释放白名单 / 记录模板文件

server.on('error', (err) => {
  console.error('网关启动失败：' + err.message);
  process.exitCode = 1;
});
server.listen(LISTEN_PORT, String(CFG.host || '0.0.0.0'), () => {
  console.log('');
  console.log('  鉴权网关已启动' + (AUTH_ON ? '' : '（鉴权已关闭，纯转发模式）'));
  console.log('  对外地址: http://' + CFG.host + ':' + LISTEN_PORT);
  console.log('  本机访问: http://localhost:' + LISTEN_PORT);
  const ip = lanIP();
  if (ip) console.log('  手机 / 小程序: http://' + ip + ':' + LISTEN_PORT + '  (公网需自行反代 HTTPS)');
  console.log('  主端口路径路由 (' + UPSTREAMS.length + ')（管理端可在线增改删，保存实时生效，无需重启）:');
  if (!UPSTREAMS.length) console.log('    （无，服务都走独立端口）');
  UPSTREAMS.forEach((u) => console.log('    - ' + u.name + '  ' + u.prefix
    + (u.hosts.length ? ' [Host: ' + u.hosts.join(',') + ']' : '')
    + ' → http://' + u.host + ':' + u.port
    + (u.stripPrefix ? (u.prefix === '/' ? ' (去前缀)' : ' (去前缀 + 子路径改写)') : '')));
  UPSTREAMS.filter((u) => u.prefix !== '/' && !u.stripPrefix).forEach((u) => {
    console.log('    ! ' + u.name + ' 挂在 ' + u.prefix + ' 但「保留前缀」：若后端不认该前缀会 404，'
      + '请在管理端改成「去掉前缀 + 子路径适配」');
  });
  // 独立端口服务：每个端口一个 server，路径原样转发，后端零改动
  if (PORT_ROUTES.size) console.log('  独立端口服务 (路径原样转发，后端零改动):');
  syncListeners();
  console.log('  登录方式: ' + MODE_LABEL + ' [' + MODE + ']');
  const wlNow = whitelist();
  console.log('  白名单: ' + DB_FILE + '（' + (MODE === 'device'
    ? '已授权设备 ' + wlNow.devices.filter((d) => d && d.enabled !== false).length
      + ' 台，待授权 ' + wlNow.devicePending.length + ' 台'
    : wlNow.users.filter((u) => u && u.enabled !== false).length + ' 人') + '，改完即时生效）');
  console.log('  授权记录: ' + DB_FILE + '（成功/失败均记录，含 IP 与归属地）');
  console.log('  IP 归属地: ' + (iploc.ready()
    ? iploc.count() + ' 个网段 · ' + (IPDB_EMBED ? 'exe 内嵌' : IPDB_FILE)
    : '未加载（记录里地区为空）'));
  console.log('  配置文件: ' + CONFIG_FILE + '（仅启动端口 / 监听地址；其余配置见管理端「系统配置」）');
  console.log('  管理端: http://localhost:' + LISTEN_PORT + '/__auth/admin  （管理员 ' + ADMINS.length + ' 个）');
  if (initialPasswords.length) {
    console.log('');
    console.log('  ===== 管理员初始密码（只显示这一次，已哈希写入数据库）=====');
    initialPasswords.forEach((a) => console.log('    账号: ' + a.username + '    密码: ' + a.password));
    console.log('  ================================================================');
  }
  if (generated.length) console.log('  已自动生成并写回配置: ' + generated.join(', '));
  console.log('  停止: Ctrl + C');
  console.log('');
  if (MODE === 'device') {
    console.log('  [设备登录模式] 微信登录入口已全部停用，不签发二维码、不处理回调、不接受小程序 code。');
    console.log('         设备首次访问会自动生成机器码并显示「无权访问」；管理员到「管理端 → 设备白名单」放行后，');
    console.log('         该设备刷新或等待自动重试即可进入。切回微信：在管理端「系统配置」页改登录模式，保存即时生效，无需重启。');
    console.log('');
  } else if (!WECHAT_OK) {
    console.log('  [重要] 尚未配置微信登录凭证，任何人都无法通过鉴权（不提供口令等绕过方式）。');
    console.log('         个人开发者推荐小程序码：微信公众平台注册「个人主体」小程序（身份证实名、0 元，');
    console.log('         不需要营业执照/公章/开发者认证），在管理端「系统配置」页填 miniAppId/miniAppSecret，');
    console.log('         把 miniprogram/pages/gate 拷进小程序并发布，保存后即时生效即可扫码登录（无需 publicBaseUrl、无需重启）。');
    console.log('         企业另有 open（开放平台网站应用：需资质认证 + 《网站信息登记表》盖章）与 mp（认证服务号），');
    console.log('         这两种必须设置 publicBaseUrl（微信回调地址，需公网可访问）。');
    console.log('');
  }
  if (!ADMINS.length) {
    console.log('  [重要] 尚未配置管理员账号，管理端无法登录。请在配置文件的 admins 添加用户名（密码留空会生成初始密码），重启后迁入 gateway.db。');
    console.log('');
  }
  if (BAD_PORT) {
    console.log('  [严重] 端口 ' + LISTEN_PORT + ' 是浏览器禁用端口，Chrome/Edge/微信内置浏览器都无法访问；');
    console.log('         请把配置文件的 port 改成 5173 / 8080 / 8000 等普通端口后重启（端口改动需重启）。');
    console.log('');
  }

  // 若后端仍监听 0.0.0.0，外部可绕过网关直连，给出醒目提示
  if (ip) {
    UPSTREAMS.filter((u) => u.host === '127.0.0.1').forEach((u) => {
      const probe = http.get({ host: ip, port: u.port, path: '/', timeout: 900 }, (r) => {
        r.resume();
        console.log('  [安全提示] 后端 ' + u.name + ' 的端口 ' + u.port
          + ' 可从局域网直连，存在绕过风险；建议把服务端 config.json 的 host 改为 127.0.0.1。');
      });
      probe.on('error', () => { /* 只能本机访问，符合预期 */ });
      probe.on('timeout', () => probe.destroy());
    });
  }
});

process.on('SIGINT', () => { console.log('\n网关已停止'); process.exit(0); });
