// 验证通过后的落地说明页。
// 不使用 web-view：内嵌网页需要业务域名校验与相应主体资质，且网关要有公网 HTTPS 域名；
// 个人主体小程序上传/发布时容易因此受阻。扫码登录链路只依赖 gate 页的 wx.login + /__auth/mp-login。
const cfg = require('../../config');

Page({
  data: { base: '', verified: false },
  onLoad() {
    this.setData({
      base: cfg.getBase(),
      verified: !!wx.getStorageSync('mp_gate_token'),
    });
  },
  copyBase() {
    wx.setClipboardData({ data: cfg.getBase() + '/' });
  },
  back() {
    wx.reLaunch({ url: '/pages/gate/index' });
  },
});
