"""翻译后端：Protocol + 注册表。

所有后端实现同一个约定：translate_batch(texts, target, settings) 返回与输入等长
的列表，单条失败用 None 占位。缓存/语言判断/降级由 service.py 统一处理，
后端不碰数据库，也不会把异常抛到 Web 层。
"""
from __future__ import annotations

import json
import re
from concurrent.futures import ThreadPoolExecutor
from typing import Protocol

import httpx

from app import config


class Backend(Protocol):
    name: str

    def translate_batch(
        self, texts: list[str], target: str, settings: config.Settings
    ) -> list[str | None]: ...


def _proxy(settings: config.Settings) -> str | None:
    t = settings.translate
    return settings.proxy.url if (t.use_proxy and settings.proxy.url) else None


class NullBackend:
    """显式关闭 / 缺少必要配置：不翻译，也不报错。"""

    name = "none"

    def translate_batch(self, texts, target, settings):
        return [None] * len(texts)


class GoogleFreeBackend:
    """Google 翻译公开单句端点（client=gtx），无需密钥；境外访问，复用代理。

    单条一个 GET，返回 [[["译文","原文",...], ...], null, "en", ...]。
    批量时用小并发，避免公开端点限流。
    """

    name = "google_free"
    ENDPOINT = "https://translate.googleapis.com/translate_a/single"
    WORKERS = 3

    def _one(self, client: httpx.Client, text: str, target: str) -> str | None:
        r = client.get(
            self.ENDPOINT,
            params={"client": "gtx", "sl": "auto", "tl": target, "dt": "t", "q": text},
        )
        r.raise_for_status()
        data = r.json()
        parts = [str(seg[0]) for seg in (data[0] or []) if seg and seg[0]]
        return "".join(parts) or None

    def _safe_one(self, client: httpx.Client, text: str, target: str) -> str | None:
        try:
            return self._one(client, text, target)
        except Exception:
            return None

    def translate_batch(self, texts, target, settings):
        headers = {"User-Agent": "Mozilla/5.0 (compatible; NewsStation/0.1)"}
        with httpx.Client(
            proxy=_proxy(settings),
            timeout=settings.translate.timeout_seconds,
            follow_redirects=True,
            headers=headers,
        ) as client:
            if len(texts) == 1:
                return [self._safe_one(client, texts[0], target)]
            with ThreadPoolExecutor(max_workers=min(self.WORKERS, len(texts))) as pool:
                futures = [pool.submit(self._safe_one, client, t, target) for t in texts]
                return [f.result() for f in futures]


def guess_source(text: str) -> str:
    """MyMemory 的 langpair 不接受 auto，按字符集粗判源语言。"""
    if re.search(r"[\u3040-\u30ff]", text):
        return "ja"
    if re.search(r"[\uac00-\ud7af]", text):
        return "ko"
    if re.search(r"[\u0400-\u04ff]", text):
        return "ru"
    if re.search(r"[\u4e00-\u9fff]", text):
        return "zh-CN"
    return "en"


class MyMemoryBackend:
    """MyMemory 公共 API：免密钥、国内直连可用，返回 JSON。

    实测共享代理 IP 的匿名额度已被其他租户耗尽（quotaFinished=true），
    直连出口正常，因此这里固定不走 proxy，不受 translate.use_proxy 影响。
    填 translate.mymemory_email 可把配额从 5000 提到 50000 字/天。
    """

    name = "mymemory"
    ENDPOINT = "https://api.mymemory.translated.net/get"
    WORKERS = 2

    def _one(self, client: httpx.Client, text: str, target: str,
             settings: config.Settings) -> str | None:
        params = {"q": text[:480], "langpair": f"{guess_source(text)}|{target}"}
        if settings.translate.mymemory_email:
            params["de"] = settings.translate.mymemory_email
        r = client.get(self.ENDPOINT, params=params)
        r.raise_for_status()
        d = r.json()
        if d.get("responseStatus") != 200:
            return None
        out = (d.get("responseData") or {}).get("translatedText")
        # 配额耗尽时 API 仍返回 200，但把警告塞进 translatedText
        if not out or "MYMEMORY WARNING" in out:
            return None
        return str(out).strip() or None

    def _safe_one(self, client, text, target, settings):
        try:
            return self._one(client, text, target, settings)
        except Exception:
            return None

    def translate_batch(self, texts, target, settings):
        headers = {"User-Agent": "Mozilla/5.0 (compatible; NewsStation/0.1)"}
        with httpx.Client(
            timeout=settings.translate.timeout_seconds,
            follow_redirects=True,
            headers=headers,
        ) as client:
            if len(texts) == 1:
                return [self._safe_one(client, texts[0], target, settings)]
            with ThreadPoolExecutor(max_workers=min(self.WORKERS, len(texts))) as pool:
                futures = [
                    pool.submit(self._safe_one, client, t, target, settings) for t in texts
                ]
                return [f.result() for f in futures]


def _parse_json_array(raw: str):
    """模型可能用 ```json ... ``` 包住结果，先剥掉再解析。"""
    s = (raw or "").strip()
    if s.startswith("```"):
        s = re.sub(r"^```(?:json)?\s*", "", s)
        s = re.sub(r"\s*```$", "", s)
    return json.loads(s)


class OpenAICompatBackend:
    """OpenAI 兼容 chat completions：一次请求翻一批，质量最好，需要 base_url。"""

    name = "openai"

    def translate_batch(self, texts, target, settings):
        cfg = settings.translate.openai
        if not cfg.base_url:
            return [None] * len(texts)
        lang = "简体中文" if target.lower().startswith("zh") else (
            "English" if target.lower().startswith("en") else target
        )
        headers = {"Content-Type": "application/json"}
        if cfg.api_key:
            headers["Authorization"] = f"Bearer {cfg.api_key}"
        payload = {
            "model": cfg.model,
            "messages": [
                {
                    "role": "system",
                    "content": (
                        f"You are a news translator. Translate every string in the JSON array"
                        f" into {lang}. Keep proper nouns, organisations and numbers accurate."
                        " Return ONLY a JSON array of translated strings in the same order and"
                        " the same length. No markdown, no explanation."
                    ),
                },
                {"role": "user", "content": json.dumps(texts, ensure_ascii=False)},
            ],
            "temperature": 0,
        }
        try:
            with httpx.Client(
                proxy=_proxy(settings),
                timeout=max(settings.translate.timeout_seconds, 30.0),
            ) as client:
                r = client.post(
                    cfg.base_url.rstrip("/") + "/chat/completions",
                    json=payload,
                    headers=headers,
                )
                r.raise_for_status()
                arr = _parse_json_array(r.json()["choices"][0]["message"]["content"])
            if isinstance(arr, list) and len(arr) == len(texts):
                return [str(x).strip() if x else None for x in arr]
        except Exception:
            pass
        return [None] * len(texts)


class LibreBackend:
    """自托管 LibreTranslate，免密钥；逐条 POST /translate。"""

    name = "libre"

    def translate_batch(self, texts, target, settings):
        cfg = settings.translate.libre
        if not cfg.url:
            return [None] * len(texts)
        tgt = target.split("-")[0].lower()
        out: list[str | None] = []
        with httpx.Client(
            proxy=_proxy(settings), timeout=settings.translate.timeout_seconds
        ) as client:
            for t in texts:
                try:
                    body = {"q": t, "source": "auto", "target": tgt, "format": "text"}
                    if cfg.api_key:
                        body["api_key"] = cfg.api_key
                    r = client.post(cfg.url.rstrip("/") + "/translate", json=body)
                    r.raise_for_status()
                    out.append(r.json().get("translatedText") or None)
                except Exception:
                    out.append(None)
        return out


class AutoBackend:
    """自动回退链：整批先试 mymemory，只把失败项交给 google_free 再试一次。"""

    name = "auto"
    CHAIN = ("mymemory", "google_free")

    def translate_batch(self, texts, target, settings):
        out: list[str | None] = [None] * len(texts)
        for name in self.CHAIN:
            missing = [i for i, v in enumerate(out) if not v]
            if not missing:
                break
            backend = BACKENDS[name]()
            res = backend.translate_batch([texts[i] for i in missing], target, settings)
            for i, val in zip(missing, res):
                if val and str(val).strip():
                    out[i] = str(val).strip()
        return out


BACKENDS: dict[str, type] = {
    "auto": AutoBackend,
    "mymemory": MyMemoryBackend,
    "google_free": GoogleFreeBackend,
    "openai": OpenAICompatBackend,
    "libre": LibreBackend,
    "none": NullBackend,
}


def get_backend(settings: config.Settings) -> Backend:
    """按配置取后端；关键配置缺失时静默降级为 NullBackend。"""
    name = (settings.translate.backend or "none").strip().lower()
    cls = BACKENDS.get(name, NullBackend)
    if name == "openai" and not settings.translate.openai.base_url:
        return NullBackend()
    if name == "libre" and not settings.translate.libre.url:
        return NullBackend()
    return cls()


def chain_of(settings: config.Settings) -> list[str]:
    """给状态接口展示实际会尝试的后端顺序。"""
    b = get_backend(settings)
    return list(b.CHAIN) if hasattr(b, "CHAIN") else [b.name]
