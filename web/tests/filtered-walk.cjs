const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// With a filter on, the viewer walks the files the filter shows. Removing one
// moves on to the next, and once the viewer has moved off it the removed file
// is gone from the walk, as it is from the grid. A file the filter hid, such as
// one removed earlier, never comes up.
const photo=(id,status='unreviewed')=>({id,path:`/archive/2010/2010-09/2010-09-07/IMG_${id}.JPG`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,status,favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07'});
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  let revision=0;
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:6,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:2,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:6,bytes:600,status:'pending',assets:[photo(1),photo(2,'cull'),photo(3),photo(4,'keep'),photo(5),photo(6)]}],memories:6,bytes:600}});
    if(url.pathname==='/api/decisions')return route.fulfill({json:{revision:++revision,previousStatus:'unreviewed',previousFavourite:false}});
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await page.addInitScript(()=>sessionStorage.setItem('cull.day-filters','["undecided"]'));
  await page.goto(`${base}/on/09-07`);
  await page.locator('.gal figure').first().waitFor();
  const grid=()=>page.locator('.gal figure').evaluateAll(nodes=>nodes.map(node=>Number(node.dataset.asset)));
  assert.deepEqual(await grid(),[1,3,5,6]);
  await page.locator('[data-asset="1"]').click();
  const viewer=page.getByRole('dialog',{name:'Photo review'});
  await viewer.waitFor();
  const showing=()=>viewer.locator('.rvstage img').getAttribute('alt');
  const press=async key=>{await page.keyboard.press(key);await page.waitForTimeout(150)};
  assert.equal(await showing(),'IMG_1.JPG');
  await press('x');
  assert.equal(await showing(),'IMG_3.JPG','removing moves on to the next undecided file');
  await press('ArrowRight');
  assert.equal(await showing(),'IMG_5.JPG');
  await press('ArrowLeft');
  assert.equal(await showing(),'IMG_3.JPG');
  await press('ArrowLeft');
  assert.equal(await showing(),'IMG_6.JPG','the file removed in this visit has left the walk');
  await press('k');
  assert.equal(await showing(),'IMG_3.JPG','keeping moves on, wrapping within the filtered files');
  await press('ArrowLeft');
  assert.equal(await showing(),'IMG_5.JPG','the file kept in this visit has left the walk too');
  await press('ArrowRight');
  await press('ArrowRight');
  assert.equal(await showing(),'IMG_5.JPG','only undecided files are left to walk');
  await page.keyboard.press('Escape');
  await viewer.waitFor({state:'hidden'});
  assert.deepEqual(await grid(),[3,5],'the grid shows what is still undecided');
  await browser.close();
  console.log('filtered walk: ok');
})().catch(error=>{console.error(error);process.exit(1)});
