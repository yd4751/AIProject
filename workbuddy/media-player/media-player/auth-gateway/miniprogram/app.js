const cfg = require('./config');

App({
  globalData: { gatewayBase: cfg.getBase() },
  onLaunch() {
    this.globalData.gatewayBase = cfg.getBase();
  },
});
