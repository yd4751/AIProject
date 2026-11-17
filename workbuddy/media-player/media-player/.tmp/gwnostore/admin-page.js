'use strict';
/* 网关管理端单页（内联字符串，便于 exe 单文件分发）。
   独立账号登录 → 概览 / 授权记录 / 白名单 / 转发服务（在线增改删，保存即生效）。 */
module.exports = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>网关管理端</title>
<style>
  * { box-sizing: border-box; }
  body { margin:0; background:#0d1117; color:#e6edf3; font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif; }
  a { color:#7aa7ff; text-decoration:none; cursor:pointer; }
  .wrap { max-width:1100px; margin:0 auto; padding:26px 18px 50px; }
  header { display:flex; align-items:center; gap:12px; margin-bottom:20px; flex-wrap:wrap; }
  header h1 { font-size:19px; margin:0; font-weight:600; }
  header .sp { flex:1; }
  .muted { color:#8b98a9; font-size:12px; }
  .card { background:#151b23; border:1px solid #263140; border-radius:14px; padding:18px; margin-bottom:16px; }
  .stats { display:grid; grid-template-columns:repeat(auto-fit,minmax(140px,1fr)); gap:12px; margin-bottom:16px; }
  .stat { background:#151b23; border:1px solid #263140; border-radius:12px; padding:15px 16px; }
  .stat b { display:block; font-size:24px; font-weight:600; line-height:1.2; }
  .stat span { color:#8b98a9; font-size:11.5px; }
  .tabs { display:flex; gap:6px; flex-wrap:wrap; margin-bottom:14px; }
  .tabs button { background:transparent; border:1px solid #263140; color:#8b98a9; border-radius:9px;
    padding:7px 14px; font-size:13px; cursor:pointer; }
  .tabs button.on { background:#4c8dff; border-color:#4c8dff; color:#fff; }
  table { width:100%; border-collapse:collapse; font-size:12.5px; }
  th,td { text-align:left; padding:9px 10px; border-bottom:1px solid #212b38; vertical-align:top; }
  th { color:#8b98a9; font-weight:500; font-size:11.5px; white-space:nowrap; }
  td code { color:#9ecbff; word-break:break-all; font-size:11.5px; }
  .tag { display:inline-block; padding:1px 7px; border-radius:20px; font-size:11px; }
  .tag.ok { background:rgba(61,220,151,.14); color:#3ddc97; }
  .tag.no { background:rgba(255,123,123,.14); color:#ff7b7b; }
  .tag.off { background:rgba(139,152,169,.14); color:#8b98a9; }
  .tag.loc { background:rgba(122,167,255,.12); color:#7aa7ff; margin-top:3px; }
  button.act { background:#22304a; border:1px solid #33456a; color:#9ecbff; border-radius:7px;
    padding:4px 9px; font-size:11.5px; cursor:pointer; margin:1px 3px 1px 0; }
  button.act.danger { background:rgba(255,123,123,.1); border-color:rgba(255,123,123,.35); color:#ff9b9b; }
  button.main { background:#4c8dff; border:0; color:#fff; border-radius:9px; padding:9px 16px; font-size:13px; cursor:pointer; }
  input, select { background:#0d1117; border:1px solid #263140; border-radius:8px; color:#e6edf3;
    padding:8px 10px; font-size:12.5px; outline:none; }
  input:focus, select:focus { border-color:#4c8dff; }
  .row { display:flex; gap:8px; flex-wrap:wrap; align-items:center; margin-bottom:12px; }
  .row input[type=text] { flex:1; min-width:180px; }
  .login { max-width:380px; margin:12vh auto; }
  .login h1 { font-size:18px; margin:0 0 4px; }
  .login p { color:#8b98a9; font-size:12.5px; margin:0 0 18px; }
  .login input { width:100%; margin-bottom:10px; padding:11px 12px; font-size:14px; }
  .login button { width:100%; }
  .msg { min-height:20px; font-size:12.5px; margin-top:10px; color:#ff9b9b; }
  .hidden { display:none !important; }
  .empty { color:#5f6c7d; font-size:12.5px; padding:16px 4px; }
  .scroll { overflow-x:auto; }
  .config-grid { display:grid; grid-template-columns:repeat(auto-fit,minmax(300px,1fr)); gap:14px; }
  fieldset { border:1px solid #263140; border-radius:12px; padding:12px 14px; margin:0; }
  legend { color:#8b98a9; font-size:12px; padding:0 6px; }
  .config-grid label { display:block; font-size:12.5px; color:#8b98a9; margin:10px 0 4px; }
  .config-grid input, .config-grid select { width:100%; }
  .config-grid label.check { display:flex; gap:8px; align-items:center; color:#e6edf3; margin-top:10px; }
</style>
</head>
<body>
<div id="login" class="card login hidden">
  <h1>网关管理端</h1>
  <p>请使用 config.json 中配置的管理员账号登录</p>
  <input id="u" type="text" placeholder="管理员账号" autocomplete="username">
  <input id="p" type="password" placeholder="密码" autocomplete="current-password">
  <button class="main" id="loginBtn">登录</button>
  <div class="msg" id="loginMsg"></div>
</div>

<div id="app" class="wrap hidden">
  <header>
    <h1>网关管理端</h1>
    <span class="muted" id="who"></span>
    <span class="sp"></span>
    <a id="reload">刷新</a> · <a href="/__auth/admin/logout">退出登录</a>
  </header>

  <div class="stats" id="stats"></div>

  <div class="tabs">
    <button data-tab="records" class="on">授权记录</button>
    <button data-tab="whitelist">微信白名单</button>
    <button data-tab="devices">设备白名单</button>
    <button data-tab="routes">转发服务</button>
    <button data-tab="config">系统配置</button>
    <button data-tab="about">关于 / 配置</button>
  </div>

  <div class="card" id="tab-records">
    <div class="row">
      <select id="rtype"><option value="all">全部</option><option value="failure">仅失败</option>
        <option value="success">仅成功</option></select>
      <input type="text" id="rq" placeholder="搜索 openid / 原因 / 来源 / IP / 地区">
      <button class="act" id="rsearch">筛选</button>
      <span class="sp" style="flex:1"></span>
      <button class="act danger" id="rclear">清空记录</button>
    </div>
    <div class="scroll"><table><thead><tr><th>时间</th><th>结果</th><th>来源</th>
      <th>用户 / openid</th><th>IP / 地区</th><th>原因</th><th>说明</th><th>操作</th></tr></thead>
      <tbody id="records"></tbody></table></div>
    <div class="empty" id="recordsEmpty"></div>
  </div>

  <div class="card hidden" id="tab-whitelist">
    <div class="row">
      <input type="text" id="newOid" placeholder="微信 openid">
      <input type="text" id="newName" placeholder="备注名（可选）">
      <button class="main" id="addBtn">加入白名单</button>
    </div>
    <div class="scroll"><table><thead><tr><th>备注</th><th>openid</th><th>unionid</th>
      <th>状态</th><th>操作</th></tr></thead><tbody id="users"></tbody></table></div>
    <div class="empty" id="usersEmpty"></div>
    <div class="muted" style="margin-top:12px">待授权申请（授权失败自动记录）：<span id="pending"></span></div>
  </div>

  <div class="card hidden" id="tab-devices">
    <div class="row">
      <input type="text" id="dvCode" placeholder="机器码 MC-XXXX-XXXX-XXXX-XXXX" style="flex:1;min-width:230px">
      <input type="text" id="dvName" placeholder="备注名（可选）">
      <button class="main" id="dvAllow">放行设备</button>
    </div>
    <div class="msg" id="dvMsg"></div>
    <div class="muted" style="margin:6px 0 10px">待授权设备（未授权访问自动记录）：</div>
    <div class="scroll"><table><thead><tr><th>机器码</th><th>设备信息</th>
      <th>最近申请</th><th>操作</th></tr></thead><tbody id="dvPending"></tbody></table></div>
    <div class="empty" id="dvPendingEmpty"></div>
    <div class="muted" style="margin:16px 0 8px">已授权设备：</div>
    <div class="scroll"><table><thead><tr><th>备注</th><th>机器码</th><th>状态</th>
      <th>最近访问 / IP</th><th>操作</th></tr></thead><tbody id="dvList"></tbody></table></div>
    <div class="empty" id="dvEmpty"></div>
    <div class="muted" style="margin-top:12px">机器码由「持久设备 ID + 设备指纹」在网关加盐生成；放行后该设备刷新页面即自动进入。停用 / 删除后已签发的会话立即失效。切换登录模式请在「系统配置」页操作，保存即时生效，无需重启。</div>
  </div>

  <div class="card hidden" id="tab-routes">
    <div class="row">
      <input type="text" id="rtName" placeholder="服务名，如：影音播放器" style="flex:1;min-width:150px">
      <input type="text" id="rtListen" placeholder="对外端口 如 7001（推荐）" style="width:180px">
      <input type="text" id="rtPrefix" placeholder="路径前缀（可选，如 /demo）" style="flex:1;min-width:150px">
      <input type="text" id="rtHost" placeholder="目标主机 127.0.0.1" style="flex:1;min-width:140px">
      <input type="text" id="rtPort" placeholder="后端端口" style="width:100px">
    </div>
    <div class="row">
      <select id="rtStrip"><option value="false">保留前缀</option><option value="true">去掉前缀 + 子路径适配</option></select>
      <select id="rtEnabled"><option value="true">启用</option><option value="false">停用</option></select>
      <input type="text" id="rtHosts" placeholder="Host 限定（可选，逗号分隔）" style="flex:1;min-width:180px">
      <button class="main" id="rtSave">新增服务</button>
      <button class="act hidden" id="rtCancel">取消编辑</button>
    </div>
    <div class="msg" id="rtMsg"></div>
    <div class="scroll"><table><thead><tr><th>服务名</th><th>入口</th><th>转发目标</th>
      <th>子路径适配</th><th>Host 限定</th><th>状态</th><th>操作</th></tr></thead>
      <tbody id="routes"></tbody></table></div>
    <div class="empty" id="routesEmpty"></div>
    <div class="muted" style="margin-top:12px">新增 / 修改 / 删除 / 启停 <b>立即生效，无需重启</b>，并存于同目录
      <code id="rtFile">gateway.db</code>。授权用户登录后会看到这里已启用的服务列表。</div>
    <div class="muted" style="margin-top:10px">推荐填「<b>对外端口</b>」：网关会单独监听该端口，请求按 <b>原路径</b> 整个转发给后端，
      服务始终以为自己就在根路径下，<b>后端零改动、无任何路径改写</b>。留空则退回「主端口 + 路径前缀」模式
      （如 <code>/media</code>），这种情况需选「去掉前缀 + 子路径适配」，且 JS 拼出的地址可能需要兜底。</div>
  </div>

  <div class="card hidden" id="tab-config">
    <div class="muted" style="margin-bottom:14px">以下配置全部存入同目录 <code id="cfgDb">gateway.db</code>，保存后立即生效，无需重启网关；仅「监听端口 / 监听地址」保留在 config.json，需改文件并重启。</div>
    <div class="config-grid">
      <fieldset>
        <legend>启动配置（config.json，需重启生效）</legend>
        <label>监听端口</label>
        <input type="text" id="cfgPort" disabled>
        <label>监听地址</label>
        <input type="text" id="cfgHost" disabled>
      </fieldset>
      <fieldset>
        <legend>登录与访问</legend>
        <label>登录模式</label>
        <select id="cfgLoginMode">
          <option value="wechat">微信扫码登录</option>
          <option value="device">设备登录（机器码）</option>
        </select>
        <label>微信登录方式（auto 按已配凭证自动选）</label>
        <select id="cfgMode">
          <option value="auto">自动</option>
          <option value="wechat-open">开放平台扫码（需 publicBaseUrl）</option>
          <option value="wechat-mp">服务号网页授权（需 publicBaseUrl）</option>
          <option value="wechat-mini">小程序码（个人主体即可）</option>
        </select>
        <label>对外访问地址 publicBaseUrl</label>
        <input type="text" id="cfgPublic" placeholder="https://auth.example.com" autocomplete="off">
        <label>局域网访问地址 lanBaseUrl（可选）</label>
        <input type="text" id="cfgLan" placeholder="http://192.168.x.x:PORT" autocomplete="off">
        <label>授权后落地页 landing（可选，留空跳服务列表）</label>
        <input type="text" id="cfgLanding" placeholder="留空则跳转到服务列表" autocomplete="off">
        <label>访问根路径 / 时</label>
        <select id="cfgRootMode">
          <option value="portal">显示服务列表（推荐，后端没启动也不会先弹错误页）</option>
          <option value="proxy">直接转发到挂在 / 的服务（旧行为）</option>
        </select>
      </fieldset>
      <fieldset>
        <legend>会话与安全</legend>
        <label>用户会话时长（小时）</label>
        <input type="number" id="cfgSession" min="1" step="1">
        <label>管理员会话时长（小时）</label>
        <input type="number" id="cfgAdminSession" min="1" step="1">
        <label class="check"><input type="checkbox" id="cfgTokenQuery"> 允许在 URL 查询参数中携带 token（?token=…）</label>
        <label>CORS 允许来源（逗号分隔，可选）</label>
        <input type="text" id="cfgCors" placeholder="https://a.com,https://b.com" autocomplete="off">
      </fieldset>
      <fieldset>
        <legend>微信凭证</legend>
        <label>服务号 AppId（mpAppId）</label>
        <input type="text" id="cfgMpAppId" placeholder="wx… 留空不启用" autocomplete="off">
        <label>服务号 AppSecret（mpAppSecret）</label>
        <input type="password" id="cfgMpSecret" autocomplete="new-password">
        <label>开放平台 AppId（openAppId，网站应用）</label>
        <input type="text" id="cfgOpenAppId" placeholder="留空不启用" autocomplete="off">
        <label>开放平台 AppSecret（openAppSecret）</label>
        <input type="password" id="cfgOpenSecret" autocomplete="new-password">
        <label>小程序 AppId（miniAppId）</label>
        <input type="text" id="cfgMiniAppId" placeholder="个人主体小程序即可" autocomplete="off">
        <label>小程序 AppSecret（miniAppSecret）</label>
        <input type="password" id="cfgMiniSecret" autocomplete="new-password">
        <label>小程序落地页 miniPage</label>
        <input type="text" id="cfgMiniPage" placeholder="/pages/gate/gate" autocomplete="off">
        <label>小程序环境 miniEnv</label>
        <select id="cfgMiniEnv"><option value="release">release</option><option value="trial">trial</option><option value="develop">develop</option></select>
        <div class="muted" style="margin-top:8px;font-size:11.5px">密钥框留空表示不修改已保存的值；填入新值即覆盖。凭证改动保存后即时生效，无需重启。</div>
      </fieldset>
    </div>
    <div class="row" style="margin-top:16px">
      <button class="main" id="cfgSave">保存配置</button>
      <span class="msg" id="cfgMsg"></span>
    </div>
  </div>

  <div class="card hidden" id="tab-about">
    <div id="about"></div>
    <div class="row" style="margin-top:18px;border-top:1px solid #212b38;padding-top:16px">
      <input type="password" id="oldPw" placeholder="当前密码">
      <input type="password" id="newPw" placeholder="新密码（至少 8 位）">
      <button class="main" id="pwBtn">修改密码</button>
    </div>
    <div class="msg" id="pwMsg"></div>
  </div>
</div>

<script>
(function () {
  var $ = function (id) { return document.getElementById(id); };
  var esc = function (s) { return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]; }); };
  var fmt = function (s) { try { return new Date(s).toLocaleString('zh-CN', { hour12: false }); }
    catch (e) { return s || ''; } };

  function api(url, opt) {
    // 统一去掉前导斜杠，避免拼出 /api//xxx 这种 404 路径
    var clean = String(url || '').replace(/^\\/+/, '');
    return fetch('/__auth/admin/api/' + clean, Object.assign({ credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' } }, opt || {}))
      .then(function (r) { return r.json().then(function (d) { return { status: r.status, data: d }; }); });
  }
  function post(url, body) {
    return api(url, { method: 'POST', body: JSON.stringify(body || {}) });
  }

  function showLogin(msg) {
    $('login').classList.remove('hidden'); $('app').classList.add('hidden');
    if (msg) $('loginMsg').textContent = msg;
  }
  function doLogin() {
    $('loginMsg').textContent = '';
    fetch('/__auth/admin/login', { method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: $('u').value.trim(), password: $('p').value }) })
      .then(function (r) { return r.json(); }).then(function (d) {
        if (d.ok) { boot(); } else { $('loginMsg').textContent = d.error || '登录失败'; $('p').value = ''; }
      }).catch(function () { $('loginMsg').textContent = '网络错误，请重试'; });
  }
  $('loginBtn').onclick = doLogin;
  $('p').addEventListener('keydown', function (e) { if (e.key === 'Enter') doLogin(); });
  $('u').addEventListener('keydown', function (e) { if (e.key === 'Enter') doLogin(); });

  function statCard(n, label) { return '<div class="stat"><b>' + n + '</b><span>' + label + '</span></div>'; }
  function loadOverview() {
    api('overview').then(function (r) {
      if (!r.data.ok) return;
      var d = r.data;
      $('stats').innerHTML = statCard(d.activeUsers, '已启用微信用户')
        + statCard(d.pending, '待授权申请') + statCard(d.failures, '授权失败记录')
        + statCard(d.successes, '授权成功记录') + statCard(d.todaySuccess, '今日成功授权')
        + statCard(d.activeDevices || 0, '已授权设备') + statCard(d.devicePending || 0, '待授权设备');
    });
  }
  function loadRecords() {
    var q = 'records?type=' + encodeURIComponent($('rtype').value)
      + '&q=' + encodeURIComponent($('rq').value.trim());
    api(q).then(function (r) {
      var list = (r.data && r.data.records) || [];
      $('recordsEmpty').textContent = list.length ? '' : '暂无授权记录';
      $('records').innerHTML = list.map(function (x) {
        var target = x.device || x.openid || '';
        var isDev = !!x.device;
        var resolved = x.resolved ? 1 : 0;
        var statusTag = x.type === 'success' ? '<span class="tag ok">成功</span>'
          : (resolved ? '<span class="tag ok">已解决</span>' : '<span class="tag no">失败</span>');
        var allow = (x.type === 'failure' && target && !resolved)
          ? '<button class="act" data-allow="' + esc(target) + '" data-kind="'
            + (isDev ? 'device' : 'wechat') + '">放行</button>' : '';
        return '<tr><td style="white-space:nowrap">' + esc(fmt(x.at)) + '</td>'
          + '<td>' + statusTag + '</td>'
          + '<td>' + esc(x.src) + '</td>'
          + '<td><code>' + esc(x.device || x.openid || x.name || '-') + '</code>'
          + (x.name && x.openid ? '<br><span class="muted">' + esc(x.name) + '</span>' : '') + '</td>'
          + '<td style="white-space:nowrap">'
          + (x.ip ? '<code>' + esc(x.ip) + '</code>' : '<span class="muted">-</span>')
          + (x.region ? '<br><span class="tag loc">' + esc(x.region) + '</span>' : '')
          + '</td>'
          + '<td>' + esc(x.reason) + (x.errcode !== '' && x.errcode != null
            ? '<br><span class="muted">errcode ' + esc(x.errcode) + '</span>' : '') + '</td>'
          + '<td>' + esc(x.message || x.errmsg || '')
          + (resolved ? '<br><span class="muted">已解决：' + esc(x.resolvedBy || '') + ' · '
            + esc(fmt(x.resolvedAt)) + (x.resolvedNote ? ' · ' + esc(x.resolvedNote) : '') + '</span>' : '')
          + '</td>'
          + '<td style="white-space:nowrap">' + allow + '</td></tr>';
      }).join('');
      Array.prototype.forEach.call($('records').querySelectorAll('[data-allow]'), function (b) {
        b.onclick = function () {
          var v = b.getAttribute('data-allow');
          var isDev = b.getAttribute('data-kind') === 'device';
          post(isDev ? 'devices/allow' : 'allow',
            isDev ? { code: v } : { openid: v, name: '手动放行' }).then(function () {
            loadRecords(); loadWhitelist(); loadDevices(); loadOverview();
          });
        };
      });
    });
  }
  function loadWhitelist() {
    api('whitelist').then(function (r) {
      var d = r.data || {}, list = d.users || [];
      $('usersEmpty').textContent = list.length ? '' : '白名单为空，扫码后非白名单微信用户会提示「无权访问」并自动记入待授权';
      $('users').innerHTML = list.map(function (u) {
        return '<tr><td>' + esc(u.name || '-') + '</td><td><code>' + esc(u.openid) + '</code></td>'
          + '<td><code>' + esc(u.unionid || '-') + '</code></td>'
          + '<td><span class="tag ' + (u.enabled === false ? 'off' : 'ok') + '">'
          + (u.enabled === false ? '已停用' : '生效中') + '</span></td>'
          + '<td style="white-space:nowrap"><button class="act" data-toggle="' + esc(u.openid) + '">'
          + (u.enabled === false ? '启用' : '停用') + '</button>'
          + '<button class="act danger" data-del="' + esc(u.openid) + '">删除</button></td></tr>';
      }).join('');
      $('pending').textContent = (d.pending || []).length
        ? (d.pending || []).map(function (p) { return p.openid; }).join('、') : '无';
      Array.prototype.forEach.call($('users').querySelectorAll('[data-del]'), function (b) {
        b.onclick = function () {
          if (!confirm('确认删除该微信用户？')) return;
          post('revoke', { openid: b.getAttribute('data-del'), remove: true }).then(function () {
            loadWhitelist(); loadOverview();
          });
        };
      });
      Array.prototype.forEach.call($('users').querySelectorAll('[data-toggle]'), function (b) {
        b.onclick = function () {
          var oid = b.getAttribute('data-toggle');
          api('whitelist/save', { method: 'POST', body: JSON.stringify({ users: list.map(function (u) {
            return u.openid === oid ? { openid: u.openid, unionid: u.unionid, name: u.name,
              enabled: u.enabled === false } : u;
          }) }) }).then(function () { loadWhitelist(); loadOverview(); });
        };
      });
    });
  }
  function deviceMsg(t, ok) {
    $('dvMsg').textContent = t || '';
    $('dvMsg').style.color = ok ? '#3ddc97' : '#ff9b9b';
  }
  function allowDevice(code, name) {
    deviceMsg('');
    post('devices/allow', { code: code, name: name || '' }).then(function (r) {
      if (!r.data.ok) { deviceMsg(r.data.error || '放行失败'); return; }
      deviceMsg('已放行 ' + code + '，该设备刷新后自动进入', true);
      $('dvCode').value = ''; $('dvName').value = '';
      loadDevices(); loadOverview(); loadRecords();
    });
  }
  function loadDevices() {
    api('devices').then(function (r) {
      var d = r.data || {}, list = d.devices || [], pend = d.pending || [];
      $('dvEmpty').textContent = list.length ? '' : '暂无已授权设备；设备访问登录页后会出现在下方待授权列表';
      $('dvPendingEmpty').textContent = pend.length ? '' : '暂无待授权设备';
      $('dvList').innerHTML = list.map(function (u) {
        var on = u.enabled !== false;
        return '<tr><td>' + esc(u.name || '-') + '</td>'
          + '<td><code>' + esc(u.code) + '</code></td>'
          + '<td><span class="tag ' + (on ? 'ok' : 'off') + '">' + (on ? '生效中' : '已停用') + '</span></td>'
          + '<td>' + esc(fmt(u.lastSeen || u.firstSeen || '-'))
          + '<br><span class="muted">' + esc(u.ip || '-') + '</span></td>'
          + '<td style="white-space:nowrap">'
          + '<button class="act" data-dvcopy="' + esc(u.code) + '">复制</button>'
          + '<button class="act" data-dvname="' + esc(u.code) + '">改备注</button>'
          + '<button class="act" data-dvtoggle="' + esc(u.code) + '" data-dven="' + (on ? '0' : '1') + '">'
          + (on ? '停用' : '启用') + '</button>'
          + '<button class="act danger" data-dvdel="' + esc(u.code) + '">删除</button></td></tr>';
      }).join('');
      $('dvPending').innerHTML = pend.map(function (p) {
        return '<tr><td><code>' + esc(p.code) + '</code></td>'
          + '<td>' + esc(p.label || p.ua || '-') + '</td>'
          + '<td>' + esc(fmt(p.at)) + '</td>'
          + '<td><button class="act" data-dvallow="' + esc(p.code) + '">放行</button></td></tr>';
      }).join('');
      Array.prototype.forEach.call($('dvPending').querySelectorAll('[data-dvallow]'), function (b) {
        b.onclick = function () { allowDevice(b.getAttribute('data-dvallow'), ''); };
      });
      Array.prototype.forEach.call($('dvList').querySelectorAll('[data-dvtoggle]'), function (b) {
        b.onclick = function () {
          var code = b.getAttribute('data-dvtoggle');
          var enable = b.getAttribute('data-dven') === '1';
          post(enable ? 'devices/allow' : 'devices/revoke', { code: code })
            .then(function () { loadDevices(); loadOverview(); });
        };
      });
      Array.prototype.forEach.call($('dvList').querySelectorAll('[data-dvdel]'), function (b) {
        b.onclick = function () {
          var code = b.getAttribute('data-dvdel');
          if (!confirm('确认删除设备「' + code + '」？该设备需要重新授权。')) return;
          post('devices/revoke', { code: code, remove: true })
            .then(function () { loadDevices(); loadOverview(); });
        };
      });
      Array.prototype.forEach.call($('dvList').querySelectorAll('[data-dvname]'), function (b) {
        b.onclick = function () {
          var code = b.getAttribute('data-dvname');
          var cur = (list.filter(function (u) { return u.code === code; })[0] || {}).name || '';
          var v = prompt('设备备注名', cur);
          if (v === null) return;
          post('devices/rename', { code: code, name: v.trim() })
            .then(function () { loadDevices(); });
        };
      });
      Array.prototype.forEach.call($('dvList').querySelectorAll('[data-dvcopy]'), function (b) {
        b.onclick = function () {
          if (navigator.clipboard) navigator.clipboard.writeText(b.getAttribute('data-dvcopy'));
          b.textContent = '已复制';
        };
      });
    });
  }

  var editingId = '';
  function routeMsg(text, ok) {
    $('rtMsg').textContent = text || '';
    $('rtMsg').style.color = ok ? '#3ddc97' : '#ff9b9b';
  }
  function resetRouteForm() {
    editingId = '';
    $('rtName').value = ''; $('rtPrefix').value = '/'; $('rtHost').value = '127.0.0.1';
    $('rtListen').value = ''; $('rtPort').value = '6001';
    $('rtStrip').value = 'false'; $('rtEnabled').value = 'true';
    $('rtHosts').value = ''; $('rtSave').textContent = '新增服务';
    $('rtCancel').classList.add('hidden');
  }
  function fillRouteForm(u) {
    editingId = u.id;
    $('rtName').value = u.name || ''; $('rtPrefix').value = u.prefix || '/';
    $('rtListen').value = u.listenPort ? String(u.listenPort) : '';
    $('rtHost').value = u.host || '127.0.0.1'; $('rtPort').value = u.port || 6001;
    $('rtStrip').value = u.stripPrefix ? 'true' : 'false';
    $('rtEnabled').value = u.enabled === false ? 'false' : 'true';
    $('rtHosts').value = (u.hosts || []).join(', ');
    $('rtSave').textContent = '保存修改'; $('rtCancel').classList.remove('hidden');
    $('rtName').focus();
    routeMsg('正在编辑「' + (u.name || u.prefix) + '」，保存后立即生效');
  }
  function collectRoute() {
    var listen = $('rtListen').value.trim();
    var body = {
      name: $('rtName').value.trim(), prefix: $('rtPrefix').value.trim(),
      host: $('rtHost').value.trim(), port: Number($('rtPort').value.trim()),
      listenPort: listen ? Number(listen) : 0,
      stripPrefix: $('rtStrip').value === 'true',
      enabled: $('rtEnabled').value === 'true',
      hosts: $('rtHosts').value.split(/[,\\s，]+/).filter(Boolean),
    };
    if (editingId) body.id = editingId;
    return body;
  }
  function loadRoutes() {
    api('routes').then(function (r) {
      var d = r.data || {}, list = d.routes || [];
      if (d.file) $('rtFile').textContent = d.file;
      $('routesEmpty').textContent = list.length ? '' : '暂无转发服务，用上方表单添加，保存立即生效';
      $('routes').innerHTML = list.map(function (u) {
        var on = u.enabled !== false;
        var lp = u.listenPort || 0;
        var entry = lp
          ? '<code>:' + esc(lp) + '</code> <span class="tag ok">独立端口</span>'
          : '<code>' + esc(u.prefix) + '</code>';
        var open = lp
          ? location.protocol + '//' + location.hostname + ':' + lp + '/'
          : u.url;
        return '<tr><td>' + esc(u.name || '-') + '</td><td>' + entry + '</td>'
          + '<td><code>' + esc(u.host) + ':' + esc(u.port) + '</code></td>'
          + '<td>' + (lp ? '<span class="tag off">不需要</span>'
            : (u.stripPrefix
              ? '<span class="tag ok">去前缀' + (u.prefix !== '/' ? ' + 改写' : '') + '</span>'
              : '保留')) + '</td>'
          + '<td>' + esc((u.hosts && u.hosts.length) ? u.hosts.join(', ') : '不限') + '</td>'
          + '<td><span class="tag ' + (on ? 'ok' : 'off') + '">' + (on ? '生效中' : '已停用') + '</span></td>'
          + '<td style="white-space:nowrap">'
          + (on ? '<a href="' + esc(open) + '" target="_blank" rel="noopener" style="font-size:11.5px">打开</a> ' : '')
          + '<button class="act" data-redit="' + esc(u.id) + '">编辑</button>'
          + '<button class="act" data-rtoggle="' + esc(u.id) + '" data-en="' + (on ? '0' : '1') + '">'
          + (on ? '停用' : '启用') + '</button>'
          + '<button class="act danger" data-rdel="' + esc(u.id) + '">删除</button></td></tr>';
      }).join('');
      Array.prototype.forEach.call($('routes').querySelectorAll('[data-redit]'), function (b) {
        b.onclick = function () {
          var u = list.filter(function (x) { return x.id === b.getAttribute('data-redit'); })[0];
          if (u) fillRouteForm(u);
        };
      });
      Array.prototype.forEach.call($('routes').querySelectorAll('[data-rtoggle]'), function (b) {
        b.onclick = function () {
          routeMsg('');
          post('routes/toggle', { id: b.getAttribute('data-rtoggle'),
            enabled: b.getAttribute('data-en') === '1' }).then(function (r) {
              if (!r.data.ok) { routeMsg(r.data.error || '操作失败'); return; }
              routeMsg('已更新，路由实时生效', true); loadRoutes();
            });
        };
      });
      Array.prototype.forEach.call($('routes').querySelectorAll('[data-rdel]'), function (b) {
        b.onclick = function () {
          var id = b.getAttribute('data-rdel');
          var u = list.filter(function (x) { return x.id === id; })[0] || {};
          if (!confirm('确认删除转发服务「' + (u.name || u.prefix || id) + '」？删除后立即失效。')) return;
          post('routes/delete', { id: id }).then(function (r) {
            if (!r.data.ok) { routeMsg(r.data.error || '删除失败'); return; }
            if (editingId === id) resetRouteForm();
            routeMsg('已删除，路由实时生效', true); loadRoutes();
          });
        };
      });
    });
  }
  function loadAbout(me) {
    var d = me || {};
    $('about').innerHTML = '<table><tbody>'
      + '<tr><th>登录方式</th><td>' + esc(d.modeLabel) + ' [' + esc(d.mode) + ']'
      + (d.wechatConfigured || d.deviceMode ? '' : ' <span class="tag no">未配置凭证</span>') + '</td></tr>'
      + '<tr><th>鉴权状态</th><td>' + (d.authEnabled ? '已开启（所有转发均需先通过微信白名单）' : '已关闭（纯转发）') + '</td></tr>'
      + '<tr><th>监听端口</th><td>' + esc(d.port) + '</td></tr>'
      + '<tr><th>管理员账号</th><td>' + esc((d.admins || []).join('、')) + '</td></tr>'
      + '<tr><th>配置文件</th><td><code>' + esc((d.files || {}).config) + '</code>（仅端口 / 监听地址；其余配置见「系统配置」）</td></tr>'
      + '<tr><th>数据存储</th><td><code>' + esc((d.files || {}).db) + '</code>（白名单 / 设备 / 授权记录 / 系统配置均存于此 SQLite 库）</td></tr>'
      + '<tr><th>IP 归属地库</th><td>' + (d.ipdb && d.ipdb.ready
        ? esc(d.ipdb.count) + ' 个网段 · ' + esc(d.ipdb.source) + '（离线解析，记录里的「地区」由此生成）'
        : '<span class="tag no">未加载</span> 缺少 ipdb.bin，记录中的地区将为空') + '</td></tr>'
      + '</tbody></table>'
      + '<div class="muted" style="margin-top:14px">管理员账号存于 gateway.db，可在下方「修改密码」处修改；首次运行会从 config.json 的 admins 导入，明文密码自动转成 scrypt 哈希。'
      + '放行某个微信用户后，其下次扫码即可通过；停用或删除后，已签发的会话会立即失效。'
      + '转发服务可在「转发服务」页在线增改删，系统参数可在「系统配置」页在线修改，均立即生效并存于 gateway.db，不需要重启网关。</div>'
      + (d.deviceMode
        ? '<div class="muted" style="margin-top:12px">当前为设备登录模式：微信登录入口已全部停用。设备访问登录页会自动生成机器码并显示「无权访问」；'
          + '在「设备白名单」放行后即可进入。切回微信扫码：在「系统配置」页把登录模式改为微信扫码，保存即时生效，无需重启。</div>'
        : (d.wechatConfigured ? '' : '<div class="muted" style="margin-top:12px;color:#ffd479">个人开发者接入：注册「个人主体」小程序（身份证实名、0 元，无需营业执照 / 公章 / 开发者认证），'
          + '在「系统配置」页填 miniAppId / miniAppSecret，把 miniprogram/pages/gate 拷进小程序并发布，保存后即时生效即可扫码登录（不需要 publicBaseUrl、不需要重启）。'
          + '开放平台「网站应用」需企业资质与《网站信息登记表》盖章（个人备案可手写签名），门槛更高。</div>'));
  }

  function cfgMsg(t, ok) {
    $('cfgMsg').textContent = t || '';
    $('cfgMsg').style.color = ok ? '#3ddc97' : '#ff9b9b';
  }
  function loadConfig() {
    api('config').then(function (r) {
      var d = r.data || {};
      $('cfgDb').textContent = (d.files && d.files.db) || 'gateway.db';
      if (d.port !== undefined) $('cfgPort').value = d.port;
      if (d.host !== undefined) $('cfgHost').value = d.host;
      $('cfgLoginMode').value = d.loginMode || 'wechat';
      $('cfgMode').value = d.mode || 'auto';
      $('cfgPublic').value = d.publicBaseUrl || '';
      $('cfgLan').value = d.lanBaseUrl || '';
      $('cfgLanding').value = d.landing || '';
      $('cfgRootMode').value = d.rootMode === 'proxy' ? 'proxy' : 'portal';
      $('cfgSession').value = d.sessionHours != null ? d.sessionHours : 168;
      $('cfgAdminSession').value = d.adminSessionHours != null ? d.adminSessionHours : 12;
      $('cfgTokenQuery').checked = d.allowTokenInQuery !== false;
      $('cfgCors').value = d.corsOrigins || '';
      var w = d.wechat || {};
      $('cfgMpAppId').value = w.mpAppId || '';
      $('cfgOpenAppId').value = w.openAppId || '';
      $('cfgMiniAppId').value = w.miniAppId || '';
      $('cfgMiniPage').value = w.miniPage || '';
      $('cfgMiniEnv').value = w.miniEnv || 'release';
      [['cfgMpSecret', w.mpAppSecret], ['cfgOpenSecret', w.openAppSecret], ['cfgMiniSecret', w.miniAppSecret]].forEach(function (p) {
        var el = $(p[0]); el.value = ''; el.placeholder = (p[1] && p[1] !== '********') ? '已设置（留空则不修改）' : '未设置';
      });
    }).catch(function () { cfgMsg('加载配置失败'); });
  }
  function saveConfig() {
    cfgMsg('');
    var body = {
      loginMode: $('cfgLoginMode').value,
      mode: $('cfgMode').value,
      publicBaseUrl: $('cfgPublic').value.trim(),
      lanBaseUrl: $('cfgLan').value.trim(),
      landing: $('cfgLanding').value.trim(),
      rootMode: $('cfgRootMode').value,
      sessionHours: Number($('cfgSession').value) || 168,
      adminSessionHours: Number($('cfgAdminSession').value) || 12,
      allowTokenInQuery: $('cfgTokenQuery').checked,
      corsOrigins: $('cfgCors').value.trim(),
      mpAppId: $('cfgMpAppId').value.trim(),
      mpAppSecret: $('cfgMpSecret').value,
      openAppId: $('cfgOpenAppId').value.trim(),
      openAppSecret: $('cfgOpenSecret').value,
      miniAppId: $('cfgMiniAppId').value.trim(),
      miniAppSecret: $('cfgMiniSecret').value,
      miniPage: $('cfgMiniPage').value.trim(),
      miniEnv: $('cfgMiniEnv').value,
    };
    post('config', body).then(function (r) {
      if (r.data.ok) { cfgMsg('已保存，配置即时生效', true); loadConfig(); }
      else cfgMsg(r.data.error || '保存失败');
    }).catch(function () { cfgMsg('网络错误，请重试'); });
  }

  function switchTab(name) {
    Array.prototype.forEach.call(document.querySelectorAll('.tabs button'), function (b) {
      b.classList.toggle('on', b.getAttribute('data-tab') === name);
    });
    ['records', 'whitelist', 'devices', 'routes', 'config', 'about'].forEach(function (n) {
      $('tab-' + n).classList.toggle('hidden', n !== name);
    });
  }
  Array.prototype.forEach.call(document.querySelectorAll('.tabs button'), function (b) {
    b.onclick = function () { switchTab(b.getAttribute('data-tab')); };
  });
  $('rsearch').onclick = loadRecords;
  $('rq').addEventListener('keydown', function (e) { if (e.key === 'Enter') loadRecords(); });
  $('rtype').onchange = loadRecords;
  $('rclear').onclick = function () {
    if (!confirm('确认清空所有授权记录？')) return;
    post('records/clear', {}).then(function () { loadRecords(); loadOverview(); });
  };
  $('dvAllow').onclick = function () {
    var c = $('dvCode').value.trim().toUpperCase();
    if (!c) { deviceMsg('请填写机器码'); return; }
    allowDevice(c, $('dvName').value.trim());
  };
  $('dvCode').addEventListener('keydown', function (e) { if (e.key === 'Enter') $('dvAllow').click(); });
  $('rtSave').onclick = function () {
    routeMsg('');
    post('routes/save', collectRoute()).then(function (r) {
      if (!r.data.ok) { routeMsg(r.data.error || '保存失败'); return; }
      var wasEdit = !!editingId;
      routeMsg(wasEdit ? '已保存修改，路由实时生效' : '已新增服务，路由实时生效', true);
      resetRouteForm(); loadRoutes();
    }).catch(function () { routeMsg('网络错误，请重试'); });
  };
  $('rtCancel').onclick = function () { resetRouteForm(); routeMsg(''); };
  $('cfgSave').onclick = saveConfig;
  $('addBtn').onclick = function () {
    var oid = $('newOid').value.trim();
    if (!oid) { alert('请填写微信 openid'); return; }
    post('allow', { openid: oid, name: $('newName').value.trim() || '手动添加' }).then(function (r) {
      if (r.data.ok) { $('newOid').value = ''; $('newName').value = '';
        loadWhitelist(); loadOverview(); loadRecords(); }
      else alert(r.data.error || '添加失败');
    });
  };
  $('reload').onclick = function () {
    loadOverview(); loadRecords(); loadWhitelist(); loadDevices(); loadRoutes(); loadConfig();
  };
  $('pwBtn').onclick = function () {
    $('pwMsg').textContent = '';
    post('password', { oldPassword: $('oldPw').value, newPassword: $('newPw').value })
      .then(function (r) {
        $('pwMsg').textContent = r.data.ok ? '密码已更新，下次登录请用新密码'
          : (r.data.error || '修改失败');
        if (r.data.ok) { $('oldPw').value = ''; $('newPw').value = ''; }
      });
  };

  function boot() {
    api('me').then(function (r) {
      if (r.status === 401 || !r.data.loggedIn) { showLogin(''); return; }
      $('login').classList.add('hidden'); $('app').classList.remove('hidden');
      $('who').textContent = '管理员：' + r.data.username;
      loadAbout(r.data);
      resetRouteForm();
      loadOverview(); loadRecords(); loadWhitelist(); loadDevices(); loadRoutes(); loadConfig();
    }).catch(function () { showLogin('无法连接网关管理接口'); });
  }
  boot();
})();
</script>
</body>
</html>`;
