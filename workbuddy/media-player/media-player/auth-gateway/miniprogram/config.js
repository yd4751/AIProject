// 网关地址配置。
// 部署前把 DEFAULT_BASE 改成鉴权网关的「公网 HTTPS」地址，并在小程序后台配置
// request 合法域名（微信要求 HTTPS + 已备案域名，不能用 IP / 内网地址 / http）。
// 发布后也可以直接在小程序里点「设置网关地址」修改，无需重新上传代码。
const DEFAULT_BASE = 'https://your-domain.com';
const STORE_KEY = 'mp_gate_base';

function clean(s) { return String(s || '').trim().replace(/\/+$/, ''); }

function getBase() {
  try {
    const v = clean(wx.getStorageSync(STORE_KEY));
    if (v) return v;
  } catch (err) { /* 存储不可用时用默认值 */ }
  return clean(DEFAULT_BASE);
}

function isPlaceholder() {
  return getBase() === clean(DEFAULT_BASE) && clean(DEFAULT_BASE).indexOf('your-domain') >= 0;
}

function setBase(v) {
  const s = clean(v);
  if (!/^https:\/\/[^\s/]+/i.test(s)) {
    return { ok: false, error: '请填写 https:// 开头的公网网关地址' };
  }
  try { wx.setStorageSync(STORE_KEY, s); } catch (err) {
    return { ok: false, error: '本机保存失败，请稍后重试' };
  }
  return { ok: true, base: s };
}

module.exports = { DEFAULT_BASE: clean(DEFAULT_BASE), STORE_KEY, getBase, isPlaceholder, setBase };
