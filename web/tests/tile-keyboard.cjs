const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// A tile takes focus from the keyboard, and Enter or Space opens it, as a click does.
const photo=(id,name)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07'});
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.clock.setFixedTime(new Date('2026-09-07T10:00:00'));
  await page.route('**/api/**',route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:2,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:2,bytes:200,status:'pending',assets:[photo(1,'ONE.JPG'),photo(2,'TWO.JPG')]}],memories:2,bytes:200}});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const viewer=page.getByRole('dialog',{name:'Photo review'});
  await page.goto(`${base}/on/09-07`);
  const tiles=page.locator('figure.mo');
  await tiles.first().waitFor();
  for(const [index,key] of [[0,'Enter'],[1,' ']]){
    await tiles.nth(index).focus();
    await page.keyboard.press(key==='Enter'?'Enter':'Space');
    await viewer.waitFor();
    assert.match(page.url(),new RegExp(`/photo/${index+1}$`),`${key==='Enter'?'Enter':'Space'} opens the focused tile`);
    await page.keyboard.press('Escape');
    await viewer.waitFor({state:'hidden'});
  }
  // A key pressed on a button inside the tile is that button's, not the tile's.
  await tiles.first().locator('button').first().focus();
  await page.keyboard.press('Enter');
  await page.waitForTimeout(300);
  assert.doesNotMatch(page.url(),/\/photo\//,'Enter on a tile button does not open the photo');
  await browser.close();
  console.log('tile keyboard: ok');
})().catch(error=>{console.error(error);process.exit(1)});
