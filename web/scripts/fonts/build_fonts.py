"""按 fonts.lock.json 下载并校验字体，切片成 WOFF2，生成 fonts.css，并把输出的哈希写回锁文件。

只在更新字体时手动运行，用仓库外的虚拟环境：
    ~/.local/loop-now-font-tools/bin/python web/scripts/fonts/build_fonts.py [--download]

原始文件缓存在 ~/.cache/loop-now-fonts（可用 --cache 指定），不进入仓库。
"""

from __future__ import annotations

import argparse
import hashlib
import io
import json
import os
import re
import sys
import urllib.request
import zipfile
from concurrent.futures import ProcessPoolExecutor
from pathlib import Path

# 固定时间戳，保证重复运行时输出逐字节相同。
os.environ["SOURCE_DATE_EPOCH"] = "1760313600"  # 2025-10-13T00:00:00Z

from fontTools import subset  # noqa: E402
from fontTools.ttLib import TTFont  # noqa: E402
from fontTools.varLib import instancer  # noqa: E402

WEB = Path(__file__).resolve().parents[2]
LOCK = WEB / "scripts/fonts/fonts.lock.json"
ASSETS = WEB / "src/assets/fonts"
PUBLIC = WEB / "public/fonts"
CSS = WEB / "src/styles/fonts.css"

# 西文切片的范围，取 Google Fonts 的 latin 子集。
LATIN_RANGES = [
    (0x0000, 0x00FF), (0x0131, 0x0131), (0x0152, 0x0153), (0x02BB, 0x02BC), (0x02C6, 0x02C6),
    (0x02DA, 0x02DA), (0x02DC, 0x02DC), (0x0304, 0x0304), (0x0308, 0x0308), (0x0329, 0x0329),
    (0x2000, 0x206F), (0x20AC, 0x20AC), (0x2122, 0x2122), (0x2191, 0x2191), (0x2193, 0x2193),
    (0x2212, 0x2212), (0x2215, 0x2215), (0xFEFF, 0xFEFF), (0xFFFD, 0xFFFD),
]
LATIN = {cp for start, end in LATIN_RANGES for cp in range(start, end + 1)}


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def fetch(url: str, target: Path, expected: str | None, download: bool) -> bytes:
    if not target.exists():
        if not download:
            sys.exit(f"缺少 {target}，请加 --download 下载")
        print(f"下载 {url}")
        with urllib.request.urlopen(url) as response:
            target.write_bytes(response.read())
    data = target.read_bytes()
    if expected and sha256(data) != expected:
        sys.exit(f"{target.name} 的 SHA-256 不符：期望 {expected}，实际 {sha256(data)}")
    return data


def parse_slices(text: str) -> list[list[int]]:
    """切片表中的子集按优先级从低到高列出；返回按优先级从高到低排列的码位列表。

    逐行解析：码位后面的注释会写出字符本身，其中可能有花括号，不能按括号匹配整块。
    """
    slices: list[list[int]] = []
    current: list[int] | None = None
    for line in text.splitlines():
        line = line.strip()
        if line == "subsets {":
            current = []
        elif line == "}" and current is not None:
            slices.append(current)
            current = None
        elif current is not None:
            match = re.match(r"codepoints: (\d+)", line)
            if match:
                current.append(int(match.group(1)))
    if sum(len(s) for s in slices) != int(re.search(r"# (\d+) codepoints in", text).group(1)):
        sys.exit("切片表解析出的码位总数与表头不符")
    return list(reversed(slices))


def options() -> subset.Options:
    opts = subset.Options()
    opts.layout_features = ["*"]
    opts.name_IDs = ["*"]
    opts.name_languages = ["*"]
    opts.notdef_outline = True
    opts.recalc_timestamp = False
    opts.flavor = "woff2"
    return opts


def subset_bytes(font_bytes: bytes, codepoints: list[int], instance: dict | None = None) -> bytes:
    font = TTFont(io.BytesIO(font_bytes), recalcTimestamp=False, lazy=False)
    if instance:
        font = instancer.instantiateVariableFont(font, instance)
    font.recalcTimestamp = False
    subsetter = subset.Subsetter(options())
    subsetter.populate(unicodes=codepoints)
    subsetter.subset(font)
    out = io.BytesIO()
    font.flavor = "woff2"
    font.save(out)
    return out.getvalue()


def unicode_range(codepoints: list[int]) -> str:
    cps = sorted(set(codepoints))
    ranges: list[str] = []
    start = prev = cps[0]
    for cp in cps[1:] + [None]:
        if cp is not None and cp == prev + 1:
            prev = cp
            continue
        ranges.append(f"U+{start:X}" if start == prev else f"U+{start:X}-{prev:X}")
        if cp is not None:
            start = prev = cp
    return ", ".join(ranges)


def build_job(job: tuple[str, bytes, list[int], dict | None]) -> tuple[str, bytes]:
    name, font_bytes, codepoints, instance = job
    return name, subset_bytes(font_bytes, codepoints, instance)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--cache", type=Path, default=Path.home() / ".cache/loop-now-fonts")
    parser.add_argument("--download", action="store_true", help="缓存中缺少的文件从锁文件中的地址下载")
    parser.add_argument("--jobs", type=int, default=max(1, (os.cpu_count() or 2) - 2))
    args = parser.parse_args()
    args.cache.mkdir(parents=True, exist_ok=True)

    lock = json.loads(LOCK.read_text(encoding="utf-8"))
    table = lock["sliceTable"]
    slice_text = fetch(table["url"], args.cache / Path(table["path"]).name, table["sha256"], args.download).decode("utf-8")
    slices = parse_slices(slice_text)

    outputs: dict[str, str] = {}
    css: list[str] = [
        "/* 由 scripts/fonts/build_fonts.py 生成，不要手工修改。来源与许可证见 scripts/fonts/fonts.lock.json。 */",
    ]

    for spec in lock["fonts"]:
        source = spec["source"]
        raw = fetch(source["url"], args.cache / source["file"], source["sha256"], args.download)
        if "zipMember" in source:
            archive = zipfile.ZipFile(io.BytesIO(raw))
            font_bytes = archive.read(source["zipMember"])
            license_bytes = archive.read(spec["licenseText"]["zipMember"])
        else:
            font_bytes = raw
            text = spec["licenseText"]
            license_bytes = fetch(text["url"], args.cache / text["file"], text["sha256"], args.download)

        font_id = spec["id"]
        cmap = set(TTFont(io.BytesIO(font_bytes), lazy=True).getBestCmap().keys())
        build = spec["build"]
        jobs: list[tuple[str, bytes, list[int], dict | None]] = []
        ranges: dict[str, list[int]] = {}
        if build["kind"] == "cjk-slices":
            for index, codepoints in enumerate(slices):
                covered = sorted(cmap.intersection(codepoints))
                if covered:
                    name = f"{font_id}-{index:03d}.woff2"
                    jobs.append((name, font_bytes, covered, None))
                    ranges[name] = covered
            if build.get("latinSlice"):
                covered = sorted(cmap & LATIN)
                jobs.append((f"{font_id}-latin.woff2", font_bytes, covered, None))
                ranges[f"{font_id}-latin.woff2"] = covered
        else:
            covered = sorted(cmap & LATIN)
            name = f"{font_id}-latin.woff2"
            jobs.append((name, font_bytes, covered, build.get("instance")))
            ranges[name] = covered

        target = ASSETS / font_id
        target.mkdir(parents=True, exist_ok=True)
        for old in target.glob("*.woff2"):
            old.unlink()
        with ProcessPoolExecutor(max_workers=args.jobs) as pool:
            results = dict(pool.map(build_job, jobs))
        for name, _font, _cps, _inst in jobs:
            data = results[name]
            (target / name).write_bytes(data)
            outputs[f"src/assets/fonts/{font_id}/{name}"] = sha256(data)
            css.append(
                "@font-face {\n"
                f"  font-family: '{spec['family']}';\n"
                "  font-style: normal;\n"
                f"  font-weight: {build['fontWeight']};\n"
                "  font-display: swap;\n"
                f"  src: url('../assets/fonts/{font_id}/{name}') format('woff2');\n"
                f"  unicode-range: {unicode_range(ranges[name])};\n"
                "}"
            )
        print(f"{spec['family']}：{len(jobs)} 个文件，{sum(len(results[n]) for n, *_ in jobs) / 1024:.1f} KiB")

        license_dir = PUBLIC / font_id
        license_dir.mkdir(parents=True, exist_ok=True)
        (license_dir / "OFL.txt").write_bytes(license_bytes)
        outputs[f"public/fonts/{font_id}/OFL.txt"] = sha256(license_bytes)

    css_text = "\n\n".join(css) + "\n"
    CSS.write_text(css_text, encoding="utf-8")
    outputs["src/styles/fonts.css"] = sha256(css_text.encode("utf-8"))

    lock["outputs"] = dict(sorted(outputs.items()))
    LOCK.write_text(json.dumps(lock, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"输出 {len(outputs)} 个文件，锁文件已更新")


if __name__ == "__main__":
    main()
