#!/usr/bin/env python3
"""Parallel HTTP Range downloader for Godot export templates.

Why this exists: the .tpz is ~1.28 GB and GitHub throttles a single connection.
Splitting into N byte ranges across several mirrors finishes much faster.
Resumable: existing .part files are kept and resumed from their current size.
"""
import argparse
import os
import sys
import time
import urllib.request
import zipfile
from concurrent.futures import ThreadPoolExecutor, as_completed

RELEASE = (
    "https://github.com/godotengine/godot/releases/download/"
    "4.7.2-stable/Godot_v4.7.2-stable_export_templates.tpz"
)
# urllib picks up the system proxy on this machine, and that proxy rejects the
# direct GitHub host (502). Only use mirrors the proxy accepts.
MIRRORS = [
    "https://ghfast.top/" + RELEASE,
    "https://gh-proxy.com/" + RELEASE,
    "https://ghproxy.net/" + RELEASE,
]
UA = {"User-Agent": "Mozilla/5.0"}


def head_size(url):
    req = urllib.request.Request(url, method="HEAD", headers=UA)
    with urllib.request.urlopen(req, timeout=40) as r:
        return int(r.headers["Content-Length"])


def fetch_part(job):
    idx, start, end, url, path = job
    expected = end - start + 1
    for attempt in range(12):
        have = os.path.getsize(path) if os.path.exists(path) else 0
        if have == expected:
            return idx, expected, "cached"
        begin = start + have
        src = MIRRORS[(idx + attempt) % len(MIRRORS)]
        req = urllib.request.Request(
            src, headers=dict(UA, Range="bytes=%d-%d" % (begin, end))
        )
        try:
            with urllib.request.urlopen(req, timeout=90) as r, open(path, "ab") as f:
                # Some mirrors ignore Range and return 200 with the whole file.
                # Never accept that, and never write past the part boundary.
                if r.status != 206:
                    raise RuntimeError("server ignored Range: %s" % r.status)
                remaining = expected - have
                while remaining > 0:
                    chunk = r.read(min(1024 * 1024, remaining))
                    if not chunk:
                        break
                    f.write(chunk)
                    remaining -= len(chunk)
            size = os.path.getsize(path)
            if size == expected:
                return idx, size, "ok"
        except Exception as exc:  # noqa: BLE001 - resumable download
            print("[part %d] attempt %d: %s" % (idx, attempt + 1, exc), flush=True)
            time.sleep(2)
    got = os.path.getsize(path) if os.path.exists(path) else 0
    return idx, got, "FAILED"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True, help="final .tpz path")
    ap.add_argument("--parts", type=int, default=8)
    ap.add_argument("--size", type=int, default=0, help="skip HEAD if given")
    ap.add_argument("--no-merge", action="store_true", help="only download parts")
    args = ap.parse_args()

    total = args.size or head_size(MIRRORS[0])
    print("[INFO] total=%d bytes (%.1f MiB), parts=%d" % (
        total, total / 1048576.0, args.parts), flush=True)

    part_dir = os.path.join(os.path.dirname(os.path.abspath(args.out)), "_parts")
    os.makedirs(part_dir, exist_ok=True)
    step = total // args.parts
    jobs = []
    for i in range(args.parts):
        start = i * step
        end = total - 1 if i == args.parts - 1 else (i + 1) * step - 1
        jobs.append((
            i, start, end, MIRRORS[i % len(MIRRORS)],
            os.path.join(part_dir, "part%02d.bin" % i),
        ))

    t0 = time.time()
    results = {}
    with ThreadPoolExecutor(max_workers=args.parts) as pool:
        futures = [pool.submit(fetch_part, j) for j in jobs]
        for fu in as_completed(futures):
            idx, size, status = fu.result()
            results[idx] = (size, status)
            done = sum(v[0] for v in results.values())
            print("[PROGRESS] part %d %s, total %.1f MiB / %.1f MiB, %.1fs" % (
                idx, status, done / 1048576.0, total / 1048576.0,
                time.time() - t0), flush=True)

    bad = [i for i, (size, status) in results.items() if status == "FAILED"]
    if bad:
        print("[ERROR] failed parts: %s" % bad, flush=True)
        return 1

    if args.no_merge:
        return 0

    with open(args.out, "wb") as out:
        for i in range(args.parts):
            with open(jobs[i][4], "rb") as f:
                while True:
                    chunk = f.read(4 * 1048576)
                    if not chunk:
                        break
                    out.write(chunk)
    final = os.path.getsize(args.out)
    print("[MERGED] %d bytes in %.1fs -> %s" % (
        final, time.time() - t0, args.out), flush=True)
    if final != total:
        print("[ERROR] size mismatch %d != %d" % (final, total), flush=True)
        return 1

    with zipfile.ZipFile(args.out) as z:
        names = z.namelist()
        android = [n for n in names if "android" in n.lower()]
    print("[ZIP] entries=%d, android entries=%s" % (len(names), android[:12]), flush=True)
    print("[DONE] OK", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
