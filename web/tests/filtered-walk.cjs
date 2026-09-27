const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// With a filter on, the viewer walks the files the grid showed when it opened.
// Removing one moves on to the next of those, never to a file the filter hid,
// such as one removed earlier.
const photo=(id,status='unreviewed')=>({id,path:`/archive/2010/2010-09/2010-09-07/IMG_${id}.JPG`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,status,favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07'});
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  let revision=0;
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:5,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:2,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:5,bytes:500,status:'pending',assets:[photo(1),photo(2,'cull'),photo(3),photo(4,'keep'),photo(5)]}],memories:5,bytes:500}});
    if(url.pathname==='/api/decisions')return route.fulfill({json:{revision:++revision,previousStatus:'unreviewed',previousFavourite:false}});
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await page.addInitScript(()=>sessionStorage.setItem('cull.day-filters','["undecided"]'));
  await page.goto(`${base}/on/09-07`);
  await page.locator('.gal figure').first().waitFor();
  assert.deepEqual(await page.locator('.gal figure').evaluateAll(nodes=>nodes.map(node=>Number(node.dataset.asset))),[1,3,5]);
  await page.locator('[data-asset="1"]').click();
  const viewer=page.getByRole('dialog',{name:'Photo review'});
  await viewer.waitFor();
  const showing=()=>viewer.locator('.rvstage img').getAttribute('alt');
  assert.equal(await showing(),'IMG_1.JPG');
  await page.keyboard.press('x');
  await page.waitForTimeout(200);
  assert.equal(await showing(),'IMG_3.JPG','removing moves on to the next undecided file');
  await page.keyboard.press('k');
  await page.waitForTimeout(200);
  assert.equal(await showing(),'IMG_5.JPG','keeping moves on too');
  await page.keyboard.press('ArrowRight');
  assert.equal(await showing(),'IMG_1.JPG','the walk wraps within the filtered files');
  await page.keyboard.press('ArrowLeft');
  await page.keyboard.press('ArrowLeft');
  assert.equal(await showing(),'IMG_3.JPG','a file decided in this visit stays in the walk');
  await page.keyboard.press('Escape');
  await viewer.waitFor({state:'hidden'});
  assert.deepEqual(await page.locator('.gal figure').evaluateAll(nodes=>nodes.map(node=>Number(node.dataset.asset))),[5],'the grid shows what is still undecided');
  await browser.close();
  console.log('filtered walk: ok');
})().catch(error=>{console.error(error);process.exit(1)});
