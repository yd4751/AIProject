const $ = (s) => document.querySelector(s);
const $$ = (s) => Array.from(document.querySelectorAll(s));

const PAGE_META = {
  overview: ['总览', '服务运行状态与实时数据'],
  ports: ['端口管理', '实时新增与关闭端口映射'],
  connections: ['实时连接', '当前活跃的转发隧道'],
  clients: ['客户端', '所有曾连接过的内网客户端'],
  auth: ['授权管理', '黑白名单与访问控制'],
  traffic: ['流量记录', '历史隧道连接明细'],
};

function fmtBytes(n) {
  n = Number(n) || 0;
  if (n < 1024) return n + ' B';
  const u = ['KB', 'MB', 'GB', 'TB'];
  let i = -1;
  do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
  return n.toFixed(1) + ' ' + u[i];
}
function fmtDur(ms) {
  const s = Math.floor((ms || 0) / 1000);
  if (s < 60) return s + 's';
  if (s < 3600) return Math.floor(s / 60) + 'm' + (s % 60) + 's';
  return Math.floor(s / 3600) + 'h' + Math.floor((s % 3600) / 60) + 'm';
}
function fmtTime(sec) {
  if (!sec) return '-';
  return new Date(sec * 1000).toLocaleString('zh-CN', { hour12: false });
}
function shortMachine(id) { return id && id.length > 12 ? id.slice(0, 12) : (id || '-'); }
function esc(s) { return String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])); }
async function api(path, opts) {
  const r = await fetch(path, opts);
  const text = await r.text();
  if (!r.ok) throw new Error(text || r.statusText);
  try { return JSON.parse(text); } catch { return {}; }
}
function msg(el, text, err) {
  el.textContent = text;
  el.classList.toggle('err', !!err);
  if (text) setTimeout(() => { if (el.textContent === text) el.textContent = ''; }, 3500);
}

// ---- 导航 ----
function switchTab(tab) {
  $$('.nav-item').forEach(x => x.classList.toggle('active', x.dataset.tab === tab));
  $$('.panel').forEach(x => x.classList.remove('active'));
  const panel = document.getElementById('tab-' + tab);
  if (panel) panel.classList.add('active');
  const meta = PAGE_META[tab] || ['', ''];
  $('#page-title').textContent = meta[0];
  $('#page-sub').textContent = meta[1];
  if (tab === 'clients') loadClients();
  if (tab === 'auth') { loadRules(); loadMode(); }
  if (tab === 'traffic') loadTraffic();
  if (tab === 'ports') { loadPorts(); loadClientOptions(); }
  if (tab === 'overview') loadHourly();
}
$$('.nav-item').forEach(n => n.addEventListener('click', () => switchTab(n.dataset.tab)));

// ---- 时钟 ----
setInterval(() => { $('#clock').textContent = new Date().toLocaleTimeString('zh-CN', { hour12: false }); }, 1000);

// ---- 总览 ----
async function loadOverview() {
  try {
    const d = await api('/api/overview');
    $('#m-online').textContent = d.online_clients;
    $('#m-tunnels').textContent = d.active_tunnels;
    $('#m-ports').textContent = (d.ports_count != null) ? d.ports_count : '—';
    $('#m-traffic').textContent = fmtBytes(d.bytes_up + d.bytes_down);
    $('#m-conns').textContent = d.total_conns + ' 次连接';
  } catch (e) {}
}
async function loadHourly() {
  try { drawChart((await api('/api/hourly?hours=24')).points || []); } catch (e) {}
}
function drawChart(points) {
  const el = $('#chart');
  const W = el.clientWidth || 700, H = 250, pl = 46, pr = 12, pt = 16, pb = 28;
  if (!points.length) { el.innerHTML = '<div class="empty-state">暂无流量数据</div>'; return; }
  const max = Math.max(1, ...points.map(p => (p.bytes_up || 0) + (p.bytes_down || 0)));
  const base = H - pb, plotH = H - pt - pb;
  const step = (W - pl - pr) / points.length;
  const bw = Math.max(3, Math.min(20, step * 0.55));
  let g = `<svg width="100%" height="${H}" viewBox="0 0 ${W} ${H}">`;
  // 网格线与刻度
  for (let i = 0; i <= 4; i++) {
    const y = pt + plotH * i / 4;
    const val = max * (1 - i / 4);
    g += `<line x1="${pl}" y1="${y}" x2="${W - pr}" y2="${y}" stroke="#eef0f5"/>`;
    g += `<text x="${pl - 8}" y="${y + 4}" fill="#a6adbd" font-size="10" text-anchor="end">${fmtBytes(val)}</text>`;
  }
  points.forEach((p, i) => {
    const x = pl + i * step + (step - bw) / 2;
    const up = (p.bytes_up || 0) / max * plotH;
    const down = (p.bytes_down || 0) / max * plotH;
    g += `<rect x="${x}" y="${base - up}" width="${bw}" height="${up}" fill="#0f9d76" rx="2"><title>上行 ${fmtBytes(p.bytes_up)}</title></rect>`;
    g += `<rect x="${x}" y="${base - up - down}" width="${bw}" height="${down}" fill="#7c5cf5" rx="2"><title>下行 ${fmtBytes(p.bytes_down)}</title></rect>`;
    if (i % Math.ceil(points.length / 6) === 0) {
      const t = new Date(p.hour * 1000);
      g += `<text x="${x + bw / 2}" y="${H - 8}" fill="#a6adbd" font-size="10" text-anchor="middle">${String(t.getHours()).padStart(2,'0')}:00</text>`;
    }
  });
  g += '</svg>';
  el.innerHTML = g;
}

// ---- 端口管理 ----
async function loadClientOptions() {
  try {
    const d = await api('/api/clients');
    const sel = $('#p-machine');
    const prev = sel.value;
    const online = (d.clients || []).filter(c => c.online);
    sel.innerHTML = online.length
      ? online.map(c => `<option value="${esc(c.machine_id)}">${esc(c.hostname) || shortMachine(c.machine_id)} · ${shortMachine(c.machine_id)}</option>`).join('')
      : '<option value="">暂无在线客户端</option>';
    if (prev && online.some(c => c.machine_id === prev)) sel.value = prev;
  } catch (e) {}
}
function renderPorts(ports) {
  const body = $('#ports-body');
  $('#nav-port-count').textContent = ports.length;
  if (!ports.length) { body.innerHTML = '<tr><td colspan="7" class="empty-state">暂无端口映射</td></tr>'; return; }
  body.innerHTML = ports.map(p => `<tr>
    <td><span class="tag ${p.active ? 'ok' : 'off'}">${p.active ? '运行中' : (p.online ? '已关闭' : '客户端离线')}</span></td>
    <td><span class="mono" style="font-weight:700">:${p.remote_port}</span></td>
    <td class="mono">${esc(p.local_ip)}:${p.local_port}</td>
    <td>${esc(p.name)}</td>
    <td class="mono" title="${esc(p.machine_id)}">${esc(p.hostname) || shortMachine(p.machine_id)}</td>
    <td><span class="tag ${p.source}">${p.source === 'dynamic' ? '动态' : '配置'}</span></td>
    <td><button class="btn sm danger" onclick="closePort('${esc(p.machine_id)}',${p.remote_port})">关闭</button></td>
  </tr>`).join('');
}
async function loadPorts() {
  try { renderPorts((await api('/api/ports')).ports || []); } catch (e) {}
}
$('#port-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const out = { machine_id: $('#p-machine').value, name: $('#p-name').value.trim(),
    local_ip: $('#p-localip').value.trim(), local_port: +$('#p-localport').value,
    remote_port: +$('#p-remoteport').value, note: $('#p-note').value.trim() };
  if (!out.machine_id) { msg($('#port-msg'), '请选择在线客户端', true); return; }
  try {
    const r = await api('/api/ports', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(out) });
    msg($('#port-msg'), r.client_acked ? '已新增并生效' : '已新增（客户端尚未确认）');
    $('#p-name').value = ''; $('#p-localport').value = ''; $('#p-remoteport').value = ''; $('#p-note').value = '';
    loadPorts(); loadOverview();
  } catch (err) { msg($('#port-msg'), err.message, true); }
});
window.closePort = async (machineID, port) => {
  if (!confirm(`确认关闭端口 :${port}？该连接上的活跃隧道会立即断开。`)) return;
  try {
    await api(`/api/ports?machine_id=${encodeURIComponent(machineID)}&remote_port=${port}`, { method: 'DELETE' });
    loadPorts(); loadOverview();
  } catch (err) { alert('关闭失败: ' + err.message); }
};

// ---- 实时连接 ----
function renderLive(conns) {
  const body = $('#live-body');
  const mini = $('#mini-live');
  if (!conns || !conns.length) {
    body.innerHTML = '<tr><td colspan="7" class="empty-state">暂无活跃连接</td></tr>';
    mini.innerHTML = '<div class="empty-state">暂无活跃连接</div>';
    return;
  }
  body.innerHTML = conns.map(c => `<tr>
    <td class="mono">${c.id}</td><td>${esc(c.service)}</td><td class="mono">${esc(c.remote_ip)}</td>
    <td class="mono" title="${esc(c.machine_id)}">${shortMachine(c.machine_id)}</td>
    <td style="color:var(--up)">${fmtBytes(c.up)}</td><td style="color:var(--down)">${fmtBytes(c.down)}</td>
    <td>${fmtDur(c.duration_ms)}</td>
  </tr>`).join('');
  mini.innerHTML = conns.slice(0, 6).map(c => `<div class="mini-row">
    <span class="mini-svc"></span>
    <div class="mini-main"><strong>${esc(c.service)}</strong><span class="mono">${esc(c.remote_ip)} · ${fmtDur(c.duration_ms)}</span></div>
    <span class="mini-bytes">↑${fmtBytes(c.up)} ↓${fmtBytes(c.down)}</span>
  </div>`).join('');
}

// ---- 客户端 ----
async function loadClients() {
  try {
    const d = await api('/api/clients');
    const body = $('#clients-body');
    if (!d.clients.length) { body.innerHTML = '<tr><td colspan="7" class="empty-state">暂无数据</td></tr>'; return; }
    body.innerHTML = d.clients.map(c => `<tr>
      <td><span class="tag ${c.online ? 'ok' : 'off'}">${c.online ? '在线' : '离线'}</span></td>
      <td class="mono" title="${esc(c.machine_id)}">${shortMachine(c.machine_id)}</td>
      <td>${esc(c.hostname) || '-'}</td><td class="mono">${esc(c.ip)}</td>
      <td style="color:var(--up)">${fmtBytes(c.bytes_up)}</td><td style="color:var(--down)">${fmtBytes(c.bytes_down)}</td>
      <td class="sub">${fmtTime(c.last_seen)}</td>
    </tr>`).join('');
  } catch (e) {}
}

// ---- 授权 ----
async function loadMode() {
  try {
    const d = await api('/api/settings');
    const r = document.querySelector(`input[name=mode][value="${d.auth_mode}"]`);
    if (r) r.checked = true;
  } catch (e) {}
}
$('#save-mode').addEventListener('click', async () => {
  const sel = document.querySelector('input[name=mode]:checked');
  if (!sel) return;
  try {
    await api('/api/settings', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ auth_mode: sel.value }) });
    msg($('#mode-msg'), '已保存');
  } catch (e) { msg($('#mode-msg'), '保存失败', true); }
});
async function loadRules() {
  try {
    const d = await api('/api/rules');
    const body = $('#rules-body');
    if (!d.rules.length) { body.innerHTML = '<tr><td colspan="6" class="empty-state">暂无规则</td></tr>'; return; }
    body.innerHTML = d.rules.map(r => `<tr>
      <td><span class="tag ${r.kind}">${r.kind === 'ip' ? 'IP' : '机器码'}</span></td>
      <td class="mono">${esc(r.value)}</td>
      <td><span class="tag ${r.action}">${r.action === 'allow' ? '允许' : '拒绝'}</span></td>
      <td>${esc(r.note) || '-'}</td>
      <td><button class="btn sm" onclick="toggleRule(${r.id},${!r.enabled})">${r.enabled ? '已启用' : '已禁用'}</button></td>
      <td><button class="btn sm danger" onclick="delRule(${r.id})">删除</button></td>
    </tr>`).join('');
  } catch (e) {}
}
$('#rule-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const body = { kind: $('#r-kind').value, value: $('#r-value').value.trim(),
    action: $('#r-action').value, note: $('#r-note').value.trim() };
  if (!body.value) return;
  try {
    await api('/api/rules', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    $('#r-value').value = ''; $('#r-note').value = '';
    loadRules();
  } catch (err) { alert('添加失败: ' + err.message); }
});
window.toggleRule = async (id, enabled) => {
  await api('/api/rules', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ id, enabled }) });
  loadRules();
};
window.delRule = async (id) => {
  if (!confirm('确认删除该规则？')) return;
  await api('/api/rules?id=' + id, { method: 'DELETE' });
  loadRules();
};

// ---- 流量 ----
async function loadTraffic() {
  try {
    const d = await api('/api/traffic?limit=100');
    const body = $('#traffic-body');
    if (!d.traffic.length) { body.innerHTML = '<tr><td colspan="7" class="empty-state">暂无记录</td></tr>'; return; }
    body.innerHTML = d.traffic.map(t => `<tr>
      <td class="sub">${fmtTime(t.ended_at)}</td><td>${esc(t.service)}</td>
      <td class="mono">${esc(t.remote_ip)}</td><td class="mono">${shortMachine(t.machine_id)}</td>
      <td style="color:var(--up)">${fmtBytes(t.bytes_up)}</td><td style="color:var(--down)">${fmtBytes(t.bytes_down)}</td>
      <td>${t.duration_sec}s</td>
    </tr>`).join('');
  } catch (e) {}
}

// ---- SSE 实时推送 ----
function connectSSE() {
  const src = new EventSource('/api/events');
  src.onopen = () => { $('#connDot').classList.add('on'); $('#connText').textContent = '实时已连接'; };
  src.onerror = () => { $('#connDot').classList.remove('on'); $('#connText').textContent = '重连中…'; };
  src.onmessage = (e) => {
    try {
      const d = JSON.parse(e.data);
      renderLive(d.connections || []);
      const on = document.getElementById('m-online'), tn = document.getElementById('m-tunnels');
      if (on) on.textContent = d.online_clients;
      if (tn) tn.textContent = d.active_tunnels;
      // 端口列表页打开时也随之刷新状态
      if ($('#tab-ports').classList.contains('active') && d.ports) renderPorts(d.ports);
    } catch (err) {}
  };
}

// ---- 初始化 ----
loadOverview();
loadHourly();
connectSSE();
setInterval(loadOverview, 5000);
