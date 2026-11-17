import { AbilityConstant, UIAbility, Want } from '@kit.AbilityKit';
import { window } from '@kit.ArkUI';
import { hilog } from '@kit.PerformanceAnalysisKit';

const DOMAIN: number = 0x5D0C;
const TAG: string = 'EntryAbility';

export default class EntryAbility extends UIAbility {
  onCreate(want: Want, launchParam: AbilityConstant.LaunchParam): void {
    hilog.info(DOMAIN, TAG, 'onCreate');
  }

  onWindowStageCreate(windowStage: window.WindowStage): void {
    windowStage.loadContent('pages/Index', (err) => {
      if (err.code) {
        hilog.error(DOMAIN, TAG, 'loadContent failed: %{public}d', err.code);
      } else {
        hilog.info(DOMAIN, TAG, 'loadContent ok');
      }
    });

    // 沉浸式布局：让网页内容延伸到状态栏/导航栏下方。
    // 刻意不隐藏系统栏 —— 保留手势返回，避免全屏网页「进得去出不来」。
    windowStage.getMainWindow().then((win: window.Window) => {
      win.setWindowLayoutFullScreen(true).then(() => {
        hilog.info(DOMAIN, TAG, 'immersive layout enabled');
      }).catch((err: Object) => {
        hilog.warn(DOMAIN, TAG, 'immersive layout failed: %{public}s', String(err));
      });
    }).catch((err: Object) => {
      hilog.warn(DOMAIN, TAG, 'getMainWindow failed: %{public}s', String(err));
    });
  }

  onWindowStageDestroy(): void {
    hilog.info(DOMAIN, TAG, 'onWindowStageDestroy');
  }

  onForeground(): void {
    hilog.info(DOMAIN, TAG, 'onForeground');
  }

  onBackground(): void {
    hilog.info(DOMAIN, TAG, 'onBackground');
  }
}
