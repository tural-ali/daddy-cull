const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Two copies of one clip whose metadata differs are offered as copies on the
// day page and on the Duplicates page, labelled as the same footage rather
// than as identical files, each with its own size. The copy that records
// where it was taken is kept by default, even when its name is the noisier,
// and the one that does not is flagged. Every name is a synthetic fixture.
const day='2023-09-30';
const clip=(id,name,size,located)=>({id,path:`/archive/2023/2023-09/${day}/${name}`,capturedAt:Date.parse(`${day}T00:00:00Z`)/1000,kind:'video',source:'archive',size,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:1,day,located});
const plain=clip(101,'IMG_2208.MOV',113379250,false);
const second=clip(102,'IMG_2208 (2).MOV',113375301,true);
const group={hash:'footage:0f1e2d3c',proof:'footage',size:113379250,reclaimable:113375301,members:[plain,second]};
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const svg='<svg xmlns="http://www.w3.org/2000/svg" width="96" height="54"><rect width="96" height="54" fill="#4d6f94"/></svg>';

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:900}});
  let posted=null;
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/addons')return route.fulfill({json:[]});
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:2,synthetic:true,snapshotAt:'2026-09-30 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-30')return route.fulfill({json:{md:'09-30',label:'30 September',previous:'09-29',next:'10-01',years:[{day,year:2023,files:2,bytes:plain.size+second.size,status:'pending',assets:[plain,second]}],memories:2,bytes:plain.size+second.size}});
    if(url.pathname==='/api/duplicates')return route.fulfill({json:[group]});
    if(url.pathname==='/api/duplicate-report')return route.fulfill({json:{candidates:0,hashed:0,settled:true,unproven:[],groups:[group]}});
    if(url.pathname==='/api/decisions/batch'){posted=JSON.parse(route.request().postData());return route.fulfill({json:posted.map(()=>({revision:1}))})}
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:svg});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });

  await page.goto(`${base}/on/09-30`);
  const copies=page.locator('.xcopy');
  await copies.waitFor();
  assert.match((await copies.locator('.xmeta').innerText()).replace(/\s+/g,' '),/Same footage 2 copies · only the metadata differs · 108\.1 MB reclaimable/);
  const rows=copies.locator('.xtable tbody tr');
  assert.deepEqual(await rows.evaluateAll(nodes=>nodes.map(node=>[Number(node.dataset.asset),node.className])),[[101,'binned'],[102,'keeper']],'the copy that knows the place is kept');
  assert.equal(await rows.nth(0).locator('.dupeflag').innerText(),'no location');
  assert.equal(await rows.nth(1).locator('.dupeflag').count(),0);
  assert.deepEqual(await rows.locator('td.num').allInnerTexts(),['108.1 MB','108.1 MB']);
  await page.screenshot({path:process.env.SHOT||'/tmp/footage-day.png'});
  await page.setViewportSize({width:390,height:844});
  await page.waitForFunction(()=>![...document.querySelectorAll('.xdupes *')].some(node=>node.getBoundingClientRect().right>391),null,{timeout:3000}).catch(()=>{});
  assert.deepEqual(await page.evaluate(()=>[...document.querySelectorAll('.xdupes *')].filter(node=>node.getBoundingClientRect().right>391).map(node=>node.className)),[],'the group fits a phone');
  if(process.env.SHOT_PHONE)await page.locator('.xdupes').screenshot({path:process.env.SHOT_PHONE});
  await page.setViewportSize({width:1280,height:900});
  await copies.getByRole('button',{name:/Keep the selected copy/}).click();
  await page.getByText(/1 verified copy marked for the Bin/).waitFor();
  assert.deepEqual(posted.map(change=>[change.assetId,change.status]),[[101,'cull'],[102,'keep']]);

  await page.goto(`${base}/duplicates`);
  const set=page.locator('.dupegroup');
  await set.waitFor();
  assert.equal(await set.locator('.dupeproof').innerText(),'Same footage, different metadata');
  assert.match(await page.locator('.dupepage > .hint').first().innerText(),/only their metadata differs/);
  assert.equal(await set.locator('.dupetile.keeper .dupelabel > span:first-child').innerText(),'IMG_2208 (2).MOV');
  assert.deepEqual(await set.locator('.dupetile:not(.keeper) .dupeflag').allInnerTexts(),['no location']);
  assert.match(await set.getByRole('button',{name:'Merge 2 copies'}).getAttribute('title'),/because it records where it was taken and the other copy does not/);
  await page.screenshot({path:process.env.SHOT_PAGE||'/tmp/footage-page.png',fullPage:true});
  await browser.close();
  console.log('footage copies: ok');
})().catch(error=>{console.error(error);process.exit(1)});
