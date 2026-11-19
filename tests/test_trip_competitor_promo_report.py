from __future__ import annotations
import importlib.util,sys
from pathlib import Path
import openpyxl
SCRIPT=Path(__file__).parents[1]/"scripts/trip_competitor_promo_report.py";spec=importlib.util.spec_from_file_location("promo",SCRIPT);promo=importlib.util.module_from_spec(spec);sys.modules[spec.name]=promo;spec.loader.exec_module(promo)
def row(v,pid,name,label,note=""):return [v,pid,"u",name,"i",label,note]
def book(path,rows):
 w=openpyxl.Workbook();w.remove(w.active);h=["厂商","商品ID","URL","商品名称","商品资讯","总促销标签","抓取备注"]
 for s in ("HK","RU"):
  ws=w.create_sheet(s);ws.append(h)
  for r in rows.get(s,[]):ws.append(r)
 w.save(path);return path
def target(path):
 w=openpyxl.Workbook();ws=w.active;ws.append(["TSIM 網址","分類","目的地","BC","TGT","FR","JY","其他"]);ws.append([1,"x","Paris",2,3,4,5,6]);w.save(path);return path
def test_all_vendor_coverage_raw_and_conservative_notes(tmp_path):
 old={s:[row("TSIM 網址",1,"t","减8%")]+[row(v,i,v,"减5%") for i,v in enumerate(("BC","TGT","FR","JY","其他"),2)] for s in ("HK","RU")}
 now={s:[row("TSIM 網址",1,"","减8%")]+[row(v,i,v,"减20%","抓取失败" if v=="FR" else "") for i,v in enumerate(("BC","TGT","FR","JY","其他"),2)] for s in ("HK","RU")}
 e,d,a,p,raw,h=promo.build(book(tmp_path/'n.xlsx',now),book(tmp_path/'o.xlsx',old),target(tmp_path/'t.xlsx'))
 assert {x['competitor'] for x in d}=={"BC","TGT","JY","其他"};assert a['pending_reasons']['current_nonempty_failure_note']==2;assert a['pending_reasons']['current_missing_name']==2
 out=tmp_path/'260930-promo-replay';promo.main(['--current',str(tmp_path/'n.xlsx'),'--previous',str(tmp_path/'o.xlsx'),'--target',str(tmp_path/'t.xlsx'),'--output-dir',str(out)])
 w=openpyxl.load_workbook(out/'report.xlsx');assert w['HK'].max_row==7 and '竞品商品ID' in [c.value for c in w['异动明细'][1]];w.close()
def test_percent_duplicate_old_blank_and_direct_comparison_messages(tmp_path):
 assert promo.non50_discount("八折")[1]=='promo_percent_unparseable';assert promo.non50_discount("101%")[1]=='promo_percent_out_of_range'
 old={"HK":[row("TSIM",1,"t","减8%"),row("BC",2,"b",""),row("BC",2,"b","减5%")],"RU":[]};now={"HK":[row("TSIM",1,"t","减8%"),row("BC",2,"b","减20%")],"RU":[]}
 e,d,a,p,*_=promo.build(book(tmp_path/'n.xlsx',now),book(tmp_path/'o.xlsx',old),target(tmp_path/'t.xlsx'));assert not d and a['pending_reasons']['duplicate_baseline_key']==1
 x={"destination":"Paris","site":"HK","competitor":"BC","old":5,"new":20,"tsim":8,"gap":12,"impact":"risk"};y={**x,"site":"RU"};lines=promo.message([x,y]);assert len(lines)==1 and len(lines[0].split('｜'))==4 and lines[0]=="Paris｜HK、RU｜BC 5%→20%｜TSIM 8% < BC 20%" and '商品ID' not in lines[0] and '50%' not in lines[0] and all(word not in lines[0] for word in ('pp','领先','落后','risk'))
 y['new']=21;assert len(promo.message([x,y]))==2

def test_message_comparison_operators_and_unknown_tsim():
 base={"destination":"Paris","site":"HK","competitor":"TGT","old":5,"new":15,"gap":None,"impact":"disclaimer"}
 assert promo.message([{**base,"tsim":8}])==["Paris｜HK｜TGT 5%→15%｜TSIM 8% < TGT 15%"]
 assert promo.message([{**base,"tsim":20}])==["Paris｜HK｜TGT 5%→15%｜TSIM 20% > TGT 15%"]
 assert promo.message([{**base,"tsim":15}])==["Paris｜HK｜TGT 5%→15%｜TSIM 15% = TGT 15%"]
 unknown=promo.message([{**base,"tsim":None}])[0]
 assert unknown=="Paris｜HK｜TGT 5%→15%｜TSIM 待确认" and all(word not in unknown for word in ('pp','领先','落后','disclaimer'))

def test_message_mixed_unknown_values_is_deterministic():
 base={"destination":"Paris","site":"HK","competitor":"BC","old":5,"new":20,"impact":"risk"}
 events=[{**base,"tsim":None,"gap":None},{**base,"destination":"Rome","tsim":8,"gap":12}]
 assert promo.message(events)==promo.message(list(reversed(events)))

def test_target_uses_shared_schema_sheet_not_active_decoy(tmp_path):
 w=openpyxl.Workbook(); decoy=w.active; decoy.title="Decoy";decoy.append(["目的地","TSIM 網址","BC"]);decoy.append(["Wrong",1,2])
 ws=w.create_sheet("Target");ws.append(["TSIM 網址","分類","目的地","BC","TGT","FR","JY","其他"]);ws.append([1,"x","Paris",2,3,4,5,6]);p=tmp_path/'target.xlsx';w.save(p)
 targets,vendors=promo.load_targets(p)
 assert targets[("BC","2")][0].destination=="Paris" and "BC" in vendors

def test_empty_pending_numeric_text_joined_vendor_and_distinct_sites(tmp_path):
 # Same frozen paired identity in BC/TGT columns resolves the joined live vendor once.
 w=openpyxl.Workbook();ws=w.active;ws.append(["TSIM 網址","分類","目的地","BC","TGT","FR","JY","其他"]);ws.append([1,"x","Paris",2,2,"","",""]);t=tmp_path/'t.xlsx';w.save(t)
 old={"HK":[row("TSIM",1,"t","减8%"),row("BC/TGT",2,"b","减50%")],"RU":[]}
 now={"HK":[row("TSIM",1,"t","减8%"),row("BC/TGT",2,"b","减20%")],"RU":[]}
 e,d,a,p,raw,h=promo.build(book(tmp_path/'n.xlsx',now),book(tmp_path/'o.xlsx',old),t)
 assert d[0]['competitor']=='BC/TGT' and 'BC/TGT' in a['competitor_vendors']
 assert not p and a['pending_union_count']==0 and a['fifty_only_exclusion_counts']['previous']==1
 assert promo.fmt(0)=='无非50促销' and promo.gap_text(0)=='TSIM持平'
 assert promo.non50_discount('减50%')==(0.0,None) # 50 add/remove semantics stay zero.
 x={"destination":"Paris","site":"HK","competitor":"BC","old":5,"new":20,"tsim":8,"gap":12,"impact":"risk"}
 assert not ({**x,"site":"HK"}).get('synchronized',False)
 out=tmp_path/'260930-promo-replay';promo.main(['--current',str(tmp_path/'n.xlsx'),'--previous',str(tmp_path/'o.xlsx'),'--target',str(t),'--output-dir',str(out)])
 wb=openpyxl.load_workbook(out/'report.xlsx');assert wb['异动明细']['F2'].data_type=='s';assert wb['待确认'].max_row==2;wb.close()

def test_sync_requires_distinct_sites(tmp_path):
 old={s:[row('TSIM',1,'t','减8%'),row('BC',2,'b','减5%')] for s in ('HK','RU')}
 now={s:[row('TSIM',1,'t','减8%'),row('BC',2,'b','减20%')] for s in ('HK','RU')}
 e,d,*_=promo.build(book(tmp_path/'n.xlsx',now),book(tmp_path/'o.xlsx',old),target(tmp_path/'t.xlsx'))
 assert len(d)==2 and all(x['synchronized'] for x in d)
 assert 'HK、RU' in promo.message(d)[0]
