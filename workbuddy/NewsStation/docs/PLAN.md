# NewsStation 规划

全球新闻自动捕获 → 自动整理 → 热点可视化的单机站点。Python 全栈，SQLite 存储，服务端渲染，无前端构建链。

## 1. 技术栈

| 关注点 | 选择 | 理由 |
| --- | --- | --- |
| 运行时 | Python 3.13（本机 3.13.14，pip 26） | 分词/NER/聚类/LLM 全都有成熟库 |
| Web | FastAPI + Jinja2Templates + StaticFiles | 异步友好，模板直出，无 Node 构建 |
| 存储 | SQLite（标准库 `sqlite3`，WAL 模式） | 单文件、零运维；查询含标签联表，裸 SQL 比 ORM 更可控 |
| 调度 | APScheduler `AsyncIOScheduler` | 进程内定时，抓取 30min / 摘要邮件随抓取 / 每日清理 |
| 抓取 | `httpx.AsyncClient` + `feedparser` | 异步并发；feedparser 解析在 `asyncio.to_thread` 里跑，不阻塞事件循环 |
| 分词 | `jieba`（中文）+ 自建英文分词 | 成熟、快；`jieba.analyse` 自带 TF-IDF，省掉 sklearn |
| 匹配 | `pyahocorasick` | 四维词典合成一个自动机，一次扫描拿到全部命中 |
| NER | 三层可切换：词典 / HanLP / LLM | 见 4.2，默认词典，配置切换 |
| 配置 | `pydantic-settings` + YAML | 与 FastAPI 同栈，校验直接报错 |

不引入：Docker、Redis、消息队列、前端框架、ORM。单人站点，这些只增加运维面积。

## 2. 目录结构

```
NewsStation/
  pyproject.toml
  config.yaml                    运行配置（含 SMTP / 翻译 / 社媒 / 阅读代理 / 搜索）
  sources.yaml                   RSS / HTML 列表源清单
  social_sources.yaml            M6：社交媒体热榜源清单
  run.bat                        一键启动（venv + 绝对路径，绕开损坏的 py 启动器）
  app/
    main.py                      FastAPI 实例、lifespan、路由挂载
    config.py                    pydantic 配置模型
    db.py                        schema 初始化、WAL、连接与事务封装
    ingest/
      registry.py                Source 协议 + 注册表（按 type 分发 RSS / HTML）
      rss.py                     通用 RSS 源
      html_source.py             无 RSS 的列表页源（解析器注册表 + HTMLSource）
      fetch.py                   并发抓取、限流、重试、去重入库
      social/                    M6：社媒热榜插件（独立注册表）
        base.py                   SocialSource + YAML 装载
        parsers.py                5 个平台解析器，统一输出稳定 URL
        store.py                  榜单快照落库、排名升降、过期清理
    analyze/
      pipeline.py                编排：分词 → 标签 → 聚类 → 热度
      tokenize.py                jieba / 英文分词、停用词
      keywords.py                jieba.analyse TF-IDF
      tagger.py                  Tagger 协议 + DictTagger / HanLPTagger / LLMTagger
      dicts/                     region.yaml / industry.yaml / type.yaml / entity.yaml
      cluster.py                 相似度、并簇
      heat.py                    热度公式
    web/
      routes.py                  页面与 JSON 接口
      queries.py                 展示层查询：四视图共用筛选语义、点阵/矩阵/象限坐标
      related.py                 M8：事件相似度（关键词 IDF / 实体 / 标题 / 标签 / 时间）
      social_queries.py          M6：榜单批次、排名变化、跨平台共振、平台状态
      templates/
        _filters.html            共用筛选条（地区/行业/类型，条件写进 URL）
        _cards.html              事件卡流
        _quad.html               热度 × 增速象限 SVG
        _matrix.html             行业热力矩阵
        _view_dot.html           点阵视图
        social.html              M6：五平台热榜 + 跨平台共振
      static/app.css, app.js     原生样式与交互（点阵 tooltip / 标签表单 / 粒度化翻译 / 站内阅读抽屉加载态），无构建链
    notify/                       M4：订阅匹配、SMTP 摘要
      matcher.py, mailer.py
    translate/                    M5：可插拔翻译后端 + SQLite 缓存
      backends.py, service.py
    reader/                       M7：站内阅读代理（服务端代取 + 正文清洗 + SSRF 防护）
      safety.py                    SSRF 四层校验（协议/端口/域名白名单/公网 IP，重定向逐跳重校）
      extract.py                  标准库 HTMLParser 正文选区 + 标签白名单 + 链接/图片改写
      fetcher.py                  直连优先、代理回退、限大小、错误降级、逐跳重定向校验
      store.py                    阅读缓存（url_hash 主键，TTL 复用）
    search/                       M7/M10：按引擎的关键字搜索
      engines.py                  M10：引擎注册表、选择解析、chip 链接生成
      local.py                    站内事件/报道/社媒热榜一次检索，多词 AND
      providers.py                URL 模板 + 解析（bing / sogou / baidu / google_news / google_global）
      service.py                  按引擎独立代取/缓存/报错；保留 M7 顶层兼容字段
    templates/
      read.html                   M7：站内阅读页（embed 模式供抽屉 iframe 载入）
      search.html                 M10：引擎选择器 + 每引擎独立结果区（全部走 /read 站内阅读）
  data/news.db
  tests/
  docs/PLAN.md
```

## 3. 插件契约

Python 不需要"编译期注册"的技巧，但也不该上 `importlib` 动态加载——那会让报错栈变难读。用 **`Protocol` + 显式注册表**，两类源：

```python
# app/ingest/registry.py
from typing import Protocol
from dataclasses import dataclass
from datetime import datetime

@dataclass(slots=True)
class Article:
    url: str
    title: str
    summary: str
    published_at: datetime | None
    lang: str
    source_id: str

class Source(Protocol):
    id: str
    name: str
    homepage: str
    lang: str
    def fetch(self, client: httpx.AsyncClient) -> list[Article]: ...

_registry: dict[str, Source] = {}

def register(s: Source) -> None:
    _registry[s.id] = s

def all_sources() -> list[Source]: ...
```

1. **配置型（主流）** —— `sources.yaml` 一条记录一个源，由 `RSSSource` 统一驱动：

```yaml
- id: bbc-world
  name: BBC · World
  homepage: https://www.bbc.com/news/world
  lang: en
  feed: https://feeds.bbci.co.uk/news/world/rss.xml
```

2. **配置型 HTML** —— 官网已停止供应 RSS、但列表页可直接解析的源，`type: html` 由 `HTMLSource` 驱动，`parser` 对应 `app/ingest/html_source.py::PARSERS` 的键。当前用于观察者网（首页配对提取标题+链接，URL 反解发布日期）：

```yaml
- id: guancha
  name: 观察者网
  lang: zh
  region: cn
  type: html
  parser: guancha
  url: https://www.guancha.cn/
  base_url: https://www.guancha.cn/
  limit: 30
```

3. **代码型** —— 无 RSS 或要走 API 的源，在 `app/ingest/sources/` 下实现 `fetch()` 并 `register()`。

首批分两批，按**当前网络实测**结果划分（2026-11-04 探测）：

**A. 可直连**（返回 `text/xml`，延迟 0.1~0.3s）

| 源 | feed | 覆盖 |
| --- | --- | --- |
| 新浪国际 | `https://rss.sina.com.cn/news/world/focus15.xml` | 国际综合 |
| 人民网国际 | `http://www.people.com.cn/rss/world.xml` | 国际综合 |
| 中新网国际 | `http://www.chinanews.com.cn/rss/world.xml` | 国际综合 |
| 界面新闻 | `https://a.jiemian.com/index.php?m=article&a=rss` | 财经 |
| 爱范儿 | `https://www.ifanr.com/feed` | 科技 |
| 钛媒体 | `https://www.tmtpost.com/rss` | 科技 |
| InfoQ 中文 | `https://www.infoq.cn/feed` | 技术 |
| 少数派 | `https://sspai.com/feed` | 科技/产品 |

**B. 需代理**（实测 ConnectTimeout / ReadTimeout）

BBC World、Guardian、NYT World、SCMP、Google News World、FT 中文网、NYT 中文网。这些源标 `proxy_required: true`，没配代理时**自动跳过且不记为失败**。

已剔除的源：36氪 `/feed` 现返回 SPA 的 HTML（非 feed）；环球网、联合早报返回 HTML；参考消息 404。都不可用，不硬啃。

## 4. 数据模型与标签体系

```sql
CREATE TABLE articles (
  id INTEGER PRIMARY KEY,
  source_id TEXT NOT NULL,
  url TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  summary TEXT,
  lang TEXT NOT NULL,
  category TEXT,
  published_at INTEGER,
  fetched_at INTEGER NOT NULL,
  event_id INTEGER
);
CREATE INDEX idx_articles_time ON articles(published_at DESC);
CREATE INDEX idx_articles_cat ON articles(category, published_at DESC);
CREATE INDEX idx_articles_event ON articles(event_id);

CREATE TABLE events (
  id INTEGER PRIMARY KEY,
  title TEXT NOT NULL,
  category TEXT,
  heat REAL DEFAULT 0,
  article_count INTEGER DEFAULT 0,
  source_count INTEGER DEFAULT 0,
  keywords TEXT,               -- JSON 数组
  first_seen INTEGER,
  last_seen INTEGER
);
CREATE INDEX idx_events_heat ON events(heat DESC, last_seen DESC);

CREATE TABLE subscriptions (
  id INTEGER PRIMARY KEY,
  keyword TEXT,
  tag_filter TEXT,             -- JSON：{"region":["us"],"industry":["tech"]}
  created_at INTEGER,
  last_matched_at INTEGER,
  unread_count INTEGER DEFAULT 0,
  notify_email INTEGER DEFAULT 1
);

CREATE TABLE source_state (
  source_id TEXT PRIMARY KEY,
  enabled INTEGER DEFAULT 1,
  last_run INTEGER,
  last_status TEXT,
  fail_streak INTEGER DEFAULT 0,
  last_count INTEGER
);

CREATE TABLE tags (
  id INTEGER PRIMARY KEY,
  dim TEXT NOT NULL,           -- region / industry / type / entity
  value TEXT NOT NULL,         -- us / tech / earnings / nvidia
  label TEXT NOT NULL,         -- 美国 / 科技 / 财报业绩 / 英伟达
  aliases TEXT,                -- JSON：["US","United States","美利坚"]
  parent TEXT,                 -- 地区归属：hk/mo/tw → cn
  UNIQUE(dim, value)
);

CREATE TABLE article_tags (
  article_id INTEGER NOT NULL,
  dim TEXT NOT NULL,
  value TEXT NOT NULL,
  confidence REAL,
  origin TEXT,                 -- dict / hanlp / llm / manual
  PRIMARY KEY (article_id, dim, value)
);
CREATE INDEX idx_at_value ON article_tags(dim, value);

CREATE TABLE event_tags (
  event_id INTEGER NOT NULL,
  dim TEXT NOT NULL,
  value TEXT NOT NULL,
  hit_count INTEGER DEFAULT 0,
  locked INTEGER DEFAULT 0,
  PRIMARY KEY (event_id, dim, value)
);
CREATE INDEX idx_et_value ON event_tags(dim, value);
CREATE INDEX idx_et_event ON event_tags(event_id, dim, hit_count DESC);
```

### 4.1 四个维度

| 维度 | 取值 | 多值 | 来源 |
| --- | --- | --- | --- |
| region 地区 | ISO 3166-1 alpha-2 + `global` | 是，事件取前 2 | 词典匹配 + 源所属地 |
| industry 行业 | 政治、财经、科技、能源、汽车、医疗健康、消费、地产、军工、文体娱乐、社会、气候环境 | 是，取前 2 | 关键词规则打分 |
| type 类型 | 突发灾害、冲突安全、政策法规、财报业绩、并购融资、产品发布、诉讼调查、人事变动、科研突破、体育赛事 | 是，取前 2 | 关键词组合模式 |
| entity 实体 | 机构、人物、产品（500 个高频实体起步） | 是，取前 3 | NER（三层可切） |

**为什么用统一 `tags` 表而不是给 events 加列**：地区天然多值（"英伟达财报"牵涉美国、中国台湾），行业也常跨两类。加列只能存一个，后面必改建表；`dim + value` 还能顺手支持任意维度筛选和点阵切换着色。

**归一化是重点**：`US / United States / 美国 / 美利坚` 必须落到同一个 `region:us`，否则筛选和统计会碎。别名词典进 `tags.aliases`，入库前统一走 `normalize(dim, raw)`。

**地区口径**：港澳台固定 `hk` / `mo` / `tw`，显示「中国香港 / 中国澳门 / 中国台湾」，`parent` 指 `cn`，聚合"中国"时一并计入。写死在词典里，避免各处口径不一。

### 4.2 NER 三层（Python 是分水岭）

Python 侧中文 NER 有成熟实现，所以不再像 Go 那样被迫用词典。但模型体积与 Python 3.13 的兼容性要先验证，因此做成三层：

| 层 | 实现 | 准确率 | 代价 | 默认 |
| --- | --- | --- | --- | --- |
| L1 词典 | `pyahocorasick` + entity.yaml | 中（新实体漏） | 0 依赖、微秒级 | 开 |
| L2 模型 | HanLP 小模型（ELECTRA-based，torch 后端） | 高 | torch ~200MB + 模型首次下载，加载占内存 | 验证通过后开 |
| L3 大模型 | LLM 抽取结构化 JSON | 最高 | 需 API key、有延迟与费用 | 按需 |

三者共用 `Tagger` 协议：

```python
class Tagger(Protocol):
    def tag(self, title: str, summary: str, lang: str) -> list[Tag]: ...
```

`pipeline` 只认协议，`config.yaml` 里 `analyze.tagger: dict|hanlp|llm` 切换，可以叠加（L1 先跑，L2 补齐，冲突时 L2 覆盖）。

**先验证再承诺**：M2 第一步单独跑一次 HanLP 安装试验（`pip install hanlp` + 载入小模型）。实测结论（2026-11-04）：**依赖装得上，模型下不来**——`hanlp 2.1.5 + torch 2.15 + transformers 5.22` 在 3.13 正常导入，但 `file.hankcs.com` 已不供 zip（返回落地页），`download.hanlp.com` 证书域名不匹配。L2 因此挂起，两条替代路径：离线投放模型目录，或改用 HF 上的中文 NER 权重（HF 与 hf-mirror 实测可达）。当前默认 L1 词典。

## 5. 分析管道

每次抓取后执行，只处理新入库文章：

1. **语言判定**：CJK 字符占比 > 15% 判中文，否则英文。
2. **分词与关键词**：中文 `jieba.lcut`，英文小写化去停用词；`jieba.analyse.extract_tags` 取前 8 词，标题词权重 ×1.5（不用 sklearn，省一个重依赖）。
3. **四维标签**：四个 YAML 词典合成一张词表 → `pyahocorasick` 一次扫描 → 按维度归一化、打分、截断。
   - 置信度：标题命中 0.9，摘要 0.6，别名 ×0.9；< 0.5 丢弃。
   - 类型用组合模式而非单词：`营收/净利润/guidance` → `earnings`；`收购/并购/takeover` → `mna`；`起诉/调查/probe` → `lawsuit`。
   - 实体维度交给 `Tagger`；L1 与 L2 结果按 `(dim, value)` 合并，L2 优先。
4. **事件聚类**：与最近 48h 内、行业有交集的候选事件比较：

```
sim = 0.7 * cos(title_tokens) + 0.3 * jaccard(article_tags, event_tags)
```

   `sim ≥ 0.45` 并入最相似事件，否则新建。行业无交集直接跳过，省算力也防跨行业误并。向量就是 token 计数字典，余弦相似度手写 20 行，不引 numpy。
5. **事件标签聚合**：簇内按 `hit_count` 排序，region/industry/type 各取前 2、entity 取前 3 写 `event_tags`；`locked=1` 优先保留。`events.category` 取 industry 首位，仅用于排序分组，不参与筛选。
6. **热度**：

```
recent    = 事件近 24h 文章数               # 原设计为 6h，实测会把隔夜事件全部归零
sources   = 参与媒体数
momentum  = (近3h - 前3h) / (近3h + 前3h)   # 比例式，天然落在 [-1,1]；近 6h 样本 <3 篇时记 0
base      = ln(1 + recent) * (1 + 0.3*(sources-1)) * (1 + 0.5*momentum)
heat      = base * exp(-hours_since_last_seen / 6)  # 6 小时半衰期
```

结果落在 `events` 上（含 `momentum`，供「热度×增速」象限用），页面只读不重算。

## 6. 页面与可视化

首页四种视图，`?view=` 保持可分享：

- **筛选条**（所有视图共用）：地区 / 行业 / 类型 三行 chip，可交叉，条件写进 URL（`?view=dot&region=us&industry=tech&type=earnings`）。地区首项固定「全球」，选「中国」自动含港澳台（走 `parent`）。
- **榜单 + 事件卡**（默认）：左侧 Top 10 热度榜（红条=热度，标注媒体数）；中间事件卡流（热度、媒体数、四维标签、最新 3 条标题、趋势箭头）；右侧「热度 × 增速」象限。
- **点阵视图**：每点一篇文章，按事件成团（簇心轻微抖动避重叠），**着色维度可切换**（地区/行业/类型），悬停出事件标题与篇数。仅近 24h。
- **行业热力矩阵**：行=12 类行业，列=近 12 个 2 小时时段，色深=文章数。
- **事件详情**：24h 热度曲线、四维标签（可人工改，改后 `locked=1`）、关系网络（事件—媒体连线，节点大小=该媒体贡献篇数）、全量报道倒序。

其他页面：行业页（矩阵 + 行业事件榜）、订阅页（关键字/标签时间线 + 未读数）、源管理页（启停、上次抓取状态、失败次数）。

视觉规范：浅色底、0.5px 描边、圆角 12px；热度红色梯度（涨=红）；标签 chip 统一灰底，只用 6px 圆点区分维度（地区蓝、行业紫、类型青、实体灰），避免彩虹色；行业固定 12 色调色板，点阵与矩阵共用。

前端不引框架：服务端把事件与坐标算好塞进模板，点阵用内联 SVG 画几百个 `<circle>`，交互用 60 行原生 JS。够用且没有构建步骤。

## 7. 订阅与邮件

- 命中规则：`title || summary` 含关键字（英文小写化），入库时同步匹配，写 `last_matched_at`、`unread_count++`。
- 标签订阅：`tag_filter` JSON，如 `{"region":["us"],"industry":["semiconductor"]}`，走 `event_tags` 联表，比关键字稳。
- 邮件：每轮抓取结束，把本轮新命中合成**一封**摘要（每订阅最多 5 条，按热度排序），避免轰炸。

```yaml
smtp:
  host: smtp.qq.com
  port: 465
  user: you@example.com
  pass: ""          # 授权码，填了才启用邮件
  to: you@example.com

proxy:
  url: ""           # 例 http://127.0.0.1:7890；留空则只跑 A 组源
  for_foreign: true # 仅 proxy_required 的源走代理，国内源直连
```

`pass` 为空时自动降级为仅站内，不报错。

## 8. 里程碑与验收

**M1 采集底座**（骨架 + DB + 插件框架 + RSS 源 + 调度）—— ✅ 已完成 2026-11-04
实测：15 个源全部 ok，单轮 511 条入库（zh 281 / en 230），全量 8.6s；重复执行只新增真实更新条目，URL 无重复。证据见 `.workbuddy/memory/2026-11-04.md`。
落地时补的两条：入库时效门槛 `max_age_days: 7`（实测源里混有 2018 年的旧条目）、传输层 3 次指数退避重试（代理出口对 BBC 存在间歇性连接重置）。

**M2 分析管道**（分词、关键词、四维标签、聚类、热度）—— ✅ 已完成 2026-11-04

实测（452 篇真实文章）：
- 产出 366 个事件，其中 4 个事件由 ≥3 家媒体共同报道（中期选举 4 源、欧洲航班炸弹威胁 4 源、西班牙交通部长辞职 5 源）
- 标签覆盖率：region 91% / industry 73% / type 30% / entity 19%（type 与 entity 覆盖率低是词典法固有特征，不是漏跑）
- 人工标注基准（`tests/fixtures/tag_labels.yaml`，24 条）准确率：region 91.3%、industry 87.0%、type 91.3%，均过验收线；`python -m tests.eval_tags <db>` 可复跑
- 21 个单测全过（含 US 代词不误判、chairman 不命中 china、YAML `no` 布尔、管道幂等）

落地时改掉的四处设计：
1. **热度计数窗口 6h → 24h**：半衰期也是 6h，基数只看 6h 会让隔夜事件一律归零，早上首页全是 0 热度。
2. **momentum 换比例式**：原式 `(last-prev)/max(prev,1)` 在 prev=0 时直接饱和，单篇新稿也吃满加成；改为占比式，且近 6h 样本 <3 篇不计算增速。
3. **源属地兜底（region）**：国内媒体的国内稿标题里根本不写「中国」，纯词典召回只有 65%。`sources.yaml` 给 `jiemian/ifanr/tmtpost/infoq-cn/sspai` 加 `region: cn`，无地区标签时以 0.55 置信度补上，把地区准确率抬到 91%。
4. **实体带行业提示**（`entity_industry.yaml`）：标题只写公司名（「AMD 财报大超预期」）时行业会漏标，命中实体后按主营行业补一次权重，行业覆盖率 65% → 73%。

**HanLP 安装试验结论（已验，结论为「暂不可用」）**：`hanlp 2.1.5 + torch 2.15(cpu) + transformers 5.22` 在 Python 3.13 上安装、导入均正常（import 8s）。但模型下载源 `file.hankcs.com` 已失效——返回落地页 HTML 而非 zip，官方给的 `download.hanlp.com` 又存在证书域名不匹配（代理中间人）。因此 **L2 当前拿不到权重**，退路是：① 手动提供离线模型目录；② 改用 HuggingFace 上的中文 NER 权重（HF 与 hf-mirror 实测均可达，`transformers` 已随 hanlp 装好）。L3（LLM）因缺 API key 未启用。

已知限制（不假装解决）：
- **跨语言不合并**：同一事件的中文稿与英文稿几乎没有共同 token，会被拆成两个 `event_id`（例如中期选举的中英文两组）。这是词典+余弦方案的边界，要跨语言得靠实体/时间/LLM。
- **阈值 0.45 偏保守**：实测 0.25~0.44 区间的中文异源标题对大量是「同国家、不同事件」（马尔代夫选举 vs 美国中期选举，0.408），降阈值会引入误合并，因此不下调。
- 单源模板稿（ETF 行情稿）会自成一簇并占据热度前列，M3 的榜单需要过滤。

**M3 展示层**（四视图 + 筛选条 + 行业页 + 事件详情）—— ✅ 已完成 2026-11-04，线上复验 2026-11-05

实现：`app/web/queries.py` 集中所有展示查询，页面与 `/api/events` 共用 `build_where`；四个视图 `?view=list|quad|dot|matrix` 可分享，筛选条件（region/industry/type）写进 URL，选「中国」按 `tags.parent` 自动展开 cn/hk/mo/tw。新增 `/industries`（12 行业卡片 + 全局矩阵）、`/industry/{value}`（行业节奏条 + 事件榜）、`/event/{id}`（24h 曲线、事件—媒体关系网络、四维标签编辑、全量报道）。

实测（生产库 538 篇 / 427 事件）：
- 四视图、行业页、事件详情、源管理页、`/api/events` 全部 200；首页 64KB、点阵 163KB、矩阵 30KB
- 组合筛选结果（`region=cn&industry=tech&type=earnings`）与 SQL 直查逐条一致（6 个 `tests/test_web.py` 用例覆盖）
- 点阵三个着色维度（地区/行业/类型）映射正确，行业复用固定 12 色；按报道量取前 80 个事件成团
- 事件详情标签改动经 `pipeline._refresh_event_tags` 刷新后仍在：`locked=1` 锁定/人工添加保留、`locked=-1` 屏蔽不重建、支持恢复
- 27 个 pytest 全过（M1 5 + M2 16 + M3 6）
- 服务 `http://127.0.0.1:8000` 重启后稳定运行，四视图线上 HTTP 200

落地时改掉的三处设计：
1. **模板稿过滤条件放宽**：原设计「单源 + 标题模板化 + 无实体标签」，实测「小米机器人…ETF」这类稿子被实体词典标了 `xiaomi`，会漏网。改为「单源 + 篇数≥3 + 标题含行情模板词（ETF/标的指数/涨停/涨超…）」即剔除，详情页仍可访问。
2. **象限分界线用热度中位数**：原用 `max_heat*0.45` 作高热门槛，真实数据只有 2 个事件过线，右侧两象限全空；改用中位数，与页面标注的「中位数」一致。
3. **`event_tags.locked` 扩成三态**：`1` 人工锁定、`0` 自动、`-1` 人工屏蔽。所有展示查询过滤 `-1`，`pipeline` 重建时同时跳过锁定与屏蔽值，人工编辑才不会被下一轮聚类覆盖。

**M4 订阅、邮件与交付**（订阅匹配、SMTP 摘要、启动脚本/部署说明）—— ✅ 已完成 2026-11-05

实现：`app/notify/`（`matcher.py` / `mailer.py` / `__init__.py`），新增 `subscription_hits` 表；`/subscriptions` 页面（新增/删除/标记已读、命中时间线、未读徽标）；`run_once` 记录 `MAX(id)` 水位 → 分析 → `after_fetch` 匹配 → 合成一封 SMTP 摘要。

实测（生产库 + 线上服务）：
- 触发一轮真实抓取：新增 212 篇文章（1204 → 1416），15 个源全部 ok；订阅关键字「AI」命中 104 条，订阅页时间线与未读徽标正确渲染，验证后已清理演示订阅
- 34 个 pytest 全过（M4 新增 7 个）：关键字/标签/组合匹配、UNIQUE 去重、未读计数与标记已读、摘要「每订阅最多 5 条按热度排序」、mock SMTP 验证「一轮只发一封」、端到端 `run_once` 触发命中
- 未配 `smtp.pass` 时自动降级为仅站内：命中照常记录、摘要照常合成，但不发信、不报错（页面有明确提示）
- **真实 SMTP 投递未验证**：本机没有邮箱授权码，发送路径由 mock SMTP 覆盖；填入 `config.yaml` 的 `smtp.pass` 即可启用

落地时改掉的三处设计：
1. **表单不引入 `python-multipart`**：环境里没有该依赖，订阅表单用 `await request.body()` + `parse_qs` 手工解析 `application/x-www-form-urlencoded`，与 M3 标签编辑同样零新增依赖。
2. **匹配必须排在分析之后**：标签订阅走 `event_tags`，事件归属只有 `pipeline.process` 跑完才确定，因此 `after_fetch` 在 `to_thread(pipeline.process)` 之后调用。
3. **水位用 `MAX(articles.id)` 而不是时间戳**：同一秒内多源并发写入会让 `fetched_at` 相同，用时间戳会把旧文章误判成新命中；自增 id 单调递增，无此问题。

**M5 一键翻译**（外文标题/摘要 → 中文，可一键切回原文）—— ✅ 已完成 2026-11-05

实现：`app/translate/`（`backends.py` / `service.py`），新增 `translations` 缓存表；Jinja 全局 `tr_attr` 只给外文文本打 `data-tr` + 原文 + 已缓存译文；`POST /api/translate` 批量翻译、`GET /api/translate/status` 查看后端与缓存。

**M8 粒度化改造（2026-11-12）**：原「整页一次全翻」改为 Twitter 式按需翻译。`tr_attr` 增加 `data-tr-scope`（`title` / `para`），顶栏与阅读页都改成三档范围控件 `译标题 / 译段落 / 译全文`；同时每个外文元素、每个正文段落旁注入独立「译」按钮，点哪条翻哪条、可单独切回「原文」。单条只发 1 条文本到 `/api/translate`（命中缓存则零外部请求），范围档仍走原来的批量请求；localStorage 只记住上次选择的范围，刷新后仅在该范围全部已有译文时自动恢复，不再默认整页全翻。后端与缓存表零改动。

后端可插拔（`translate.backend`）：

| 值 | 说明 |
| --- | --- |
| `auto`（默认） | 回退链 mymemory → google_free，任一成功即用，免密钥 |
| `mymemory` | MyMemory 公共 API，免密钥，国内直连可用 |
| `google_free` | Google 公开端点，境外走代理 |
| `openai` | OpenAI 兼容 chat completions，一次请求翻一批，质量最好；需 `openai.base_url` 与 `api_key` |
| `libre` | 自托管 LibreTranslate；需 `libre.url` |
| `none` | 关闭（按钮隐藏，只显示已缓存译文） |

关键取舍：
1. **默认 `auto` 而不是单一免费端点**：实测本机代理出口 IP 上 Google 4 个端点（gtx / dict-chrome-ex / clients5 / google.com）、MyMemory、Edge 匿名 token、Lingva、公共 LibreTranslate 全部 429 / 404 / 502；MyMemory **直连**（不走代理）正常，故默认链路为「直连 MyMemory 优先，失败再走代理 Google」。
2. **译文按 (文本 hash, 目标语言) 缓存**：`translations` 表 UNIQUE 约束；模板渲染时一次性读回全部成功译文注入 `data-zh`，刷新页面/换视图都不重复请求；失败结果写 1 小时冷却，避免反复打不通的接口；后端没配好时**不写**失败缓存，填好配置立刻生效。
3. **语言检测挡住中文**：CJK 占比 ≥20% 或纯数字/过短文本不送后端，中文标题原样保留；目标改成 `en` 时判定反转（中→英）。
4. **服务端注入 + 原生 JS，零新增依赖**：`tr_attr` 返回 `Markup`（内部 `html.escape`），模板直接 `{{ tr_attr(x) }}` 无需 `|safe`；JS 用 `data-text` 作原文与请求 key，避免 DOM 文本与缓存 key 不一致；`localStorage` 记住「显示译文」偏好。

实测：MyMemory 直连翻英/日/法 3 条全部返回正确译文；`auto` 链路端到端 2 条英文 1.29s 成功、中文跳过、二次调用全命中缓存；42 个 pytest 全过（M5 新增 8 个）。

**M6 社交媒体热榜**（国内 5 + 境外 3 平台：榜单快照 + 事件流注入）—— ✅ 已完成 2026-11-12

范围：用户要求**先接国内**，境外在本地代理就绪后补齐。2026-11-12 实测本机 `7890` 代理未监听，当日只接了国内 5 源；同日稍后用户开启本地代理，**境外 3 平台接入**：Hacker News（Algolia API）、Lobsters（hottest.json）、GitHub Trending（HTML），三者经代理实测 200 可达，且 `proxy_required: true` 复用 `config.proxy.url`；新增解析器 + `social_sources.yaml` 一行即可，编排零改动。

实现：`app/ingest/social/` 独立注册表 + 8 个解析器，`social_sources.yaml` 配置化；实现与 RSS 相同的 `Source` 协议，复用 `run_once` 的并发、重试与 `source_state` 记录。新增 `social_trends` 快照表（一轮一个 `batch_id`，存平台 / 排名 / 平台热度 / 升降 / 采集时间，`rank_delta` NULL = 新进榜，保留 7 天）。可选 `social.inject_articles`：每平台前 `inject_top` 条转成 `Article` 注入主事件流，自动获得四维标签、聚类、订阅命中与一键翻译；境外平台在 YAML 里用 `inject_top: 10` 覆盖全局 12，控制英文噪音。

平台实测（2026-11-12；国内直连 200，境外经代理 200，全部免鉴权）：

| 平台 | 区域 | 接口 | 条数/轮 | 热度字段 |
| --- | --- | --- | --- | --- |
| 微博热搜 | 国内 | `weibo.com/ajax/side/hotSearch`（需 Referer） | 30 | `num` |
| 百度热搜 | 国内 | `top.baidu.com/board?tab=realtime`（HTML 内 `<!--s-data:...-->`） | 30 | `hotScore` |
| 今日头条热榜 | 国内 | `toutiao.com/hot-event/hot-board/` | 30 | `HotValue` |
| 抖音热点 | 国内 | `iesdouyin.com/.../hotsearch/billboard/word/` | 30 | `hot_value` |
| B 站热门 | 国内 | `api.bilibili.com/x/web-interface/popular`（视频榜） | 30 | `stat.view` |
| Hacker News | 境外 | `hn.algolia.com/api/v1/search?tags=front_page` | 30 | `points` |
| Lobsters | 境外 | `lobste.rs/hottest.json` | 25 | `score` |
| GitHub 趋势 | 境外 | `github.com/trending`（HTML，h2 内取仓库链接） | 10–25 | `stars today` 优先，回退总 star |

未接入：知乎热榜 API 返回 **401**（需 cookie）；Reddit `r/popular` 经代理仍 **403**（反爬，与 UA 无关）；X / YouTube / TikTok 需 API key。三者均已在架构上留好扩展位（解析器 + 一行配置）。

实测：真实抓取 8 源 → 快照全 OK、境外 3 源经代理入库成功；**80 个 pytest** 全过（M6 共 16 个社媒测试）。

**M7 站内阅读代理 + 关键字搜索**（B/S 服务端代取，浏览器不直连外站、不拉起默认浏览器）—— ✅ 已完成 2026-11-12

两个相邻需求一并落地：① 点「原文」时由**服务端**去抓目标网页、清洗正文后在站内渲染（右侧抽屉 iframe 载入 `/read?embed=1`，ESC/遮罩关闭，Ctrl/中键走站内新页）；② 搜索框输入关键字 → 站内全库检索 + 外部结果由服务端代取并清洗成站内结果页，点击任一结果再走 ①。两条路径全程浏览器只与 NewsStation 通信。

实现：
- `app/reader/`：SSRF 四层防护（`safety.py`：协议/端口/账号密码/域名白名单/公网 IP，且**重定向逐跳重校**，IP 字面量无论是否做 DNS 都校验公网，覆盖 `192.168.x`、`169.254.169.254` 云元数据等内网地址）；`extract.py` 用标准库 `HTMLParser` 做正文选区（段落父节点投票 + 链接密度惩罚，避开头条/导航噪声）+ 标签白名单 + 把相对链接与图片改写成站内路径（`/api/read/asset` 代理图片）；`fetcher.py` 直连优先、代理回退、流式限大小、错误降级；`store.py` 按 `url_hash` 缓存（默认 24h 复用）。新增 `reader_cache` 表。
- `app/search/`：`local.py` 站内事件/报道/社媒热榜一次检索（中文不分词，子串匹配；多词空格分隔取**并且**）；`providers.py` 外部搜索源插件（`bing_rss` 稳定、`sogou_news` 中文资讯、`baidu_news` 反爬不稳仅备选），统一「构造 URL → 服务端代取 → 解析」，任一源失败**不影响**站内检索；`service.py` 合并并按 URL 去重。新增 `search_cache` 表（默认 30min 复用）。
- 全站 5 处原文链接（`_cards` / `event` / `subscriptions` / `social` 四个模板）由 `target="_blank"` 直跳改为 `read_url()` + `data-read`，列表页不再拉起默认浏览器。新增 `/read`、`/api/read`、`/api/read/asset`、`/api/read/status`、`/search`、`/api/search`、`/api/search/status`，以及 `read.html`、`search.html`。

关键取舍：
1. **SSRF 是服务端代取的第一要务**：B/S 结构下浏览器把任意 URL 交给服务端，不加校验就成了打内网的开放代理。除 IP 字面量/域名白名单外，每一跳 302/307 重定向都重新过校验，防「先给公网、后 302 到 169.254.169.254」的绕过。
2. **正文提取零第三方**：不引入 readability/lxml，用标准库 `HTMLParser` + 评分，避免再养一个重依赖；实测界面新闻/中新网/快讯均能收敛到干净正文（页头下载 APP、相关新闻列表等噪声块被排除）。
3. **图片与相对链接都走站内**：正文里的 `<img src=...>` 改写成 `/api/read/asset?url=...`，外站域名不出现在客户端；相对链接补成原站绝对路径，但仍经 `/read` 代取，保证「浏览器不直连外站」。
4. **外部搜索只作补充**：站内检索即时完成、零网络；外部结果缓存复用、失败自动降级并提示，绝不阻塞主流程。

实测：22 个新测试全过（含 SSRF 拦截内网 IP、GBK 解码、正文白名单、缓存命中、失败降级、外部 Mock 代取与去重）；全量 **77 个 pytest** 通过。


关键取舍：
1. **快照表与文章表分开**：热榜是「同一话题反复上榜、排名/热度随时间变化」，`articles.url` UNIQUE 是「一篇文章只入库一次」；硬塞会丢掉排名变化。
2. **URL 必须自己构造**：头条原 `Url` 带每轮变化的 `log_pb` / `impr_id`，照抄会让同一话题每 30 分钟重复入库并反复触发订阅；统一改成 `trending/<ClusterId>/`。微博 / 百度 / 抖音按热搜词构造搜索链接，B 站按 `bvid` 构造视频链接。
3. **注入可控**：默认开但只取每平台前 12 条，避免热搜口语化短词淹没新闻事件流；`inject_articles: false` 即纯榜单模式。
4. **共振只比较词榜平台**：B 站长视频标题容易包含热搜词，参与聚合会误判，故单独成卡；共振区按标题归一化 + 包含关系合并，要求 ≥2 个平台。
5. **显式装载**：社媒注册表由 lifespan / CLI 装载，脚本和既有测试不装载就不会凭空多出 5 个网络源（保持测试零回归）。
6. **顺带修复**：`record_state()` 首次插入时 `fail_streak` 写死 0、`ON CONFLICT` 分支不执行，导致「第一次失败」不计入连续失败次数；改为插入时也带正确值。

展示：导航「社媒热榜」→ `/social`（五平台卡片榜、平台筛选、`新 / ↑n / ↓n` 升降徽标、跨平台共振）、`/api/social/trends`、`/api/social/status`；`/sources` 增加「社交媒体热榜源」表；`/api/stats` 增加 `social_trends` / `social_platforms`。

实测：真实抓取 5 源 **1.8s** → 150 条快照 + 60 条注入文章、源状态全 ok；55 个 pytest 全过（M6 新增 13 个）。

### M8 体验优化：等待反馈 / 相关事件 / 粒度化翻译 —— ✅ 2026-11-12

用户反馈三点，逐条落地（搜索按用户要求「暂时不动」）：

**1. 原文打开等待久** —— 根因是 `reader.proxy: auto` 每轮都从直连试起，境外站连不上要等满 12s 才回退代理；前端只有一行静态提示，白屏期间不知道有没有在动。

| 层面 | 改动 |
| --- | --- |
| 后端加速 | 新增 `reader_routes` 表按域名记住上次成功的 `direct`/`proxy`，`_chain()` 让记忆通道优先；`connect_timeout_seconds: 4` 给建连单独限时，连不通立即换通道（原为整页读取共用 12s） |
| 前端反馈 | 抽屉加载态改为骨架屏 + 旋转指示 + **实时计时**（已等待 N 秒）+ 分阶段文案（0–3s 连接 / 3–8s 代取清洗 / 8–16s 原站慢 / 16s+ 异常慢）；8s 后浮出「重新获取 / 改用完整页打开」兜底；iframe `load` 或关闭时停表 |
| 配置项 | `reader.remember_route`（可关）、`reader.connect_timeout_seconds`；尊重 `prefers-reduced-motion` |

实测（观察者网首页，清空记忆后）：第 1 次 0.66s 写入 `direct`，第 2 次 0.46s 直接复用。境外站首次仍需数秒，但计时与阶段提示让等待可感知、可操作。

**2. 相关事件毫无关联** —— 原实现是「共享主行业 + 热度降序取 5 条」，本质是把同行业热门事件随便塞进来。新增 `app/web/related.py`：

- 加权相似度：关键词 **IDF 加权 Jaccard**（压低「中国/发展」这类满库都是的词，权重 3.0）、共享实体（2.2+，最强信号）、标题分词 Jaccard（1.2）、行业/类型/地区标签（0.35/0.25/0.1，仅作佐证）、同期加成（0.5，且**只在有内容重叠时**才给）；
- 候选集 = 近 10 天事件 + 共享实体的历史事件，上限 400，避免 2367 条全表两两算；
- **硬门槛**：必须有关键词/实体/标题的实质重叠且分数 ≥ 1.5，否则不返回 —— 宁可显示「暂无达到关联阈值的事件」，也不用同行业热门凑数；
- 页面每条显示关联理由（`同一实体 nvidia · 共同关键词 board、huang、jensen`）与关联度。

真实库抽查：Nvidia 一组 5 条全为同一事件的英文报道；「二十大」系列 5 条全部同系列；商务部谈判聚到外交部相关表态；孤立事件（教皇、APEC 餐桌）返回 0 条而非凑数。

**3. 翻译要像 Twitter 一样按需、分粒度** —— 原「一键翻译」整页一次全翻。见 M5 的 M8 改造说明：`tr_attr` 增加 `data-tr-scope`（`title`/`para`），顶栏与阅读页各三档范围按钮，另给每个外文元素/正文段落注入独立「译」按钮；单条只发 1 条文本，命中 SQLite 译文缓存则零外部请求；可单条切回「原文」。

验证：`pytest -q` **95 passed**（87 → +3 reader 通道记忆、+4 相关事件、+1 粒度标记）；重启后 `/`、`/event/1719`、`/sources`、`/social`、`/search`、`/read` 全 200；顶栏/阅读页三档控件与 `data-tr-scope` 均上线；单条翻译接口 `requested=1 translated=1`；生产库 `reader_routes` 已写入记录。

## 10. 运行与部署

### 启动
- Windows 源码模式：双击 `run.bat`。脚本先检查 `.venv\Scripts\python.exe`，不存在则 `python -m venv .venv` + `pip install -q -e .`，再执行 `python -m app.server`；**监听地址与端口不再写在 bat 里**，一律读 `config.yaml` 的 `server` 段。
- Windows exe 模式：双击 `dist\NewsStation.exe`（构建方式见第 11 节），读同目录 `config.yaml`。
- 等价命令：`.venv\Scripts\python.exe -m app.server`；临时换端口 `... -m app.server --port 8080`。
- 排障：`.venv\Scripts\python.exe -m app.server --print-config`（打印生效参数与全部路径，不启动服务）、`--self-check`（实际加载模板/词典/分词表/源清单/数据库）。
- 要求 Python ≥3.12（本机 3.13.14）。本机 `py` 启动器指向已损坏的 `E:\install\py3`，**所有脚本一律用 venv 内解释器绝对路径**。
- 从零验证（2026-11-05）：新建空白 venv → `pip install -e .` → `import app.main` 全部依赖导入成功；再 `pip install -e ".[dev]"` 补全 pytest → **34 个测试全过**。`run.bat` 只装运行依赖（`pip install -e .`），跑测试需额外装 dev 依赖。

### 配置 `config.yaml`
| 段 | 关键项 | 说明 |
| --- | --- | --- |
| `server` | `host: 127.0.0.1` / `port: 8000` / `log_level: info` / `auto_open_browser: false` | **M9 新增**：监听端口等运行参数。`0.0.0.0` 允许局域网访问；`workers` / `reload` 只在源码模式生效；改完重启生效，命令行 `--host` / `--port` 可临时覆盖。详见第 11 节 |
| `db_path` | `data/news.db` | 单文件 SQLite，WAL 模式；相对「运行目录」解析（源码=项目根，exe=exe 同目录），也可写绝对路径 |
| `fetch` | `interval_minutes: 30` | APScheduler 定时抓取；`max_age_days: 7` 挡旧闻 |
| `proxy` | `url: http://127.0.0.1:7890` | 只给 `proxy_required` 的 B 组源用；留空则跳过境外源且不算失败 |
| `smtp` | `user / pass / to` | `pass` 留空 = 仅站内；填授权码 = 每轮抓取发一封合并摘要 |
| `translate` | `backend: auto` / `target: zh-CN` | 粒度化翻译；默认免密钥回退链，六种后端见 M5，交互见 M8 |
| `social` | `enabled: true` / `inject_articles: true` / `inject_top: 12` | 社媒热榜；`inject_articles: false` = 纯榜单模式，`retention_days: 7` 控制快照保留 |
| `reader` | `enabled: true` / `proxy: auto` / `cache_hours: 6` / `connect_timeout_seconds: 4` / `remember_route: true` | 站内阅读代理；`proxy: auto` 直连优先、失败回退 `config.proxy.url`；M8 新增按域名记忆成功通道（`reader_routes` 表）与建连限时，缩短境外站等待；`allowed_domains` 留空=不限域名但强制公网 IP；`proxy_images` 控制正文图片是否也经服务端代取 |
| `search` | `engines: [default, google]` / `default_engine: default` / `providers: [sogou_news, bing_rss, baidu_news]` / `google.*` | M10：搜索引擎可选，默认引擎=站内三组+三个外部源，谷歌引擎=Google 资讯 RSS+网页搜索，结果按引擎独立成区；首次打开只跑 `default_engine`。`external_enabled: false` 只留站内；各源独立缓存（`cache_minutes: 30`）、失败单源隔离。详见第 12 节 |

### 媒体源 `sources.yaml` / `social_sources.yaml`
RSS 一条记录一个源（YAML 插件）：`id / name / homepage / lang / feed / proxy_required / region`。增删改只需改文件并重启；`region` 用于地区标签兜底。另支持 `type: html`（字段 `parser / url / base_url / limit`，由 `HTMLSource` 驱动）。当前 **44 个源**（43 个 RSS + 1 个 HTML 列表源）：直连 20 个（国内综合/时政/科技垂直）、需代理 24 个（全球综合/财经/科技/学术）；另 8 个社媒热榜源，合计 **52 个源**。

`social_sources.yaml` 同构，额外有 `platform / parser / url / headers / limit / inject_top`；`parser` 对应 `app/ingest/social/parsers.py::PARSERS` 的键。当前 8 个源：国内 5 个免鉴权直连（`region: cn`），境外 3 个（HN / Lobsters / GitHub，2026-11-12 接入）标 `proxy_required: true` 经 `config.proxy.url` 抓取、`region: global`；`inject_top` 可覆盖全局 `social.inject_top`。

**媒体源扩充记录（2026-11-12，第一轮）**：RSS 源由 15 → 38。国内直连新增 6 个（新华时政、人民时政、中新国内、新浪国内/科技、极客公园），补上此前偏少的国内综合/时政覆盖；境外代理新增 17 个（BBC 商业/科技/科学三频道、华盛顿邮报、经济学人、彭博、半岛、法广、DW、NPR、Verge、TechCrunch、Ars Technica、Wired、Hacker News、Nature、Science）。所有候选均经 `scripts/check_feeds.py` 连通性实测（国内直连 + 本地 7890 代理），只收录 HTTP 200 且 feedparser 可解析出条目的源；AP/NHK/Reuters 已无公开 RSS，央视等返回反爬 HTML 或 404，均未列入。

**媒体源扩充记录（2026-11-12，第二轮：无官方 RSS 的中文媒体）**：38 → 44，把首轮因「官网不供 RSS」排除的虎嗅/36氪/澎湃/联合早报/观察者全部接入。**实测结论：这 5 家都不需要 Cookie / API key / 代理**，只需换正确入口：

| 站点 | 官网障碍 | 采用的入口 | 条数/轮 |
| --- | --- | --- | --- |
| 虎嗅 | 阿里云 WAF 的 JS 挑战页，所有路径（含 `/rss/1.xml`）都回同一个 16KB 挑战页、只下发 `acw_tc` Cookie | `plink.anyfeeder.com/huxiu`（第三方 RSS 镜像） | 60 |
| 36氪 | `36kr.com` 是 SPA 空壳，无内嵌 `__NEXT_DATA__` | `www.36kr.com/feed`（**官方** RSS，仅 www 域有效） | 30 |
| 澎湃 | `/rss*` 返回首页 HTML | `plink.anyfeeder.com/thepaper` | 21 |
| 联合早报 | 官网 `/rss/*` 全部 404（`.sg` 域同样），首页无 feed 声明 | anyfeeder `zaobao/realtime/china` + `/world` 两条 | 24 × 2 |
| 观察者 | 无任何 RSS（官网 `/rss` 回首页、anyfeeder 无此源） | **新增 `type: html` 通道**：`HTMLSource` + `guancha` 解析器，从首页配对提取 `<a href="/栏目/日期_编号.shtml">标题</a>`，URL 去重并从路径反解日期供过期过滤 | 30 |

前四家用镜像/官方 feed，零改代码；观察者因完全无 RSS，新增 `app/ingest/html_source.py`（解析器注册表 + `HTMLSource`）并让 `registry.load_sources_yaml()` 按 `type` 分发，产出标准 `Article`，因此自动获得四维标签、聚类、订阅命中与一键翻译。4 个镜像连续两次拉取 + 直连/代理对比均 200 且条目稳定，全部标 `region: cn`、无需代理。实测 `pytest -q` **87 passed**；真实抓取 6 源 7.5s，189 条全部入库、`ok=6 fail=0`；服务重启后 `/api/stats` `total=52 ok=52 fail=0`。

> 风险：anyfeeder 为公共第三方镜像，无 SLA；若下线，单源失败会被 `run_one` 隔离、其它源照常，届时换镜像或改回官方入口即可。虎嗅 WAF 挑战页**无法**靠加请求头绕过（需 JS 执行算 `acw_sc__v2`），故不走官网直抓。

### 页面与接口
| 路径 | 内容 |
| --- | --- |
| `/` | 四视图 `?view=list\|quad\|dot\|matrix`，筛选 `?region=&industry=&type=`，点阵着色 `?color=` |
| `/industries`、`/industry/{value}` | 行业总览 / 行业页 |
| `/social` | 社媒热榜：八平台卡片（国内 5 + 境外 3）、平台筛选 `?platform=`、升降徽标、跨平台共振 |
| `/event/{id}` | 事件详情、四维标签人工锁定与屏蔽、相关事件（加权相似度 + 关联理由） |
| `/subscriptions` | 订阅管理、命中时间线、未读 |
| `/sources` | RSS 源状态 + 社媒热榜源状态、立即抓取 |
| 全站页头 | 三档范围翻译 `译标题 / 译段落 / 译全文` + 每条外文旁的独立「译」按钮（可单条切回原文）；译文按文本缓存 |
| `/api/stats`、`/api/events`、`/api/subscriptions` | JSON 接口（`/api/events` 与页面共用同一套筛选 SQL；`/api/stats` 含 `social_trends` / `social_platforms`） |
| `/api/translate`、`/api/translate/status` | 批量翻译（POST）与后端/缓存状态（GET） |
| `/api/social/trends`、`/api/social/status` | 最新榜单（`?platform=&limit=`）与各平台抓取状态、共振数 |
| `/search` | M10 关键字搜索：引擎选择器（默认 / 谷歌，可多选）+ 每个引擎独立结果区；站内事件/报道/社媒热榜 + 全网结果由服务端代取，全部原文走站内阅读 |
| `/read` | 站内阅读页（`?embed=1` 供抽屉 iframe 载入）；服务端代取原网页、清洗正文后渲染 |
| `/api/read`、`/api/read/asset`、`/api/read/status` | 取正文（GET）、代取图片（GET）、阅读代理状态（GET） |
| `/api/search`、`/api/search/status` | 搜索结果（GET，支持 `?scope=all\|local\|external` 与 `?engines=default,google\|all`，返回 `engines` 独立区块并保留 M7 顶层字段）与引擎/缓存状态（GET，`engines` 列出可选引擎及其 provider） |

### 常用命令
```
.venv\Scripts\python.exe -m app.server                                             # 起服务（host/port 读 config.yaml）
.venv\Scripts\python.exe -m app.server --print-config                            # 打印生效配置与路径，不启动
.venv\Scripts\python.exe -m app.server --self-check                              # 检查模板/词典/分词/源清单/数据库
.venv\Scripts\python.exe -m app.server --port 8080 --no-browser                  # 临时覆盖运行参数
.venv\Scripts\python.exe -m app.ingest.fetch                                      # 手动抓一轮（含分析与订阅匹配）
.venv\Scripts\python.exe -m app.ingest.fetch --source social:weibo               # 只抓指定源（可重复；不跑分析，适合验单源）
.venv\Scripts\python.exe -m app.analyze.pipeline --rebuild                       # 改词典/公式后重算全部事件
.venv\Scripts\python.exe -m pip install -e ".[dev]"                            # 开发时补装 pytest（仅首次）
.venv\Scripts\python.exe -m pip install -e ".[build]"                          # 补装 PyInstaller（build_exe.bat 会自动装）
build_exe.bat                                                                     # 打包单文件 exe 到 dist\
.venv\Scripts\python.exe -m pytest -q                                            # 全量测试
.venv\Scripts\python.exe -m tests.eval_tags data/news.db                         # 标签准确率基准
```

### 数据与版权
`data/news.db` 只存标题、摘要、链接、发布时间，不存正文。「原文」由服务端代取后在站内渲染（见 M7），浏览器不直连外站、不拉起默认浏览器；代取内容**不落库**（只缓存清洗后的 HTML 24h 供复查），仍只保留标题/摘要/链接。抓取周期 30 分钟，超过 `analyze.retention_days`（默认 14 天）的文章与空事件会被清理。

### 已知限制（不假装解决）
- 跨语言中英文同一事件不合并；阈值 0.45 偏保守；事件详情尚不支持「拆成新事件」。
- HanLP L2 模型 CDN 失效，当前默认 L1 词典 NER；L3（LLM）需 API key，未启用。
- `fail_streak` 只做记录与页面标红，PLAN 里写的「≥3 次降频 / 7 次自动禁用」尚未实现；实测源失败多为瞬时波动（一轮重试后 15 源可全部恢复）。
- 真实 SMTP 投递未用真实邮箱验证（缺授权码），发送路径由 mock 覆盖。
- 免费翻译端点依赖公共服务匿名额度：本机代理出口 IP 上 Google / MyMemory / Edge / Lingva / 公共 LibreTranslate 均被限流，默认 `auto` 靠 MyMemory 直连工作；额度耗尽时降级为「部分保持原文」，不报错。要稳定高质量可填 `translate.backend: openai` 或自建 LibreTranslate；译文缓存让同一文本只消耗一次额度。
- **社媒源：国内 5 + 境外 3 共 8 个**：知乎热榜需 cookie（401），Reddit 经代理仍 403（反爬），X / YouTube / TikTok 需 API key，均未接入；平台接口为非官方公开端点，字段改版会导致该源失败（单源失败隔离，其它源照常），届时改 `parsers.py` 即可；境外源依赖本地代理可达，代理关闭时 `proxy_required` 源被安全跳过（不算失败）。
- **虎嗅/澎湃/联合早报走第三方 RSS 镜像（anyfeeder）**：这些站已无官方 feed，官网还有 WAF / SPA 障碍，镜像是唯一免鉴权入口。镜像无 SLA，若下线或改版，这 4 个源会失败并被隔离，其它 48 源照常；可随时换其它镜像或改 `feed` 指回官方入口。观察者走 `type: html` 抓首页列表，官网 HTML 结构改版会导致该源解析为空（已有「未解析出条目」明确报错），改 `html_source.py` 里一个正则即可恢复。
- 榜单快照保留 7 天，只记录排名与平台热度值，不采集评论、用户数据或正文；共振区按标题文字聚合，不做语义级事件合并，可能出现同话题不同措辞未合并的情况。
- **站内阅读代理正文提取靠规则，不靠模型**：标准库 `HTMLParser` + 评分，对主流新闻站效果良好，但结构异常或反爬严重的页面可能选区偏差；正文图片默认经服务端代取（可关），遇到原站防盗链会回退透明图。代取内容不落库，仅缓存 24h。
- **搜索外部结果依赖第三方公开搜索页**：Bing RSS 实测约五成概率连接重置（已加重试），百度资讯反爬会偶发 401/验证码；任一源失败自动降级并提示，站内检索不受影响。`proxy_images` / 外站重定向均强制公网 IP 校验，防 SSRF。
- 站内全文检索是**子串匹配**，非语义搜索：多词空格分隔取「并且」，不会做同义/近义扩展；社媒热榜快照里搜不到下架话题。

## 9. 已知风险与对策

| 风险 | 对策 |
| --- | --- |
| 版权 | 只存标题、摘要、链接，不存正文；点击跳原文 |
| RSS 停更/改版 | `fail_streak` 记录连续失败，管理页标红；≥3 次降频到 2 小时 |
| 发布时间缺失 | 回落 `fetched_at`，卡片标「约」 |
| 聚类误合并 | 阈值 0.45 起步，M2 用真实数据调；M3 事件详情提供人工改标签（锁定/屏蔽），「拆成新事件」尚未实现 |
| 抓取被封 | 统一 UA、单源串行、全站并发 ≤6、每源间隔 1s |
| **境外源不可达**（实测全部超时） | 源分 A/B 两组；B 组标 `proxy_required`，无代理时跳过不算失败；`config.proxy.url` 一填即启用 |
| **服务端代取变开放代理（SSRF）** | `reader.safety` 四层校验：协议/端口/账号密码/域名白名单/公网 IP；IP 字面量无论是否 DNS 都拦内网；**每一跳重定向重新校验**，`192.168.x`/`169.254.169.254` 云元数据等均不可达 |
| 源清单失效 | 每个源单独 `fail_streak`；连续失败 3 次降频到 2 小时，7 次自动禁用并在源管理页提示 |
| 标签漏标/误标 | 标题+摘要双通道 + 置信度过滤；源属地兜底补地区；实体带行业提示；详情页人工改并锁定；词典 YAML 热改 |
| 跨语言同一事件被拆开 | 接受为已知限制；要合并需引入实体+时间+语义，M3 不处理，留给 L3 |
| HanLP 模型源失效（`file.hankcs.com` 返回落地页） | 已实测确认；改用 HF 中文 NER 权重或离线模型目录，L2 默认关闭 |
| 单源模板稿刷榜（ETF 行情稿） | M3 已实现：单源 + 篇数≥3 + 标题含行情模板词 → 剔除出榜单/卡流，详情页仍可访问 |
| 免费翻译端点被限流（实测代理出口 429） | M5 默认 `auto` 回退链（直连 MyMemory → 代理 Google）；译文缓存 + 失败 1h 冷却；可切 OpenAI 兼容 / 自托管 LibreTranslate |
| 社媒榜单接口无 SLA / 字段改版 | 解析器与源配置分离，单源失败隔离并写入 `source_state`；稳定 URL 自己构造，不依赖平台跳转参数；每平台只注入前 N 条（境外 10 / 国内 12），避免污染新闻事件流 |
| 热搜词短且口语化，聚类易误并 | 聚类阈值 0.45 保守；注入条数受 `inject_top` 限制；纯榜单模式可在 config 一键关闭 |
| HanLP 装不上（3.13/torch） | 已实测：能装能导入（torch 2.15 cpu），但模型 CDN 失效，L2 挂起；见 4.2 |
| `py` 启动器已损坏（指向 E:\install\py3） | 脚本与文档一律用 venv 内的绝对路径，不依赖 `py` |
| 时区 | 全库存 UTC，`localtime` 模板过滤器转本地并输出「N 分钟前 / N 小时前」 |

## 11. M9：打包成 exe、监听端口等配置外置 —— ✅ 2026-11-12

### 交付物
| 文件 | 作用 |
| --- | --- |
| `dist/NewsStation.exe` | Windows x64 单文件可执行程序（约 27 MB，保留控制台看日志），双击即启动站点 |
| `dist/config.yaml` | **可编辑**：`server` 段改端口/监听地址，其余段改代理、抓取、翻译、阅读、搜索等 |
| `dist/sources.yaml` | **可编辑**：44 个媒体源清单 |
| `dist/social_sources.yaml` | **可编辑**：8 个社媒热榜源清单 |
| `launcher.py` / `newsstation.spec` / `build_exe.bat` | PyInstaller 入口、打包描述、一键构建脚本 |
| `app/runtime.py` / `app/server.py` | 运行时路径解析、统一启动入口 |

改完 YAML **重启程序即生效**，不需要重新打包。`data/news.db` 也在 exe 同目录自动创建。

### 构建
```
build_exe.bat                         # 默认单文件：dist\NewsStation.exe + 三份 YAML
set NS_ONEDIR=1 && build_exe.bat      # 目录版：dist\NewsStation\NewsStation.exe（启动更快，文件较多）
```
脚本自动检测并安装 `pyinstaller>=6.10`（也声明在 `pyproject.toml` 的 `[build]` 可选依赖里），调用 `PyInstaller --noconfirm --clean newsstation.spec`，构建后把三份 YAML 复制到 exe 同目录。spec 收集了模板、静态文件、标签词典、jieba 词典与 `certifi` 根证书；剔除了用不上的 numpy / pandas / scipy 和 jieba 的 `lac_small` 模型，体积从 49 MB 降到 **27 MB**。重新构建时**已存在的 YAML 不会被覆盖**，用户改过的端口/源清单得以保留；想恢复默认配置，删掉对应 YAML 后重跑构建（或让 exe 首次启动自动释放种子配置）。每次构建还会输出一份最新的 `config.example.yaml`，程序升级新增配置段（如 M10 的 `search.engines`）时用它对比自己的 `config.yaml`。

### 配置：`server` 段
| 项 | 默认值 | 说明 |
| --- | --- | --- |
| `host` | `127.0.0.1` | 只本机访问；`0.0.0.0` 允许局域网/容器外访问 |
| `port` | `8000` | 1–65535，pydantic 越界直接报错；被占用时 uvicorn 会明确报 `address already in use` |
| `workers` | `1` | 仅源码模式；单文件 exe 强制按 1 处理 |
| `reload` | `false` | 仅源码模式；exe 忽略 |
| `log_level` | `info` | `critical/error/warning/info/debug/trace`，非法值自动回退 `info` 并给出警告 |
| `access_log` | `true` | 是否打印每条 HTTP 访问日志 |
| `auto_open_browser` | `false` | 启动 1.2 秒后自动打开浏览器；双击 exe 使用时建议打开 |
| `proxy_headers` / `forwarded_allow_ips` | `true` / `127.0.0.1` | 放在反向代理后面时读取 `X-Forwarded-*` |

同一份 `config.yaml` 对源码模式和 exe 模式都生效；`--host` / `--port` 只在本次启动临时覆盖，**不写回文件**。

### 路径规则（`app/runtime.py`）
| 概念 | 源码模式 | exe 模式 | 放什么 |
| --- | --- | --- | --- |
| 运行目录 `home()` | 项目根 | exe 同目录（只读时退回 `%APPDATA%\NewsStation`） | `config.yaml`、两份源清单、`data/news.db` |
| 程序目录 `BUNDLE_DIR` | 项目根 | PyInstaller 解压出的临时目录 | 模板、静态资源、标签词典、jieba 词典、三份「种子配置」 |

- **首次启动**把内嵌的种子配置复制到运行目录；之后只认外部那一份，用户改过的内容不会被覆盖。
- 模板/静态/词典默认用内嵌版；在运行目录建同名目录（如 `app\web\templates`）即可覆盖，方便改样式而不用重打包。
- 环境变量 `NEWSSTATION_HOME` / `NEWSSTATION_CONFIG` 与命令行 `--home` / `--config` 可分别指定运行目录和配置文件。注意 `--config` 只换配置文件，数据库与源清单仍相对 `home()` 解析；要整套搬家用 `--home`。

### 命令行
```
NewsStation.exe                                  # 按 config.yaml 启动
NewsStation.exe --print-config                  # 打印生效参数与全部路径，不启动服务
NewsStation.exe --self-check                    # 实际加载配置/源清单/词典/分词/模板/静态/数据库
NewsStation.exe --port 8080 --no-browser        # 临时覆盖
NewsStation.exe --home D:\ns                    # 指定运行目录
NewsStation.exe --config D:\ns\my.yaml          # 指定配置文件
```
`--print-config` / `--self-check` 对 `python -m app.server` 同样可用。双击 exe 时若启动异常，控制台会保留 traceback 并等待回车，不会一闪而过。

### 已知约束
- 单文件 exe 每次启动要解压资源到临时目录，冷启动比目录版/源码模式慢几秒；追求启动速度用 `NS_ONEDIR=1`。
- `workers > 1` 与 `reload` 在打包态被忽略（PyInstaller 单文件不支持子进程托管开发服务器）。
- exe 未做代码签名，部分杀软可能误报，必要时加白名单或自行签名。
- 改配置后需重启；运行中的进程不会热加载 YAML。

### 验收记录（2026-11-12）
- `pytest -q` **104 passed**（新增 `tests/test_packaging.py` 9 个用例覆盖配置校验、路径跟随 home、种子文件只释放一次、CLI 覆盖、IPv6/通配地址）。
- 源码模式 `--print-config` / `--self-check` 8/8 通过（M10 后自检新增「搜索引擎」一项）。
- 干净目录只放 exe 首次运行 → 自动释放 3 份 YAML；把 `server.port` 改成 `18080` 后启动，实际监听 18080。
- exe 自检 8/8：44 媒体源 / 8 社媒源、204 个标签面、jieba 分词、15 个模板、2 个静态文件、默认引擎 3 源 + 谷歌引擎 2 源、数据库可读写。
- exe 端到端：`/`、`/sources`、`/social`、`/search`、`/static/*`、`/api/stats`、`/api/social/status`、`/api/read/status` 全 200；`POST /fetch` 跑完一整轮 **52 源 ok=52 / fail=0**，入库 1460 篇 → 380 个事件、215 条社媒快照 / 8 平台；`/api/read?url=https://www.baidu.com` 返回 `status=ok`（验证 HTTPS 根证书与网络库已正确打包）。

## 12. M10：搜索引擎可选、结果独立展示 —— ✅ 2026-11-13

### 需求与做法
| 诉求 | 实现 |
| --- | --- |
| 「现在这套」归纳为默认引擎 | `default` = M7 原有整套：站内事件/报道/社媒热榜 + `search.providers` 的搜狗资讯 / Bing RSS / 百度资讯 |
| 新增可选谷歌引擎 | `google` = `google_news`（Google 资讯 RSS 本地版）+ `google_global`（同一 RSS 的全球英文版），两个 provider 的结果合在一个「谷歌引擎」区，另附「直接在 Google 中打开」按钮 |
| 可选引擎 | 搜索页引擎 chips 可多选（点选中=取消、点未选中=加上，不允许取消到 0 个）；首次打开只跑 `search.default_engine`，保持 M7 原有体验与速度 |
| **搜索栏内直接选**（用户反馈修正） | 全站顶栏搜索框与搜索页主输入框都内嵌 `<select name="engines">`，随 GET 提交；`_site()` 向所有页面注入 `engine_options / engines_param`，`app.js` 用 `ns_search_engines` 记忆并联动两处下拉 |
| **独立显示** | 每个引擎一个独立卡片 `#engine-{id}`，带自己的条数、副标题、数据源状态、错误提示；结果列表互不混排、去重只在引擎内部做 |

新增 `app/search/engines.py` 做注册表与选择解析；`service.py` 重写为「按引擎生成区块」，逐源缓存、逐源隔离；`providers.py` 增加两个 Google provider。`/api/search` 保留 M7 的 `groups / external / sources / errors / total` 顶层字段（指向默认引擎），另增 `engines / selected / engines_param / engine_choices`，既有调用方不受影响。

### 配置（`config.yaml` → `search`）
| 项 | 默认值 | 说明 |
| --- | --- | --- |
| `engines` | `[default, google]` | 页面上可勾选的引擎；删掉 `google` 或把 `google.enabled` 改 `false` 即彻底关闭 |
| `default_engine` | `default` | 首次打开只跑它；`?engines=all` 可一次选全部 |
| `google.providers` | `[google_news, google_global]` | 本地版 + 全球版资讯 RSS，两路合并去重，条数翻倍 |
| `google.results_per_provider` | `20` | 每个 Google 源最多取多少条（原先复用 `max_results: 12`） |
| `google.hl / gl / ceid / num` | `zh-CN / CN / CN:zh-Hans / 10` | 资讯 RSS 的语言、地区；`num` 现已不用于取结果，仅用于「Google 网页搜索」直达链接 |
| `google.timeout_seconds` | `8` | 境外端点单独限时，不拖慢整页 |
| `google.prefer_proxy` | `true` | 优先走 `proxy.url`；`_remote` 对 Google 不做外层重试（内部已有 direct/proxy 两条通道） |

### URL 参数
```
/search?q=AI&engines=default            # 只跑默认引擎（默认行为）
/search?q=AI&engines=default,google     # 两个引擎，各自独立成区
/search?q=AI&engines=google             # 只跑谷歌引擎
/api/search?q=AI&engines=all            # 全部引擎；单引擎别名 &engine=google 也可用
```
`scope=local` 只对默认引擎生效；只要选了谷歌引擎，仍会联网检索（页面在区块内提示「当前为『仅站内』模式，该设置只作用于默认引擎」）。

### 搜索栏入口（用户反馈「搜索栏没位置选引擎」后的修正）
- 第一版只在 `/search` 页面下方放了一行 chips：顶栏搜索框里没有选择入口，提交时也不带 `engines`，从任意页面发起搜索都会丢掉选择；`@media (max-width:1100px)` 还会把顶栏搜索框整块隐藏。
- 现在全站顶栏与搜索页主表单都是用户指定的**三段式**：`[ 引擎下拉 ▾ ] [ 输入框 ] [ 搜索按钮 ]`，控件紧贴成一条（`gap: 0`、左右圆角只留两端），下拉带明确箭头；顶栏 `routes._site()` → `_engine_options()` → `base.html`，主表单 `#sch-engine`。
- 下拉平时**只有「默认引擎 / 谷歌引擎」两项**（`_engine_options` 只列单引擎）；仅当 URL 或下方多选开关产生组合时，才临时补一条「全部引擎 / 当前组合」以正确显示选中态。去掉了主表单前那段「搜索引擎」文字标签，避免变成四段。
- 下方 chips 降级为次要的「想同时搜多个引擎（可选）」开关，不再是抢视觉的浅蓝卡片；主选择入口就是搜索栏里的下拉。
- 下拉是表单字段 `name="engines"`，GET 提交后直接进入 URL，`/search` 与 `/api/search` 无需任何改动；`engine=google` 单参数同样兼容，`engines=all` 会规范化成 `default,google`。
- 选择写入 `localStorage['ns_search_engines']`：URL 显式带参数时以 URL 为准并回写记忆（点了下方 chips 也会记住），否则回填上次选择；同页两个下拉联动。
- 禁用谷歌引擎或 `search.engines` 只剩一项时自动隐藏两处下拉、改回 hidden 字段，不显示没得选的空控件；缺 `_site()` 上下文的页面（如阅读页）靠模板 `default([], true)` 兜底。
- CSS：三段连成一条（`.sch-form { gap: 0 }`，select 左圆角 / input 无圆角 / button 右圆角），两个下拉都有 SVG 箭头；1100px 以下只缩小，900px 以下顶栏改两行（nav 换行），**任何宽度都不再隐藏搜索栏**。

### Google provider 细节（2026-11-13 晚重做：从「几乎没有结果」到 40 条）
实测结论：**Google 网页搜索从 2025 年起强制 JS**，服务端代取只能拿到约 94 KB 的脚本空壳（各种 UA / `gbv=1` / Cookie / `udm=14` 全试过，`<h3>` 数为 0），且频繁 429；RSS 里的 `news.google.com/rss/articles/...` 跳转链也无法在服务端解码成原文 URL。因此把「网页搜索结果」从数据源里彻底移除，只保留真实可解析的资讯 RSS，并补一个浏览器直达入口。

- `google_news`：`https://news.google.com/rss/search?q=...&hl=zh-CN&gl=CN&ceid=CN:zh-Hans`，本地中文版。
- `google_global`：同一 RSS 换 `hl=en-US&gl=US&ceid=US:en`，全球英文版；与本地版内容基本不重合，两路合并后条数翻倍（默认每源 20 条）。
- 每个源都按 `results_per_provider` 取条数，不再复用 `search.max_results`（12 会把两路压成 12 条）。
- 区块顶部「直接在 Google 中查看」两个按钮，`target="_blank"` 打开 `google.com/search?q=...` 与 `news.google.com/search?q=...`，需要网页级结果时走浏览器（浏览器能执行 JS）。
- 结果标题改为 `target="_blank"` 由 Google 跳转原文（浏览器会自动跟随到真实站点），旁边保留「站内阅读」小链接；因为跳转链无法服务端解码，站内代取这类链接可能只拿到 Google 的空壳页，所以默认走浏览器。
- 缓存策略修正：**空结果不再写入缓存**，且已存在的空缓存视为未命中；此前 `google_web` 的 `[]` 被写进 `search_cache` 缓存 30 分钟，导致代理后来开了也一直吃空结果。
- `google_web` 已从 `config.yaml`、`config.py` 默认 `providers`、`engines.py` 文案和前端说明中移除，**不再启用**；`providers.py` 里仅保留其 URL 模板与解析函数作向后兼容（万一 Google 恢复服务端可读的 HTML，改配置即可重新启用）。
- 代理：`prefer_proxy=true` 时优先走 `proxy.url`；代理未开时该引擎区块单独提示，默认引擎不受影响。

### 已知限制（不假装解决）
- 免 API key 的 Google 网页搜索已确认**不可用**（JS 强制 + 429），所以本站的谷歌引擎返回的是「Google 资讯」结果，不是 Google 网页搜索的十条链接；需要网页级结果请用区块顶部的直达按钮。
- Google 资讯 RSS 的链接是 `news.google.com` 跳转而非原站 URL（编码格式常变，不做硬解码），因此结果默认在浏览器打开。
- 引擎之间**不做跨引擎去重**（独立展示的代价）：同一篇报道可能同时出现在默认引擎和谷歌引擎里。
- 谷歌引擎依赖本地代理（`config.yaml` 的 `proxy.url`，默认 7890），未开代理时该区块为空并给出提示。

### 验收记录（2026-11-13）
- `pytest -q` **120 passed**（新增 `tests/test_search_engines.py` 16 个用例：Google URL 参数、RSS/HTML 解析、consent 页空结果、引擎选择/禁用/toggle、双引擎独立区块、失败隔离、代理预检秒回、页面与接口参数）。
- 源码服务（18081）：默认页只有 `engine-default` 一个区（86 条站内结果）；`engines=default,google` 页出现两个独立区；`/api/search?engines=google` 返回 `selected=["google"]`；`/api/search/status` 列出 default(3 源) / google(2 源)；新 CSS 200。
- 代理不可达时 `/search?engines=google&scope=external` **0.86 秒**返回并提示「proxy.url 当前不可达，请先开启代理软件（默认引擎不受影响）」，不会拖慢整页。
- exe 已重新打包（27 MB）并同步 `dist/config.yaml`、新增 `dist/config.example.yaml`；exe 自检 **8/8**（含搜索引擎项），exe 内 `/api/search/status` 与双引擎页面均验证通过。
- **搜索栏修正（同日）**：`pytest -q` **130 passed**（新增 `tests/test_engine_picker.py` 10 例：全站顶栏下拉、URL 选中态、单参数兼容、主框下拉与参数生效、禁用降级、无上下文渲染、静态资源）；源码服务 18083 与重建后的 exe 18084 都验证顶栏三选项、主框下拉、选 `google` 后只剩 `engine-google` 区块；exe 自检仍 8/8。
- **布局二次修正（同日）**：用户明确要求「下拉框(谷歌/默认) → 输入框 → 搜索按钮」。改为三段一体条，下拉常驻两项、组合时才临时补第三项，去掉主框的文字 label，chips 弱化为次要开关。`pytest -q` **130 passed**；源码服务 18085 验证顶栏与主框控件顺序均为 `select → input → button`、选项 `[default, google]`、`engines=google` 只出谷歌区块、`engines=all` 下拉补「全部引擎」并选中；exe 重建后（18086）同样验证通过，自检 8/8。
- **「还是没有下拉框」根因（同日，重要）**：用户实际访问的 `127.0.0.1:8000` 由 **2026-11-12 17:11 启动、从未重启的旧 uvicorn 进程**提供（`python -m uvicorn app.main:app --port 8000`，PID 2240/43104）。旧进程里 `routes._site()` 是启动时加载的旧模块，没有 `engine_options`，模板 `default([], true)` 兜底成空列表 → select 永远不渲染；实测该端口首页 `<select>` 数为 0。此前每轮验证都用 18083~18086 临时端口，没碰 8000。**修复**：停掉旧进程，用 `python -m app.server` 在 8000 重启（沿用项目根 `data/news.db`，数据不丢）；新增 HTML `Cache-Control: no-store`、CSS/JS `?v=<mtime>` 版本号、页脚「页面构建 时间」标记（旧进程会显示「未知（服务未重启）」），窄屏顶栏改两行不再隐藏搜索栏。`pytest -q` **131 passed**，exe 已重建。教训：**改完必须验证用户实际访问的那个端口，而不是另起临时端口自测**。
- **谷歌引擎重做（同日，用户反馈「几乎没有结果」）**：定位到三重原因 —— `google_web` 因 Google 强制 JS 恒为 0 条（还常 429）；空结果被写进 `search_cache` 缓存 30 分钟，代理后来开了也一直吃空缓存（库里确有 `google_web|AI → []`）；`scope=local` 时谷歌引擎被直接掐断。改为 `google_news`（本地）+ `google_global`（全球）两路 RSS、每源 20 条、空结果不缓存、选了谷歌就忽略仅站内抑制、区块加 Google 直达按钮、结果标题浏览器直开。**8000 实测 `q=AI 芯片` → 40 条、2.8 秒**（两源各 20 条，无错误）；仅站内模式下同样 40 条并提示「该设置只作用于默认引擎」；双引擎页正常。`pytest -q` **141 passed**（新增 `tests/test_google_results.py`）。
