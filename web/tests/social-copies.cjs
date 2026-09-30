const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Saved from social offers the copies of its videos at the top, as a day page
// does. Settling a group marks the other copies for the Bin and leaves the
// one kept undecided, still on the list to be judged as social or not. Every
// name is a synthetic fixture.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const day='2023-01-02';
const clip=(id,file,extra={})=>({id,path:`/archive/2023/2023-01/${day}/${file}`,capturedAt:Date.parse(`${day}T00:00:00Z`)/1000,kind:'video',source:'archive',size:5000,status:'unreviewed',favourite:false,revision:2,alternativeCount:0,relatedCount:0,...extra});
const item=(id,file)=>({...clip(id,file),day,name:file,score:12,band:'likely',evidence:'uuid filename',width:720,height:1280,duration:12,letterbox:false,poster:true});
const svg='<svg xmlns="http://www.w3.org/2000/svg" width="90" height="160"><rect width="90" height="160" fill="#6b7f95"/></svg>';

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:900}});
  let items=[item(1,'5f1c2a.mp4'),item(2,'5f1c2a (1).mp4'),item(3,'9b7d11.mp4')];
  const group={hash:'footage:ab12',proof:'footage',size:5100,reclaimable:5000,members:[{...clip(2,'5f1c2a (1).mp4'),day,located:false},{...clip(1,'5f1c2a.mp4',{size:5100}),day,located:false}]};
  let posted=null;
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/addons')return route.fulfill({json:[]});
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:3,synthetic:true,snapshotAt:'2026-09-30 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/social')return route.fulfill({json:{items,total:items.length,shown:items.length,bytes:items.length*5000,likely:items.length,possible:0,watch:0,letterboxed:0,social:items.length,unsure:0,kept:0,marked:3-items.length}});
    if(url.pathname==='/api/social-copies')return route.fulfill({json:posted?[]:[group]});
    if(url.pathname==='/api/decisions/batch'){
      posted=JSON.parse(route.request().postData());
      const gone=new Set(posted.map(change=>change.assetId));
      items=items.filter(entry=>!gone.has(entry.id));
      return route.fulfill({json:posted.map(()=>({revision:3}))});
    }
    if(url.pathname.startsWith('/api/media/')||url.pathname.startsWith('/api/social-poster/'))return route.fulfill({contentType:'image/svg+xml',body:svg});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });

  await page.goto(`${base}/social`);
  const copies=page.locator('.xcopy');
  await copies.waitFor();
  assert.match(await page.locator('.xdupes > h2').innerText(),/Copies\s+1 group/);
  assert.match((await copies.locator('.xmeta').innerText()).replace(/\s+/g,' '),/Same footage 2 copies · only the metadata differs/);
  const rows=copies.locator('.xtable tbody tr');
  assert.deepEqual(await rows.evaluateAll(nodes=>nodes.map(node=>[Number(node.dataset.asset),node.className])),[[2,'binned'],[1,'keeper']],'the plainer name is kept by default');
  assert.equal(await page.locator('main figure[data-asset]').count(),3);
  await page.screenshot({path:process.env.SHOT||'/tmp/social-copies.png'});
  // On a phone the group fits the screen.
  await page.setViewportSize({width:390,height:844});
  await page.waitForFunction(()=>![...document.querySelectorAll('.xdupes *')].some(node=>node.getBoundingClientRect().right>391),null,{timeout:3000}).catch(()=>{});
  assert.deepEqual(await page.evaluate(()=>[...document.querySelectorAll('.xdupes *')].filter(node=>node.getBoundingClientRect().right>391).map(node=>node.className)),[],'the group fits a phone');
  if(process.env.SHOT_PHONE)await page.screenshot({path:process.env.SHOT_PHONE});
  await page.setViewportSize({width:1280,height:900});

  await copies.getByRole('button',{name:/Keep the selected copy/}).click();
  await page.getByText(/Marked 1 copy of 5f1c2a\.mp4 for the Bin, 4\.9 KB in all\. The copy left stays on this list to be judged\./).waitFor();
  assert.deepEqual(posted.map(change=>[change.assetId,change.status,change.expectedRevision]),[[2,'cull',2]],'only the other copy is decided');
  assert.equal(await page.locator('.xdupes').count(),0);
  assert.deepEqual(await page.locator('main figure[data-asset]').evaluateAll(nodes=>nodes.map(node=>Number(node.dataset.asset))),[1,3]);
  await page.waitForFunction(()=>(document.querySelector('.ysum')?.textContent??'').startsWith('2 undecided'));

  await browser.close();
  console.log('social copies: ok');
})().catch(error=>{console.error(error);process.exit(1)});
