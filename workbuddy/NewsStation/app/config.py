"""配置加载：config.yaml -> pydantic 模型，缺失项用默认值。"""

from __future__ import annotations

from pathlib import Path

import yaml
from pydantic import BaseModel, Field

from app import runtime


class ServerConfig(BaseModel):
    """M9：监听端口等运行参数全部走 config.yaml，exe 与源码共用同一份。

    打包态下 workers / reload 会被强制忽略（单文件 exe 不支持多进程与热重载），
    其余项在两种运行方式下行为一致。
    """
    host: str = "127.0.0.1"           # 只允许本机访问；要局域网访问改 0.0.0.0
    port: int = Field(default=8000, ge=1, le=65535)
    workers: int = Field(default=1, ge=1, le=16)
    reload: bool = False              # 仅源码模式生效
    log_level: str = "info"           # critical/error/warning/info/debug/trace
    access_log: bool = True
    auto_open_browser: bool = False   # 启动后自动打开默认浏览器
    proxy_headers: bool = True        # 放在反向代理后面时读取 X-Forwarded-*
    forwarded_allow_ips: str = "127.0.0.1"


class FetchConfig(BaseModel):
    interval_minutes: int = 30
    concurrency: int = 6
    timeout_seconds: float = 15.0
    per_source_delay_ms: int = 800
    max_age_days: int = 7
    user_agent: str = "NewsStation/0.1 (+personal news aggregator)"


class ProxyConfig(BaseModel):
    url: str = ""
    for_foreign: bool = True


class AnalyzeConfig(BaseModel):
    tagger: str = "dict"                 # dict / hanlp / llm，见 PLAN 4.2
    cluster_threshold: float = 0.45
    cluster_window_hours: int = 48
    cluster_max_members: int = 12        # 比对时取事件最近 N 篇做候选，避免全量比较
    heat_half_life_hours: float = 6.0
    heat_window_hours: int = 24          # 热度基数统计窗口
    retention_days: int = 14             # 超过该天数的文章与空事件清理掉
    batch_size: int = 500                # 单轮最多处理多少篇新文章


class SmtpConfig(BaseModel):
    host: str = ""
    port: int = 465
    user: str = ""
    password: str = Field(default="", alias="pass")
    to: str = ""

    model_config = {"populate_by_name": True}

    @property
    def enabled(self) -> bool:
        return bool(self.host and self.user and self.password and self.to)


class OpenAICompatConfig(BaseModel):
    """任意 OpenAI 兼容的 chat completions 端点（一次请求翻一批）。"""
    base_url: str = ""
    api_key: str = ""
    model: str = "gpt-4o-mini"


class LibreConfig(BaseModel):
    """自托管 LibreTranslate，免密钥。"""
    url: str = ""
    api_key: str = ""


class TranslateConfig(BaseModel):
    """一键翻译：后端可插拔，默认自动回退链，开箱免密钥。"""
    backend: str = "auto"             # auto / mymemory / google_free / openai / libre / none
    target: str = "zh-CN"
    use_proxy: bool = True            # 境外端点复用 settings.proxy.url；MyMemory 固定直连
    batch_size: int = 20              # 单次请求最多翻多少条
    timeout_seconds: float = 15.0
    mymemory_email: str = ""          # 可选；填了把匿名配额 5000 提到 50000 字/天
    openai: OpenAICompatConfig = OpenAICompatConfig()
    libre: LibreConfig = LibreConfig()


class SocialConfig(BaseModel):
    """M6：社交媒体热榜。榜单存快照，另可把前 N 条注入主事件流。"""
    enabled: bool = True
    inject_articles: bool = True      # 注入 articles，让热点自动走分类/订阅/翻译
    inject_top: int = 12              # 每个平台注入前 N 条（控制事件流噪音）
    limit: int = 30                   # 每个平台榜单取多少条
    retention_days: int = 7           # 快照保留天数


class ReaderConfig(BaseModel):
    """M7 站内阅读：浏览器只与本站通信，由服务端代取原网页、清洗正文后返回。"""
    enabled: bool = True
    proxy: str = "auto"               # auto（直连优先、失败回退代理） / direct / force
    timeout_seconds: float = 12.0
    connect_timeout_seconds: float = 4.0   # 建连单独限时：不通就快速换通道，别干等
    remember_route: bool = True       # 记住每个域名上次成功的通道，下次优先复用
    max_bytes: int = 2_000_000        # 单页最大下载量，超过即截断
    max_redirects: int = 4            # 每跳都重新做 SSRF 校验
    cache_hours: float = 6.0          # 成功页面的缓存时长
    min_text_chars: int = 120         # 正文低于此长度判为「内容不足」
    allowed_domains: list[str] = []   # 非空时只放行这些域名（含子域）
    allow_external: bool = True       # 失败时是否显示「手动在浏览器打开」兜底
    proxy_images: bool = True         # 正文图片也由服务端代取，避免防盗链/外站直连
    user_agent: str = (
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
        " (KHTML, like Gecko) Chrome/124.0 Safari/537.36"
    )


class GoogleSearchConfig(BaseModel):
    """谷歌引擎：Google 资讯 RSS「本地版 + 全球版」+ DuckDuckGo 网页结果。

    Google 网页搜索对数据中心 IP 一律 429 并强制 JS（实测各种 UA / gbv / udm /
    Cookie 均 0 条），服务端根本取不到结果列表；所以「网页结果」改由
    DuckDuckGo 的 HTML 版提供（`ddg_web`，服务端代取稳定、链接是真实 URL），
    同时页面仍保留「在 Google 中直接打开」入口给需要原始 Google 页面的场景。
    `google_web` 的解析函数保留但默认不启用，Google 哪天放开可直接加回 providers。
    """
    enabled: bool = True
    providers: list[str] = ["google_news", "google_global"]
    hl: str = "zh-CN"           # 本地版语言/地区
    gl: str = "CN"
    ceid: str = "CN:zh-Hans"
    global_hl: str = "en-US"    # 全球版与本地版参数不同，结果不重复
    global_gl: str = "US"
    global_ceid: str = "US:en"
    num: int = Field(default=10, ge=1, le=50)
    results_per_provider: int = Field(default=20, ge=1, le=50)  # 每路 RSS 取多少条
    # 「网页结果」子分区：DuckDuckGo 可服务端代取；置空即不要网页结果
    web_providers: list[str] = ["ddg_web"]
    web_results: int = Field(default=10, ge=0, le=30)
    web_kl: str = "cn-zh"           # DDG 地区：cn-zh 偏中文，wt-wt 为不限
    timeout_seconds: float = 8.0    # 境外端点单独限时，别拖慢整个搜索页
    prefer_proxy: bool = True       # 境外站优先走 settings.proxy.url，少等一次直连超时

    @property
    def url_opts(self) -> dict:
        return {
            "hl": self.hl, "gl": self.gl, "ceid": self.ceid, "num": self.num,
            "ghl": self.global_hl, "ggl": self.global_gl, "gceid": self.global_ceid,
            "kl": self.web_kl,
        }


class SearchConfig(BaseModel):
    """M7 关键字搜索：站内全库检索 + 外部搜索结果由服务端代取。
    M10：按「引擎」组织，默认引擎 = 原有整套，谷歌引擎可选；结果各自独立展示。
    """
    enabled: bool = True
    external_enabled: bool = True
    engines: list[str] = ["default", "google"]   # 页面上可勾选的引擎
    default_engine: str = "default"               # 首次打开只跑它，保持 M7 的体验
    google: GoogleSearchConfig = GoogleSearchConfig()
    providers: list[str] = ["sogou_news", "bing_rss", "baidu_news"]
    max_results: int = 12             # 每个外部源最多取多少条
    local_limit: int = 40             # 每个站内分区最多取多少条
    cache_minutes: int = 30           # 外部搜索结果缓存
    timeout_seconds: float = 12.0


class Settings(BaseModel):
    db_path: str = "data/news.db"
    server: ServerConfig = ServerConfig()
    fetch: FetchConfig = FetchConfig()
    proxy: ProxyConfig = ProxyConfig()
    smtp: SmtpConfig = SmtpConfig()
    analyze: AnalyzeConfig = AnalyzeConfig()
    translate: TranslateConfig = TranslateConfig()
    social: SocialConfig = SocialConfig()
    reader: ReaderConfig = ReaderConfig()
    search: SearchConfig = SearchConfig()


def load(path: str | Path | None = None) -> Settings:
    """默认读运行目录的 config.yaml；也可用 NEWSSTATION_CONFIG / --config 指定。"""
    p = Path(path) if path else runtime.config_path()
    if not p.exists():
        return Settings()
    data = yaml.safe_load(p.read_text(encoding="utf-8")) or {}
    return Settings.model_validate(data)


def abs_db_path(settings: Settings) -> Path:
    return runtime.resolve(settings.db_path)
