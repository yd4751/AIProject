'use strict';
/* 设备登录页：自动采集本机设备信息 → 网关生成机器码 → 白名单校验。
   未授权时明确显示「无权访问 + 机器码」，管理员放行后本页自动进入。
   内联成字符串，便于网关单文件分发 / exe 打包。
   注意：这是 Node 模板字符串，里面写浏览器正则时反斜杠必须双写。 */
module.exports = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>设备登录 · 网关验证</title>
<style>
  * { box-sizing: border-box; }
  body { margin:0; min-height:100vh; display:flex; align-items:center; justify-content:center;
    background:#0d1117; color:#e6edf3; font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif; }
  .card { width:min(92vw,430px); background:#151b23; border:1px solid #263140; border-radius:18px;
    padding:28px 26px 20px; box-shadow:0 18px 50px rgba(0,0,0,.45); text-align:center; }
  .logo { width:46px; height:46px; border-radius:13px; background:#1c242e; display:flex; align-items:center;
    justify-content:center; margin:0 auto 12px; font-size:22px; }
  h1 { font-size:18px; margin:0 0 4px; font-weight:600; }
  .sub { color:#8b98a9; font-size:12.5px; margin:0 0 20px; }
  .state { min-height:22px; margin:6px 0 4px; font-size:13.5px; }
  .state.ok { color:#3ddc97; } .state.err { color:#ff7b7b; }
  .hint { color:#8b98a9; font-size:11.5px; min-height:18px; margin-bottom:10px; }
  .box { text-align:left; background:#0d1117; border:1px solid #263140; border-radius:11px;
    padding:13px 14px; margin:12px 0; }
  .box .k { color:#8b98a9; font-size:11px; margin-bottom:5px; }
  .code { display:block; word-break:break-all; font-family:ui-monospace,Consolas,monospace;
    font-size:17px; letter-spacing:.5px; color:#9ecbff; }
  .meta { color:#5f6c7d; font-size:11px; margin-top:8px; word-break:break-all; }
  .deny { display:none; margin-top:12px; padding:13px; background:rgba(255,123,123,.07);
    border:1px solid rgba(255,123,123,.28); border-radius:11px; text-align:left; }
  .deny h2 { margin:0 0 5px; font-size:14px; color:#ff7b7b; }
  .deny p { margin:0 0 10px; font-size:12px; color:#8b98a9; }
  button { background:#4c8dff; color:#fff; border:0; border-radius:9px; padding:9px 15px;
    font-size:13px; cursor:pointer; }
  .deny button { width:100%; margin-top:9px; }
  button.ghost { background:#22304a; border:1px solid #33456a; color:#9ecbff; }
  .foot { margin-top:16px; font-size:11px; color:#5f6c7d; }
  .foot a { color:#7aa7ff; cursor:pointer; text-decoration:none; }
</style>
</head>
<body>
<div class="card">
  <div class="logo">▣</div>
  <h1>设备登录</h1>
  <p class="sub">自动识别本机设备，授权后方可访问</p>

  <div class="state" id="state">正在识别本机设备…</div>
  <div class="hint" id="hint">无需账号密码，请稍候</div>

  <div class="box" id="box" style="display:none">
    <div class="k">本机机器码</div>
    <code class="code" id="code">—</code>
    <div class="meta" id="meta"></div>
  </div>

  <div class="deny" id="deny">
    <h2>无权访问</h2>
    <p>该设备未在授权列表中。请把上面的机器码发给管理员，在「网关管理端 → 设备白名单」放行；放行后本页会自动进入，无需刷新。</p>
    <button id="copy">复制机器码</button>
    <button class="ghost" id="retry">立即重试</button>
  </div>

  <div class="foot"><a href="/__auth/admin">管理端</a> · <span id="sp">设备信息仅用于生成机器码</span></div>
</div>
<script>
(function () {
  var $ = function (id) { return document.getElementById(id); };
  var state = $('state'), hint = $('hint'), box = $('box'), deny = $('deny');
  var code = '', timer = null, busy = false, tries = 0;
  var DEV_KEY = 'mp_device_id', DEV_COOKIE = 'mp_dev';

  function setState(t, c) { state.textContent = t; state.className = 'state' + (c ? ' ' + c : ''); }

  function cookieVal(name) {
    var parts = document.cookie.split(';');
    for (var i = 0; i < parts.length; i++) {
      var p = parts[i].trim();
      if (p.indexOf(name + '=') === 0) return decodeURIComponent(p.slice(name.length + 1));
    }
    return '';
  }
  function uuid() {
    try { return crypto.randomUUID(); } catch (e) { return 'd' + Date.now() + Math.random().toString(36).slice(2); }
  }
  /* 持久设备 ID：服务端下发的 cookie 优先，其次 localStorage，最后新生成。
     清浏览器数据会产生新的机器码，需要管理员重新放行。 */
  function deviceId() {
    var v = cookieVal(DEV_COOKIE);
    if (v) return v;
    try { v = localStorage.getItem(DEV_KEY) || ''; } catch (e) { v = ''; }
    if (!v) { v = uuid(); try { localStorage.setItem(DEV_KEY, v); } catch (e) {} }
    return v;
  }
  /* 设备指纹：只取稳定字段，不用任何敏感或需要授权的数据 */
  function fingerprint() {
    var p = [], n = navigator, s = screen;
    p.push(n.userAgent || '', n.platform || '', n.language || '', (n.languages || []).join(','));
    try { p.push(Intl.DateTimeFormat().resolvedOptions().timeZone || ''); } catch (e) {}
    p.push(s.width + 'x' + s.height + 'x' + s.colorDepth + '@' + (window.devicePixelRatio || 1));
    p.push(String(n.hardwareConcurrency || ''), String(n.deviceMemory || ''));
    try {
      var c = document.createElement('canvas'), x = c.getContext('2d');
      x.textBaseline = 'top'; x.font = '14px Arial'; x.fillText('mp-gateway-device', 2, 2);
      p.push(c.toDataURL().slice(-96));
    } catch (e) {}
    return p.join('|');
  }
  function label() {
    return String(navigator.platform || '') + ' / ' + String(navigator.userAgent || '').slice(0, 90);
  }
  function nextUrl() {
    var m = location.search.match(new RegExp('[?&]next=([^&]+)'));
    if (!m) return '';
    var v;
    try { v = decodeURIComponent(m[1]); } catch (e) { return ''; }
    return v.charAt(0) === '/' && v.charAt(1) !== '/' ? v : '';
  }
  function showCode(d) {
    code = d.machineCode || d.device || '';
    $('code').textContent = code || '（未取到）';
    $('meta').textContent = '设备：' + (d.label || label()) + '　｜　状态：等待管理员放行';
    box.style.display = 'block';
  }
  function showDeny(d) {
    showCode(d);
    setState('无权访问', 'err');
    hint.textContent = (d.hint || '本次申请已记录，放行后自动进入');
    deny.style.display = 'block';
    startPoll();
  }
  function stop() { if (timer) { clearInterval(timer); timer = null; } }
  function startPoll() { stop(); timer = setInterval(function () { attempt(true); }, 3000); }

  function attempt(silent) {
    if (busy) return;
    busy = true;
    if (!silent) setState('正在识别本机设备…', '');
    fetch('/__auth/device/login', {
      method: 'POST', cache: 'no-store',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: deviceId(), fp: fingerprint(), label: label(), next: nextUrl() }),
    }).then(function (r) { return r.json(); }).then(function (d) {
      busy = false; tries++;
      if (d.ok && d.token) {
        stop(); deny.style.display = 'none';
        setState('设备已授权，正在进入…', 'ok');
        hint.textContent = '机器码 ' + (d.machineCode || '') + ' 已放行';
        /* 优先用服务端下发的落地页（默认是服务列表 /__auth/apps，可在里面点「进入」选任意服务）；
           没有时绝不退化到第一个服务，统一回服务列表，避免多服务时进不了第二项。 */
        location.replace(d.next || '/__auth/apps');
        return;
      }
      if (d.code === 'NOT_AUTHORIZED') {
        showDeny(d);
        if (tries > 1) hint.textContent = '已等待 ' + (tries * 3) + ' 秒，管理员放行后本页会自动进入';
        return;
      }
      stop();
      setState(d.error || '设备识别失败', 'err');
      hint.textContent = '请点「立即重试」，或联系管理员检查网关配置';
      deny.style.display = 'block';
    }).catch(function () {
      busy = false;
      if (!silent) { setState('无法连接网关', 'err'); hint.textContent = '网络恢复后会自动重试'; }
    });
  }

  $('copy').onclick = function () {
    if (!code) return;
    if (navigator.clipboard) navigator.clipboard.writeText(code);
    this.textContent = '已复制';
  };
  $('retry').onclick = function () { tries = 0; attempt(false); };
  attempt(false);
})();
</script>
</body>
</html>`;
