const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
(async()=>{
 const browser=await chromium.launch({channel:'chrome',headless:true});const page=await browser.newPage();
 const assets=Array.from({length:7},(_,i)=>({id:i+1,path:`/fixture/photo-${i+1}.jpg`,capturedAt:1600000000,kind:'image',source:'archive',size:100,status:'unreviewed',favourite:false,revision:0,alternativeCount:0}));
 let fail=false;const requests=[];const receipts=new Map();
 await page.route('**/api/**',async route=>{
  const u=new URL(route.request().url());
  if(u.pathname==='/api/stats')return route.fulfill({json:{total:7,synthetic:true,candidates:0}});
  if(u.pathname==='/api/assets'){const status=u.searchParams.get('status');return route.fulfill({json:{assets:assets.filter(a=>!status||a.status===status),next:''}})}
  if(u.pathname==='/api/decisions'){
   const d=route.request().postDataJSON();requests.push(d);await new Promise(r=>setTimeout(r,200));
   if(fail)return route.fulfill({status:503,body:'offline'});
   if(receipts.has(d.requestId))return route.fulfill({json:receipts.get(d.requestId)});
   const a=assets.find(a=>a.id===d.assetId);assert.equal(d.expectedRevision,a.revision);
   const result={revision:a.revision+1,previousStatus:a.status,previousFavourite:a.favourite};Object.assign(a,{status:d.status,revision:result.revision,favourite:d.favourite});receipts.set(d.requestId,result);return route.fulfill({json:result});
  }
  return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="800" height="600"/>'});
 });
 const drained=()=>page.waitForFunction(()=>Object.keys(localStorage).filter(k=>k.startsWith('cull.pending.')).every(k=>JSON.parse(localStorage[k]).length===0));
 await page.goto(process.env.APP_URL||'http://127.0.0.1:8840/');await page.getByRole('button',{name:'Start reviewing',exact:true}).click();
 await page.waitForFunction(()=>[...document.querySelectorAll('.review-frame img')].every(i=>i.complete&&i.naturalWidth));
 for(let i=0;i<3;i++)await page.keyboard.press('k');
 await page.waitForFunction(()=>document.querySelector('.details .path').textContent.includes('photo-4'));await drained();
 assert.deepEqual(requests.map(d=>d.assetId),[1,2,3]);
 await page.reload();await page.locator('.details .path').waitFor();assert.match(await page.locator('.details .path').innerText(),/photo-4/);
 await page.waitForFunction(()=>[...document.querySelectorAll('.review-frame img')].every(i=>i.complete&&i.naturalWidth));fail=true;await page.keyboard.press('k');await page.getByRole('button',{name:'Retry pending saves'}).waitFor();const pending=await page.evaluate(()=>Object.keys(localStorage).filter(k=>k.startsWith('cull.pending.')).flatMap(k=>JSON.parse(localStorage[k])));assert.equal(pending.length,1);assert.equal(pending[0].asset.id,4);
 fail=false;await page.reload();await drained();await page.locator('.details .path').waitFor();assert.match(await page.locator('.details .path').innerText(),/photo-5/);
 const retries=requests.filter(r=>r.assetId===4);assert.ok(retries.length>=2);assert.ok(retries.every(r=>r.requestId===retries[0].requestId));
 console.log(JSON.stringify({rapidKeysSaved:[1,2,3],refreshResumesAt:4,failedSaveRetained:true,reloadRecoveredWithSameRequestId:true,afterRecoveryResumesAt:5},null,2));await browser.close();
})().catch(e=>{console.error(e);process.exit(1)});
