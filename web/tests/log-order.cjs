const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// The Log opens on the latest choice and reads back to the first a page at a
// time. Files deleted from the Bin and still waiting come after the choices,
// so they never push the latest choice out of sight.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const total=230;
const event=index=>({requestId:`r${index}`,status:'cull',favourite:false,previousStatus:'unreviewed',previousFavourite:false,
  createdAt:new Date(Date.UTC(2026,8,27,9,0,0)-index*60000).toISOString(),
  asset:{id:index+1,path:`/archive/2026/2026-09/2026-09-27/IMG_${index}.JPG`,capturedAt:1,kind:'image',source:'archive',size:100,status:'cull',favourite:false,revision:1,alternativeCount:0,relatedCount:0}});
const waiting={key:'w1',group:'g1',source:'bin',name:'OLD.JPG',original:'/archive/OLD.JPG',kind:'image',size:100,sidecars:0,removedAt:'2026-09-20T10:00:00Z',deletedAt:'2026-09-26T10:00:00Z',dueAt:'2026-10-26T10:00:00Z',attempts:0};

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  const offsets=[];
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:3,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:2,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/log'){
      const limit=Number(url.searchParams.get('limit')),offset=Number(url.searchParams.get('offset')??0);
      offsets.push(offset);
      return route.fulfill({json:Array.from({length:Math.max(0,Math.min(limit,total-offset))},(_,index)=>event(offset+index))});
    }
    if(url.pathname==='/api/trash/deleting')return route.fulfill({json:{graceDays:30,items:[waiting],lastRun:'',lastDeleted:0,lastError:'',checkIntervalMinutes:15}});
    if(url.pathname.startsWith('/api/media/')||url.pathname.startsWith('/api/trash/preview'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await page.goto(`${base}/log`);
  await page.locator('.logtile').first().waitFor();
  assert.equal(await page.locator('.logtile').first().getAttribute('data-asset'),'1','the latest choice comes first');
  assert.equal(await page.locator('.logtile').count(),200);
  const first=await page.locator('.logtile').first().boundingBox();
  assert.ok(first.y<400,`the latest choice is in view on opening, not at ${first.y}`);
  assert.equal(await page.locator('.bingrid').count(),0,'Log never mounts the recovery queue');
  assert.equal(await page.getByRole('link',{name:'Recover files pending permanent deletion'}).getAttribute('href'),'/bin?tab=deleting');

  await page.getByRole('button',{name:'Show earlier choices'}).click();
  await page.locator('.logtile').nth(229).waitFor();
  assert.deepEqual(offsets,[0,200]);
  assert.equal(await page.locator('.logtile').last().getAttribute('data-asset'),'230','the first choice ever comes last');
  assert.equal(await page.getByRole('button',{name:'Show earlier choices'}).count(),0,'nothing earlier is left to read');
  await page.getByText('230 saved choices, newest first').waitFor();
  await browser.close();
  console.log('log order: ok');
})().catch(error=>{console.error(error);process.exit(1)});
