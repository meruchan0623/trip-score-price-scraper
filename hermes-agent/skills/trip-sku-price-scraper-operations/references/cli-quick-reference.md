# Trip 价格 CLI 速查

仓库根目录固定为：

```bash
cd /absolute/path/to/trip-score-price-scraper
```

日常执行先读根目录 `README.md` 和本 Skill；已有二进制可用且源码/依赖未变化时，不为确认参数重复运行 `--help`。

## 任务路由

| 请求 | 入口 | 关键参数 | 输出 |
|---|---|---|---|
| SKU、套餐、外显价、实价、促销、同规格比价 | `./trip-score-price-scraper` 或 `/tmp/trip-score-cdp` | `--run-mode mode1` | `trip_scoreandprice_results_<stamp>.xlsx` 或厂商分片/比价文件 |
| 评分、评价数、预订数、商品资讯，不要价格/SKU | 同上 | `--run-mode mode2` | `trip_score_results_<stamp>.xlsx`；不应含 SKU/价格列 |
| 发送已验证结果 | 根目录 `trip_deliver.py` | `<xlsx或run_dir> --to telegram:<chat_id>` | `.delivered.json` 或 `.pending_delivery.json` |
| 同地区厂商比价 | Go CLI 内建比较器 | `--excel-input ... --compare-vendor "TSIM 網址"` | `vendor_compare_<stamp>.xlsx/.json` |

## 可复制命令

### 指定产品 mode1（HK/HKD）

```bash
cd /absolute/path/to/trip-score-price-scraper
./trip-score-price-scraper \
  --run-mode mode1 \
  --no-headless \
  --site hk \
  --locale zh-HK \
  --currency HKD \
  --delay-profile normal \
  --input 'https://hk.trip.com/things-to-do/detail/<PRODUCT_ID>/' \
  --output-dir '/absolute/path/to/trip-sku-price-scrape/manual-<PRODUCT_ID>' \
  --product-info \
  --no-quality-rerun
```

多个产品按用户顺序重复 `--input`。不要把 mode1 换成 mode2 来抓价格。

### mode2 URL 清单

```bash
cd /absolute/path/to/trip-score-price-scraper
/tmp/trip-score-cdp \
  --run-mode mode2 \
  --urls-file '/absolute/path/urls.txt' \
  --site hk \
  --output-dir '/absolute/path/run-dir'
```

多站点改用 `--sites hk,ru`；`--sites` 存在时忽略 `--site`。mode2 固定不进入 SKU/价格路径。

### 厂商表 mode1 + 内建同地区比价

```bash
cd /absolute/path/to/trip-score-price-scraper
/tmp/trip-score-cdp \
  --run-mode mode1 \
  --excel-input '/absolute/path/vendors.xlsx' \
  --compare-vendor 'TSIM 網址' \
  --delay-profile fast \
  --timeout 1800 \
  --workers 2 \
  --output-dir '/absolute/path/run-dir'
```

`--compare-vendor` 必须精确匹配输入表第一行的基准厂商列名。

### 交付（脚本只在仓库根目录）

```bash
python3 /absolute/path/to/trip-score-price-scraper/trip_deliver.py \
  '/absolute/path/result.xlsx' \
  --to 'telegram:<chat_id>' \
  --subject '携程抓取结果'
```

不要使用不存在的 `scripts/trip_deliver.py` 或 `hermes-agent/scripts/trip_deliver.py`。只有脚本返回 0 且目标运行目录写出匹配的 `.delivered.json` 才算送达。

## `trip_sku_pair_region_compare.py` 当前状态

历史上曾从仓库根目录查询：

```bash
python3 scripts/trip_sku_pair_region_compare.py --help
```

但当前工作树和当前 Git 历史中均不存在 `scripts/trip_sku_pair_region_compare.py`，所以它不是可复制的现行入口。需要同地区 TSim/竞对比价时，使用上面的 Go 内建 `--compare-vendor` 命令并读取 `vendor_compare_<stamp>.xlsx/.json`；除非脚本重新进入当前源码并有 README/参数定义，否则不要凭历史命令猜参数。

## 历史 `--help` 证据（2026-08-21 至 2026-10-21）

`calls.json` 是调用记录，不等于每条都是“查用法”。按完全相同的 `date + command` 去除重放后：

- Trip Go CLI：179 条原始记录，122 个唯一调用，另有 57 条完全相同的重放行。唯一调用中，112 个是构建/测试链验证；6 个是明确的独立/定向查参数（`87855`、`95807`、`100523`、`105921`、`106519/106622`、`107209`）；4 个只是运行前二进制就绪探测（`97990/98089`、`98109/98535`、`111706/113318`、`116594`），不宣称为真实求助。构建验证代表：`76080/82029`、`82514`、`87566/87620`。
- `trip_deliver.py`：21 条原始记录，19 个唯一调用；16 个独立查用法/就绪检查、3 个构建验证。反复暴露路径混淆的证据：`82522` 查询了错误的 `scripts/trip_deliver.py`，随后 `82528` 查询根目录脚本；后续重复见 `89416`、`93830`、`95873`、`119747/119888`。
- `trip_sku_pair_region_compare.py`：`87582/87636` 是同一时间、同一命令的重放重复，只能算 1 次真实查用法；当前文件已不存在。

分类是保守启发式审计：测试/构建链中以 `--help` 验证二进制的调用归为“构建验证”，完全相同时间与命令归为“重放重复”，其余独立/筛选参数调用归为“查用法或就绪探测”；不把命令字符串里出现 `help` 一律宣称为求助。
