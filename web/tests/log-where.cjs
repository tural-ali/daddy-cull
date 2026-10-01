const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// A choice's file may have left its day folder since: into the Bin, deleted
// from it and waiting out its days, or gone. Each tile shows the picture from
// wherever it is, points to the Bin instead of offering an Undo the Bin would
// refuse, and a file that is gone says so instead of a broken preview.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const event=(index,where)=>({requestId:`r${index}`,status:'cull',favourite:false,previousStatus:'unreviewed',previousFavourite:false,
  createdAt:new Date(Date.UTC(2026,8,27,9,0,0)-index*60000).toISOString(),...(where?{where}:{}),
  asset:{id:index+1,path:`/archive/2026/2026-09/2026-09-27/IMG_${index}.JPG`,capturedAt:1,kind:'image',source:'archive',size:100,status:'cull',favourite:false,revision:1,alternativeCount:0,relatedCount:0}});
const events=[event(0),event(1,'bin'),event(2,'deleting'),event(3,'deleted')];

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  const media=new Set();
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/catalogue')return route.fulfill({json:{generation:1}});
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:4,synthetic:false,snapshotAt:'',candidates:0,calendarDays:1,reviewedDays:0,decisions:4,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/log')return route.fulfill({json:events});
    if(url.pathname==='/api/trash/deleting')return route.fulfill({json:{graceDays:30,items:[],lastRun:'',lastDeleted:0,lastError:'',checkIntervalMinutes:15}});
    if(url.pathname.startsWith('/api/media/')){media.add(url.pathname.split('/')[3]);return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#526b52"/></svg>'})}
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await page.goto(`${base}/log`);
  const tile=id=>page.locator(`.logtile[data-asset="${id}"]`);
  await tile(1).waitFor();
  await page.waitForFunction(()=>document.querySelectorAll('.logtile img').length===3);
  assert.deepEqual([...media].sort((a,b)=>a.localeCompare(b)),['1','2','3'],'every file still on disk is shown, from wherever it is');

  assert.equal(await tile(1).getByRole('button',{name:'Undo'}).count(),1,'a file in its day folder can be undone');
  for(const [id,label] of [[2,'In the Bin'],[3,'Waiting to go']]){
    assert.equal(await tile(id).getByRole('button',{name:'Undo'}).count(),0,`${label}: no Undo the Bin would refuse`);
    assert.equal(await tile(id).getByRole('link',{name:label}).getAttribute('href'),id===3?'/bin?tab=deleting':'/bin');
  }
  const gone=tile(4);
  assert.match(await gone.innerText(),/Deleted from the Bin/);
  assert.doesNotMatch(await gone.innerText(),/Original untouched|Preview unavailable/);
  assert.equal(await gone.getByRole('button',{name:/Look at/}).count(),0,'nothing to open for a file that is gone');
  assert.equal(await gone.getByRole('button',{name:'Undo'}).count(),0);
  assert.equal(await gone.locator('img').count(),0);

  await browser.close();
  console.log('log where: ok');
})().catch(error=>{console.error(error);process.exit(1)});
