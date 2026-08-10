#!/usr/bin/env python3
"""
daily_mode2_urls.py — 从 日常抓取目标.xlsx 生成 mode2 多站点 URL 列表

用法：
  python3 daily_mode2_urls.py [--output PATH] [--source-site hk]

产出：
  - 一行一个产品 URL（格式 `https://hk.trip.com/things-to-do/detail/<ID>/`）
  - 去重、只读 Sheet1 列 TSIM 網址/BC/TGT/FR/JY/其他 中所有数字 ID
  - 默认输出到 /tmp/trip_mode2_daily_template.txt

此脚本只做纯数据转换，不启动浏览器、不发请求、不运行抓取。
"""
import argparse
import re
from pathlib import Path

import openpyxl

DEFAULT_INPUT = (
    Path(__file__).resolve().parent / "docs" / "日常抓取目标.xlsx"
)
DEFAULT_OUTPUT = Path("/tmp/trip_mode2_daily_template.txt")
VENDOR_COLS = ["TSIM 網址", "BC", "TGT", "FR", "JY", "其他"]


def extract_product_ids(path: Path) -> list[int]:
    wb = openpyxl.load_workbook(path, data_only=True)
    ws = wb["Sheet1"]
    col_map = {}
    for c in ws[1]:
        if c.value in VENDOR_COLS:
            col_map[c.value] = c.column
    ids: list[int] = []
    seen = set()
    for row in ws.iter_rows(min_row=2, values_only=True):
        for col_name in VENDOR_COLS:
            c = col_map.get(col_name)
            if c is None:
                continue
            v = row[c - 1]
            if v is not None:
                try:
                    pid = int(str(v).strip())
                except ValueError:
                    continue
                if pid not in seen:
                    seen.add(pid)
                    ids.append(pid)
    wb.close()
    return ids


def main(argv: list[str] | None = None) -> None:
    p = argparse.ArgumentParser(description="生成 mode2 多站点 URL 列表")
    p.add_argument("--input", default=str(DEFAULT_INPUT), help="日常抓取目标.xlsx 路径")
    p.add_argument("--output", default=str(DEFAULT_OUTPUT))
    p.add_argument("--source-site", default="hk", help="URL 来源站点 code（默认 hk）")
    args = p.parse_args(argv)
    ids = extract_product_ids(Path(args.input))
    lines = [
        f"https://{args.source_site}.trip.com/things-to-do/detail/{pid}/"
        for pid in ids
    ]
    Path(args.output).write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"{len(lines)} URLs -> {args.output}")


if __name__ == "__main__":
    main()
