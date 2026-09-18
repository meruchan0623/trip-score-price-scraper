#!/usr/bin/env python3
"""Retry only failed rows in a daily HK Outerlist workbook at a controlled API interval."""
from __future__ import annotations
import argparse, importlib.util, json, sys, time
from datetime import datetime
from pathlib import Path
import openpyxl
COLLECTOR=Path('/Users/example/Develop/trip-score-price-scraper/scripts/trip_outerlist_mode2_api_daily.py')
def load_collector():
 spec=importlib.util.spec_from_file_location('daily_collector',COLLECTOR); module=importlib.util.module_from_spec(spec); assert spec and spec.loader; spec.loader.exec_module(module); return module
def value_rows(ws,headers):
 result={}
 for row in ws.iter_rows(min_row=3,values_only=True):
  values={header:'' if i>=len(row) or row[i] is None else str(row[i]) for i,header in enumerate(headers)}; pid=values.get('商品ID','').strip()
  if pid: result[pid]=values
 return result
def main():
 parser=argparse.ArgumentParser(); parser.add_argument('--run-dir',required=True); parser.add_argument('--baseline-workbook',required=True); parser.add_argument('--interval-seconds',type=float,default=5.0); args=parser.parse_args()
 if args.interval_seconds<1: raise SystemExit('--interval-seconds 必须不小于 1')
 run=Path(args.run_dir); marker=run/'.collection_done'; metadata=json.loads(marker.read_text(encoding='utf-8')); output=Path(metadata['output_path']); baseline=Path(args.baseline_workbook)
 if not output.is_file() or not baseline.is_file(): raise SystemExit('输出或基线工作簿不存在')
 collector=load_collector(); previous=collector.metrics_from_book(baseline)
 wb=openpyxl.load_workbook(output,read_only=True,data_only=True); outer,hk=wb['Outerlist商品清单'],wb['HK']; outer_headers=[str(c.value) for c in outer[2]]; hk_headers=[str(c.value) for c in hk[2]]; outer_rows=value_rows(outer,outer_headers); existing=value_rows(hk,hk_headers); wb.close()
 failures=[pid for pid,row in existing.items() if row.get('抓取备注','').strip()]
 if not failures: print('无需重试：HK 工作表无失败行'); return
 recovered=0
 for index,pid in enumerate(failures,start=1):
  item={'id':pid,'vendor':outer_rows.get(pid,{}).get('厂商') or '未标注'}; refreshed=collector.fetch('hk','zh-HK','HKD',item); existing[pid]=refreshed
  if not refreshed.get('抓取备注'): recovered+=1
  print(f'{index}/{len(failures)} {pid} '+('OK' if not refreshed.get('抓取备注') else 'RETRY_FAILED'),flush=True)
  if index<len(failures): time.sleep(args.interval_seconds)
 ordered=[existing[pid] for pid in outer_rows if pid in existing]; collector.add_sales_sheets(output,datetime.now().date(),{'HK':ordered},previous)
 still_failed=[pid for pid,row in existing.items() if row.get('抓取备注','').strip()]
 metadata.update({'failed_count':len(still_failed),'failed_samples':[f'HK/{pid}' for pid in still_failed[:20]],'baseline_run':str(baseline),'retry_method':'public_api_serial_interval','retry_interval_seconds':args.interval_seconds,'retry_attempted_count':len(failures),'retry_recovered_count':recovered,'completed_at':datetime.now().isoformat()})
 marker.write_text(json.dumps(metadata,ensure_ascii=False,indent=2),encoding='utf-8')
 print(json.dumps({'attempted':len(failures),'recovered':recovered,'still_failed':len(still_failed),'output':str(output)},ensure_ascii=False))
 if still_failed: raise SystemExit(2)
if __name__=='__main__': main()
