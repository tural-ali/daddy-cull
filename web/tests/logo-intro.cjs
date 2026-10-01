const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// A page load shows the whole logo for 10 s while light passes over it, then
// the wordmark slides in behind the tiles and only the mark stays. Moving
// around the app keeps the mark; reading the page again plays it again.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const whole=24*2016/308,mark=24*505.5/308;

async function open(browser,options={}){
  const page=await browser.newPage({viewport:{width:1280,height:800},...options});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:10,synthetic:false,snapshotAt:'',screenshots:0,bin:0,notifications:0}});
    if(url.pathname==='/api/notifications')return route.fulfill({json:{unread:0,items:[]}});
    if(url.pathname==='/api/trash')return route.fulfill({json:[]});
    if(url.pathname==='/api/trash/deleting')return route.fulfill({json:{graceDays:0,items:[],lastRun:'',lastDeleted:0,lastError:'',checkIntervalMinutes:15}});
    if(url.pathname==='/api/tasks')return route.fulfill({json:{tasks:[],active:0}});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await page.goto(`${base}/bin`);
  await page.getByRole('heading',{name:'Bin',exact:true}).waitFor();
  return page;
}
const logo=page=>page.locator('.brand svg:not(.brandmark)');
const state=page=>logo(page).evaluate(svg=>{
  const box=svg.getBoundingClientRect(),word=svg.querySelector('.word').getBoundingClientRect();
  return {className:svg.getAttribute('class')??'',width:box.width,right:box.right,wordRight:word.right,
    animations:svg.getAnimations({subtree:true}).map(animation=>animation.animationName).sort()};
});
// Holds every part of the intro at `ms` into it, to look at one frame.
const frame=(page,ms)=>logo(page).evaluate((svg,at)=>{for(const animation of svg.getAnimations({subtree:true})){animation.pause();animation.currentTime=at}},ms);
const near=(actual,expected,label)=>assert.ok(Math.abs(actual-expected)<0.6,`${label}: ${actual} is not ${expected}`);

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await open(browser);
  const started=Date.now();
  let now=await state(page);
  assert.match(now.className,/\bintro\b/,'a page load plays the intro');
  near(now.width,whole,'the whole logo shows first');
  assert.deepEqual(now.animations,['logo-fold','logo-shine','logo-word']);
  assert.equal(await page.getByRole('link',{name:/Daddy, Cull!/}).count(),1,'the logo keeps its name');

  // Frames, held still: the light half way over, the whole logo just before
  // it goes, the wordmark half way in, and the end.
  if(shots){
    for(const [name,ms] of [['shine',1500],['rest',9600],['fold',10450],['end',10900]]){
      await frame(page,ms);
      await page.screenshot({path:`${shots}/logo-${name}.png`,clip:{x:0,y:0,width:420,height:64}});
    }
  }
  await frame(page,10450);
  now=await state(page);
  assert.ok(now.width>mark+4&&now.width<whole-4,`half way the logo is narrowing, at ${now.width}`);
  assert.ok(now.wordRight<=now.right+0.5,'the wordmark never shows past the logo');
  await logo(page).evaluate(svg=>{for(const animation of svg.getAnimations({subtree:true}))animation.play()});

  // It ends as the mark alone and stays that way.
  await page.waitForFunction(()=>document.querySelector('.brand svg:not(.brandmark)')?.classList.contains('folded'),null,{timeout:14000});
  const took=Date.now()-started;
  assert.ok(took>9500&&took<12500,`the intro takes about 11 s, took ${took} ms`);
  now=await state(page);
  near(now.width,mark,'only the mark is left');
  assert.ok(now.wordRight<=now.right+0.5,'the wordmark is hidden behind the mark');
  assert.deepEqual(now.animations,[],'nothing runs after the intro');
  const [brand,bell]=await Promise.all([page.locator('.brand').boundingBox(),page.locator('.bellwrap').boundingBox()]);
  assert.ok(bell.x-(brand.x+brand.width)<24,'the bar closes up after the logo');
  if(shots)await page.screenshot({path:`${shots}/logo-folded-bar.png`,clip:{x:0,y:0,width:640,height:64}});

  // Going somewhere else in the app keeps the mark.
  await page.locator('.brand').click();
  await page.waitForURL(url=>new URL(url).pathname==='/year');
  now=await state(page);
  assert.match(now.className,/\bfolded\b/,'moving around the app does not play it again');
  near(now.width,mark,'still the mark');

  // Reading the page again plays it again.
  await page.goto(`${base}/bin`);
  await page.getByRole('heading',{name:'Bin',exact:true}).waitFor();
  now=await state(page);
  assert.match(now.className,/\bintro\b/,'a reload plays it again');
  near(now.width,whole,'whole again');
  await page.close();

  // With less motion asked for there is no light and no slide: the whole
  // logo, then the mark.
  const still=await open(browser,{reducedMotion:'reduce'});
  now=await state(still);
  assert.deepEqual(now.animations,[],'nothing moves');
  near(now.width,whole,'the whole logo shows first');
  const shine=await still.locator('.shine').evaluate(rect=>{const box=rect.getBoundingClientRect(),svg=rect.ownerSVGElement.getBoundingClientRect();return box.right<=svg.left});
  assert.ok(shine,'the light waits off the logo');
  await still.close();

  // Dark, and a phone, where the bar is smaller.
  const dark=await open(browser,{colorScheme:'dark'});
  if(shots){await frame(dark,1600);await dark.screenshot({path:`${shots}/logo-shine-dark.png`,clip:{x:0,y:0,width:420,height:64}})}
  await dark.close();
  const phone=await open(browser,{viewport:{width:390,height:800}});
  near((await state(phone)).width,20*2016/308,'on a phone the logo is 20 px high');
  await frame(phone,10900);
  near((await state(phone)).width,20*505.5/308,'and folds to its mark');
  if(shots)await phone.screenshot({path:`${shots}/logo-phone.png`,clip:{x:0,y:0,width:390,height:64}});
  await browser.close();
  console.log('logo-intro: ok');
})().catch(error=>{console.error(error);process.exit(1)});
