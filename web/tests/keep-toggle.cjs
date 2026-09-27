const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// K and the Keep button undo a keep; removing a photo withdraws its keep and
// its heart, and a heart on a removed photo brings it back.
const photo=(id,name)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07'});
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.clock.setFixedTime(new Date('2026-09-07T10:00:00'));
  const writes=[];
  await page.route('**/api/**',route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:2,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:2,bytes:200,status:'pending',assets:[photo(1,'ONE.JPG'),photo(2,'TWO.JPG')]}],memories:2,bytes:200}});
    if(url.pathname==='/api/decisions'){const body=request.postDataJSON();writes.push(body);return route.fulfill({json:{revision:body.expectedRevision+1,previousStatus:'unreviewed',previousFavourite:false}})}
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const viewer=page.getByRole('dialog',{name:'Photo review'});
  const keep=viewer.locator('.rvact.keep');
  const heart=viewer.locator('.rvact.fav');
  const settled=async n=>{for(let i=0;i<50&&writes.length<n;i++)await page.waitForTimeout(50);assert.equal(writes.length,n,'decision count');return writes[n-1]};

  await page.goto(`${base}/on/09-07/photo/1`);
  await viewer.waitFor();
  await page.keyboard.press('k');
  assert.deepEqual((({status,favourite})=>({status,favourite}))(await settled(1)),{status:'keep',favourite:false});
  await page.waitForURL(/\/photo\/2$/);
  await page.keyboard.press('ArrowLeft');
  await page.waitForURL(/\/photo\/1$/);
  assert.equal(await keep.getAttribute('aria-pressed'),'true');
  await page.keyboard.press('k');
  assert.equal((await settled(2)).status,'unreviewed','K on a kept photo undoes the keep');
  assert.match(page.url(),/\/photo\/1$/,'undoing stays on the photo');
  assert.equal(await keep.getAttribute('aria-pressed'),'false');

  await keep.click();
  assert.equal((await settled(3)).status,'keep');
  await page.keyboard.press('ArrowLeft');
  await page.waitForURL(/\/photo\/1$/);
  await keep.click();
  assert.equal((await settled(4)).status,'unreviewed','the Keep button undoes a keep too');

  await page.keyboard.press('f');
  assert.deepEqual((({status,favourite})=>({status,favourite}))(await settled(5)),{status:'unreviewed',favourite:true});
  await page.keyboard.press('k');
  await settled(6);
  await page.keyboard.press('ArrowLeft');
  await page.waitForURL(/\/photo\/1$/);
  await page.keyboard.press('x');
  assert.deepEqual((({status,favourite})=>({status,favourite}))(await settled(7)),{status:'cull',favourite:false},'removing withdraws the keep and the heart');
  await page.keyboard.press('ArrowLeft');
  await page.waitForURL(/\/photo\/1$/);
  assert.equal(await heart.getAttribute('aria-pressed'),'false');
  assert.equal(await keep.getAttribute('aria-pressed'),'false');

  await page.keyboard.press('f');
  assert.deepEqual((({status,favourite})=>({status,favourite}))(await settled(8)),{status:'unreviewed',favourite:true},'a heart on a removed photo brings it back');
  // Each choice builds on the revision the previous one saved, even when it
  // was made before the server had confirmed that previous choice.
  assert.deepEqual(writes.map(write=>[write.assetId,write.expectedRevision]),[0,1,2,3,4,5,6,7].map(revision=>[1,revision]));
  await browser.close();
  console.log('keep toggle: ok');
})().catch(error=>{console.error(error);process.exit(1)});
