const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// The keyboard tip on a day is a snackbar in the bottom-left corner: it shows
// on the first day opened in a tab, goes after fifteen seconds and stays away
// while the reviewer moves between days, until the page is refreshed.
const photo=(id,name)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07'});
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.clock.install({time:new Date('2026-09-07T10:00:00')});
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:3,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:2,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:2,bytes:200,status:'pending',assets:[photo(1,'ONE.JPG'),photo(2,'TWO.JPG')]}],memories:2,bytes:200}});
    if(url.pathname==='/api/today/09-08')return route.fulfill({json:{md:'09-08',label:'8 September',previous:'09-07',next:'09-09',years:[{day:'2010-09-08',year:2010,files:1,bytes:100,status:'pending',assets:[photo(3,'THREE.JPG')]}],memories:1,bytes:100}});
    if(url.pathname==='/api/decisions'){const body=route.request().postDataJSON();return route.fulfill({json:{revision:body.expectedRevision+1,previousStatus:'unreviewed',previousFavourite:false}})}
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const tip=page.getByRole('status').filter({hasText:'Click any photo to review.'});

  await page.goto(`${base}/on/09-07`);
  await tip.waitFor();
  const box=await tip.boundingBox();
  const side=await page.locator('#side').boundingBox();
  assert.ok(box.x>=side.x+side.width&&box.x<side.x+side.width+40&&box.y+box.height>760,`the tip sits in the panel's bottom-left corner, clear of the sidebar, not at ${JSON.stringify(box)}`);
  assert.equal(await page.locator('.fbar').count(),0,'the footer bar is gone');
  // Removing a photo shows no notice: the tile itself says what happened.
  await page.locator('.gal figure').nth(0).focus();
  await page.keyboard.press('x');
  await page.clock.runFor(1000);
  assert.equal(await page.locator('.snack').count(),1,'only the tip is in the corner after a decision');
  assert.equal(await page.locator('.flash').count(),0,'no banner above the grid');
  await page.clock.runFor(13000);
  await tip.waitFor({state:'visible'});
  await page.clock.runFor(1500);
  await tip.waitFor({state:'hidden'});

  // The next day, opened without a reload, does not bring it back.
  await page.getByRole('button',{name:/^7 September/}).click();
  await page.getByRole('navigation',{name:'Nearby dates'}).getByRole('link',{name:'8 Sep'}).click();
  // The day is read in place now, so the page's fake clock is the one it
  // waits on: move it along until the day arrives.
  for(let step=0;step<50&&!await page.locator('.gal figure[data-asset="3"]').count();step++)await page.clock.runFor(100);
  await page.locator('.gal figure[data-asset="3"]').waitFor();
  await page.clock.runFor(500);
  assert.equal(await tip.count(),0,'moving to another day shows no tip');

  // The fake clock leaves the page no navigation timing to read, so the
  // refresh is told the way the browser would tell it.
  await page.addInitScript(()=>{const real=performance.getEntriesByType.bind(performance);performance.getEntriesByType=type=>type==='navigation'?[{type:'reload'}]:real(type)});
  await page.reload();
  await tip.waitFor();
  await browser.close();
  console.log('day tip: ok');
})().catch(error=>{console.error(error);process.exit(1)});
