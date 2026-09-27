const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');

// Clips start muted; turning the sound on carries to every later clip in the
// tab, and turning it off again carries too. A new tab starts muted.
const clip=fs.readFileSync(path.join(__dirname,'fixtures','clip.webm'));
const video=(id,name)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'video',source:'archive',size:clip.length,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07'});
const clips=[video(1,'FIRST.MP4'),video(2,'SECOND.MP4'),video(3,'THIRD.MP4')];
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');

async function mock(page){
  await page.clock.setFixedTime(new Date('2026-09-07T10:00:00'));
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:3,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:3,bytes:clip.length*3,status:'pending',assets:clips}],memories:3,bytes:clip.length*3}});
    if(/^\/api\/media\/\d+\/original$/.test(url.pathname))return route.fulfill({contentType:'video/webm',body:clip});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
}

const state=page=>page.locator('.rv video').evaluate(v=>({muted:v.muted,volume:Math.round(v.volume*100)/100}));
async function playing(page){await page.waitForFunction(()=>{const v=document.querySelector('.rv video');return v&&!v.paused&&v.currentTime>0})}

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true,args:['--autoplay-policy=no-user-gesture-required']});
  const context=await browser.newContext({viewport:{width:1280,height:800}});
  const page=await context.newPage();
  await mock(page);
  await page.goto(`${base}/on/09-07/photo/1`);
  await page.getByRole('dialog',{name:'Photo review'}).waitFor();
  await playing(page);
  assert.deepEqual(await state(page),{muted:true,volume:1},'the first clip starts muted');

  // The viewer turns the sound on and lowers it, as the video controls would.
  await page.locator('.rv video').evaluate(v=>{v.muted=false;v.volume=0.4});
  await page.waitForFunction(()=>JSON.parse(sessionStorage.getItem('cull.video-sound')||'{}').muted===false);
  await page.keyboard.press('ArrowRight');
  await page.waitForURL(/\/photo\/2$/);
  await playing(page);
  assert.deepEqual(await state(page),{muted:false,volume:0.4},'the next clip keeps the sound on at the same volume');

  await page.reload();
  await page.getByRole('dialog',{name:'Photo review'}).waitFor();
  await playing(page);
  assert.deepEqual(await state(page),{muted:false,volume:0.4},'a reload in the same tab keeps the sound on');

  await page.locator('.rv video').evaluate(v=>{v.muted=true});
  await page.waitForFunction(()=>JSON.parse(sessionStorage.getItem('cull.video-sound')||'{}').muted===true);
  await page.keyboard.press('ArrowRight');
  await page.waitForURL(/\/photo\/3$/);
  await playing(page);
  assert.equal((await state(page)).muted,true,'muting again carries to the next clip');

  const other=await context.newPage();
  await mock(other);
  await other.goto(`${base}/on/09-07/photo/1`);
  await other.getByRole('dialog',{name:'Photo review'}).waitFor();
  await playing(other);
  assert.equal((await state(other)).muted,true,'a new tab starts muted');
  await browser.close();

  // Without a click in this page load, browsers refuse to autoplay with sound.
  // Headless Chrome never enforces that, so the refusal is reproduced here.
  // The clip must still play, muted, and the saved choice must survive.
  const strict=await chromium.launch({channel:'chrome',headless:true});
  const guarded=await (await strict.newContext()).newPage();
  await mock(guarded);
  await guarded.addInitScript(()=>{
    sessionStorage.setItem('cull.video-sound',JSON.stringify({muted:false,volume:0.7}));
    const play=HTMLMediaElement.prototype.play;
    HTMLMediaElement.prototype.play=function(){return this.muted?play.call(this):Promise.reject(new DOMException('play() needs a user gesture','NotAllowedError'))};
  });
  await guarded.goto(`${base}/on/09-07/photo/1`);
  await guarded.getByRole('dialog',{name:'Photo review'}).waitFor();
  await playing(guarded);
  assert.equal((await state(guarded)).muted,true,'a refused autoplay falls back to a muted start');
  assert.deepEqual(await guarded.evaluate(()=>JSON.parse(sessionStorage.getItem('cull.video-sound'))),{muted:false,volume:0.7},'the forced mute is not saved as the viewer choice');
  await strict.close();
  console.log('video sound: ok');
})().catch(error=>{console.error(error);process.exit(1)});
