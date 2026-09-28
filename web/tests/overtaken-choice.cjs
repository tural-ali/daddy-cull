const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Two quick choices on one photo are saved in order. When the first is
// confirmed while the second still waits, the photo keeps showing the second:
// it does not flip back to the older choice, and ⌘Z then undoes the second.
const photo=(id,name)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07'});
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  const writes=[],held=[];
  await page.route('**/api/**',route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:1,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:1,bytes:100,status:'pending',assets:[photo(1,'ONE.JPG')]}],memories:1,bytes:100}});
    if(url.pathname==='/api/decisions'){
      const body=request.postDataJSON();writes.push(body);
      const reply=()=>route.fulfill({json:{revision:body.expectedRevision+1,previousStatus:'unreviewed',previousFavourite:false}});
      // The first two saves are held until the test lets them through.
      if(writes.length<=2)held.push(reply);else return reply();
      return;
    }
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"/>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const heart=page.locator('.gal figure').first();
  const favourite=()=>heart.evaluate(node=>node.classList.contains('fav'));

  await page.goto(`${base}/on/09-07`);
  await heart.waitFor();
  await heart.click();
  const viewer=page.getByRole('dialog',{name:'Photo review'});
  await viewer.waitFor();
  await page.keyboard.press('f');
  await page.keyboard.press('f');
  while(held.length===0)await page.waitForTimeout(20);
  // Only the first save has gone; the second waits behind it.
  assert.equal(writes.length,1);
  await held[0]();
  while(held.length<2)await page.waitForTimeout(20);
  assert.deepEqual([writes[0].favourite,writes[1].favourite],[true,false]);
  // The first is confirmed, the second not yet: the photo still shows it.
  await page.waitForTimeout(200);
  assert.equal(await favourite(),false,'the photo does not flip back to the older choice');
  await held[1]();
  await page.keyboard.press('Escape');
  await viewer.waitFor({state:'hidden'});
  assert.equal(await favourite(),false,'the photo shows the later choice');

  // ⌘Z undoes the later choice, not the one confirmed first.
  await page.keyboard.press('Meta+z');
  for(let i=0;i<100&&writes.length<3;i++)await page.waitForTimeout(20);
  assert.equal(writes[2]?.favourite,true,'⌘Z undoes the unfavourite');
  await page.waitForFunction(()=>document.querySelector('.gal figure')?.classList.contains('fav'));
  await browser.close();
  console.log('overtaken choice: ok');
})().catch(error=>{console.error(error);process.exit(1)});
