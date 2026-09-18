#!/usr/bin/env python3
"""Retry only failed Trip star-distribution records serially after rate-limit cooling."""
from __future__ import annotations
import importlib.util, json, time
from pathlib import Path

ROOT=Path('/Users/example/Documents/trip-sku-price-scrape/260831-rating-audit')
SRC=Path('/Users/example/Develop/trip-score-price-scraper/scripts/collect_trip_rating_audit.py')
spec=importlib.util.spec_from_file_location('collector',SRC)
if spec is None or spec.loader is None:
    raise RuntimeError(f'cannot load collector: {SRC}')
mod=importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
path=ROOT/'main_product_stars.json'
rows=json.loads(path.read_text())
failed=[x for x in rows if 'error' in x]
print('retry_count',len(failed),flush=True)
for i,item in enumerate(failed,1):
    pid=item['product_id']
    try:
        result=mod.stars({k:v for k,v in item.items() if k!='error'})
        print(i,'/',len(failed),pid,'OK',result['counted_reviews'],'/',result['review_total'],flush=True)
    except Exception as e:
        result={**item,'error':repr(e)}
        print(i,'/',len(failed),pid,'ERR',repr(e),flush=True)
    rows=[result if x.get('product_id')==pid else x for x in rows]
    path.write_text(json.dumps(sorted(rows,key=lambda x:x['product_id']),ensure_ascii=False,indent=2))
    time.sleep(2.5)
print('DONE',flush=True)
