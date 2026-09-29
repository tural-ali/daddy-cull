const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
(async()=>{
 const browser=await chromium.launch({channel:'chrome',headless:true});const context=await browser.newContext();
 const assets=['PAIR.JPG','PAIR (2).JPG','NEXT.JPG'].map((name,i)=>({id:i+1,path:'/fixture/'+name,capturedAt:1600000000,kind:'image',source:'archive',size:100,status:'unreviewed',favourite:false,revision:0,relatedCount:i<2?1:0,alternativeCount:0}));
 const requests=[];let failReply=true;let releaseImages;const imagesReady=new Promise(r=>releaseImages=r);const receipts=new Map();
 await context.route('**/api/**',async route=>{
  const url=new URL(route.request().url());
  if(url.pathname==='/api/stats')return route.fulfill({json:{total:3,synthetic:true,candidates:0}});
  if(url.pathname==='/api/assets')return route.fulfill({json:{assets:[assets.find(a=>a.id<3&&a.status==='unreviewed'),assets[2]].filter(Boolean),next:''}});
  if(url.pathname.endsWith('/related'))return route.fulfill({json:assets.slice(0,2)});
  if(url.pathname==='/api/decisions/batch'){
   const jobs=route.request().postDataJSON();requests.push(jobs);
   const results=jobs.map(job=>{
    if(receipts.has(job.requestId))return receipts.get(job.requestId);
    const asset=assets.find(a=>a.id===job.assetId);assert.equal(job.expectedRevision,asset.revision);
    const result={revision:asset.revision+1,previousStatus:asset.status,previousFavourite:asset.favourite};
    Object.assign(asset,{status:job.status,revision:result.revision});receipts.set(job.requestId,result);return result;
   });
   return failReply?route.fulfill({status:503,body:'simulated lost acknowledgement after commit'}):route.fulfill({json:results});
  }
  if(url.pathname.startsWith('/api/media/')){await imagesReady;return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="800" height="600"/>'})}
  throw Error(url.pathname);
 });
 let page=await context.newPage();await page.goto('http://127.0.0.1:8840/queue.html',{waitUntil:'domcontentloaded'});
 await page.locator('.tile').first().click();await page.locator('.compare-grid article').nth(1).waitFor();
 assert.equal(await page.getByRole('button',{name:'Keep this file',exact:true}).first().isDisabled(),true);
 await page.keyboard.press('k');assert.equal(requests.length,0);releaseImages();
 await page.getByRole('button',{name:'Keep this file',exact:true}).first().waitFor();
 await page.getByRole('button',{name:'Keep both files',exact:true}).click();await page.getByRole('button',{name:'Save these 2 choices',exact:true}).click();
 await page.getByRole('button',{name:'Retry same save',exact:true}).waitFor();
 assert.equal(await page.evaluate(()=>Object.keys(localStorage).filter(k=>k.startsWith('cull.group.pending.')).length),1);
 await page.close();failReply=false;page=await context.newPage();await page.goto('http://127.0.0.1:8840/queue.html');
 await page.waitForFunction(()=>Object.keys(localStorage).filter(k=>k.startsWith('cull.group.pending.')).length===0);
 await page.locator('.tile').first().waitFor();assert.equal(await page.locator('.tile').count(),1);assert.match(await page.locator('.tile').innerText(),/NEXT.JPG/);
 assert.equal(requests.length,2);assert.deepEqual(requests[1],requests[0]);assert.deepEqual(assets.slice(0,2).map(a=>a.revision),[1,1]);
 await browser.close();console.log(JSON.stringify({undecodedPreviewBlocksKeyboardChoice:true,closedTabGroupJournalRecovered:true,lostAcknowledgementReusesRequestIds:true,noDoubleDecision:true,resolvedGroupNotRepeated:true,scope:'synthetic browser workflow'},null,2));
})().catch(error=>{console.error(error);process.exit(1)});
