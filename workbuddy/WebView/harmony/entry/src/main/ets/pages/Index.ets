// 站点坞 · 主页面
// 产品口径（用户 2026-11-11 拍板）：不做 HTML 解析、不卡片化，
// 所有站点一律由系统 ArkWeb 原样渲染；HarmonyOS 7 (API 26) 上铺满整屏。
// 返回链路：网页内后退 → 回站点列表 → 退出应用。
import { webview } from '@kit.ArkWeb';
import { common } from '@kit.AbilityKit';
import { promptAction } from '@kit.ArkUI';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { Site, SiteDraft, SiteStatus, SiteStatusEntry, emptyStatus } from '../model/Site';
import { SiteStore } from '../model/SiteStore';
import { SiteChecker } from '../model/SiteChecker';
import { SiteDockConst } from '../common/Constants';

const DOMAIN: number = 0x5D0C;
const TAG: string = 'Index';

// 表单输入框统一样式（@Extend 只能放在组件外，且只含属性方法、不含事件）
@Extend(TextInput)
function fieldInput() {
  .height(44)
  .fontSize(14)
  .fontColor($r('app.color.text_main'))
  .backgroundColor('#FFFFFF')
  .borderRadius(10)
  .padding({ left: 12, right: 12 })
}

@Entry
@Component
struct Index {
  @State view: string = 'dashboard';
  @State sites: Site[] = [];
  @State statusList: SiteStatusEntry[] = [];
  @State currentUrl: string = '';
  @State currentName: string = '';
  @State webLoading: boolean = false;
  @State searchText: string = '';
  @State editorId: string = '';
  @State formName: string = '';
  @State formUrl: string = '';
  @State formGroup: string = '';
  @State formIcon: string = '';
  @State formPinned: boolean = false;

  private store: SiteStore = SiteStore.getInstance();
  private controller: webview.WebviewController = new webview.WebviewController();
  private firstBrowse: boolean = true;

  async aboutToAppear(): Promise<void> {
    const ctx: common.Context = getContext(this);
    const count: number = await this.store.init(ctx);
    this.sites = this.store.list();
    try {
      // 调试期开启，配合 hdc fport tcp:9222 tcp:9222 + chrome://inspect 抓网页
      webview.WebviewController.setWebDebuggingAccess(SiteDockConst.WEB_DEBUGGING);
    } catch (err) {
      hilog.warn(DOMAIN, TAG, 'setWebDebuggingAccess failed: %{public}s', String(err));
    }
    hilog.info(DOMAIN, TAG, 'sites loaded: %{public}d', count);
    this.runHealthCheck();
  }

  onBackPress(): boolean {
    if (this.view === 'browse') {
      // 网页自己有历史时先站内后退，否则回仪表盘；返回 true 表示事件已消费
      try {
        if (this.controller.accessBackward()) {
          this.controller.backward();
          return true;
        }
      } catch (err) {
        hilog.warn(DOMAIN, TAG, 'accessBackward failed: %{public}s', String(err));
      }
      this.view = 'dashboard';
      return true;
    }
    if (this.view === 'editor') {
      this.view = 'dashboard';
      return true;
    }
    // 仪表盘交还系统，正常退出
    return false;
  }

  build() {
    Stack() {
      if (this.view === 'dashboard') {
        this.Dashboard()
      } else if (this.view === 'browse') {
        this.Browse()
      } else {
        this.Editor()
      }
    }
    .width('100%')
    .height('100%')
  }

  // ------------------------------------------------------------ 仪表盘

  @Builder
  Dashboard() {
    Column() {
      Row() {
        Column() {
          Text('站点坞')
            .fontSize(26)
            .fontWeight(FontWeight.Bold)
            .fontColor($r('app.color.text_main'))
          Text('我的站点 · 全屏浏览')
            .fontSize(12)
            .fontColor($r('app.color.text_muted'))
            .margin({ top: 4 })
        }
        .alignItems(HorizontalAlign.Start)
        .layoutWeight(1)

        Button('＋ 添加')
          .fontSize(14)
          .height(36)
          .backgroundColor($r('app.color.brand'))
          .onClick(() => {
            this.openEditorAdd()
          })
      }
      .width('100%')
      .padding({ left: 20, right: 20, top: 20, bottom: 14 })

      TextInput({ text: this.searchText, placeholder: '搜索站点名称或地址' })
        .height(40)
        .fontSize(14)
        .backgroundColor('#FFFFFF')
        .borderRadius(10)
        .margin({ left: 20, right: 20, bottom: 10 })
        .onChange((v: string) => {
          this.searchText = v
        })

      if (this.visibleSites().length === 0) {
        Column() {
          Text('没有匹配的站点')
            .fontSize(16)
            .fontColor($r('app.color.text_muted'))
          Text('点右上角「＋ 添加」把站点加进来')
            .fontSize(12)
            .fontColor($r('app.color.text_muted'))
            .margin({ top: 8 })
        }
        .layoutWeight(1)
        .justifyContent(FlexAlign.Center)
      } else {
        Scroll() {
          Column() {
            ForEach(this.visibleSites(), (site: Site) => {
              this.SiteCard(site)
            }, (site: Site) => site.id)
          }
          .width('100%')
          .padding({ left: 16, right: 16, bottom: 24 })
        }
        .layoutWeight(1)
        .scrollBar(BarState.Off)
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor($r('app.color.page_bg'))
    .expandSafeArea([SafeAreaType.SYSTEM], [SafeAreaEdge.TOP, SafeAreaEdge.BOTTOM])
  }

  @Builder
  SiteCard(site: Site) {
    Column() {
      // 第一行：点击主体进入整屏网页
      Row() {
        Text(site.icon.length > 0 ? site.icon : '🌐')
          .fontSize(24)
          .width(42)
        Column() {
          Text(site.name)
            .fontSize(16)
            .fontWeight(FontWeight.Medium)
            .fontColor($r('app.color.text_main'))
            .maxLines(1)
            .textOverflow({ overflow: TextOverflow.Ellipsis })
          Text(site.url)
            .fontSize(11)
            .fontColor($r('app.color.text_muted'))
            .maxLines(1)
            .textOverflow({ overflow: TextOverflow.Ellipsis })
            .margin({ top: 3 })
          Text(this.statusText(site.id))
            .fontSize(11)
            .fontColor(this.statusColor(site.id))
            .margin({ top: 5 })
        }
        .alignItems(HorizontalAlign.Start)
        .layoutWeight(1)

        Text('●')
          .fontSize(12)
          .fontColor(this.statusColor(site.id))
          .margin({ left: 8 })
      }
      .width('100%')
      .onClick(() => {
        this.openSite(site)
      })

      // 第二行：操作区与主体分离，避免点击事件冒泡误触发打开网页
      Row() {
        Text(site.group.length > 0 ? site.group : '未分组')
          .fontSize(11)
          .fontColor($r('app.color.text_muted'))
        Blank()
        Text(site.pinned ? '★ 取消置顶' : '☆ 置顶')
          .fontSize(12)
          .fontColor($r('app.color.brand'))
          .onClick(() => {
            this.togglePin(site.id)
          })
        Text('编辑')
          .fontSize(12)
          .fontColor($r('app.color.brand'))
          .margin({ left: 14 })
          .onClick(() => {
            this.openEditor(site)
          })
        Text('删除')
          .fontSize(12)
          .fontColor('#E34D4D')
          .margin({ left: 14 })
          .onClick(() => {
            this.confirmDelete(site)
          })
      }
      .width('100%')
      .margin({ top: 12 })
    }
    .width('100%')
    .padding(16)
    .margin({ bottom: 10 })
    .backgroundColor($r('app.color.card_bg'))
    .borderRadius(14)
  }

  // ------------------------------------------------------------ 全屏网页

  @Builder
  Browse() {
    Stack() {
      Web({ src: this.currentUrl, controller: this.controller })
        .width('100%')
        .height('100%')
        .javaScriptAccess(true)
        .domStorageAccess(true)
        .fileAccess(false)
        .mixedMode(MixedMode.All)
        .zoomAccess(true)
        .onPageBegin(() => {
          this.webLoading = true
        })
        .onPageEnd(() => {
          this.webLoading = false
        })
        .onErrorReceive(() => {
          this.webLoading = false
        })

      if (this.webLoading) {
        Column() {
          Text('加载中 · ' + this.currentName)
            .fontSize(13)
            .fontColor('#FFFFFF')
        }
        .padding({ left: 14, right: 14, top: 8, bottom: 8 })
        .backgroundColor('#99000000')
        .borderRadius(8)
      }
    }
    .width('100%')
    .height('100%')
    .expandSafeArea([SafeAreaType.SYSTEM], [SafeAreaEdge.TOP, SafeAreaEdge.BOTTOM])
  }

  // ------------------------------------------------------------ 添加 / 编辑

  @Builder
  Editor() {
    Column() {
      Row() {
        Text('取消')
          .fontSize(15)
          .fontColor($r('app.color.text_muted'))
          .onClick(() => {
            this.view = 'dashboard'
          })
        Blank()
        Text(this.editorId.length === 0 ? '添加站点' : '编辑站点')
          .fontSize(18)
          .fontWeight(FontWeight.Bold)
          .fontColor($r('app.color.text_main'))
        Blank()
        Text('保存')
          .fontSize(15)
          .fontColor($r('app.color.brand'))
          .onClick(() => {
            this.saveEditor()
          })
      }
      .width('100%')
      .padding({ left: 20, right: 20, top: 20, bottom: 16 })

      Column() {
        this.FieldLabel('站点名称')
        TextInput({ text: this.formName, placeholder: '例如：大礼帽' })
          .fieldInput()
          .onChange((v: string) => {
            this.formName = v
          })

        this.FieldLabel('站点地址')
        TextInput({ text: this.formUrl, placeholder: 'https://bighat.icu:7000' })
          .fieldInput()
          .onChange((v: string) => {
            this.formUrl = v
          })
        Text('支持 http / 裸 IP / 非标端口；未写协议时自动补 https://')
          .fontSize(11)
          .fontColor($r('app.color.text_muted'))
          .margin({ top: 6, bottom: 4 })

        this.FieldLabel('分组（可空）')
        TextInput({ text: this.formGroup, placeholder: '例如：自有 / 工具 / 示例' })
          .fieldInput()
          .onChange((v: string) => {
            this.formGroup = v
          })

        this.FieldLabel('图标文字（可空）')
        TextInput({ text: this.formIcon, placeholder: '一个 emoji 或字符' })
          .fieldInput()
          .onChange((v: string) => {
            this.formIcon = v
          })

        Row() {
          Text('置顶显示')
            .fontSize(14)
            .fontColor($r('app.color.text_main'))
          Blank()
          Text(this.formPinned ? '★ 已置顶' : '☆ 未置顶')
            .fontSize(14)
            .fontColor($r('app.color.brand'))
            .onClick(() => {
              this.formPinned = !this.formPinned
            })
        }
        .width('100%')
        .padding({ top: 14, bottom: 6 })

        Button('保存站点')
          .fontSize(15)
          .fontColor(Color.White)
          .backgroundColor($r('app.color.brand'))
          .height(44)
          .width('100%')
          .margin({ top: 18 })
          .onClick(() => {
            this.saveEditor()
          })
      }
      .width('100%')
      .padding({ left: 20, right: 20 })
    }
    .width('100%')
    .height('100%')
    .backgroundColor($r('app.color.page_bg'))
    .expandSafeArea([SafeAreaType.SYSTEM], [SafeAreaEdge.TOP, SafeAreaEdge.BOTTOM])
  }

  @Builder
  FieldLabel(text: string) {
    Text(text)
      .fontSize(13)
      .fontColor($r('app.color.text_muted'))
      .width('100%')
      .margin({ top: 10, bottom: 6 })
  }

  // ------------------------------------------------------------ 行为

  private openSite(site: Site): void {
    this.currentUrl = site.url
    this.currentName = site.name
    this.webLoading = true
    this.view = 'browse'
    if (this.firstBrowse) {
      this.firstBrowse = false
      promptAction.showToast({
        message: '网页已全屏 · 返回键或侧滑回到站点列表',
        duration: 3000
      })
    }
  }

  private openEditorAdd(): void {
    this.editorId = ''
    this.formName = ''
    this.formUrl = ''
    this.formGroup = ''
    this.formIcon = ''
    this.formPinned = false
    this.view = 'editor'
  }

  private openEditor(site: Site): void {
    this.editorId = site.id
    this.formName = site.name
    this.formUrl = site.url
    this.formGroup = site.group
    this.formIcon = site.icon
    this.formPinned = site.pinned
    this.view = 'editor'
  }

  private async saveEditor(): Promise<void> {
    const name: string = this.formName.trim()
    const rawUrl: string = this.formUrl.trim()
    if (name.length === 0 || rawUrl.length === 0) {
      promptAction.showToast({ message: '名称和地址不能为空' })
      return
    }
    const draft: SiteDraft = {
      name: name,
      url: this.store.normalizeUrl(rawUrl),
      group: this.formGroup.trim(),
      icon: this.formIcon.trim(),
      pinned: this.formPinned
    }
    if (this.editorId.length === 0) {
      const ok: boolean = await this.store.add(draft)
      if (!ok) {
        promptAction.showToast({ message: '已达 ' + SiteDockConst.MAX_SITES.toString() + ' 个站点上限' })
        return
      }
    } else {
      await this.store.update(this.editorId, draft)
    }
    this.sites = this.store.list()
    this.view = 'dashboard'
    this.runHealthCheck()
  }

  private async togglePin(id: string): Promise<void> {
    await this.store.togglePin(id)
    this.sites = this.store.list()
  }

  private async confirmDelete(site: Site): Promise<void> {
    try {
      const res = await promptAction.showDialog({
        title: '删除站点',
        message: '确定删除「' + site.name + '」？',
        buttons: [
          { text: '取消', color: '#7A8699' },
          { text: '删除', color: '#E34D4D' }
        ]
      })
      if (res.index === 1) {
        await this.store.remove(site.id)
        this.sites = this.store.list()
        promptAction.showToast({ message: '已删除' })
      }
    } catch (err) {
      hilog.warn(DOMAIN, TAG, 'showDialog failed: %{public}s', String(err))
    }
  }

  private async runHealthCheck(): Promise<void> {
    const targets: Site[] = []
    for (const s of this.sites) {
      if (s.pinned && targets.length < SiteDockConst.CHECK_TOP_N) {
        targets.push(s)
      }
    }
    for (let i = 0; i < targets.length; i += SiteDockConst.CHECK_CONCURRENCY) {
      const batch: Site[] = targets.slice(i, i + SiteDockConst.CHECK_CONCURRENCY)
      const jobs: Promise<void>[] = []
      for (const s of batch) {
        jobs.push(this.checkOne(s))
      }
      await Promise.all(jobs)
    }
  }

  private async checkOne(site: Site): Promise<void> {
    this.setStatus(site.id, {
      state: 'checking', code: 0, latencyMs: 0, checkedAt: Date.now(), message: ''
    })
    const st: SiteStatus = await SiteChecker.check(site.url, SiteDockConst.CHECK_TIMEOUT_MS)
    this.setStatus(site.id, st)
  }

  private setStatus(id: string, st: SiteStatus): void {
    const next: SiteStatusEntry[] = []
    for (const e of this.statusList) {
      if (e.id !== id) {
        next.push(e)
      }
    }
    next.push({ id: id, status: st })
    this.statusList = next
  }

  private statusOf(id: string): SiteStatus {
    for (const e of this.statusList) {
      if (e.id === id) {
        return e.status
      }
    }
    return emptyStatus()
  }

  private statusColor(id: string): string {
    const st: SiteStatus = this.statusOf(id)
    if (st.state === 'online') {
      return '#2FA36B'
    }
    if (st.state === 'offline') {
      return '#E34D4D'
    }
    if (st.state === 'checking') {
      return '#E5A23C'
    }
    return '#A9B2C3'
  }

  private statusText(id: string): string {
    const st: SiteStatus = this.statusOf(id)
    if (st.state === 'online') {
      return '在线 · ' + st.latencyMs.toString() + ' ms'
    }
    if (st.state === 'offline') {
      return '不可达' + (st.code > 0 ? ' · HTTP ' + st.code.toString() : '')
    }
    if (st.state === 'checking') {
      return '检测中…'
    }
    return '未检测'
  }

  private visibleSites(): Site[] {
    const key: string = this.searchText.trim().toLowerCase()
    const out: Site[] = []
    for (const s of this.sites) {
      if (key.length === 0 ||
        s.name.toLowerCase().includes(key) ||
        s.url.toLowerCase().includes(key)) {
        out.push(s)
      }
    }
    return out
  }
}
