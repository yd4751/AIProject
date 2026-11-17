'use strict';
/* 网页端微信扫码登录页。内联成字符串，让网关保持「几个文件就能独立分发」，
   打包 exe 时也不需要额外内嵌静态资源。已移除任何固定口令登录入口。 */
module.exports = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<title>微信扫码登录 · 网关验证</title>
<style>
  * { box-sizing: border-box; }
  body { margin:0; min-height:100vh; display:flex; align-items:center; justify-content:center;
    background:#0d1117; color:#e6edf3; font:14px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif; }
  .card { width:min(92vw,400px); background:#151b23; border:1px solid #263140; border-radius:18px;
    padding:28px 26px 20px; box-shadow:0 18px 50px rgba(0,0,0,.45); text-align:center; }
  .logo { width:46px; height:46px; border-radius:13px; background:#1c242e; display:flex; align-items:center;
    justify-content:center; margin:0 auto 12px; }
  .logo img { width:30px; height:30px; }
  h1 { font-size:18px; margin:0 0 4px; font-weight:600; }
  .sub { color:#8b98a9; font-size:12.5px; margin:0 0 20px; }
  .qrbox { width:236px; height:236px; margin:0 auto; background:#fff; border-radius:12px; padding:8px;
    display:flex; align-items:center; justify-content:center; overflow:hidden; }
  .qrbox svg, .qrbox img { width:100%; height:100%; display:block; }
  .qrbox .ph { color:#8b98a9; font-size:12px; padding:12px; text-align:left; }
  .state { min-height:22px; margin:16px 0 4px; font-size:13.5px; }
  .state.ok { color:#3ddc97; } .state.err { color:#ff7b7b; }
  .hint { color:#8b98a9; font-size:11.5px; min-height:18px; margin-bottom:8px; }
  .deny { display:none; margin-top:14px; padding:14px; background:rgba(255,123,123,.07);
    border:1px solid rgba(255,123,123,.28); border-radius:11px; text-align:left; }
  .deny h2 { margin:0 0 5px; font-size:14px; color:#ff7b7b; }
  .deny p { margin:0 0 10px; font-size:12px; color:#8b98a9; }
  .deny code { display:block; word-break:break-all; background:#0d1117; border:1px solid #263140;
    border-radius:8px; padding:8px 10px; font-size:11.5px; color:#9ecbff; }
  button { background:#4c8dff; color:#fff; border:0; border-radius:9px; padding:9px 15px;
    font-size:13px; cursor:pointer; }
  .deny button { width:100%; margin-top:9px; }
  .foot { margin-top:16px; font-size:11px; color:#5f6c7d; }
  .foot a { color:#7aa7ff; cursor:pointer; text-decoration:none; }
</style>
</head>
<body>
<div class="card">
  <div class="logo"><img src="/__auth/logo" alt=""></div>
  <h1>微信扫码登录</h1>
  <p class="sub">验证微信身份后方可访问，未授权账号无权访问</p>
  <div class="qrbox" id="qr"><span class="ph">正在生成二维码…</span></div>
  <div class="state" id="state">请用微信扫一扫</div>
  <div class="hint" id="hint"></div>
  <div class="deny" id="deny">
    <h2>无权访问</h2>
    <p id="denyMsg">该微信账号未获授权，请联系管理员放行。</p>
    <code id="oid"></code>
    <button id="copy">复制 openid</button>
  </div>
  <div class="foot"><a id="refresh">重新获取二维码</a> · <a id="status">检查状态</a> ·
    <a href="/__auth/apps">服务入口</a> · <a href="/__auth/admin">管理端</a></div>
</div>
<script>
(function () {
  var $ = function (id) { return document.getElementById(id); };
  var ticket = '', timer = null, busy = false;
  var box = $('qr'), state = $('state'), hint = $('hint'), deny = $('deny');

  function setState(text, cls) { state.textContent = text; state.className = 'state' + (cls ? ' ' + cls : ''); }
  function stop() { if (timer) { clearInterval(timer); timer = null; } }
  function startPoll() { stop(); timer = setInterval(function () { poll(); }, 1500); }

  function showDeny(d) {
    stop();
    setState('无权访问', 'err');
    hint.textContent = '本次授权失败已记录，管理员可在管理端一键放行';
    $('denyMsg').textContent = d.reason || '该微信账号未获授权，请联系管理员把 openid 加入白名单。';
    $('oid').textContent = d.openid ? ('openid: ' + d.openid + (d.unionid ? '  |  unionid: ' + d.unionid : '')) : '（本次未取到微信 openid）';
    deny.style.display = 'block';
  }

  function poll() {
    if (!ticket || busy) return;
    fetch('/__auth/poll?ticket=' + encodeURIComponent(ticket), { cache: 'no-store' })
      .then(function (r) { return r.json(); })
      .then(function (d) {
        if (d.status === 'ok') {
          stop(); setState('验证通过，正在进入…', 'ok');
          location.replace(d.next || '/__auth/apps');
        } else if (d.status === 'denied') {
          showDeny(d);
        } else if (d.status === 'expired') {
          stop(); setState('二维码已过期', 'err'); hint.textContent = '点下方「重新获取二维码」重试';
        } else if (d.status === 'scanned') {
          setState('已扫码，请在微信里确认登录', '');
        }
      })
      .catch(function () { /* 网络抖动忽略，下一轮继续 */ });
  }

  function newQR() {
    stop(); busy = true; deny.style.display = 'none';
    setState('正在生成二维码…', ''); hint.textContent = '';
    box.innerHTML = '<span class="ph">正在生成二维码…</span>';
    fetch('/__auth/qr', { cache: 'no-store' })
      .then(function (r) { return r.json(); })
      .then(function (d) {
        busy = false;
        if (d.error) {
          setState('暂时无法登录', 'err');
          box.innerHTML = '<span class="ph">' + escape(d.error) + '</span>';
          hint.textContent = d.hint || '请联系管理员配置微信登录凭证';
          return;
        }
        ticket = d.ticket;
        if (d.imageUrl) box.innerHTML = '<img alt="小程序码" src="' + d.imageUrl + '">';
        else if (d.qrSvg) box.innerHTML = d.qrSvg;
        setState(d.scanText || '请用微信扫一扫', '');
        hint.textContent = d.hint || '';
        startPoll();
      })
      .catch(function () { busy = false; setState('生成失败，请重试', 'err'); });
  }
  function escape(s) { return String(s || '').replace(/[&<>]/g, function (c) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c]; }); }

  $('refresh').onclick = newQR;
  $('status').onclick = function () { poll(); };
  $('copy').onclick = function () {
    var t = $('oid').textContent.replace(/^openid:\s*/, '').split('|')[0].trim();
    if (navigator.clipboard) navigator.clipboard.writeText(t);
    this.textContent = '已复制';
  };
  newQR();
})();
</script>
</body>
</html>`;
