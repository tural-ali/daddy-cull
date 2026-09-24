const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
(async()=>{
 const browser=await chromium.launch({headless:true,channel:'chrome'});
 const page=await browser.newPage();
 const assets=Array.from({length:12},(_,i)=>({id:i+1,path:`/fixture/photo-${i+1}.jpg`,capturedAt:1600000000,kind:'image',source:'archive',size:100,status:'unreviewed',favourite:false,revision:0,alternativeCount:0}));
 const requests=[];
 await page.route('**/api/**',async route=>{
  const u=new URL(route.request().url());
  if(u.pathname==='/api/stats')return route.fulfill({json:{total:12,synthetic:true,candidates:0}});
  if(u.pathname==='/api/assets')return route.fulfill({json:{assets,next:''}});
  if(u.pathname==='/api/decisions'){requests.push(route.request().postDataJSON());await new Promise(r=>setTimeout(r,600));return route.fulfill({json:{revision:1,previousStatus:'unreviewed',previousFavourite:false}})}
  if(u.pathname.startsWith('/api/media/')){await new Promise(r=>setTimeout(r,300));return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="800" height="600"><rect width="800" height="600" fill="#526c77"/></svg>'})}
  return route.abort();
 });
 await page.goto(process.env.APP_URL||'http://127.0.0.1:8840/queue.html');
 await page.getByRole('button',{name:'Start reviewing',exact:true}).click();
 await page.waitForTimeout(1200);
 await page.evaluate(()=>{
  window.__start=0;window.__next=0;window.__ready=0;
  window.addEventListener('keydown',()=>{if(!window.__start)window.__start=performance.now()},{capture:true,once:true});
  function frame(){if(document.querySelector('.details .path')?.textContent.includes('photo-2')){
   if(!window.__next)window.__next=performance.now();
   const img=document.querySelector('.review-frame:not([hidden]) img, .review .stage > img');if(img?.complete&&img.naturalWidth){window.__ready=performance.now();return}
  }requestAnimationFrame(frame)}requestAnimationFrame(frame);
 });
 await page.keyboard.press('k');await page.waitForFunction(()=>window.__ready>0);
 const result=await page.evaluate(()=>({advanceMs:window.__next-window.__start,imageReadyMs:window.__ready-window.__start}));
 result.conditions={saveDelayMs:600,previewDelayMs:300,fixtureAssets:12};
 if(process.env.EXPECT_FAST){if(result.advanceMs>150||result.imageReadyMs>150)throw Error(JSON.stringify(result));}
 console.log(JSON.stringify(result,null,2));
 await browser.close();
})().catch(e=>{console.error(e);process.exit(1)});
