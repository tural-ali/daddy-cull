const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Loading the app's own address opens on the year with the logo alone in the
// middle, light passing over it; the camera dives through the day tiles and
// every date flies in to its place. Only the bare address plays it: /year,
// Year in the sidebar and a visitor who asks for less motion see the calendar
// at once, and any key skips it.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const length=[31,29,31,30,31,30,31,31,30,31,30,31];
const states=['todo','done','todo','part','none'];
const months=length.map((days,month)=>({name:new Date(Date.UTC(2000,month,1)).toLocaleString('en',{month:'long',timeZone:'UTC'}),cells:Array.from({length:31},(_day,index)=>{
  if(index>=days)return null;
  const state=states[(month*7+index*3)%states.length];
  return {md:`${String(month+1).padStart(2,'0')}-${String(index+1).padStart(2,'0')}`,dom:index+1,years:state==='none'?0:3,files:60,done:state==='done'?3:0,waiting:state==='done'||state==='none'?0:(index*13+month*29)%160+1,state};
})}));

async function open(browser,address,options={}){
  const page=await browser.newPage({viewport:{width:1440,height:900},...options});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  await page.route('**/api/**',async route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:10,synthetic:false,snapshotAt:'',bin:0,notifications:0}});
    // The year takes a moment to read, as it does on a big archive.
    if(url.pathname==='/api/year'){await new Promise(done=>setTimeout(done,400));return route.fulfill({json:{months,prog:{dates:366,done:120,part:40,filesDone:0,files:20000},today:'10-01',streak:0,week:{days:0,seconds:0}}})}
    if(url.pathname==='/api/notifications')return route.fulfill({json:{unread:0,items:[]}});
    if(url.pathname==='/api/tasks')return route.fulfill({json:{tasks:[],active:0}});
    if(url.pathname==='/api/trash')return route.fulfill({json:[]});
    if(url.pathname==='/api/trash/deleting')return route.fulfill({json:{graceDays:0,items:[],lastRun:'',lastDeleted:0,lastError:'',checkIntervalMinutes:15}});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await page.goto(`${base}${address}`);
  return page;
}
// Holds every part of the opening still at ms into the phase now playing.
const hold=(page,ms)=>page.evaluate(at=>{for(const animation of document.getAnimations())if(animation.animationName?.startsWith('opening-')){animation.pause();animation.currentTime=at}},ms);
const playing=page=>page.evaluate(()=>document.getAnimations().filter(animation=>animation.animationName?.startsWith('opening-')&&animation.playState==='running').length);

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  let page=await open(browser,'/');
  const opening=page.locator('.opening');
  await opening.waitFor();
  assert.equal(new URL(page.url()).pathname,'/year','the bare address is the year');
  assert.match(await opening.getAttribute('class'),/\blogo\b/,'the logo shows first');
  assert.equal(await opening.locator('svg[aria-label="Daddy, Cull!"] .shine').count(),1,'light passes over the logo');
  await page.waitForTimeout(700);
  const logo=await opening.locator('svg').boundingBox();
  assert.ok(Math.abs(logo.x+logo.width/2-720)<2&&Math.abs(logo.y+logo.height/2-450)<2,`the logo is in the middle: ${JSON.stringify(logo)}`);
  if(shots)await page.screenshot({path:`${shots}/opening-logo.png`});
  await page.locator('.opening.dive').waitFor();
  assert.ok(await page.locator('.yearview.opening-arrive').count(),'the dates fly in as the camera dives');
  if(shots){
    for(const ms of [180,420,700,1000]){
      await hold(page,ms);
      await page.screenshot({path:`${shots}/opening-dive-${ms}.png`});
    }
    await page.evaluate(()=>{for(const animation of document.getAnimations())animation.play()});
  }
  await opening.waitFor({state:'detached',timeout:4000});
  assert.equal(await page.locator('.yearview').getAttribute('class'),'yearview','the year is left as it always is');
  assert.equal(await playing(page),0);
  // Every date is in its place and can be pressed.
  const cell=page.locator('.cmonth').nth(5).locator('.cell').nth(14);
  const box=await cell.boundingBox();
  assert.equal(await page.evaluate(([x,y])=>document.elementFromPoint(x,y)?.closest('.cell')?.getAttribute('href'),[box.x+box.width/2,box.y+box.height/2]),'/on/06-15');
  if(shots)await page.screenshot({path:`${shots}/opening-end.png`});

  // Year in the sidebar, and /year itself, show the calendar at once.
  await page.getByRole('link',{name:'Bin',exact:true}).click();
  await page.getByRole('heading',{name:'Bin',exact:true}).waitFor();
  await page.getByRole('link',{name:'Year',exact:true}).click();
  await page.locator('.cmonth').nth(11).waitFor();
  assert.equal(await page.locator('.opening').count(),0,'moving to the year inside the app does not play it');
  await page.close();
  page=await open(browser,'/year');
  await page.locator('.cmonth').nth(11).waitFor();
  assert.equal(await page.locator('.opening').count(),0,'opening /year does not play it');
  await page.close();

  // A key skips it.
  page=await open(browser,'/');
  await page.locator('.opening').waitFor();
  await page.keyboard.press('Escape');
  await page.locator('.cmonth').nth(11).waitFor();
  await page.locator('.opening').waitFor({state:'detached',timeout:1000});
  await page.waitForTimeout(100);
  assert.equal(await page.locator('.yearview').getAttribute('class'),'yearview','skipped, the calendar shows at once');
  await page.close();

  // Less motion: no opening at all.
  page=await open(browser,'/',{reducedMotion:'reduce'});
  await page.locator('.cmonth').nth(11).waitFor();
  assert.equal(await page.locator('.opening').count(),0,'no opening for less motion');
  await browser.close();
  console.log('year opening: ok');
})().catch(error=>{console.error(error);process.exit(1)});
