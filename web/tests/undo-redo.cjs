const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// ⌘Z undoes the last choice on a day, ⌘U (or ⇧⌘Z) does it again, in the
// viewer and in the grid, and the history holds the last hundred actions.
const photo=(id,name)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07'});
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const pick=({assetId,status,favourite})=>({assetId,status,favourite});

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.clock.setFixedTime(new Date('2026-09-07T10:00:00'));
  const writes=[];
  const progress=[],reviews=[],batches=[];
  await page.route('**/api/**',route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:3,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:3,bytes:300,status:'pending',assets:[photo(1,'ONE.JPG'),photo(2,'TWO.JPG'),photo(3,'THREE.JPG')]}],memories:3,bytes:300}});
    if(url.pathname==='/api/decisions'){const body=request.postDataJSON();writes.push(body);return route.fulfill({json:{revision:body.expectedRevision+1,previousStatus:'unreviewed',previousFavourite:false}})}
    if(url.pathname==='/api/day-progress'){progress.push(request.postDataJSON());return route.fulfill({json:{}})}
    if(url.pathname==='/api/dates/09-07/reviewed'){reviews.push(request.postDataJSON());return route.fulfill({json:{days:['2010-09-07'],kept:[1,2,3].map(id=>({id,revision:50+reviews.length})),tally:{total:3,removed:0,bytes:0,kept:3,favourites:0}}})}
    if(url.pathname==='/api/decisions/batch'){const body=request.postDataJSON();batches.push(body);return route.fulfill({json:body.map(change=>({revision:change.expectedRevision+1,previousStatus:'keep',previousFavourite:false}))})}
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const viewer=page.getByRole('dialog',{name:'Photo review'});
  const flash=page.locator('.snack');
  const settled=async n=>{for(let i=0;i<100&&writes.length<n;i++)await page.waitForTimeout(50);assert.equal(writes.length,n,'decision count');return writes[n-1]};

  await page.goto(`${base}/on/09-07/photo/1`);
  await viewer.waitFor();
  await page.keyboard.press('k');
  await page.waitForURL(/\/photo\/2$/);
  await page.keyboard.press('x');
  await page.waitForURL(/\/photo\/3$/);
  assert.deepEqual((await settled(2)).status,'cull');

  await page.keyboard.press('Meta+z');
  assert.deepEqual(pick(await settled(3)),{assetId:2,status:'unreviewed',favourite:false},'⌘Z puts the removed photo back');
  await assert.doesNotReject(flash.filter({hasText:'Undone: removed TWO.JPG.'}).waitFor());
  await page.keyboard.press('Meta+z');
  assert.deepEqual(pick(await settled(4)),{assetId:1,status:'unreviewed',favourite:false},'a second ⌘Z undoes the keep before it');
  await assert.doesNotReject(flash.filter({hasText:'Undone: kept ONE.JPG.'}).waitFor());
  assert.match(page.url(),/\/photo\/3$/,'undo does not move the viewer');

  await page.keyboard.press('Meta+u');
  assert.deepEqual(pick(await settled(5)),{assetId:1,status:'keep',favourite:false},'⌘U redoes the keep');
  await assert.doesNotReject(flash.filter({hasText:'Redone: kept ONE.JPG.'}).waitFor());
  await page.keyboard.press('Shift+Meta+z');
  assert.deepEqual(pick(await settled(6)),{assetId:2,status:'cull',favourite:false},'⇧⌘Z redoes as well');
  await page.keyboard.press('Meta+u');
  await assert.doesNotReject(flash.filter({hasText:'Nothing to redo.'}).waitFor());
  assert.equal(writes.length,6);

  // A new choice after an undo drops what could have been redone.
  await page.keyboard.press('Meta+z');
  await settled(7);
  await page.keyboard.press('f');
  assert.deepEqual(pick(await settled(8)),{assetId:3,status:'unreviewed',favourite:true});
  await page.keyboard.press('Meta+u');
  await assert.doesNotReject(flash.filter({hasText:'Nothing to redo.'}).waitFor());
  assert.equal(writes.length,8,'nothing was redone after a new choice');
  await page.keyboard.press('Meta+z');
  assert.deepEqual(pick(await settled(9)),{assetId:3,status:'unreviewed',favourite:false},'the new choice undoes');

  // In the grid, with the viewer closed, the same keys work on the tiles.
  await page.keyboard.press('Escape');
  await viewer.waitFor({state:'hidden'});
  await page.locator('.gal figure').nth(1).focus();
  await page.keyboard.press('x');
  assert.deepEqual(pick(await settled(10)),{assetId:2,status:'cull',favourite:false},'the tile removes the photo');
  await page.keyboard.press('Meta+z');
  assert.deepEqual(pick(await settled(11)),{assetId:2,status:'unreviewed',favourite:false},'⌘Z in the grid brings it back');
  await page.locator('.gal figure').nth(1).locator('.undo').waitFor({state:'hidden'});
  // Marking the date reviewed keeps every undecided photo in one step; ⌘Z
  // unmarks the year and takes the keeps back, ⌘U does both again.
  const seen=()=>page.locator('.gal figure.seen').count();
  await page.getByRole('button',{name:'Mark 7 September reviewed'}).click();
  await page.locator('.yr.settled').waitFor();
  assert.equal(reviews.length,1);
  assert.equal(await seen(),3,'every undecided photo is kept');
  await page.keyboard.press('Escape');
  await page.keyboard.press('Meta+z');
  await page.locator('.yr:not(.settled)').waitFor();
  assert.deepEqual(progress.map(p=>[p.day,p.status]),[['2010-09-07','pending']],'⌘Z unmarks the year');
  await page.waitForFunction(()=>document.querySelectorAll('.gal figure.seen').length===0);
  assert.deepEqual(batches[0].map(change=>[change.assetId,change.status,change.expectedRevision]),[[1,'unreviewed',51],[2,'unreviewed',51],[3,'unreviewed',51]],'and takes the keeps back');
  await page.keyboard.press('Meta+u');
  await page.locator('.yr.settled').waitFor();
  assert.equal(reviews.length,2,'⌘U marks it again');
  assert.equal(await seen(),3);

  // The history is a hundred actions deep: after 101 hearts on one photo, a
  // hundred undos take it back to the state after the first, and no further.
  await page.reload();
  writes.length=0;
  await page.locator('.gal figure').nth(2).click();
  await viewer.waitFor();
  for(let i=1;i<=101;i++){await page.keyboard.press('f');if(i%10===0)await settled(i)}
  await settled(101);
  for(let i=1;i<=100;i++){await page.keyboard.press('Meta+z');if(i%10===0)await settled(101+i)}
  await settled(201);
  assert.deepEqual(pick(writes[200]),{assetId:3,status:'unreviewed',favourite:true},'the hundredth undo lands on the state after the first heart');
  await page.keyboard.press('Meta+z');
  await assert.doesNotReject(flash.filter({hasText:'Nothing to undo.'}).waitFor());
  assert.equal(writes.length,201,'the hundred-and-first action is beyond the history');
  await browser.close();
  console.log('undo redo: ok');
})().catch(error=>{console.error(error);process.exit(1)});
