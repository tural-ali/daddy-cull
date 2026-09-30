const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Upgrades lets you choose, as Duplicates does, which of a photo and the
// higher-resolution copy added beside it stays: each pair shows both files
// with their pixels and sizes, the one with more pixels ticked, and Merge
// marks the other for the Bin. A pair already settled is not shown, a copy
// the catalogue has not indexed yet is listed, and a photo whose copy is not
// added yet still offers Add. Every name is a synthetic fixture.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const dir='/archive/2023/2023-04/2023-04-22';
const photo=(id,path,extra={})=>({id,path,capturedAt:0,kind:'image',source:'archive',size:1000,status:'unreviewed',favourite:false,revision:3,alternativeCount:0,relatedCount:0,...extra});
const copy=id=>({asset:photo(id+1000,`/upgrades/Takeout/Google Photos/Trip/DSC0${id}.JPG`,{source:'takeout',size:5*1024**2}),pixels:'6902x4601',ratio:11.4,date:'2023:04:22 10:00:00',album:'Trip',available:true});
const group=(id,{added,catalogued=added,original={},hires={}}={})=>({archive:photo(id,`${dir}/DSC0${id}.JPG`,original),day:'2023-04-22',pixels:'2048x1365',
  ...(added?{accepted:`${dir}/DSC0${id} (hi-res).jpg`,acceptedFrom:copy(id).asset.path}:{}),
  ...(catalogued?{acceptedAsset:photo(id+500,`${dir}/DSC0${id} (hi-res).jpg`,{size:5*1024**2,...hires})}:{}),
  copies:[copy(id),{...copy(id),asset:{...copy(id).asset,id:id+2000,path:`/upgrades/Takeout/Google Photos/Photos from 2023/DSC0${id}.JPG`},album:'Photos from 2023'}]});
const landscape='<svg xmlns="http://www.w3.org/2000/svg" width="300" height="200"><rect width="300" height="200" fill="#6b7f95"/></svg>';

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1440,height:900}});
  const posted=[];
  await page.route('**/api/**',route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.pathname==='/api/addons')return route.fulfill({json:[]});
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:1,synthetic:true,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/upgrades')return route.fulfill({json:{groups:[
      group(1,{added:true,original:{favourite:true}}),
      group(4,{added:true,original:{status:'cull'}}),
      group(5,{added:true,catalogued:false}),
      group(2),group(3)],total:5,pending:2,accepted:3,bytes:15*1024**2}});
    if(url.pathname==='/api/decisions/batch'){const body=request.postDataJSON();posted.push(['decide',body]);return route.fulfill({json:body.map(()=>({revision:4}))})}
    if(url.pathname==='/api/upgrade-actions/preview'){posted.push(['preview',request.postDataJSON()]);return route.fulfill({json:{id:'plan-2',archiveAssetId:2,sourceAssetId:1002,source:'s',destination:'2023/2023-04/2023-04-22/DSC02 (hi-res).jpg',size:5*1024**2,hash:'h',state:'planned',created:''}});}
    if(url.pathname==='/api/upgrade-actions/execute'){posted.push(['execute',request.postDataJSON()]);return route.fulfill({json:{id:'plan-2',archiveAssetId:2,sourceAssetId:1002,source:'s',destination:'2023/2023-04/2023-04-22/DSC02 (hi-res).jpg',size:5*1024**2,hash:'h',state:'accepted',created:''}});}
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:landscape});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await page.goto(`${base}/upgrades`);
  const sets=page.locator('.dupegroup');
  await sets.first().waitFor();
  await page.waitForFunction(()=>[...document.querySelectorAll('img')].every(img=>img.complete&&img.naturalWidth>0));

  // The one open pair; the settled one is counted, not shown.
  assert.equal(await sets.count(),1,'only the unsettled pair is offered');
  assert.equal((await page.locator('.ysum').innerText()).replace(/\s+/g,' '),'1 pair to settle · 1.0 KB can be freed · 3 copies added so far, 15.0 MB · 1 settled');
  const set=sets.first();
  assert.equal(await set.locator('h2').innerText(),'22 April 2023');
  assert.equal(await set.locator('.dupeproof').innerText(),'11.4× the pixels');
  const tiles=set.locator('.dupetile');
  assert.deepEqual(await tiles.locator('.dupelabel > span:first-child').allInnerTexts(),['DSC01.JPG','DSC01 (hi-res).jpg']);
  assert.deepEqual(await tiles.locator('.dupesize').allInnerTexts(),['2048×1365 · 1.0 KB','6902×4601 · 5.0 MB']);
  assert.deepEqual(await tiles.evaluateAll(list=>list.map(tile=>tile.classList.contains('keeper'))),[false,true],'the copy with more pixels is kept by default');
  assert.match(await set.getByRole('button',{name:'Merge 2 copies'}).getAttribute('title'),/Keeps DSC01 \(hi-res\)\.jpg because it has 11\.4× the pixels of the original, and marks DSC01\.JPG for the Bin/);

  // The rule turns every pair round; a click picks for one pair.
  await page.locator('.dupekeep select').selectOption('original');
  assert.deepEqual(await tiles.evaluateAll(list=>list.map(tile=>tile.classList.contains('keeper'))),[true,false]);
  assert.match(await page.locator('.dupebulk').innerText(),/1 file for the Bin, 5\.0 MB/);
  await page.locator('.dupekeep select').selectOption('pixels');
  await tiles.nth(0).getByRole('button',{name:/Keep DSC01\.JPG instead/}).click();
  assert.deepEqual(await tiles.evaluateAll(list=>list.map(tile=>tile.classList.contains('keeper'))),[true,false]);
  assert.match(await set.getByRole('button',{name:'Merge 2 copies'}).getAttribute('title'),/because you picked it/);

  // The not-yet-catalogued copy is listed; the photos without one offer Add.
  assert.match(await page.locator('ul.plain').innerText(),/DSC05 \(hi-res\)\.jpg · beside DSC05\.JPG/);
  const pending=page.locator('section.pair');
  assert.equal(await pending.count(),2);
  const boxes=await pending.evaluateAll(list=>list.map(pair=>pair.getBoundingClientRect().top));
  assert.equal(boxes[0],boxes[1],'two photos to add share a row on a wide screen');
  if(shots)await page.screenshot({path:`${shots}/upgrades.png`,fullPage:true});

  // Merging keeps the ticked file, carries the favourite to it and marks the
  // other for the Bin; the settled pair leaves the page.
  await set.getByRole('button',{name:'Merge 2 copies'}).click();
  await page.getByText(/1 copy marked for the Bin, 5\.0 MB in all/).waitFor();
  assert.deepEqual(posted[0][1].map(change=>[change.assetId,change.status,change.favourite,change.expectedRevision]),[[1,'keep',true,3],[501,'cull',false,3]]);
  assert.equal(await sets.count(),0);
  assert.match(await page.locator('.note.ok').innerText(),/Every upgraded photo is settled/);
  assert.match((await page.locator('.ysum').innerText()).replace(/\s+/g,' '),/^0 pairs to settle · 0\.0 KB can be freed · 3 copies added so far, 15\.0 MB · 2 settled$/);

  // Adding a copy moves the photo to the ones waiting for the catalogue.
  await pending.first().getByRole('button',{name:'Add the higher-resolution copy'}).click();
  await page.getByText(/Added DSC02 \(hi-res\)\.jpg beside the archive copy/).waitFor();
  assert.deepEqual(posted.slice(1),[['preview',{archiveAssetId:2,sourceAssetId:1002}],['execute',{id:'plan-2'}]]);
  assert.equal(await pending.count(),1);
  assert.match(await page.locator('ul.plain').innerText(),/DSC02 \(hi-res\)\.jpg · beside DSC02\.JPG/);
  assert.match((await page.locator('.ysum').innerText()).replace(/\s+/g,' '),/4 copies added so far, 20\.0 MB/);

  // On a phone everything fits the screen's width once the frame has settled.
  await page.setViewportSize({width:390,height:844});
  await page.waitForFunction(()=>document.documentElement.scrollWidth<=innerWidth,null,{timeout:3000}).catch(()=>{});
  assert.deepEqual(await page.evaluate(()=>[...document.querySelectorAll('.dupepage *')].filter(node=>node.getBoundingClientRect().right>391).map(node=>node.className)),[],'the page fits a phone');

  await browser.close();
  console.log('Upgrades offers each pair to keep one of, and adds a copy.');
})().catch(error=>{console.error(error);process.exit(1)});
