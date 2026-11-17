"""四维标签抽取。

一份词典 → 一张 Aho-Corasick 自动机 → 一次扫描拿到全部命中。
「地区 / 行业 / 类型 / 实体」共用同一次扫描，避免四条正则各扫一遍。
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass
from functools import lru_cache
from pathlib import Path

import ahocorasick
import yaml

from app import runtime

DICT_DIR = runtime.dicts_dir()

DIM_CAPS = {"region": 2, "industry": 2, "type": 2, "entity": 3}
DIM_CONF_FLOOR = {"region": 0.5, "industry": 0.5, "type": 0.45, "entity": 0.5}

_ASCII_RE = re.compile(r"^[a-z0-9.\-'&\s]+$")


@dataclass(slots=True, frozen=True)
class Tag:
    dim: str
    value: str
    label: str
    confidence: float
    origin: str = "dict"


@dataclass(slots=True, frozen=True)
class Surface:
    dim: str
    value: str
    label: str
    word: str = ""              # 实际词面（小写），用于定位匹配起点与大小写校验
    canonical: bool = False
    case_sensitive: bool = False


def _alias(item) -> tuple[str, bool]:
    """别名支持两种写法：'United States' 或 {w: US, cs: true}。"""
    if isinstance(item, dict):
        return str(item["w"]), bool(item.get("cs", False))
    return str(item), False


class DictTagger:
    """L1 词典标签器。零外部模型，微秒级，覆盖高频实体与全部地区/行业/类型。"""

    def __init__(self, dict_dir: Path | str | None = None):
        # 延迟解析，保证 --home / NEWSSTATION_HOME 在实例化时仍生效
        self.dict_dir = Path(dict_dir) if dict_dir else runtime.dicts_dir()
        self._auto = ahocorasick.Automaton()
        self._surfaces: dict[str, list[Surface]] = {}
        self.labels: dict[tuple[str, str], str] = {}
        self.parents: dict[str, str] = {}
        self.aliases: dict[tuple[str, str], list[str]] = {}
        self._type_patterns: list[dict] = []
        self.entity_industry: dict[str, str] = {}
        self._load()

    # ---- 装载 ----

    def _load(self) -> None:
        for name, dim in (("region", "region"), ("entity", "entity")):
            self._load_aliases(self.dict_dir / f"{name}.yaml", dim)
        self._load_keywords(self.dict_dir / "industry.yaml", "industry")
        self._load_types(self.dict_dir / "type.yaml")
        self._load_entity_industry(self.dict_dir / "entity_industry.yaml")
        self._auto.make_automaton()

    def _load_entity_industry(self, path: Path) -> None:
        if not path.exists():
            return
        raw = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
        for industry, entities in raw.items():
            for value in entities or []:
                if ("entity", str(value)) in self.labels:
                    self.entity_industry[str(value)] = str(industry)

    def _register(self, surface: str, entry: Surface) -> None:
        key = surface.strip().lower()
        if not key:
            return
        entry = Surface(
            entry.dim, entry.value, entry.label, key, entry.canonical, entry.case_sensitive
        )
        bucket = self._surfaces.setdefault(key, [])
        if entry not in bucket:
            bucket.append(entry)
        # bucket 是可变列表，同一 key 多次 add_word 指向同一对象，后补的 Surface 自动生效
        self._auto.add_word(key, bucket)
        self.labels[(entry.dim, entry.value)] = entry.label

    def _load_aliases(self, path: Path, dim: str) -> None:
        for row in yaml.safe_load(path.read_text(encoding="utf-8")):
            # YAML 1.1 会把 no/yes/on/off 解析成布尔，别名词典里恰好有 ISO 代码 "no"（挪威），
            # 统一转字符串，避免上游写法差异把装载层搞崩。
            value, label = str(row["value"]), str(row["label"])
            surfaces: list[str] = []
            for item in row.get("aliases", []):
                word, cs = _alias(item)
                surfaces.append(word)
                # canonical：词面本身就是 value 或 label，说明这是主词而非别名
                canonical = word.lower() in (value.lower(), label.lower())
                self._register(word, Surface(dim, value, label, canonical=canonical, case_sensitive=cs))
            self.aliases[(dim, value)] = surfaces
            if row.get("parent"):
                self.parents[value] = row["parent"]

    def _load_keywords(self, path: Path, dim: str) -> None:
        for row in yaml.safe_load(path.read_text(encoding="utf-8")):
            value, label = str(row["value"]), str(row["label"])
            words = [str(w) for w in row.get("keywords", [])]
            for word in words:
                self._register(word, Surface(dim, value, label, canonical=True))
            self.aliases[(dim, value)] = words

    def _load_types(self, path: Path) -> None:
        for row in yaml.safe_load(path.read_text(encoding="utf-8")):
            value, label = str(row["value"]), str(row["label"])
            words = [str(w) for w in row.get("any", [])]
            groups = [[str(w) for w in g] for g in row.get("all", [])]
            for word in words:
                self._register(word, Surface("type", value, label, canonical=True))
            for group in groups:
                for word in group:
                    self._register(word, Surface("type", value, label, canonical=True))
            self._type_patterns.append(
                {"value": value, "label": label, "groups": groups, "words": words}
            )
            self.aliases[("type", value)] = words

    # ---- 匹配 ----

    def scan(self, title: str, summary: str = "") -> dict[str, dict[str, float]]:
        """返回 {dim: {value: score}}，score 为加权命中（标题 ×2，摘要 ×1）。"""
        title = title or ""
        summary = summary or ""
        raw = f"{title}\n{summary}"
        text = raw.lower()
        if not text.strip():
            return {}
        title_end = len(title)

        scores: dict[str, dict[str, float]] = {}
        # pyahocorasick 的 end 是「末字符下标（含）」，不是独占末尾，切片要 end+1
        for end, surfaces in self._auto.iter(text):
            for surface in surfaces:
                start = end - len(surface.word) + 1
                if start < 0:
                    continue
                if surface.case_sensitive:
                    if raw[start : end + 1] != surface.word.upper():
                        continue
                if _ASCII_RE.match(surface.word):
                    before = text[start - 1] if start > 0 else " "
                    after = text[end + 1] if end + 1 < len(text) else " "
                    if before.isalnum() or after.isalnum():
                        continue
                weight = 2.0 if start < title_end else 1.0
                bucket = scores.setdefault(surface.dim, {})
                bucket[surface.value] = bucket.get(surface.value, 0.0) + weight

        # 类型靠组合模式加分：营收 + 增长 才算财报，单个词不算
        for pattern in self._type_patterns:
            for group in pattern["groups"]:
                if group and all(self._present(w, text) for w in group):
                    bucket = scores.setdefault("type", {})
                    bucket[pattern["value"]] = bucket.get(pattern["value"], 0.0) + 3.0

        # 实体带行业提示：标题只写"AMD"时也能拿到科技标签
        for entity, score in scores.get("entity", {}).items():
            industry = self.entity_industry.get(entity)
            if industry:
                bucket = scores.setdefault("industry", {})
                bucket[industry] = max(bucket.get(industry, 0.0), score)
        return scores

    @staticmethod
    def _present(word: str, text: str) -> bool:
        word = word.lower()
        idx = text.find(word)
        return idx >= 0

    def tag(self, title: str, summary: str = "", lang: str = "zh") -> list[Tag]:
        scores = self.scan(title, summary)
        out: list[Tag] = []
        for dim, values in scores.items():
            cap = DIM_CAPS[dim]
            floor = DIM_CONF_FLOOR[dim]
            ranked = sorted(values.items(), key=lambda kv: kv[1], reverse=True)
            for value, score in ranked[:cap]:
                if score < 2.0 and dim in ("type", "industry"):
                    continue  # 只在摘要里命中一次的类型/行业，精度太差
                conf = self._confidence(dim, score)
                if conf < floor:
                    continue
                out.append(
                    Tag(dim, value, self.labels.get((dim, value), value), round(conf, 2))
                )
        return out

    @staticmethod
    def _confidence(dim: str, score: float) -> float:
        if dim == "region":
            return min(0.97, 0.55 + 0.12 * score)
        if dim == "entity":
            return min(0.95, 0.5 + 0.1 * score)
        if dim == "industry":
            return min(0.92, 0.45 + 0.1 * score)
        return min(0.9, 0.4 + 0.09 * score)


@lru_cache(maxsize=1)
def get_tagger() -> DictTagger:
    return DictTagger()


def sync_tags(conn) -> int:
    """把词典同步进 tags 表，供筛选条与标签展示取 label。"""
    tagger = get_tagger()
    rows = [
        (dim, value, tagger.labels.get((dim, value), value),
         json.dumps(tagger.aliases.get((dim, value), []), ensure_ascii=False),
         tagger.parents.get(value))
        for (dim, value) in tagger.labels
    ]
    conn.executemany(
        "INSERT INTO tags (dim, value, label, aliases, parent) VALUES (?,?,?,?,?)"
        " ON CONFLICT(dim, value) DO UPDATE SET label=excluded.label,"
        " aliases=excluded.aliases, parent=excluded.parent",
        rows,
    )
    conn.commit()
    return len(rows)
