const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Files Cull Sync did not find in Photos are grouped by what it saw instead: a
// file in no library it can read, one only in a shared album, and a name
// Photos holds only on another day. While it reads the shared albums, the page
// says so. A helper older than the server's is offered an update, since it
// cannot say what it saw.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const now='2026-09-27T10:00:00Z';
const miss=(name,day,why)=>({action:'delete',name,day,...(why?{why}:{})});
const missing=[
  miss('IMG_8157.MOV','2025-04-08'),
  miss('IMG_8160.MOV','2025-04-08'),
  miss('IMG_8190.MOV','2025-04-08','shared-album'),
  miss('IMG_0412.MOV','2024-12-25','other-day'),
];
let stage='';
let agent={online:true,lastSeen:now,version:'1.1',access:'authorized'};
const summary=()=>stage?{id:'J1',state:'checking',rev:1,stage,done:40,total:120}:{id:'J1',state:'planned',rev:2,done:0,total:0};

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:900}});
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:4,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/photos')return route.fulfill({json:{configured:true,now,agent,job:summary()}});
    if(url.pathname==='/api/photos/overview')return route.fulfill({json:{delete:4,favourite:0,held:0,undated:0,restored:[],synced:{deleted:0,favourited:0,last:''}}});
    // Opening the page checks Photos; the answer here is the same plan.
    if(url.pathname==='/api/photos/check')return route.fulfill({json:{...summary(),created:now,updated:now,toCheck:4,delete:[],favourite:[],missing,held:[],undated:0,selected:[],skipped:0}});
    if(url.pathname==='/api/photos/jobs/J1')return route.fulfill({json:{...summary(),created:now,updated:now,toCheck:4,delete:[],favourite:[],missing:stage?[]:missing,held:[],undated:0,selected:[],skipped:0}});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });

  stage='shared';
  await page.goto(`${base}/photos`);
  await page.getByText('Reading shared albums on the Mac…').first().waitFor();

  stage='';
  await page.reload();
  const groups=page.locator('details.pmore[data-why]');
  await groups.first().waitFor();
  assert.deepEqual(await groups.evaluateAll(nodes=>nodes.map(node=>node.dataset.why)),['none','shared-album','other-day']);
  assert.equal(await page.locator('details[data-why="none"] summary').innerText(),"2 files already gone from this Mac's Photos");
  // The headline says the files Photos no longer has need nothing, apart from
  // the ones it has but leaves alone.
  assert.equal((await page.locator('.ysum').innerText()).replace(/\s+/g,' '),'0 still in Photos to delete · 0 favourites to set · 2 already gone from it · 2 left alone');
  assert.equal(await page.locator('details[data-why="shared-album"] summary').innerText(),'1 file only in a shared album');
  assert.equal(await page.locator('details[data-why="other-day"] summary').innerText(),'1 file not found: Photos has the name only on another day');

  await page.locator('details[data-why="shared-album"] summary').click();
  const shared=page.locator('details[data-why="shared-album"]');
  assert.match(await shared.locator('.hint').innerText(),/never changes a shared album/);
  assert.deepEqual(await shared.locator('li .mono').allInnerTexts(),['IMG_8190.MOV']);
  await page.locator('details[data-why="none"] summary').click();
  assert.deepEqual(await page.locator('details[data-why="none"] li .mono').allInnerTexts(),['IMG_8157.MOV','IMG_8160.MOV']);
  if(shots)await page.locator('details[data-why="none"]').evaluate(node=>node.closest('section').scrollIntoView());
  if(shots)await page.screenshot({path:`${shots}/photos-missing.png`});

  // An old helper is named, offered an update, and its unexplained misses say why.
  agent={...agent,version:'1.0',outdated:true,latest:'1.1'};
  await page.reload();
  const helper=page.locator('.phelper');
  await helper.getByText('version 1.1 is ready').waitFor();
  assert.match(await helper.innerText(),/version 1\.0 · version 1\.1 is ready/);
  const update=helper.getByRole('button',{name:'Update Cull Sync'});
  assert.match(await update.getAttribute('class'),/primary/);
  await page.locator('details[data-why="none"] summary').click();
  assert.match(await page.locator('details[data-why="none"] .hint').innerText(),/too old to say which of these are only in a shared album/);
  assert.doesNotMatch(await page.locator('details[data-why="shared-album"] .hint').innerText(),/too old/);
  if(shots)await helper.screenshot({path:`${shots}/photos-outdated.png`});
  agent={...agent,version:'1.1',outdated:false,latest:undefined};
  await page.reload();
  await page.locator('details[data-why]').first().waitFor();
  assert.doesNotMatch(await helper.innerText(),/is ready/);
  assert.equal(await helper.getByRole('button').count(),0,'a current Cull Sync needs no setting up');

  // Nothing pushes the page sideways on a phone.
  await page.setViewportSize({width:375,height:760});
  // The sidebar slides away as the window narrows; look once it has gone.
  await page.waitForTimeout(400);
  await page.evaluate(()=>{scrollTo(0,0);document.getAnimations().forEach(animation=>{try{animation.finish()}catch{}})});
  await page.locator('details[data-why="none"]').scrollIntoViewIfNeeded();
  const wide=await page.evaluate(()=>document.documentElement.scrollWidth);
  assert.ok(wide<=375,`page is ${wide}px wide on a phone`);
  if(shots)await page.screenshot({path:`${shots}/photos-missing-phone.png`});
  await browser.close();
  console.log('photos missing: ok');
})().catch(error=>{console.error(error);process.exit(1)});
