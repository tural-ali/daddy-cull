const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Loading the app's own address opens on the year with the logo alone in the
// middle, light passing over it; the camera dives through the day tiles and
// every date flies in to its place. The bare address and /year play it, so a
// reload does too; Year in the sidebar and a visitor who asks for less motion
// see the calendar at once, and any key skips it.
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
  const {yearDelay=400,yearError=false,...browserOptions}=options;
  const page=await browser.newPage({viewport:{width:1440,height:900},...browserOptions});
  page.on('pageerror',error=>{throw error});
  await page.addInitScript(()=>{localStorage.setItem('cull-theme','night')});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  await page.route('**/api/**',async route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:10,synthetic:false,snapshotAt:'',bin:0,notifications:0}});
    // The year takes a moment to read, as it does on a big archive.
    if(url.pathname==='/api/year'){
      await new Promise(done=>setTimeout(done,yearDelay));
      if(yearError)return route.fulfill({status:500,json:{error:'Calendar unavailable'}});
      return route.fulfill({json:{months,prog:{dates:366,done:120,part:40,filesDone:0,files:20000},today:'10-01',streak:0,week:{days:0,seconds:0}}});
    }
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
  assert.equal(await page.locator('.openingedge').count(),16,'every outline edge has a solid side face');
  const camera=await page.locator('.openingworld').evaluate(world=>{
    const animation=world.getAnimations()[0],time=animation.currentTime;
    const frames=[300,700].map(at=>{animation.currentTime=at;const box=world.getBoundingClientRect();return {centre:box.x+box.width/2,width:box.width}});
    animation.currentTime=time;
    return {frames,wordOpacity:getComputedStyle(world.querySelector('.word')).opacity,logoAnimation:getComputedStyle(world.querySelector('.openinglogo')).animationName};
  });
  assert.ok(camera.frames.every(frame=>Math.abs(frame.centre-720)<1),'the camera travels straight without sideways re-centring');
  assert.ok(camera.frames[1].width>camera.frames[0].width,'the whole logo grows in one camera move');
  assert.equal(camera.wordOpacity,'1','the wordmark travels with the mark');
  assert.equal(camera.logoAnimation,'none','the wordmark never fades away independently');
  assert.equal(await page.locator('.openingface').count(),6,'all three tiles have front and back faces');
  const delays=await page.locator('.cell:not(.blank)').evaluateAll(cells=>cells.map(cell=>parseFloat(getComputedStyle(cell).animationDelay)));
  assert.ok(Math.min(...delays)>=1.25&&Math.max(...delays)<=1.48,'the dates wait for the camera pass');
  assert.ok(Math.max(...delays)-Math.min(...delays)>.15,'the centre arrives before the edges');
  const hidden=await page.locator('.cell:not(.blank)').evaluateAll(cells=>cells.every(cell=>getComputedStyle(cell).opacity==='0'));
  assert.ok(hidden,'no dates appear before the zoom');
  if(shots){
    for(const ms of [180,700,1100,1300,1600,2200]){
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

  // Year in the sidebar shows the calendar at once.
  await page.getByRole('link',{name:'Bin',exact:true}).click();
  await page.getByRole('heading',{name:'Bin',exact:true}).waitFor();
  await page.getByRole('link',{name:'Year',exact:true}).click();
  await page.locator('.cmonth').nth(11).waitFor();
  assert.equal(await page.locator('.opening').count(),0,'moving to the year inside the app does not play it');
  // A reload of the year, which the bare address became, plays it again.
  await page.reload();
  await page.locator('.opening').waitFor();
  await page.close();
  page=await open(browser,'/year');
  await page.locator('.opening').waitFor();
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
  await page.close();

  // Changing the OS preference while the camera is moving ends the motion.
  page=await open(browser,'/');
  await page.locator('.opening.dive').waitFor();
  await page.emulateMedia({reducedMotion:'reduce'});
  await page.locator('.opening').waitFor({state:'detached',timeout:1000});
  assert.equal(await playing(page),0);
  await page.close();

  // A slow archive must never send the camera into an empty calendar.
  page=await open(browser,'/',{yearDelay:2400});
  await page.waitForTimeout(1800);
  assert.equal(await page.locator('.opening.logo').count(),1,'wait on the logo until the archive arrives');
  await page.locator('.opening.dive').waitFor();
  await page.locator('.opening').waitFor({state:'detached',timeout:4000});
  await page.close();
  page=await open(browser,'/',{yearError:true});
  await page.getByRole('alert').waitFor();
  assert.equal(await page.locator('.opening').count(),0,'a failed load exposes Retry immediately');
  await page.close();

  for(const width of [320,768,1024,1440]){
    page=await open(browser,'/',{viewport:{width,height:900},colorScheme:width===1440?'light':'dark'});
    if(width===1440)await page.evaluate(()=>document.documentElement.dataset.theme='day');
    await page.locator('.opening.dive').waitFor();
    await hold(page,700);
    if(shots)await page.screenshot({path:`${shots}/opening-${width}-solid.png`});
    await hold(page,1600);
    if(shots)await page.screenshot({path:`${shots}/opening-${width}-landing.png`});
    const movingWidth=await page.evaluate(()=>document.documentElement.scrollWidth);
    await page.locator('.opening').waitFor({state:'detached',timeout:4000});
    assert.equal(await playing(page),0);
    assert.ok(movingWidth<=await page.evaluate(()=>document.documentElement.scrollWidth),`no added viewport overflow at ${width}px`);
    if(shots)await page.screenshot({path:`${shots}/opening-${width}-end.png`});
    await page.close();
  }
  await browser.close();
  console.log('year opening: ok');
})().catch(error=>{console.error(error);process.exit(1)});
