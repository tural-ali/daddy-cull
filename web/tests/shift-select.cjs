const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Shift-click selects a run, as in Google Photos: every tile between it and
// the last one clicked. While anything is selected a plain click adds a tile,
// and a Shift-click starts a selection when nothing is. The same on a date and
// on Screenshots. Every name is a synthetic fixture.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const shot=id=>({id,path:`/screenshots/IMG_${id}.PNG`,name:`IMG_${id}.PNG`,day:`2024-05-0${id}`,state:'waiting',capturedAt:1714000000+id,kind:'image',source:'screenshots',size:1000,
  status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,width:1179,height:2556});
const photo=id=>({id,path:`/archive/2010/2010-09/2010-09-07/P${id}.JPG`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,
  status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07',width:4032,height:3024});
const ids=[1,2,3,4,5,6];

async function open(browser,path){
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:6,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0,screenshots:6}});
    if(url.pathname==='/api/screenshots')return route.fulfill({json:{items:ids.map(shot),total:6,bytes:6000,unreviewed:6,reviewed:0,stills:6,recordings:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:6,bytes:600,status:'pending',assets:ids.map(photo)}],memories:6,bytes:600}});
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300"><rect width="400" height="300" fill="#5b6b7d"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await page.goto(`${base}${path}`);
  await page.locator('main figure[data-asset="6"]').waitFor();
  return page;
}

async function check(page,where){
  const tile=id=>page.locator(`main figure[data-asset="${id}"]`);
  const picked=()=>page.evaluate(()=>[...document.querySelectorAll('main figure.picked')].map(figure=>Number(figure.dataset.asset)));
  const shift=async target=>{await page.keyboard.down('Shift');await target.click();await page.keyboard.up('Shift')};
  const start=page.url();
  await tile(1).hover();
  await tile(1).getByRole('checkbox').click();
  assert.deepEqual(await picked(),[1],`${where}: the tick selects one`);
  await shift(tile(4));
  assert.deepEqual(await picked(),[1,2,3,4],`${where}: Shift-click on a tile selects the run`);
  if(shots){await page.waitForTimeout(400);await page.screenshot({path:`${shots}/shift-select-${where}.png`})}
  await shift(tile(6).getByRole('checkbox'));
  assert.deepEqual(await picked(),[1,2,3,4,5,6],`${where}: Shift-click on a tick runs on from the last one`);
  // A Shift-click on a selected tile clears the run back to the last one.
  await shift(tile(3));
  assert.deepEqual(await picked(),[1,2],`${where}: a Shift-click on a selected tile clears the run`);
  await tile(5).click();
  assert.deepEqual(await picked(),[1,2,5],`${where}: while selecting, a click adds a tile`);
  assert.equal(page.url(),start,`${where}: and opens nothing`);
  assert.equal(await page.evaluate(()=>String(getSelection())),'',`${where}: no text is selected by Shift`);
  await page.getByRole('button',{name:'Clear the selection'}).click();
  await shift(tile(2));
  assert.deepEqual(await picked(),[2],`${where}: Shift-click starts a selection`);
  assert.equal(page.url(),start);
  await page.keyboard.press('Escape');
  await tile(3).click();
  assert.notEqual(page.url(),start,`${where}: with nothing selected, a click opens the photo`);
}

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  await check(await open(browser,'/screenshots'),'screenshots');
  await check(await open(browser,'/on/09-07'),'date');
  await browser.close();
  console.log('shift select: ok');
})().catch(error=>{console.error(error);process.exit(1)});
