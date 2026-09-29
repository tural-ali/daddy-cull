const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Upgrades shows each photo beside its higher-resolution copy, two pairs to a
// row on a wide screen, each photo whole and none taller than the screen; Add
// copies the better one beside the original and marks the pair added.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const photo=(id,path)=>({id,path,capturedAt:0,kind:'image',source:'archive',size:1000,status:'unreviewed',favourite:false,revision:0,alternativeCount:0});
const group=(id,accepted)=>({archive:photo(id,`/archive/2023/2023-04/2023-04-22/DSC0${id}.JPG`),day:'2023-04-22',pixels:'2048x1365',
  ...(accepted?{accepted:`/archive/2023/2023-04/2023-04-22/DSC0${id} (hi-res).jpg`,acceptedAsset:photo(id+500,`/archive/2023/2023-04/2023-04-22/DSC0${id} (hi-res).jpg`)}:{}),
  copies:[{asset:photo(id+1000,`/upgrades/Takeout/Google Photos/Trip/DSC0${id}.JPG`),pixels:'6902x4601',ratio:11.4,date:'2023:04:22 10:00:00',album:'Trip',available:true}]});
const landscape='<svg xmlns="http://www.w3.org/2000/svg" width="300" height="200"><rect width="300" height="200" fill="#6b7f95"/></svg>';

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1440,height:900}});
  const posted=[];
  await page.route('**/api/**',route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:1,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/upgrades')return route.fulfill({json:{groups:[group(1,true),group(2,false),group(3,false)],total:3,pending:2,accepted:1,bytes:20*1024**2}});
    if(url.pathname==='/api/upgrade-actions/preview'){posted.push(['preview',request.postDataJSON()]);return route.fulfill({json:{id:'plan-2',archiveAssetId:2,sourceAssetId:1002,source:'s',destination:'2023/2023-04/2023-04-22/DSC02 (hi-res).jpg',size:5*1024**2,hash:'h',state:'planned',created:''}});}
    if(url.pathname==='/api/upgrade-actions/execute'){posted.push(['execute',request.postDataJSON()]);return route.fulfill({json:{id:'plan-2',archiveAssetId:2,sourceAssetId:1002,source:'s',destination:'2023/2023-04/2023-04-22/DSC02 (hi-res).jpg',size:5*1024**2,hash:'h',state:'done',created:''}});}
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:landscape});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await page.goto(`${base}/upgrades`);
  const pairs=page.locator('section.pair');
  await pairs.nth(2).waitFor();
  await page.waitForFunction(()=>[...document.querySelectorAll('.pair img')].every(img=>img.complete&&img.naturalWidth>0));

  const boxes=await pairs.evaluateAll(list=>list.map(pair=>{const box=pair.getBoundingClientRect();return {top:box.top,left:box.left,width:box.width,height:box.height}}));
  assert.equal(boxes[0].top,boxes[1].top,'two pairs share a row on a wide screen');
  assert.ok(boxes[2].top>boxes[0].top,'the third starts the next row');
  for(const box of boxes)assert.ok(box.height<520,`a pair is compact: ${box.height}px`);
  const figure=await page.locator('.pair figure').first().evaluate(node=>{const box=node.getBoundingClientRect(),img=node.querySelector('img').getBoundingClientRect(),caption=node.querySelector('figcaption').getBoundingClientRect();return {height:box.height,content:img.height+caption.height,fit:getComputedStyle(node.querySelector('img')).objectFit}});
  assert.ok(Math.abs(figure.height-figure.content)<=1,`a photo card is as tall as its photo and caption: ${figure.height} for ${figure.content}`);
  assert.equal(figure.fit,'contain','the whole photo is shown, not a crop');
  assert.equal(await pairs.first().locator('.tag.done').innerText(),'Added');
  if(shots)await page.screenshot({path:`${shots}/upgrades.png`});

  await pairs.nth(1).getByRole('button',{name:'Add the higher-resolution copy'}).click();
  await pairs.nth(1).locator('.tag.done').waitFor();
  assert.deepEqual(posted,[['preview',{archiveAssetId:2,sourceAssetId:1002}],['execute',{id:'plan-2'}]]);
  assert.equal(await pairs.nth(1).getByRole('button',{name:'Add the higher-resolution copy'}).count(),0,'an added pair has nothing left to press');
  assert.match(await page.locator('.ysum').innerText(),/1 still to decide · 2 added so far, 25\.0 MB/);

  // On a phone the pairs stack, and each fits the screen's width once the
  // frame has settled into its phone layout.
  await page.setViewportSize({width:390,height:844});
  await page.waitForFunction(()=>document.documentElement.scrollWidth<=innerWidth,null,{timeout:3000});
  const narrow=await pairs.evaluateAll(list=>list.map(pair=>pair.getBoundingClientRect().right));
  for(const right of narrow)assert.ok(right<=390,`a pair fits a phone: right edge ${right}`);

  await browser.close();
  console.log('Upgrades lays pairs out two to a row, shows each photo whole, and adds a copy.');
})().catch(error=>{console.error(error);process.exit(1)});
