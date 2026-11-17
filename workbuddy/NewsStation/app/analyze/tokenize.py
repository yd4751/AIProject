"""分词与语言判定。中英混排新闻用同一套接口，聚类只依赖 token 计数。"""

from __future__ import annotations

import re
from collections import Counter

import jieba

jieba.setLogLevel(60)

_CJK_RE = re.compile(r"[\u4e00-\u9fff]")
_EN_WORD_RE = re.compile(r"[a-z][a-z0-9'\-]+")
_PUNCT_RE = re.compile(r"^[\W_]+$", re.UNICODE)

# 停用词：通用虚词 + 新闻套话。套话必须去掉，否则每条都带 "记者/报道/表示"，
# 会把所有文章拉成高相似度，聚类直接失效。
STOPWORDS = set(
    """
的 了 和 是 在 有 就 不 人 都 一 一个 上 也 很 到 说 要 去 你 会 着 没有 看 好 自己
这 那 个 为 与 及 或 等 但 而 从 对 将 于 以 被 把 让 向 由 至 关于 因为 所以 如果
已经 可以 表示 认为 称 据 记者 报道 消息 日电 新华社 中新社 中新网 本报 编辑 责编
今日 昨天 今天 上午 下午 晚间 时间 方面 情况 问题 工作 有关 进行 目前 获悉 发布
同时 以及 其中 由于 通过 对于 各类 各项 进一步 相关 主要 重要 上述 该 其 此 我们
他们 她们 你们 我 您 之 并 则 若 虽 然而 此外 另外 目前 近日 日前 当天 当日 此次
显示 指出 强调 介绍 回应 宣布 据悉 综合 报道称 最新 首次 已 仍 未 正 将 会 能 可
"""
    .split()
)

EN_STOPWORDS = set(
    """
a an the and or but if then than that this these those of in on at to for from by with
without into onto over under above below about after before during while as is are was were
be been being am do does did done have has had having will would shall should can could may
might must not no nor so such very too also just only even still yet already again more most
much many few some any all both each other another own same here there where when what which
who whom whose why how it its it's they them their theirs he him his she her hers we us our
you your yours i me my mine one two three four five six seven eight nine ten first second
third last next new news report reports reported according said says say told tells added
amid via per vs up down out off back now today yesterday tomorrow week weeks day days year
years month months hour hours minute minutes time times people official officials
spokesperson statement announced announces announcement latest update updates live
""".split()
)

STOPWORDS |= EN_STOPWORDS


def detect_lang(text: str) -> str:
    """CJK 字符占比 > 15% 判中文，否则英文。与规划一致。"""
    if not text:
        return "en"
    cjk = len(_CJK_RE.findall(text))
    return "zh" if cjk / max(len(text), 1) > 0.15 else "en"


def _stem_en(word: str) -> str:
    """极简复数归并，让 chips/chip、companies/company 在聚类里同形。
    只处理不影响语义的后缀，不做完整词干化。"""
    if len(word) > 4 and word.endswith("ies"):
        return word[:-3] + "y"
    if len(word) > 4 and word.endswith("es") and word[-3] in "sxzh":
        return word[:-2]
    if len(word) > 3 and word.endswith("s") and not word.endswith("ss"):
        return word[:-1]
    return word


def tokenize(text: str, lang: str | None = None) -> list[str]:
    if not text:
        return []
    lang = lang or detect_lang(text)
    if lang == "zh":
        tokens = []
        for w in jieba.lcut(text):
            w = w.strip()
            if len(w) < 2 or _PUNCT_RE.match(w) or w in STOPWORDS:
                continue
            if w.isdigit():
                continue
            tokens.append(w)
        return tokens
    text = text.lower().replace("’", "'")
    return [
        _stem_en(w)
        for w in _EN_WORD_RE.findall(text)
        if len(w) > 2 and w not in STOPWORDS and not w.isdigit()
    ]


def weighted_counter(title: str, summary: str, lang: str, title_weight: float = 1.5) -> Counter:
    """标题词权重更高：新闻的信息量集中在标题，摘要常是套话复述。"""
    counter: Counter = Counter()
    for tok in tokenize(title, lang):
        counter[tok] += title_weight
    for tok in tokenize(summary, lang):
        counter[tok] += 1.0
    return counter


def top_keywords(counter: Counter, k: int = 8) -> list[str]:
    return [w for w, _ in counter.most_common(k)]
