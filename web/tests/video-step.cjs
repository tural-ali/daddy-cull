const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');

// Stepping on from a clip turned in Cull leaves only the next photograph on
// the stage: the clip goes, rather than staying beside the photograph and
// pushing it to the edge, as it did while it shared a key with its controls.
const clip=fs.readFileSync(path.join(__dirname,'fixtures','wide.webm'));
const asset=(id,name,kind)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind,source:'archive',size:clip.length,width:160,height:90,...(kind==='video'?{duration:4}:{}),
  status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07'});
const assets=[asset(1,'IMG_0001.JPG','image'),{...asset(2,'IMG_0002.MOV','video'),turn:3},asset(3,'IMG_0003.JPG','image')];
const shots=process.env.SHOTS;
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true,args:['--autoplay-policy=no-user-gesture-required','--disable-audio-output']});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  await page.route('**/api/**',route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:3,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:3,bytes:clip.length*3,status:'pending',assets}],memories:3,bytes:clip.length*3}});
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname==='/api/media/2/original'){
      const range=/bytes=(\d+)-(\d*)/.exec(request.headers().range??'');
      if(!range)return route.fulfill({contentType:'video/webm',headers:{'accept-ranges':'bytes'},body:clip});
      const start=Number(range[1]),end=range[2]?Math.min(Number(range[2]),clip.length-1):clip.length-1;
      return route.fulfill({status:206,contentType:'video/webm',headers:{'accept-ranges':'bytes','content-range':`bytes ${start}-${end}/${clip.length}`},body:clip.subarray(start,end+1)});
    }
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="90" height="160"><rect width="90" height="160" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const stage=()=>page.evaluate(()=>[...document.querySelectorAll('.rvstage > :is(img, video)')].map(node=>node.tagName.toLowerCase()));
  await page.goto(`${base}/on/09-07`);
  await page.locator('.gal figure').nth(1).click();
  await page.getByRole('dialog',{name:'Photo review'}).waitFor();
  await page.waitForFunction(()=>{const v=document.querySelector('.rvstage video');return v&&v.readyState>=2&&!document.querySelector('.rvfly')});
  assert.deepEqual(await stage(),['video']);

  for(const [key,id] of [['ArrowRight',3],['ArrowLeft',2],['ArrowLeft',1]]){
    await page.keyboard.press(key);
    await page.waitForURL(`**/photo/${id}`);
    await page.waitForTimeout(300);
    assert.deepEqual(await stage(),[id===2?'video':'img'],`photo ${id} alone is on the stage`);
    if(shots)await page.screenshot({path:`${shots}/video-step-${id}.png`});
  }
  // And the photograph is centred, not pushed aside.
  const [shown,box]=await Promise.all([page.locator('.rvstage img').boundingBox(),page.locator('.rvstage').boundingBox()]);
  assert.ok(Math.abs(shown.x+shown.width/2-(box.x+box.width/2))<2,`the photograph is centred: ${JSON.stringify([shown,box])}`);
  await browser.close();
})().catch(error=>{console.error(error);process.exit(1)});
