const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');

// A clip turned in Cull turns its picture, not its controls: the browser's own
// controls are part of the video, so while it is turned the viewer draws
// upright ones under it, and they play, pause, seek and mute the clip.
const clip=fs.readFileSync(path.join(__dirname,'fixtures','wide.webm'));
const video=(id,name)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'video',source:'archive',size:clip.length,width:160,height:90,duration:4,
  status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07'});
const shots=process.env.SHOTS;
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true,args:['--autoplay-policy=no-user-gesture-required','--disable-audio-output']});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  const turned=new Map();
  await page.route('**/api/**',route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:1,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:1,bytes:clip.length,status:'pending',assets:[video(1,'WIDE.MOV')]}],memories:1,bytes:clip.length}});
    if(url.pathname==='/api/turns'){
      const body=request.postDataJSON();
      for(const id of body.ids)turned.set(id,(((turned.get(id)??0)+body.quarters)%4+4)%4);
      return route.fulfill({json:{turns:Object.fromEntries(body.ids.map(id=>[id,turned.get(id)]))}});
    }
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    // Served in ranges, as the server does, so the clip can be seeked.
    if(url.pathname==='/api/media/1/original'){
      const range=/bytes=(\d+)-(\d*)/.exec(request.headers().range??'');
      if(!range)return route.fulfill({contentType:'video/webm',headers:{'accept-ranges':'bytes'},body:clip});
      const start=Number(range[1]),end=range[2]?Math.min(Number(range[2]),clip.length-1):clip.length-1;
      return route.fulfill({status:206,contentType:'video/webm',headers:{'accept-ranges':'bytes','content-range':`bytes ${start}-${end}/${clip.length}`},body:clip.subarray(start,end+1)});
    }
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="160" height="90"><rect width="160" height="90" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await page.goto(`${base}/on/09-07/photo/1`);
  await page.getByRole('dialog',{name:'Photo review'}).waitFor();
  const player=page.locator('.rv video');
  await page.waitForFunction(()=>{const v=document.querySelector('.rv video');return v&&v.readyState>=2});
  const bar=page.getByRole('group',{name:'Video'});
  assert.equal(await player.evaluate(v=>v.controls),true,'an upright clip keeps its own controls');
  assert.equal(await bar.count(),0);

  await page.keyboard.press(']');
  await bar.waitFor();
  assert.equal(await player.evaluate(v=>v.controls),false,'a turned clip gives up its own controls');
  // The picture stands on end, and the bar under it lies flat and upright.
  const [shown,flat]=await Promise.all([player.boundingBox(),bar.boundingBox()]);
  assert.ok(shown.height>shown.width*1.5,`the clip stands on end: ${shown.width}x${shown.height}`);
  assert.equal(await bar.evaluate(node=>getComputedStyle(node).rotate),'none');
  assert.ok(flat.width>flat.height*4,'the bar is wide and low');
  assert.ok(flat.y>=shown.y+shown.height-1,'and sits below the clip, clear of it');

  // Its buttons drive the clip, with nothing of the page over them.
  assert.equal(await page.getByText(/Click any photo to review/).count(),0,'the page\'s tip waits while a photo is open');
  const paused=()=>player.evaluate(v=>v.paused);
  if(await paused())await bar.getByRole('button',{name:'Play'}).click();
  await page.waitForFunction(()=>!document.querySelector('.rv video').paused);
  await bar.getByRole('button',{name:'Pause'}).click();
  assert.equal(await paused(),true,'Pause pauses');
  await bar.getByRole('slider',{name:'Position'}).fill('3');
  assert.ok(Math.abs(await player.evaluate(v=>v.currentTime)-3)<.2,'the slider seeks');
  await page.getByText(/^0:03 \/ 0:04$/).waitFor();
  if(shots)await page.screenshot({path:`${shots}/video-turn.png`});
  const muted=await player.evaluate(v=>v.muted);
  await bar.getByRole('button',{name:muted?'Turn the sound on':'Mute'}).click();
  assert.equal(await player.evaluate(v=>v.muted),!muted,'the sound button toggles the sound');
  // Clicking the clip itself plays it, as its own controls would.
  await player.click();
  await page.waitForFunction(()=>!document.querySelector('.rv video').paused);

  // Turned back upright, the clip has its own controls again.
  await page.keyboard.press('[');
  await bar.waitFor({state:'detached'});
  assert.equal(await player.evaluate(v=>v.controls),true);
  await browser.close();
  console.log('video turn: ok');
})().catch(error=>{console.error(error);process.exit(1)});
