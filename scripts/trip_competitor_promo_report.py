#!/usr/bin/env python3
"""Offline HK/RU promotion replay; it never changes input workbooks or cron."""
from __future__ import annotations
import argparse, hashlib, json, re, sys
from collections import Counter, defaultdict
from dataclasses import dataclass
from pathlib import Path
from typing import Any
import openpyxl
REPO_ROOT = Path(__file__).resolve().parent.parent
if str(REPO_ROOT) not in sys.path: sys.path.insert(0, str(REPO_ROOT))
from daily_mode2_urls import resolve_target_sheet

SITES=("HK","RU")
TSIM_ALIASES={"tsim","tsim网址","tsim網址","tsimurl","携程","trip"}
META_HEADERS={"目的地","分类","分類","tsim id","tsimid","id"}
PERCENT_RE=re.compile(r"(?<![\d.])(\d{1,3}(?:\.\d+)?)\s*(?:%|％|percent|percentage)(?!\w)",re.I)
THRESHOLDS={"exclude_percent":50,"significant_change_pp":10,"near_tsim_below_pp":5}
def text(v: Any)->str: return "" if v is None else (str(int(v)) if isinstance(v,float) and v.is_integer() else str(v).strip())
def norm(v: Any)->str: return re.sub(r"\s+","",text(v)).lower()
def canonical_vendor(v: str)->str: return "TSIM" if norm(v) in TSIM_ALIASES else text(v)
def sha256(path: Path)->str:
 h=hashlib.sha256()
 with path.open("rb") as f:
  for b in iter(lambda:f.read(1048576),b""): h.update(b)
 return h.hexdigest()
def columns(head): return {text(v):i for i,v in enumerate(head) if text(v)}
def non50_discount(label):
 values=[float(m.group(1)) for m in PERCENT_RE.finditer(label)]
 if not values: return None,"promo_percent_unparseable"
 if any(v<0 or v>100 for v in values): return None,"promo_percent_out_of_range"
 values=[v for v in values if v!=50]
 return (max(values) if values else 0.0),None
def split_vendor(v):
 return tuple(x.strip() for x in re.split(r"\s*(?:/|／|、|&|＆)\s*",text(v)) if x.strip())
@dataclass(frozen=True)
class Target: destination:str; vendor:str; product_id:str; tsim_product_id:str
@dataclass
class Row:
 site:str; vendor:str; product_id:str; name:str; promo:str; note:str; raw:tuple[Any,...]; discount:float|None=None; reasons:tuple[str,...]=(); destination:str=""
def find_header(head,*names):
 wanted={norm(x) for x in names}
 for k in head:
  if norm(k) in wanted:return k
 raise ValueError(f"missing column one of {names}")
def load_targets(path):
 wb=openpyxl.load_workbook(path,read_only=True,data_only=True); it=resolve_target_sheet(wb).iter_rows(values_only=True); head=columns(next(it,()))
 dest_col=find_header(head,"目的地"); tsim_cols=[k for k in head if norm(k) in TSIM_ALIASES]
 if not tsim_cols: raise ValueError("target missing TSIM alias column")
 tsim_col=tsim_cols[0]; vendor_cols=[k for k in head if norm(k) not in META_HEADERS and k not in (dest_col,tsim_col)]
 out=defaultdict(list)
 for values in it:
  get=lambda k:text(values[head[k]]) if head[k]<len(values) else ""
  dest,tsim_pid=get(dest_col),get(tsim_col)
  if not dest:continue
  if tsim_pid:out[("TSIM",tsim_pid)].append(Target(dest,"TSIM",tsim_pid,tsim_pid))
  for col in vendor_cols:
   pid=get(col)
   if pid:out[(canonical_vendor(col),pid)].append(Target(dest,canonical_vendor(col),pid,tsim_pid))
 wb.close(); return out,{canonical_vendor(x) for x in vendor_cols}
def row_reasons(pid,vendor,name,promo,note):
 why=[]
 if not pid or not vendor:why.append("missing_or_invalid_row")
 if not name:why.append("missing_name")
 if note:why.append("nonempty_failure_note")
 discount=None
 if not promo:why.append("empty_promo_without_success_evidence")
 else:
  discount,reason=non50_discount(promo)
  if reason:why.append(reason)
 # Keep a successfully parsed value for the raw-sheet audit even when the row is
 # quarantined for another reason; comparison still requires no quality reasons.
 return discount,tuple(why)
def load_rows(path):
 all_rows=[]; grouped=defaultdict(list); reasons=Counter(); headers={}; wb=openpyxl.load_workbook(path,read_only=True,data_only=True)
 for site in SITES:
  if site not in wb.sheetnames:reasons[f"missing_sheet_{site}"]+=1;continue
  it=wb[site].iter_rows(values_only=True); head=columns(next(it,())); headers[site]=tuple(head.keys())
  vcol=find_header(head,"厂商"); pcol=find_header(head,"商品ID"); ncol=find_header(head,"商品名称"); prcol=find_header(head,"总促销标签")
  # Native Go exports omit this optional column when every row succeeded.
  # Its absence means an empty note; when supplied, non-empty notes remain a
  # quality quarantine signal below.
  ntcol=next((key for key in head if norm(key)==norm("抓取备注")),None)
  for values in it:
   get=lambda k:text(values[head[k]]) if head[k]<len(values) else ""
   vendor=canonical_vendor(get(vcol)); pid,name,promo,note=get(pcol),get(ncol),get(prcol),(get(ntcol) if ntcol else ""); discount,why=row_reasons(pid,vendor,name,promo,note)
   r=Row(site,vendor,pid,name,promo,note,tuple(values),discount,why);all_rows.append(r);grouped[(site,vendor,pid)].append(r);reasons.update(why)
 for key,rows in grouped.items():
  if len(rows)>1:
   for r in rows:r.reasons=tuple(sorted(set(r.reasons)|{"duplicate_input_key"}));r.discount=None
   reasons["duplicate_input_key"]+=len(rows)
 wb.close();return all_rows,grouped,reasons,headers
def unique(grouped,key): return grouped[key][0] if len(grouped.get(key,[]))==1 else None
def add_pending(pending,key,*why):
 values=[x for x in why if x]
 if values: pending[key].update(values)
def mapping(targets,vendor,pid):
 choices=[]
 for part in split_vendor(vendor) or (vendor,): choices.extend(targets.get((part,pid),[]))
 # A joined current vendor (for example BC/TGT) can point at split frozen columns.
 # Deduplicate the paired identity before judging ambiguity.
 choices=list({(x.destination,x.tsim_product_id):x for x in choices}.values())
 return choices[0] if len({(x.destination,x.tsim_product_id) for x in choices})==1 else None
def date_from(path):
 m=re.search(r"(\d{6})(?!\d)",str(path));return "20"+m.group(1) if m else "unknown"
def build(current,previous,target):
 targets,competitors=load_targets(target);now,nowg,nr,headers=load_rows(current)
 if previous: old,oldg,oldr,_=load_rows(previous)
 else: old,oldg,oldr=[],defaultdict(list),Counter()
 pending=defaultdict(set);details=[]
 for r in now:
  t=mapping(targets,r.vendor,r.product_id)
  if t:r.destination=t.destination
 mapped_vendors=set()
 for r in now:
  if not any(part in competitors for part in (split_vendor(r.vendor) or (r.vendor,))):continue
  mapped_vendors.add(r.vendor)
  key=(r.site,r.vendor,r.product_id);add_pending(pending,key,*["current_"+x for x in r.reasons]);t=mapping(targets,r.vendor,r.product_id)
  if not t:add_pending(pending,key,"ambiguous_or_missing_frozen_target_mapping");continue
  r.destination=t.destination;before=unique(oldg,key)
  if before is None:add_pending(pending,key,"missing_baseline_not_addition" if key not in oldg else "duplicate_baseline_key");continue
  add_pending(pending,key,*["previous_"+x for x in before.reasons])
  if pending[key] or r.discount==before.discount:continue
  tsim=unique(nowg,(r.site,"TSIM",t.tsim_product_id)) if t.tsim_product_id else None; tsim_discount=tsim.discount if tsim and not tsim.reasons else None
  if tsim and tsim.reasons:add_pending(pending,(r.site,"TSIM",t.tsim_product_id),*["current_"+x for x in tsim.reasons])
  change=r.discount-before.discount;gap=None if tsim_discount is None else r.discount-tsim_discount;kind="added" if before.discount==0<r.discount else "cancelled" if r.discount==0<before.discount else "increased" if change>0 else "decreased"
  impact="促销吸引力风险，非已证实转化损失" if kind in ("added","increased") else "竞品促销回落，有利影响，非已证实转化收益"
  details.append(dict(destination=t.destination,site=r.site,competitor=r.vendor,competitor_id=r.product_id,tsim_id=t.tsim_product_id,old=before.discount,new=r.discount,tsim=tsim_discount,gap=gap,change=change,kind=kind,impact=impact,raw_old=before.promo,raw_new=r.promo))
 for r in old:
  if any(part in competitors for part in (split_vendor(r.vendor) or (r.vendor,))) and (r.site,r.vendor,r.product_id) not in nowg:add_pending(pending,(r.site,r.vendor,r.product_id),"missing_current_row_not_cancellation")
 for r in now:
  if r.vendor=="TSIM" and r.reasons:add_pending(pending,(r.site,"TSIM",r.product_id),*["current_"+x for x in r.reasons])
 sync=defaultdict(set)
 for d in details:sync[(d["destination"],d["competitor"],d["old"],d["new"],d["tsim"],d["gap"],d["impact"])].add(d["site"])
 events=[]
 for d in details:
  d["synchronized"]=len(sync[(d["destination"],d["competitor"],d["old"],d["new"],d["tsim"],d["gap"],d["impact"])])>=2;near=d["gap"] is not None and d["gap"]>=-THRESHOLDS["near_tsim_below_pp"]
  d["priority"]=(d["kind"] in ("added","increased") and near) or d["synchronized"] or abs(d["change"])>=THRESHOLDS["significant_change_pp"]
  if d["priority"]:events.append(d)
 pending={k:v for k,v in pending.items() if v}
 only50=lambda rows:sum(r.discount==0 and r.promo and not r.reasons for r in rows)
 audit={"source_paths":{"current":str(current),"previous":str(previous) if previous else None,"target":str(target)},"source_sha256":{k:sha256(v) for k,v in (("current",current),("target",target))}|({"previous":sha256(previous)} if previous else {}),"raw_rows":{"current":dict(Counter(r.site for r in now)),"previous":dict(Counter(r.site for r in old))},"input_unique_counts":{"current":len(nowg),"previous":len(oldg)},"quality_reasons":{"current":dict(nr),"previous":dict(oldr)},"thresholds":THRESHOLDS,"pending_union_count":len(pending),"pending_reasons":dict(Counter(x for s in pending.values() for x in s)),"pending_vendor_breakdown":dict(Counter(k[1] for k in pending)),"fifty_only_exclusion_counts":{"current":only50(now),"previous":only50(old)},"event_count":len(events),"detail_change_count":len(details),"competitor_vendors":sorted(competitors|mapped_vendors),"current_date":date_from(current),"baseline_date":date_from(previous) if previous else "missing"}
 return events,details,audit,pending,now,headers
def fmt(n):return "待确认" if n is None else ("无非50促销" if n==0 else f"{n:g}%")
def gap_text(g):return "待确认" if g is None else ("TSIM持平" if g==0 else (f"TSIM落后 {g:g}pp" if g>0 else f"TSIM领先 {-g:g}pp"))
def comparison_text(tsim,competitor,value):
 if tsim is None:return "TSIM 待确认"
 return f"TSIM {fmt(tsim)} {'<' if tsim<value else '>' if tsim>value else '='} {competitor} {fmt(value)}"
def message(events):
 groups=defaultdict(list)
 for e in events:groups[(e["destination"],e["competitor"],e["old"],e["new"],e["tsim"],e["gap"],e["impact"])].append(e)
 # Values may be unknown (None); never coerce them to zero merely to sort.
 # repr gives a stable ordering across mixed numeric/None tuples.
 return [f"{dest}｜{'、'.join(sorted({x['site'] for x in rows}))}｜{vendor} {fmt(old)}→{fmt(new)}｜{comparison_text(tsim,vendor,new)}" for (dest,vendor,old,new,tsim,gap,impact),rows in sorted(groups.items(), key=lambda item: repr(item[0]))]
def chunks(lines):
 out=[];cur=[];size=0;omitted=0
 for line in lines:
  add=len(line)+(1 if cur else 0)
  if cur and size+add>3750:out.append("\n".join(cur));cur=[];size=0
  if len(out)>=2 or len(line)>3750:omitted+=1;continue
  cur.append(line);size+=len(line)+(1 if len(cur)>1 else 0)
 if cur and len(out)<2:out.append("\n".join(cur))
 return out,omitted
def write_report(output,events,details,audit,pending,raw,headers):
 wb=openpyxl.Workbook();wb.remove(wb.active);dh=["目的地","站点","竞品","竞品商品ID","TSIM商品ID","原非50折扣","新非50折扣","TSIM当前促销","变更后差距pp","影响","异动","原始旧促销标签","原始新促销标签"]
 for title,rows in (("竞品重点",events),("异动明细",details)):
  ws=wb.create_sheet(title);ws.append(dh)
  for r in rows:ws.append([r.get(k,"") for k in ("destination","site","competitor","competitor_id","tsim_id","old","new","tsim","gap","impact","kind","raw_old","raw_new")])
 ws=wb.create_sheet("待确认");ws.append(["站点","厂商","商品ID","待确认原因"])
 for k,v in sorted(pending.items()):ws.append([*k,";".join(sorted(v))])
 ws.append(["待确认去重总数","","",len(pending)])
 for site in SITES:
  ws=wb.create_sheet(site);ws.append(list(headers.get(site,()))+["目的地","质量原因","解析非50折扣"])
  for r in raw:
   if r.site==site:ws.append(list(r.raw)+[r.destination,";".join(r.reasons),"" if r.discount is None else r.discount])
 ws=wb.create_sheet("说明");ws.append(["项目","内容"])
 for k,v in (("报告日期",audit["current_date"]),("基线日期",audit["baseline_date"]),("范围","离线历史回放；HK/RU；不改变抓取或定时任务"),("50%规则","仅百分号百分比；恰为50%排除；仅50%=非50折扣0"),("消息遗漏条数",audit.get("message_omitted_count",0))):ws.append([k,v])
 for ws in wb.worksheets:
  for row in ws.iter_rows():
   for cell in row:
    if cell.value is not None and cell.value!="":cell.value=text(cell.value)
    cell.number_format="@"
  ws.freeze_panes="A2";ws.auto_filter.ref=ws.dimensions
 wb.save(output/"report.xlsx");(output/"audit.json").write_text(json.dumps(audit,ensure_ascii=False,indent=2)+"\n")
def main(argv=None):
 p=argparse.ArgumentParser();p.add_argument("--current",type=Path,required=True);p.add_argument("--previous",type=Path);p.add_argument("--target",type=Path,required=True);p.add_argument("--output-dir",type=Path,required=True);a=p.parse_args(argv)
 if any(not x.is_file() for x in (a.current,a.target)) or (a.previous and not a.previous.is_file()):p.error("input missing")
 if a.output_dir.exists() or not a.output_dir.name.endswith("promo-replay"):p.error("output-dir must be fresh and end promo-replay")
 a.output_dir.mkdir(parents=True);events,details,audit,pending,raw,headers=build(a.current,a.previous,a.target);cs,omitted=chunks(message(events));audit["message_omitted_count"]=omitted
 header=f"HK/RU竞品促销报告｜日期：{audit['current_date']}｜基线：{audit['baseline_date']}｜待确认：{audit['pending_union_count']}（"+"、".join(f"{v} {n}" for v,n in sorted(audit['pending_vendor_breakdown'].items()))+"）"
 messages=[header+("\n"+chunk if chunk else "") for chunk in cs] or [header+"\n无可确认异动；缺失基线或质量问题均列为待确认。"]
 (a.output_dir/"messages.txt").write_text("\n\n--- Telegram message ---\n\n".join(messages)+"\n");write_report(a.output_dir,events,details,audit,pending,raw,headers)
 print(json.dumps({"output":str(a.output_dir),"events":len(events),"details":len(details),"pending":len(pending),"omitted":omitted},ensure_ascii=False));return 0
if __name__=="__main__":raise SystemExit(main())
