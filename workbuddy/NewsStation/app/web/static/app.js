/* M3 交互：点阵悬停提示 + 标签添加表单；M5：整页一键翻译；
   M7：原文改走站内阅读抽屉，阅读页内可翻译正文。原生 JS，无构建步骤。 */
document.addEventListener('DOMContentLoaded', function () {
  var tip = document.getElementById('tip');

  function escapeHtml(s) {
    return String(s || '').replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  function move(e) {
    var x = Math.min(e.clientX + 14, window.innerWidth - 340);
    tip.style.left = Math.max(8, x) + 'px';
    tip.style.top = (e.clientY + 14) + 'px';
  }

  if (tip) {
    Array.prototype.forEach.call(document.querySelectorAll('.dotsvg .dot'), function (d) {
      d.addEventListener('mouseenter', function (e) {
        tip.innerHTML = '<b>' + escapeHtml(d.dataset.event) + '</b>' +
          '<span>' + d.dataset.count + ' 篇 · ' + escapeHtml(d.dataset.value) +
          ' · ' + escapeHtml(d.dataset.source) + '</span><br>' +
          escapeHtml(d.dataset.article);
        tip.hidden = false;
        move(e);
      });
      d.addEventListener('mousemove', move);
      d.addEventListener('mouseleave', function () { tip.hidden = true; });
    });
  }

  Array.prototype.forEach.call(document.querySelectorAll('form[data-tagform]'), function (f) {
    f.addEventListener('submit', function () {
      var sel = f.querySelector('select');
      if (!sel) return;
      f.action = f.dataset.action + '?action=' + encodeURIComponent(f.dataset.op) +
        '&v=' + encodeURIComponent(sel.value);
    });
  });

  initSearchEngineMemory();
  initTranslate();
  initReaderDrawer();
  initReaderTranslate();
});

/* M5/M8 粒度化翻译（Twitter 式）：
   - 每条外文旁边有独立「译」按钮，点哪条翻哪条，可单独切回原文；
   - 顶栏三档范围：译标题 / 译段落 / 译全文，只翻对应 data-tr-scope；
   - 服务端已把缓存译文注入 data-zh，命中直接显示，不重复请求；
   - 未缓存的一次批量 POST /api/translate，译文按文本 hash 存 SQLite；
   - localStorage 记住上次选择的范围（默认不自动全翻）。 */
function initTranslate() {
  var group = document.getElementById('tr-group');
  if (!group) return;
  var nodes = Array.prototype.slice.call(document.querySelectorAll('[data-tr]'));
  if (!nodes.length) return;                 // 本页没有外文，控件保持隐藏

  var scopeBtns = Array.prototype.slice.call(group.querySelectorAll('[data-tr-scope]'));
  var busy = false;
  group.hidden = false;

  function scopeOf(n) { return n.dataset.trScope || 'title'; }
  function inScope(n, scope) {
    return scope === 'all' || scopeOf(n) === scope;
  }
  function applyOne(n) {
    if (!n.dataset.zh) return false;
    n.textContent = n.dataset.zh;
    n.dataset.trOn = '1';
    var b = n.__trBtn;
    if (b) { b.textContent = '原文'; b.classList.add('on'); }
    n.classList.add('tr-done');
    return true;
  }
  function restoreOne(n) {
    if (n.dataset.trOn !== '1') return;
    n.textContent = n.dataset.text;
    delete n.dataset.trOn;
    var b = n.__trBtn;
    if (b) { b.textContent = '译'; b.classList.remove('on'); }
    n.classList.remove('tr-done');
  }
  function refreshScopeBtns() {
    scopeBtns.forEach(function (b) {
      var target = nodes.filter(function (n) { return inScope(n, b.dataset.trScope); });
      var on = target.length > 0 && target.every(function (n) {
        return n.dataset.trOn === '1' || !!n.dataset.zh;
      });
      b.classList.toggle('on', on);
    });
  }

  // 单条请求：只发这一条，后端缓存命中则零外部调用
  function translateOne(n, btn) {
    if (n.dataset.trOn === '1') { restoreOne(n); refreshScopeBtns(); return; }
    if (n.dataset.zh) { applyOne(n); refreshScopeBtns(); return; }
    if (btn.disabled) return;
    btn.disabled = true; btn.textContent = '…';
    fetch('/api/translate', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ texts: [n.dataset.text] })
    }).then(function (r) { return r.json(); }).then(function (data) {
      var zh = (data.results || {})[n.dataset.text];
      if (zh) { n.dataset.zh = zh; applyOne(n); }
      else { btn.title = data.enabled ? '该条暂时没翻出来，稍后再试' : '未配置翻译后端'; }
    }).catch(function () {
      btn.title = '翻译请求失败，请检查网络或 translate 配置';
    }).then(function () {
      btn.disabled = false;
      if (n.dataset.trOn !== '1') btn.textContent = '译';
      refreshScopeBtns();
    });
  }

  // 给每条外文插入独立「译」按钮（放在元素后面，不改动原有链接结构）
  nodes.forEach(function (n) {
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'tr-inline';
    btn.textContent = '译';
    btn.title = '只翻译这一条（可点「原文」切回）';
    n.__trBtn = btn;
    if (n.parentNode) n.parentNode.insertBefore(btn, n.nextSibling);
    btn.addEventListener('click', function () { translateOne(n, btn); });
  });

  fetch('/api/translate/status').then(function (r) { return r.json(); }).then(function (s) {
    var tip = s.enabled
      ? '后端 ' + s.chain.join(' → ') + ' · 目标 ' + s.target + ' · 已缓存 ' + s.cached_ok + ' 条译文'
      : '未配置翻译后端（config.yaml 的 translate.backend）；已缓存译文仍可显示';
    scopeBtns.forEach(function (b) { b.dataset.tip = tip; });
  }).catch(function () {});

  function toggleScope(btn) {
    if (busy) return;
    var scope = btn.dataset.trScope;
    var target = nodes.filter(function (n) { return inScope(n, scope); });
    if (!target.length) return;
    var allShown = target.every(function (n) { return n.dataset.trOn === '1'; });
    if (allShown) {
      target.forEach(restoreOne);
      try { localStorage.setItem('ns_tr_scope', ''); } catch (e) {}
      refreshScopeBtns();
      return;
    }
    var pending = target.filter(function (n) { return !n.dataset.zh; });
    if (!pending.length) {
      target.forEach(applyOne);
      saveScope(scope); refreshScopeBtns();
      return;
    }
    busy = true;
    scopeBtns.forEach(function (b) { b.disabled = true; });
    var old = btn.textContent;
    btn.textContent = '翻译中…';
    fetch('/api/translate', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ texts: pending.map(function (n) { return n.dataset.text; }) })
    }).then(function (r) { return r.json(); }).then(function (data) {
      var results = data.results || {};
      pending.forEach(function (n) {
        var zh = results[n.dataset.text];
        if (zh) n.dataset.zh = zh;
      });
      target.forEach(applyOne);
      saveScope(scope);
      if (!data.enabled) {
        btn.title = '未配置翻译后端：请在 config.yaml 配置 translate.backend（默认 auto 免密钥）';
      }
    }).catch(function () {
      btn.title = '翻译请求失败，请检查网络或 translate 配置';
    }).then(function () {
      busy = false;
      scopeBtns.forEach(function (b) { b.disabled = false; });
      btn.textContent = old;
      refreshScopeBtns();
    });
  }

  function saveScope(scope) {
    try { localStorage.setItem('ns_tr_scope', scope); } catch (e) {}
  }

  scopeBtns.forEach(function (b) {
    b.addEventListener('click', function () { toggleScope(b); });
  });

  // 刷新后只恢复上次选过的范围，且必须该范围全部已有缓存译文（避免半页翻译）
  var prefer = '';
  try { prefer = localStorage.getItem('ns_tr_scope') || ''; } catch (e) {}
  if (prefer) {
    var target = nodes.filter(function (n) { return inScope(n, prefer); });
    if (target.length && target.every(function (n) { return !!n.dataset.zh; })) {
      target.forEach(applyOne);
    }
  }
  refreshScopeBtns();
}

/* M7/M8 站内阅读抽屉：点击 a[data-read] 不跳外站，右侧面板用 iframe
   载入本站 /read?url=...&embed=1（服务端代取并清洗正文）。
   M8：首次代取境外站可能要十几秒，加载态给骨架屏 + 实时计时 + 分阶段说明，
   超时后提示可改用完整页/重新获取，避免「白屏不知道有没有在动」。 */
function initReaderDrawer() {
  if (window.parent !== window) return;          // 抽屉内部不再套抽屉
  var overlay = null, frame = null, fullLink = null, label = null;
  var timer = null, started = 0, src = '';

  function stopTimer() {
    if (timer) { clearInterval(timer); timer = null; }
  }

  function close() {
    if (!overlay) return;
    stopTimer();
    overlay.remove();
    overlay = null; frame = null;
    document.body.classList.remove('reader-open');
  }

  function stage(sec) {
    if (sec < 3) return '正在连接原站…';
    if (sec < 8) return '服务端正在获取并清洗网页，首次访问该站通常需要几秒…';
    if (sec < 16) return '原站响应较慢，仍在等待（不会一直卡住，超时会自动报错）…';
    return '这个站点响应异常慢。可继续等待，或改用下方按钮。';
  }

  function tick() {
    if (!overlay) { stopTimer(); return; }
    var sec = Math.floor((Date.now() - started) / 1000);
    var box = overlay.querySelector('.rd-loading');
    if (!box || box.hidden) { stopTimer(); return; }
    var t = box.querySelector('.rd-elapsed');
    var s = box.querySelector('.rd-stage');
    if (t) t.textContent = sec < 1 ? '刚刚' : ('已等待 ' + sec + ' 秒');
    if (s) s.textContent = stage(sec);
    box.classList.toggle('is-slow', sec >= 8);
  }

  function build() {
    overlay = document.createElement('div');
    overlay.className = 'rd-overlay';
    overlay.innerHTML =
      '<div class="rd-panel" role="dialog" aria-modal="true" aria-label="站内阅读">' +
        '<div class="rd-panel-bar">' +
          '<span class="rd-panel-label">站内阅读 · 服务端代取</span>' +
          '<span class="rd-panel-title"></span>' +
          '<a class="rd-full" href="#">完整页</a>' +
          '<button type="button" class="rd-close" title="关闭">×</button>' +
        '</div>' +
        '<div class="rd-loading">' +
          '<div class="rd-loading-top">' +
            '<span class="rd-spinner" aria-hidden="true"></span>' +
            '<span class="rd-stage">正在连接原站…</span>' +
            '<span class="rd-elapsed">刚刚</span>' +
          '</div>' +
          '<div class="rd-skeleton" aria-hidden="true">' +
            '<i class="sk-title"></i><i class="sk-meta"></i>' +
            '<i class="sk-line"></i><i class="sk-line"></i><i class="sk-line short"></i>' +
            '<i class="sk-line"></i><i class="sk-line short"></i>' +
          '</div>' +
          '<div class="rd-slow-tip">等太久？' +
            '<a class="rd-retry" href="#">重新获取</a> · ' +
            '<a class="rd-open-full" href="#">改用完整页打开</a>' +
          '</div>' +
        '</div>' +
        '<iframe class="rd-frame" title="站内阅读" src="about:blank"></iframe>' +
      '</div>';
    document.body.appendChild(overlay);
    frame = overlay.querySelector('.rd-frame');
    label = overlay.querySelector('.rd-panel-title');
    fullLink = overlay.querySelector('.rd-full');
    overlay.querySelector('.rd-close').addEventListener('click', close);
    overlay.addEventListener('click', function (e) {
      if (e.target === overlay) { close(); return; }
      var retry = e.target.closest && e.target.closest('.rd-retry');
      if (retry) { e.preventDefault(); load(src, true); return; }
      var open = e.target.closest && e.target.closest('.rd-open-full');
      if (open) { e.preventDefault(); window.open(fullLink.href, '_blank', 'noopener'); }
    });
    frame.addEventListener('load', function () {
      var l = overlay && overlay.querySelector('.rd-loading');
      if (l) l.hidden = true;
      stopTimer();
    });
  }

  function load(href, refresh) {
    var sep = href.indexOf('?') >= 0 ? '&' : '?';
    src = href;
    fullLink.href = href;
    var box = overlay.querySelector('.rd-loading');
    box.hidden = false;
    box.classList.remove('is-slow');
    box.querySelector('.rd-stage').textContent = stage(0);
    box.querySelector('.rd-elapsed').textContent = '刚刚';
    var url = href + sep + 'embed=1' + (refresh ? '&refresh=1' : '');
    // 相同 src 浏览器可能不触发 load；先置空强制重载
    frame.src = 'about:blank';
    frame.src = url;
    started = Date.now();
    stopTimer();
    timer = setInterval(tick, 500);
    document.body.classList.add('reader-open');
  }

  document.addEventListener('click', function (e) {
    if (e.defaultPrevented || e.button !== 0 ||
        e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var a = e.target && e.target.closest ? e.target.closest('a[data-read]') : null;
    if (!a) return;
    var href = a.getAttribute('href');
    if (!href || href.charAt(0) !== '/') return;
    e.preventDefault();
    if (!overlay) build();
    label.textContent = (a.textContent || '').trim().slice(0, 60);
    load(href, false);
  });

  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && overlay) close();
  });
  window.addEventListener('message', function (e) {
    if (e.data === 'ns-reader-close') close();
  });
}

/* M7/M8 阅读页粒度化翻译：标题 / 段落 / 全文三档 + 每段独立「译」按钮。
   保存原始 innerHTML，切回原文时能还原正文里的链接与图片；
   最多处理 40 段，避免一篇长文打爆公共翻译额度。 */
function initReaderTranslate() {
  var group = document.getElementById('rd-tr-group');
  if (!group) return;
  var raw = Array.prototype.slice.call(document.querySelectorAll(
    '.rd-title, .reader-body p, .reader-body li, .reader-body h1, .reader-body h2, ' +
    '.reader-body h3, .reader-body blockquote, .reader-body figcaption, .reader-body td'
  )).filter(function (n) { return n.textContent.trim().length > 8; }).slice(0, 40);
  if (!raw.length) return;

  var nodes = raw.map(function (n) {
    return {
      el: n,
      html: n.innerHTML,
      text: n.textContent.trim().slice(0, 500),
      scope: (n.classList.contains('rd-title') ||
              /^H[1-3]$/.test(n.tagName)) ? 'title' : 'para',
      zh: '', on: false, btn: null
    };
  });
  var scopeBtns = Array.prototype.slice.call(group.querySelectorAll('[data-rd-scope]'));
  var busy = false;
  group.hidden = false;

  function inScope(u, scope) { return scope === 'all' || u.scope === scope; }
  function applyOne(u) {
    if (!u.zh) return false;
    u.el.textContent = u.zh;   // 译文是纯文本，切回原文时用保存的 innerHTML 还原
    u.on = true;
    if (u.btn) { u.btn.textContent = '原文'; u.btn.classList.add('on'); }
    u.el.classList.add('tr-done');
    return true;
  }
  function restoreOne(u) {
    if (!u.on) return;
    u.el.innerHTML = u.html;
    u.on = false;
    if (u.btn) { u.btn.textContent = '译'; u.btn.classList.remove('on'); }
    u.el.classList.remove('tr-done');
  }
  function refreshBtns() {
    scopeBtns.forEach(function (b) {
      var t = nodes.filter(function (u) { return inScope(u, b.dataset.rdScope); });
      b.classList.toggle('on', t.length > 0 && t.every(function (u) { return u.on || !!u.zh; }));
    });
  }
  function request(texts) {
    return fetch('/api/translate', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ texts: texts })
    }).then(function (r) { return r.json(); }).then(function (d) { return d.results || {}; });
  }

  // 每段旁插一个独立「译」按钮
  nodes.forEach(function (u) {
    var b = document.createElement('button');
    b.type = 'button'; b.className = 'tr-inline'; b.textContent = '译';
    b.title = '只翻译这一段（可点「原文」切回）';
    u.btn = b;
    if (u.el.parentNode) u.el.parentNode.insertBefore(b, u.el.nextSibling);
    b.addEventListener('click', function () {
      if (u.on) { restoreOne(u); refreshBtns(); return; }
      if (u.zh) { applyOne(u); refreshBtns(); return; }
      b.disabled = true; b.textContent = '…';
      request([u.text]).then(function (res) {
        if (res[u.text]) { u.zh = res[u.text]; applyOne(u); }
        else { b.title = '该段暂时没翻出来，稍后再试'; }
      }).catch(function () {
        b.title = '翻译请求失败，请检查 translate 配置';
      }).then(function () {
        b.disabled = false;
        if (!u.on) b.textContent = '译';
        refreshBtns();
      });
    });
  });

  function toggleScope(btn) {
    if (busy) return;
    var scope = btn.dataset.rdScope;
    var target = nodes.filter(function (u) { return inScope(u, scope); });
    if (!target.length) return;
    if (target.every(function (u) { return u.on; })) {
      target.forEach(restoreOne); refreshBtns(); return;
    }
    var pending = target.filter(function (u) { return !u.zh; });
    var finish = function () {
      target.forEach(applyOne);
      busy = false;
      scopeBtns.forEach(function (b) { b.disabled = false; });
      btn.textContent = old;
      refreshBtns();
    };
    if (!pending.length) { finish(); return; }
    busy = true;
    scopeBtns.forEach(function (b) { b.disabled = true; });
    var old = btn.textContent;
    btn.textContent = '翻译中…';
    request(pending.map(function (u) { return u.text; })).then(function (res) {
      pending.forEach(function (u) { if (res[u.text]) u.zh = res[u.text]; });
      finish();
    }).catch(function () {
      btn.title = '翻译请求失败，请检查 config.yaml 的 translate 配置';
      finish();
    });
  }

  scopeBtns.forEach(function (b) {
    b.addEventListener('click', function () { toggleScope(b); });
  });
  refreshBtns();
}

/* M10：搜索引擎下拉的记忆与联动。
   - URL 显式带 engines/engine 时以 URL 为准，并把生效值写回记忆，
     这样点击搜索页下方的引擎标签后，下次从顶栏搜索也会沿用；
   - URL 没带时用 localStorage 回填，从任意页面顶栏发起搜索都不丢选择；
   - 同一页顶栏下拉与搜索主框下拉保持同步。 */
function initSearchEngineMemory() {
  var KEY = 'ns_search_engines';
  var selects = Array.prototype.slice.call(
    document.querySelectorAll('select[data-engine-choice]'));
  if (!selects.length) return;
  var params = new URLSearchParams(window.location.search);
  var explicit = params.has('engines') || params.has('engine');
  var remembered = '';
  try { remembered = localStorage.getItem(KEY) || ''; } catch (e) {}
  function hasOption(sel, value) {
    return Array.prototype.some.call(sel.options, function (o) {
      return o.value === value;
    });
  }
  if (!explicit && remembered) {
    selects.forEach(function (sel) {
      if (hasOption(sel, remembered)) sel.value = remembered;
    });
  }
  if (explicit) {
    try { localStorage.setItem(KEY, selects[0].value); } catch (e) {}
  }
  selects.forEach(function (sel) {
    sel.addEventListener('change', function () {
      try { localStorage.setItem(KEY, sel.value); } catch (e) {}
      selects.forEach(function (other) {
        if (other !== sel && hasOption(other, sel.value)) other.value = sel.value;
      });
    });
  });
}
