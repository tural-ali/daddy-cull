const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Opening the app for the first time on a day plays the streak full screen,
// then flies it into the pill beside the logo. The pill opens a calendar of
// the run. The search sits in the middle of the bar and Settings closes the
// sidebar.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const pad=n=>String(n).padStart(2,'0');
const now=new Date();
const ym=`${now.getFullYear()}-${pad(now.getMonth()+1)}`;
const back=n=>{const d=new Date(now);d.setDate(d.getDate()-n);return `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())}`};
// Three days up to yesterday, and an older run of five.
const days=[...[40,39,38,37,36].map(back),back(3),back(2),back(1)].sort();
const photo=id=>({id,path:`/archive/2010/2010-09/2010-09-07/P${id}.JPG`,capturedAt:1283860800+id,kind:'image',source:'archive',size:100,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07'});

async function open(browser,options={}){
  const context=await browser.newContext({viewport:{width:1280,height:800},...options});
  const page=await context.newPage();
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:1,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0,calendarDates:366,reviewedDates:40,streak:3,reviewedToday:false}});
    if(url.pathname==='/api/streak')return route.fulfill({json:{streak:3,reviewedToday:false,best:5,days}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:1,bytes:100,status:'pending',assets:[photo(1)]}],memories:1,bytes:100}});
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  return {context,page};
}

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const {context,page}=await open(browser);
  await page.goto(`${base}/on/09-07`);
  const intro=page.getByRole('dialog',{name:'3 days in a row'});
  await intro.waitFor();
  await page.getByText('Review a day today to make it 4.').waitFor();
  if(shots)await page.screenshot({path:`${shots}/streak-1-count.png`});
  await page.locator('.streakintro canvas.confetti').waitFor({timeout:3000});
  await page.waitForTimeout(400);
  if(shots)await page.screenshot({path:`${shots}/streak-2-party.png`});
  await page.waitForTimeout(1350);
  if(shots)await page.screenshot({path:`${shots}/streak-3-flight.png`});
  await intro.waitFor({state:'hidden',timeout:4000});
  if(shots)await page.screenshot({path:`${shots}/streak-4-landed.png`});

  // The pill sits beside the logo, the search after it, no earlier than the
  // panel, and Settings has left the bar for the foot of the sidebar.
  const pill=page.getByRole('button',{name:/^3 days in a row/});
  // The pill bumps as the flame lands; it is measured once it has settled.
  await page.locator('.streakpill.bump').waitFor({state:'detached'});
  await page.waitForFunction(()=>document.querySelector('.streakpill').getAnimations().length===0);
  const [brand,pillBox,search,panel]=await Promise.all([page.locator('.brand svg').first().boundingBox(),pill.boundingBox(),page.locator('.search').boundingBox(),page.locator('main').boundingBox()]);
  assert.ok(Math.abs(brand.x-32)<=1,`the logo sits 32px in, at ${brand.x}`);
  assert.ok(pillBox.x>=brand.x+brand.width&&pillBox.x+pillBox.width<=search.x,'the streak follows the logo, before the search');
  assert.ok(search.x>=panel.x-1,`the search starts no earlier than the panel, at ${search.x} for ${panel.x}`);
  assert.equal(await page.locator('.gbar').getByRole('link',{name:'Settings'}).count(),0,'Settings is not in the bar');
  const settings=await page.locator('#side').getByRole('link',{name:'Settings'}).boundingBox();
  const reviewed=await page.locator('.sideprogress').boundingBox();
  assert.ok(settings.y>reviewed.y,'Settings is below the review meter, at the foot of the sidebar');

  // The calendar shows the run as joined days and the best run.
  await pill.click();
  const calendar=page.getByRole('dialog',{name:'Review streak'});
  await calendar.waitFor();
  await calendar.getByText('Longest streak').waitFor();
  await calendar.getByText('5 days').waitFor();
  const inMonth=days.filter(day=>day.startsWith(ym)).length;
  assert.equal(await calendar.locator('.d.on').count(),inMonth,'this month\'s review days are lit');
  await page.waitForTimeout(400);
  if(shots)await page.screenshot({path:`${shots}/streak-5-calendar.png`});
  if(days.some(day=>!day.startsWith(ym))){
    await calendar.getByRole('button',{name:'Previous month'}).click();
    await calendar.locator('.d.on').first().waitFor();
    await calendar.getByRole('button',{name:'Next month'}).click();
  }
  assert.equal(await calendar.getByRole('button',{name:'Next month'}).isDisabled(),true,'no months ahead of today');
  await page.keyboard.press('Escape');
  await calendar.waitFor({state:'hidden'});
  assert.equal(await pill.evaluate(node=>node===document.activeElement),true,'focus returns to the pill');
  await pill.click();
  await calendar.waitFor();
  await page.mouse.click(900,500);
  await calendar.waitFor({state:'hidden'});

  // Once a day: the next load does not play it again.
  await page.reload();
  await pill.waitFor();
  await page.waitForTimeout(500);
  assert.equal(await page.locator('.streakintro').count(),0,'the second load of the day is quiet');
  await context.close();

  // A click skips straight to the flight, 720ms, where the whole intro takes
  // about 3.4s; the wait allows for a busy test machine.
  const skipper=await open(browser);
  await skipper.page.goto(`${base}/on/09-07`);
  await skipper.page.locator('.streakintro').waitFor();
  await skipper.page.mouse.click(640,400);
  await skipper.page.locator('.streakintro').waitFor({state:'hidden',timeout:2500});
  await skipper.context.close();

  // Asking for less motion means no animation at all.
  const still=await open(browser,{reducedMotion:'reduce'});
  await still.page.goto(`${base}/on/09-07`);
  await still.page.getByRole('button',{name:/^3 days in a row/}).waitFor();
  await still.page.waitForTimeout(500);
  assert.equal(await still.page.locator('.streakintro').count(),0);
  await still.context.close();
  await browser.close();
  console.log('streak: ok');
})().catch(error=>{console.error(error);process.exit(1)});
