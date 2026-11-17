// 小程序端：网页扫码 → 微信身份校验（个人主体小程序即可，0 元、无需公章 / 企业认证）
// 链路：网页展示小程序码（getwxacodeunlimit，scene=登录票据）→ 微信扫码打开本页
//   → onLoad 取 options.scene → wx.login 拿 code → POST /__auth/mp-login（code+ticket）
//   → 网关换 openid 校验白名单 → 网页轮询到结果后自动进入。
// 网关地址取 config.js：发布后也可在小程序内点「设置网关地址」修改，无需重新上传代码。
// 小程序后台需把网关地址加入 request 合法域名（HTTPS 公网域名）；
// 本页路径要与网关 config.json 的 wechat.miniPage 一致（默认 pages/gate/index）。
// 注意：网关 loginMode=device 时微信接口返回 403 WECHAT_DISABLED，本页会提示改用浏览器做设备授权。
const cfg = require('../../config');

Page({
  data: {
    status: '正在验证微信身份…', token: '', openid: '', denied: false, done: false,
    base: '', configured: true, deviceMode: false,
  },
  onLoad(options) {
    // 扫网页上的小程序码进来时，scene 就是网页登录票据；校验通过后网页端会自动进入
    this.ticket = options.scene ? decodeURIComponent(options.scene) : (options.ticket || '');
    const placeholder = cfg.isPlaceholder();
    this.setData({ base: cfg.getBase(), configured: !placeholder });
    if (placeholder) {
      this.setData({ status: '请先点「设置网关地址」填写网关的公网 HTTPS 地址' });
      return;
    }
    this.login();
  },
  login() {
    const base = cfg.getBase();
    this.setData({ status: '正在验证微信身份…', openid: '', denied: false,
      deviceMode: false, base: base });
    if (cfg.isPlaceholder()) {
      // 未配置网关地址时，点「重新验证」也直接弹出输入框，避免点了像没反应
      this.setData({ status: '尚未设置网关地址，请在弹窗里填写公网 HTTPS 地址' });
      this.setGateway();
      return;
    }
    wx.login({
      success: ({ code }) => {
        if (!code) return this.fail('wx.login 未返回 code');
        wx.request({
          url: base + '/__auth/mp-login',
          method: 'POST',
          header: { 'content-type': 'application/json' },
          data: { code, ticket: this.ticket },
          success: (r) => {
            const d = r.data || {};
            // 网关切到设备登录时微信接口返回 403 WECHAT_DISABLED：给明确指引，别只显示一句报错
            if (d.code === 'WECHAT_DISABLED') {
              this.setData({ deviceMode: true, done: false, denied: false, openid: '',
                status: '当前为设备登录模式，微信验证已停用' });
              return;
            }
            if (d.ok && d.token) {
              wx.setStorageSync('mp_gate_token', d.token);
              this.setData({ token: d.token, done: true,
                status: this.ticket ? '验证通过，电脑端即将自动进入' : '验证通过' });
              if (!this.ticket) this.goWeb();
            } else {
              // 非白名单：网关返回「无权访问」+ openid，复制给管理员即可在管理端一键放行
              const denied = d.code === 'NOT_IN_WHITELIST';
              this.setData({
                status: denied ? '无权访问：' + (d.error || '该微信账号未获授权') : (d.error || '校验未通过'),
                openid: d.openid || '',
                denied: denied,
              });
            }
          },
          fail: () => this.fail('无法连接鉴权网关，请检查 request 合法域名'),
        });
      },
      fail: () => this.fail('wx.login 调用失败'),
    });
  },
  setGateway() {
    const cur = cfg.isPlaceholder() ? '' : cfg.getBase();
    wx.showModal({
      title: '设置网关地址',
      content: cur,
      placeholderText: 'https://网关公网域名',
      editable: true,
      success: (r) => {
        if (!r.confirm) return;
        const out = cfg.setBase(r.content);
        if (!out.ok) { wx.showToast({ title: out.error, icon: 'none' }); return; }
        this.setData({ base: out.base, configured: true, done: false,
          token: '', openid: '', denied: false });
        this.login();
      },
    });
  },
  goWeb() {
    if (!this.data.token) return;
    wx.navigateTo({ url: '/pages/web/index' });
  },
  copyOpenid() {
    if (this.data.openid) wx.setClipboardData({ data: this.data.openid });
  },
  fail(msg) { this.setData({ status: msg }); },
});

// 小程序直接调后端 API 的用法（所有 /api/* 都会经网关鉴权）：
// wx.request({
//   url: cfg.getBase() + '/api/sites',
//   header: { Authorization: 'Bearer ' + wx.getStorageSync('mp_gate_token') },
//   success: (r) => console.log(r.data),
// });
