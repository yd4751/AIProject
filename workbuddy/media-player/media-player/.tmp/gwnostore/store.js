'use strict';
/**
 * 鉴权网关的 SQLite 存储层。
 *
 * 设计原则（与用户需求一致）：
 *   - 仅「启动端口 / 监听地址」仍保留在 config.json（启动时只从这里读）；
 *   - 其余一切（转发服务、管理员、微信/设备白名单、待授权、授权记录、以及除端口外的所有配置项）
 *     全部落到 SQLite（gateway.db），不再写 JSON 数据文件。
 *
 * 兼容性：对外暴露的函数尽量与旧 JSON 实现同签名，gateway.js 调用点基本不变。
 * 首次运行会把同目录下的旧 gateway-config.json / whitelist.json / records.json（若有）迁移进库，
 * 其中 secret 一并迁入，避免已授权设备机器码失效。
 */
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const { DatabaseSync } = require('node:sqlite');

let db = null;
let ROOT = '';
let DB_PATH = '';
let IN_MEMORY = false;

// 这些配置项存入 kv 表（端口 / 监听地址除外，它们留在 config.json）
const MOVED_KEYS = ['enabled', 'mode', 'loginMode', 'publicBaseUrl', 'lanBaseUrl',
  'sessionHours', 'adminSessionHours', 'allowTokenInQuery', 'corsOrigins', 'landing', 'rootMode',
  'wechat', 'secret'];

function normPrefix(p) {
  let s = String(p || '/').trim();
  if (!s || s === '/') return '/';
  if (!s.startsWith('/')) s = '/' + s;
  return s.replace(/\/+$/, '');
}
function hostList(v) {
  const arr = Array.isArray(v) ? v : String(v || '').split(/[,\s，]+/);
  return arr.map((h) => String(h).trim().toLowerCase()).filter(Boolean);
}
function safeJSON(v, def) {
  try { const x = JSON.parse(v); return x === undefined || x === null ? def : x; } catch (e) { return def; }
}
function nowISO() { return new Date().toISOString(); }
function newId(n) { return (n || 'rec') + '_' + crypto.randomBytes(12).toString('hex'); }
function randomPassword(n = 14) {
  const abc = '23456789abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ';
  let s = '';
  const b = crypto.randomBytes(n);
  for (let i = 0; i < n; i++) s += abc[b[i] % abc.length];
  return s;
}
function hashPassword(pw, salt) { return crypto.scryptSync(String(pw), salt, 32).toString('hex'); }

function init(root) {
  ROOT = root;
  const envDb = process.env.GATEWAY_DB ? path.resolve(process.env.GATEWAY_DB) : path.join(ROOT, 'gateway.db');
  try {
    fs.mkdirSync(path.dirname(envDb), { recursive: true });
    db = new DatabaseSync(envDb, { allowExtension: false });
    DB_PATH = envDb;
  } catch (err) {
    console.error('[warn] 无法打开 SQLite 文件 ' + envDb + '（' + err.message + '），改用内存库（重启后数据不保留）');
    db = new DatabaseSync(':memory:');
    DB_PATH = ':memory:';
    IN_MEMORY = true;
  }
  db.exec('PRAGMA journal_mode = WAL;');
  db.exec('PRAGMA synchronous = NORMAL;');
  db.exec('PRAGMA foreign_keys = OFF;');
  db.exec(`CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v TEXT);`);
  db.exec(`CREATE TABLE IF NOT EXISTS routes (
    id TEXT PRIMARY KEY, name TEXT, prefix TEXT, host TEXT, port INTEGER,
    stripPrefix INTEGER DEFAULT 0, hosts TEXT DEFAULT '[]', enabled INTEGER DEFAULT 1, ord INTEGER DEFAULT 0,
    listenPort INTEGER DEFAULT 0);`);
  // 老库补列：listenPort = 网关对外监听端口（0 表示仍走「主端口 + 路径前缀」的旧模式）
  try {
    const cols = db.prepare('PRAGMA table_info(routes)').all().map((c) => c.name);
    if (!cols.includes('listenPort')) db.exec('ALTER TABLE routes ADD COLUMN listenPort INTEGER DEFAULT 0');
  } catch (err) { console.error('[warn] routes 表补列失败：' + err.message); }
  db.exec(`CREATE TABLE IF NOT EXISTS admins (
    username TEXT PRIMARY KEY, salt TEXT, passwordHash TEXT);`);
  db.exec(`CREATE TABLE IF NOT EXISTS wl_users (
    openid TEXT PRIMARY KEY, unionid TEXT DEFAULT '', name TEXT DEFAULT '', enabled INTEGER DEFAULT 1,
    note TEXT DEFAULT '', createdAt TEXT DEFAULT '', by TEXT DEFAULT '');`);
  db.exec(`CREATE TABLE IF NOT EXISTS wl_pending (
    openid TEXT PRIMARY KEY, unionid TEXT DEFAULT '', src TEXT DEFAULT '', at TEXT DEFAULT '');`);
  db.exec(`CREATE TABLE IF NOT EXISTS devices (
    code TEXT PRIMARY KEY, name TEXT DEFAULT '', enabled INTEGER DEFAULT 1, label TEXT DEFAULT '',
    createdAt TEXT DEFAULT '', by TEXT DEFAULT '', lastSeen TEXT DEFAULT '', meta TEXT DEFAULT '{}');`);
  db.exec(`CREATE TABLE IF NOT EXISTS device_pending (
    code TEXT PRIMARY KEY, at TEXT DEFAULT '', ts INTEGER DEFAULT 0, meta TEXT DEFAULT '{}');`);
  db.exec(`CREATE TABLE IF NOT EXISTS records (
    id TEXT PRIMARY KEY, at TEXT, type TEXT, action TEXT, src TEXT,
    openid TEXT, unionid TEXT, device TEXT, name TEXT, reason TEXT, message TEXT,
    errcode TEXT, errmsg TEXT, ip TEXT, region TEXT DEFAULT '', ua TEXT, ticket TEXT,
    resolved INTEGER DEFAULT 0, resolvedBy TEXT DEFAULT '', resolvedAt TEXT DEFAULT '', resolvedNote TEXT DEFAULT '');`);
  // 老库补列：region = IP 所属地区（离线库解析，历史记录读取时会自动回填）
  try {
    const rcols = db.prepare('PRAGMA table_info(records)').all().map((c) => c.name);
    if (!rcols.includes('region')) db.exec("ALTER TABLE records ADD COLUMN region TEXT DEFAULT ''");
  } catch (err) { console.error('[warn] records 表补列失败：' + err.message); }
  db.exec(`CREATE INDEX IF NOT EXISTS idx_records_at ON records(at);`);
  db.exec(`CREATE INDEX IF NOT EXISTS idx_records_keys ON records(openid, device);`);
  return { dbPath: DB_PATH, inMemory: IN_MEMORY };
}

function dbPath() { return DB_PATH; }
function isInMemory() { return IN_MEMORY; }

/* --------------------------------- helpers --------------------------------- */
function has(k) { return !!db.prepare('SELECT 1 FROM kv WHERE k=?').get(k); }
function getKV(k, def) {
  const r = db.prepare('SELECT v FROM kv WHERE k=?').get(k);
  if (!r) return def;
  try { return JSON.parse(r.v); } catch (e) { return r.v; }
}
function setKV(k, v) {
  db.prepare('INSERT INTO kv(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v')
    .run(k, JSON.stringify(v));
}
function tableHasRows(t) { return !!(db.prepare('SELECT 1 FROM ' + t + ' LIMIT 1').get()); }

/* --------------------------------- config --------------------------------- */
function getConfig() {
  const out = {};
  for (const k of MOVED_KEYS) if (has(k)) out[k] = getKV(k);
  return out;
}
function setConfig(patch) {
  for (const k of Object.keys(patch)) if (MOVED_KEYS.includes(k)) setKV(k, patch[k]);
}
function getSecret() { return getKV('secret', ''); }
function setSecret(s) { setKV('secret', s); }

/* --------------------------------- routes --------------------------------- */
function rowToRoute(r) {
  return { id: r.id, name: r.name, prefix: r.prefix, host: r.host, port: r.port,
    listenPort: Number(r.listenPort) || 0,
    stripPrefix: !!r.stripPrefix, hosts: safeJSON(r.hosts, []), enabled: r.enabled !== 0 };
}
function normalizeRouteSeed(u) {
  return {
    id: u.id || newId('r'),
    name: String(u.name || '服务'),
    prefix: normPrefix(u.prefix || '/'),
    host: String(u.host || '127.0.0.1').trim(),
    port: Number(u.port) || 6001,
    listenPort: Number(u.listenPort) || 0,
    stripPrefix: !!u.stripPrefix,
    hosts: hostList(u.hosts),
    enabled: u.enabled !== false,
  };
}
function listRoutes() {
  return db.prepare('SELECT id,name,prefix,host,port,listenPort,stripPrefix,hosts,enabled,ord FROM routes ORDER BY ord, id')
    .all().map(rowToRoute);
}
function saveRoute(route) {
  const r = normalizeRouteSeed(route);
  db.prepare(`INSERT INTO routes(id,name,prefix,host,port,listenPort,stripPrefix,hosts,enabled,ord)
    VALUES(?,?,?,?,?,?,?,?,?,?)
    ON CONFLICT(id) DO UPDATE SET name=excluded.name,prefix=excluded.prefix,host=excluded.host,
      port=excluded.port,listenPort=excluded.listenPort,stripPrefix=excluded.stripPrefix,
      hosts=excluded.hosts,enabled=excluded.enabled,ord=excluded.ord`)
    .run(r.id, r.name, r.prefix, r.host, r.port, r.listenPort, r.stripPrefix ? 1 : 0,
      JSON.stringify(r.hosts), r.enabled ? 1 : 0, Number(route.ord) || 0);
}
function replaceRoutes(list) {
  db.exec('BEGIN');
  try {
    db.prepare('DELETE FROM routes').run();
    (list || []).forEach((u, i) => saveRoute(Object.assign({ ord: i }, u)));
    db.exec('COMMIT');
  } catch (e) { db.exec('ROLLBACK'); throw e; }
}
function setRouteEnabled(id, enabled) {
  db.prepare('UPDATE routes SET enabled=? WHERE id=?').run(enabled ? 1 : 0, id);
}
function deleteRoute(id) { db.prepare('DELETE FROM routes WHERE id=?').run(id); }

/* --------------------------------- admins --------------------------------- */
function listAdmins() {
  return db.prepare('SELECT username,salt,passwordHash FROM admins')
    .all().map((r) => ({ username: r.username, salt: r.salt, passwordHash: r.passwordHash }));
}
function saveAdmins(list) {
  db.exec('BEGIN');
  try {
    db.prepare('DELETE FROM admins').run();
    (list || []).forEach((a) => db.prepare('INSERT INTO admins(username,salt,passwordHash) VALUES(?,?,?)')
      .run(a.username, a.salt, a.passwordHash));
    db.exec('COMMIT');
  } catch (e) { db.exec('ROLLBACK'); throw e; }
}
function upsertAdmin(a) {
  db.prepare(`INSERT INTO admins(username,salt,passwordHash) VALUES(?,?,?)
    ON CONFLICT(username) DO UPDATE SET salt=excluded.salt, passwordHash=excluded.passwordHash`)
    .run(a.username, a.salt, a.passwordHash);
}

/* ------------------------------- white list ------------------------------ */
function listWlUsers() {
  return db.prepare('SELECT openid,unionid,name,enabled,note,createdAt,by FROM wl_users').all()
    .map((r) => ({ openid: r.openid, unionid: r.unionid, name: r.name, enabled: r.enabled !== 0,
      note: r.note, createdAt: r.createdAt, by: r.by }));
}
function upsertWlUser(u) {
  db.prepare(`INSERT INTO wl_users(openid,unionid,name,enabled,note,createdAt,by)
    VALUES(?,?,?,?,?,?,?)
    ON CONFLICT(openid) DO UPDATE SET unionid=excluded.unionid,name=excluded.name,
      enabled=excluded.enabled,note=excluded.note,createdAt=excluded.createdAt,by=excluded.by`)
    .run(u.openid, u.unionid || '', u.name || '', u.enabled === false ? 0 : 1, u.note || '', u.createdAt || '', u.by || '');
}
function setWlUserEnabled(openid, enabled) {
  db.prepare('UPDATE wl_users SET enabled=? WHERE openid=?').run(enabled ? 1 : 0, openid);
}
function removeWlUser(openid) { db.prepare('DELETE FROM wl_users WHERE openid=?').run(openid); }

function listPending() {
  return db.prepare('SELECT openid,unionid,src,at FROM wl_pending').all()
    .map((r) => ({ openid: r.openid, unionid: r.unionid, src: r.src, at: r.at }));
}
function addPending(p) {
  db.prepare(`INSERT INTO wl_pending(openid,unionid,src,at) VALUES(?,?,?,?)
    ON CONFLICT(openid) DO UPDATE SET unionid=excluded.unionid,src=excluded.src,at=excluded.at`)
    .run(p.openid, p.unionid || '', p.src || '', p.at || nowISO());
}
function removePending(openid) { db.prepare('DELETE FROM wl_pending WHERE openid=?').run(openid); }

function listDevices() {
  return db.prepare('SELECT code,name,enabled,label,createdAt,by,lastSeen,meta FROM devices').all()
    .map((r) => ({ code: r.code, name: r.name, enabled: r.enabled !== 0, label: r.label,
      createdAt: r.createdAt, by: r.by, lastSeen: r.lastSeen, meta: safeJSON(r.meta, {}) }));
}
function upsertDevice(d) {
  db.prepare(`INSERT INTO devices(code,name,enabled,label,createdAt,by,lastSeen,meta)
    VALUES(?,?,?,?,?,?,?,?)
    ON CONFLICT(code) DO UPDATE SET name=excluded.name,enabled=excluded.enabled,label=excluded.label,
      createdAt=excluded.createdAt,by=excluded.by,lastSeen=excluded.lastSeen,meta=excluded.meta`)
    .run(d.code, d.name || '', d.enabled === false ? 0 : 1, d.label || '', d.createdAt || '',
      d.by || '', d.lastSeen || '', JSON.stringify(d.meta || {}));
}
function setDeviceEnabled(code, enabled) {
  db.prepare('UPDATE devices SET enabled=? WHERE code=?').run(enabled ? 1 : 0, code);
}
function removeDevice(code) { db.prepare('DELETE FROM devices WHERE code=?').run(code); }

function listDevicePending() {
  return db.prepare('SELECT code,at,ts,meta FROM device_pending').all()
    .map((r) => ({ code: r.code, at: r.at, ts: r.ts, meta: safeJSON(r.meta, {}) }));
}
function addDevicePending(code, meta, at, ts) {
  db.prepare(`INSERT INTO device_pending(code,at,ts,meta) VALUES(?,?,?,?)
    ON CONFLICT(code) DO UPDATE SET at=excluded.at,ts=excluded.ts,meta=excluded.meta`)
    .run(code, at || nowISO(), Number(ts) || 0, JSON.stringify(meta || {}));
}
function removeDevicePending(code) { db.prepare('DELETE FROM device_pending WHERE code=?').run(code); }

/** 一次性保存四个白名单集合（兼容 whitelist() 返回的对象整体回写） */
function saveWhitelistCollections(users, pending, devices, devicePending) {
  db.exec('BEGIN');
  try {
    db.prepare('DELETE FROM wl_users').run();
    (users || []).forEach((u) => upsertWlUser(u));
    db.prepare('DELETE FROM wl_pending').run();
    (pending || []).forEach((p) => addPending(p));
    db.prepare('DELETE FROM devices').run();
    (devices || []).forEach((d) => upsertDevice(d));
    db.prepare('DELETE FROM device_pending').run();
    (devicePending || []).forEach((d) => addDevicePending(d.code, d.meta, d.at, d.ts));
    db.exec('COMMIT');
  } catch (e) { db.exec('ROLLBACK'); throw e; }
}

/* --------------------------------- records -------------------------------- */
function rowToRecord(r) {
  return {
    id: r.id, at: r.at, type: r.type, action: r.action, src: r.src,
    openid: r.openid, unionid: r.unionid, device: r.device, name: r.name,
    reason: r.reason, message: r.message, errcode: r.errcode, errmsg: r.errmsg,
    ip: r.ip, region: r.region || '', ua: r.ua, ticket: r.ticket,
    resolved: r.resolved ? 1 : 0, resolvedBy: r.resolvedBy, resolvedAt: r.resolvedAt, resolvedNote: r.resolvedNote,
  };
}
function listRecords(limit) {
  const rows = db.prepare('SELECT * FROM records ORDER BY rowid DESC').all();
  const recs = rows.map(rowToRecord);
  return limit ? recs.slice(0, limit) : recs;
}
function insertRecord(e) {
  db.prepare(`INSERT INTO records(id,at,type,action,src,openid,unionid,device,name,reason,message,
    errcode,errmsg,ip,region,ua,ticket,resolved,resolvedBy,resolvedAt,resolvedNote)
    VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
    .run(newId('rec'), e.at, e.type, e.action, e.src, e.openid, e.unionid, e.device, e.name,
      e.reason, e.message, e.errcode, e.errmsg, e.ip, e.region || '', e.ua, e.ticket,
      e.resolved ? 1 : 0, e.resolvedBy || '', e.resolvedAt || '', e.resolvedNote || '');
  return e;
}
function clearRecords() { db.prepare('DELETE FROM records').run(); }
function updateRecord(id, patch) {
  const allowed = ['type', 'action', 'src', 'openid', 'unionid', 'device', 'name', 'reason',
    'message', 'errcode', 'errmsg', 'ip', 'region', 'ua', 'ticket', 'resolved', 'resolvedBy', 'resolvedAt', 'resolvedNote'];
  const cols = Object.keys(patch || {}).filter((k) => allowed.includes(k));
  if (!cols.length) return;
  const sets = cols.map((c) => (c + '=?')).join(', ');
  const vals = cols.map((c) => (c === 'resolved' ? (patch[c] ? 1 : 0) : patch[c]));
  db.prepare('UPDATE records SET ' + sets + ' WHERE id=?').run(...vals.concat(id));
}
/** 找到最近一条「未 resolved 的失败记录」用于放行时原地更新（而非新增记录） */
function findUnresolvedFailure(key) {
  let row = null;
  if (key && key.openid) {
    row = db.prepare("SELECT id FROM records WHERE type='failure' AND resolved=0 AND openid=? ORDER BY rowid DESC LIMIT 1").get(key.openid);
  } else if (key && key.device) {
    row = db.prepare("SELECT id FROM records WHERE type='failure' AND resolved=0 AND device=? ORDER BY rowid DESC LIMIT 1").get(key.device);
  }
  return row ? row.id : null;
}

/* --------------------------------- migration ------------------------------ */
/**
 * 首次运行把旧数据（配置 / 白名单 / 记录）迁入 SQLite。幂等：表非空则跳过对应导入。
 * @param {{cfg?:object, whitelist?:object, records?:Array, defaults?:object}} src
 * @returns {{initialPasswords: Array<{username,password}>}}
 */
function seedFrom(src) {
  const cfg = (src && src.cfg) || {};
  const wl = (src && src.whitelist) || {};
  const recs = Array.isArray(src && src.records) ? src.records : [];
  const log = { initialPasswords: [] };

  // secret：优先沿用旧配置的 secret（保证已授权设备机器码不变），否则新生成
  if (!has('secret')) {
    const s = cfg.secret ? String(cfg.secret) : crypto.randomBytes(32).toString('hex');
    setKV('secret', s);
  }
  // 其余配置项从旧配置迁入（仅当库里还没有时）
  for (const k of MOVED_KEYS) {
    if (k === 'secret') continue;
    if (!has(k) && cfg[k] !== undefined && cfg[k] !== null) setKV(k, cfg[k]);
  }

  // 转发服务
  if (!tableHasRows('routes') && Array.isArray(cfg.upstreams)) {
    replaceRoutes(cfg.upstreams.map(normalizeRouteSeed));
  }
  // 管理员（明文密码自动转哈希；空密码生成初始密码）
  if (!tableHasRows('admins') && Array.isArray(cfg.admins)) {
    const out = [];
    for (const a of cfg.admins) {
      const username = String(a.username || '').trim();
      if (!username) continue;
      let salt = String(a.salt || '');
      let hash = String(a.passwordHash || '');
      if (!salt || !hash) {
        let pw = String(a.password || '');
        if (!pw) { pw = randomPassword(14); log.initialPasswords.push({ username, password: pw }); }
        salt = crypto.randomBytes(16).toString('hex');
        hash = hashPassword(pw, salt);
      }
      out.push({ username, salt, passwordHash: hash });
    }
    if (out.length) saveAdmins(out);
  }
  // 微信 / 设备白名单
  if (!tableHasRows('wl_users') && Array.isArray(wl.users)) {
    db.exec('BEGIN');
    try {
      (wl.users || []).forEach((u) => upsertWlUser({
        openid: String(u.openid || ''), unionid: String(u.unionid || ''), name: String(u.name || ''),
        enabled: u.enabled !== false, note: u.note || '', createdAt: u.createdAt || '', by: u.by || '' }));
      (wl.pending || []).forEach((p) => addPending(p));
      (wl.devices || []).forEach((d) => upsertDevice({
        code: String(d.code || ''), name: String(d.name || ''), enabled: d.enabled !== false,
        label: d.label || '', createdAt: d.createdAt || '', by: d.by || '', lastSeen: d.lastSeen || '', meta: d.meta || {} }));
      (wl.devicePending || []).forEach((d) => addDevicePending(d.code, d.meta, d.at, d.ts));
      db.exec('COMMIT');
    } catch (e) { db.exec('ROLLBACK'); throw e; }
  }
  // 授权记录（补 id）
  if (!tableHasRows('records') && recs.length) {
    db.exec('BEGIN');
    try {
      recs.forEach((r) => insertRecord(Object.assign({
        at: nowISO(), type: 'failure', action: '', src: '', openid: '', unionid: '', device: '',
        name: '', reason: '', message: '', errcode: '', errmsg: '', ip: '', ua: '', ticket: '',
        resolved: 0, resolvedBy: '', resolvedAt: '', resolvedNote: '',
      }, r)));
      db.exec('COMMIT');
    } catch (e) { db.exec('ROLLBACK'); throw e; }
  }
  return log;
}

module.exports = {
  init, dbPath, isInMemory,
  getConfig, setConfig, getSecret, setSecret,
  listRoutes, saveRoute, replaceRoutes, setRouteEnabled, deleteRoute,
  listAdmins, saveAdmins, upsertAdmin,
  listWlUsers, upsertWlUser, setWlUserEnabled, removeWlUser,
  listPending, addPending, removePending,
  listDevices, upsertDevice, setDeviceEnabled, removeDevice,
  listDevicePending, addDevicePending, removeDevicePending,
  saveWhitelistCollections,
  listRecords, insertRecord, clearRecords, updateRecord, findUnresolvedFailure,
  seedFrom,
  MOVED_KEYS,
};
