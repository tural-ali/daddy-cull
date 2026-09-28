const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Graduation adds files to the archive at night. A page left open reads
// itself again when the catalogue moves on: in place, where it was scrolled
// to, unless that would take something away from the person using it.
const photo=id=>({id,path:`/archive/2010/2010-09/2010-09-07/IMG_${String(id).padStart(4,'0')}.JPG`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07'});
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.clock.install({time:new Date('2026-09-07T10:00:00')});
  let generation=5,files=60,dayReads=0,statsReads=0,catalogueFails=false;
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/catalogue')return catalogueFails?route.fulfill({status:503,body:'catalogue unavailable'}):route.fulfill({json:{generation}});
    if(url.pathname==='/api/stats'){statsReads++;return route.fulfill({json:{total:files,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:2,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}})}
    if(url.pathname==='/api/today/09-07'){
      dayReads++;
      const assets=Array.from({length:files},(_,index)=>photo(index+1));
      return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files,bytes:files*100,status:'pending',assets}],memories:files,bytes:files*100}});
    }
    if(url.pathname==='/api/decisions'){const body=route.request().postDataJSON();return route.fulfill({json:{revision:body.expectedRevision+1,previousStatus:'unreviewed',previousFavourite:false}})}
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const tiles=page.locator('.gal figure');
  const refreshed=page.getByRole('status').filter({hasText:'Updated with new files from the archive.'});
  const stale=page.getByRole('status').filter({hasText:'New files arrived in the archive.'});
  const tip=page.getByRole('status').filter({hasText:'Click any photo to review.'});
  const minute=async()=>{await page.clock.runFor(60_000);await page.waitForTimeout(50)};

  await page.goto(`${base}/on/09-07`);
  await tiles.first().waitFor();
  assert.equal(await tiles.count(),60);
  await minute();
  assert.equal(dayReads,1,'nothing is read again while the catalogue stands still');

  // 1. An untouched page reads itself again, where it was scrolled to.
  await page.evaluate(()=>window.scrollTo(0,420));
  const statsBefore=statsReads;
  generation=6;files=61;
  await minute();
  await page.waitForFunction(()=>document.querySelectorAll('.gal figure').length===61);
  assert.equal(Math.round(await page.evaluate(()=>window.scrollY)),420,'the page stays where it was scrolled to');
  await refreshed.waitFor();
  await page.clock.runFor(300);await page.waitForTimeout(50);
  assert.ok(statsReads>statsBefore,'the counts in the frame are read again');
  // 2. It stacks with the day's own tip in the one corner instead of on it.
  if(await tip.count()){
    const [a,b]=[await refreshed.boundingBox(),await tip.boundingBox()];
    assert.ok(a.y+a.height<=b.y||b.y+b.height<=a.y,`the two notices overlap: ${JSON.stringify([a,b])}`);
    assert.equal(await page.locator('.snacks').count(),1,'one corner holds every notice');
  }
  await page.clock.runFor(6500);
  await refreshed.waitFor({state:'hidden'});

  // 3. After a decision the page holds an undo history, so it waits to be asked.
  await tiles.nth(0).focus();
  await page.keyboard.press('x');
  await page.clock.runFor(1000);
  generation=7;files=62;
  const reads=dayReads;
  await minute();
  await stale.waitFor();
  assert.equal(dayReads,reads,'a page someone worked on is not read again by itself');
  assert.equal(await tiles.count(),61);
  await stale.getByRole('button',{name:'Refresh'}).click();
  await page.waitForFunction(()=>document.querySelectorAll('.gal figure').length===62);
  await stale.waitFor({state:'hidden'});
  assert.equal(await tiles.nth(0).locator('.undo').count(),0,'the page read again shows the removal as saved, not pending');
  assert.ok(await page.evaluate(()=>Object.keys(localStorage).some(key=>key.startsWith('cull.pending.'))),'the saved journal stays behind, empty, which must not hold refreshes back');

  // 4. An open photo holds the refresh until it is closed.
  await tiles.nth(3).click();
  await page.waitForFunction(()=>/\/photo\//.test(location.pathname));
  generation=8;files=63;
  const whileOpen=dayReads;
  await minute();
  assert.equal(dayReads,whileOpen,'nothing is read again under an open photo');
  await page.keyboard.press('Escape');
  await page.waitForFunction(()=>!/\/photo\//.test(location.pathname));
  await minute();
  await page.waitForFunction(()=>document.querySelectorAll('.gal figure').length===63);

  // 5. Coming back to the tab asks straight away, not at the next minute.
  generation=9;files=64;
  await page.evaluate(()=>{Object.defineProperty(document,'hidden',{configurable:true,get:()=>false});document.dispatchEvent(new Event('visibilitychange'))});
  await page.waitForFunction(()=>document.querySelectorAll('.gal figure').length===64);

  // 6. A server that cannot answer changes nothing.
  catalogueFails=true;
  const failing=dayReads;
  await minute();
  assert.equal(dayReads,failing);
  assert.equal(await page.locator('[role=alert]').count(),0,'no error for a check that failed');

  await browser.close();
  console.log('catalogue refresh: ok');
})().catch(error=>{console.error(error);process.exit(1)});
