const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// The camera's own file is kept over every other copy of its footage: over
// one HandBrake encoded again, even one that records the place, and over an
// export whose metadata was rewritten, even one whose sidecars name a person,
// as the Bin copies those beside the file kept. Every name is a synthetic
// fixture.
const day='2022-07-03';
const clip=(id,name,size,extra={})=>({id,path:`/archive/2022/2022-07/${day}/${name}`,capturedAt:Date.parse(`${day}T00:00:00Z`)/1000,kind:'video',source:'archive',size,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:2,day,located:true,converted:false,retagged:false,...extra});
const none={files:0,people:0,keywords:0,rating:0,captioned:false};
const camera=clip(201,'IMG_9055.MOV',305046684,{sidecars:none});
const export_=clip(202,'IMG_9055 (2022-07-03).MOV',305050689,{retagged:true,sidecars:{...none,files:2,people:1}});
const second=clip(203,'IMG_9055 (2).MOV',305046684,{sidecars:none});
const encoded=clip(211,'IMG_9056.mp4',20000000,{converted:true,sidecars:none});
const original=clip(212,'IMG_9056 (1).mp4',20000100,{located:false,sidecars:none});
const groups=[
  {hash:'footage:aa',proof:'footage',size:camera.size+4005,reclaimable:2*camera.size,members:[second,export_,camera]},
  {hash:'footage:bb',proof:'footage',size:original.size,reclaimable:encoded.size,members:[encoded,original]},
];
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const svg='<svg xmlns="http://www.w3.org/2000/svg" width="96" height="54"><rect width="96" height="54" fill="#4d6f94"/></svg>';

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:900}});
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/addons')return route.fulfill({json:[]});
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:5,synthetic:true,snapshotAt:'2026-10-01 01:00:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0,hiddenGuides:['dupes']}});
    if(url.pathname==='/api/duplicate-report')return route.fulfill({json:{candidates:0,hashed:0,settled:true,unproven:[],groups}});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:svg});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await page.goto(`${base}/duplicates`);
  const sets=page.locator('.dupegroup');
  await sets.nth(1).waitFor();
  const kept=set=>set.locator('.dupetile.keeper .dupelabel > span:first-child').innerText();
  const flags=async set=>Object.fromEntries(await set.locator('.dupetile').evaluateAll(tiles=>tiles.map(tile=>[tile.querySelector('.dupelabel > span').textContent,[...tile.querySelectorAll('.dupeflag')].map(flag=>flag.textContent)])));

  const exported=sets.filter({hasText:'IMG_9055.MOV'});
  assert.equal(await kept(exported),'IMG_9055.MOV','the camera file is kept over the tagged export');
  assert.deepEqual(await flags(exported),{'IMG_9055 (2).MOV':[],'IMG_9055 (2022-07-03).MOV':['metadata rewritten'],'IMG_9055.MOV':[]});
  assert.match(await exported.getByRole('button',{name:'Merge 3 copies'}).getAttribute('title'),/because another copy had its metadata rewritten, as an export does, and this is the camera's own file; the Bin copies its sidecars beside it/);

  const converted=sets.filter({hasText:'IMG_9056.mp4'});
  assert.equal(await kept(converted),'IMG_9056 (1).mp4','the original is kept over the HandBrake copy, place or not');
  assert.deepEqual(await flags(converted),{'IMG_9056.mp4':['HandBrake'],'IMG_9056 (1).mp4':['no location']});
  assert.match(await converted.getByRole('button',{name:'Merge 2 copies'}).getAttribute('title'),/because the other copy was encoded again by HandBrake, and this one was not/);
  if(process.env.SHOTS_DIR)await page.screenshot({path:`${process.env.SHOTS_DIR}/keeper-origin.png`,fullPage:true});
  await browser.close();
  console.log('keeper origin: ok');
})().catch(error=>{console.error(error);process.exit(1)});
