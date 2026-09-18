import fs from 'fs';
const resultPath='/Users/example/Documents/trip-sku-price-scrape/260831-rating-audit/main_product_stars.json';
let rows=JSON.parse(fs.readFileSync(resultPath,'utf8'));
const failed=rows.filter(x=>x.error);
await useOrCreateTaskSpace('trip-rating-recovery');
await gotoUrl('https://hk.trip.com/webapp/tour_comment/comment_list?channel=ttd-act&queryid=100454071&scene=PRODUCT_QUERY&origin=trip');
await new Promise(r=>setTimeout(r,2000));
for(let i=0;i<failed.length;i++){
 const item=failed[i],pid=Number(item.product_id);
 try{
  const r=await js(`(async()=>{const pid=${pid}; const nap=(ms)=>new Promise(r=>setTimeout(r,ms)); const get=async(p)=>{const b={ucpBizId:6,scene:'PRODUCT_QUERY',queryId:pid,paging:{pageNo:p,pageSize:50},extRequest:{sourceType:'all'},tagTerms:[]};const x=await fetch('/restapi/soa2/20047/listComments',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(b)});const t=await x.text();let d;try{d=JSON.parse(t)}catch(e){throw new Error('non_json status='+x.status+' '+t.slice(0,120))};if(d?.ret?.code!==0)throw new Error('api='+JSON.stringify(d?.ret));return d}; const first=await get(1); const total=Number(first.totalCount||0); const pages=Math.ceil(total/50); const seen=new Set(),c={1:0,2:0,3:0,4:0,5:0},invalid=[]; const ingest=(xs)=>{for(const x of xs||[]){const id=String(x.commentId||'');if(!id||seen.has(id))continue;seen.add(id);const s=Number(x.score);if(Object.hasOwn(c,s))c[s]++;else invalid.push({id,score:x.score})}}; ingest(first.comments);for(let p=2;p<=pages;p++){await nap(1000);ingest((await get(p)).comments)};const counted=Object.values(c).reduce((a,b)=>a+b,0);return {page_score:String(first.commentAggregation?.scoreAvg??''),review_total:String(total),five_star:String(c[5]),four_star:String(c[4]),three_star:String(c[3]),two_star:String(c[2]),one_star:String(c[1]),counted_reviews:String(counted),unclassified_reviews:String(invalid.length),pages:String(pages),stars_ok:counted+invalid.length===total}})()`);
  const clean={...item,...r}; delete clean.error;
  rows=rows.map(x=>x.product_id===item.product_id?clean:x);
  cliLog(`${i+1}/${failed.length} ${item.product_id} OK ${r.counted_reviews}/${r.review_total}`);
 }catch(e){ cliLog(`${i+1}/${failed.length} ${item.product_id} ERR ${e.message}`); }
 fs.writeFileSync(resultPath,JSON.stringify(rows.sort((a,b)=>String(a.product_id).localeCompare(String(b.product_id))),null,2));
 await new Promise(r=>setTimeout(r,3000));
}
cliLog('DONE');
