const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// A video tile says how long the clip runs in its bottom-right corner, in
// place of a play badge, and gives the corner up to the actions on hover. A
// clip whose running time is not read yet still shows it is a video. On a
// phone, where the actions always show, the running time and the capture time
// sit above them and clear of each other.
const make=(id,name,kind,duration)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id*60,kind,source:'archive',size:100,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07',...(duration?{duration}:{})});
const assets=[make(1,'CLIP.MOV','video',41.2),make(2,'LONG.MP4','video',3725),make(3,'NEW.MOV','video'),make(4,'PHOTO.JPG','image')];
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const picture='<svg xmlns="http://www.w3.org/2000/svg" width="640" height="480"><defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#d9c3a5"/><stop offset=".6" stop-color="#8c6e5a"/><stop offset="1" stop-color="#3b3a44"/></linearGradient></defs><rect width="640" height="480" fill="url(#g)"/></svg>';

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:4,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:4,bytes:400,status:'pending',assets}],memories:4,bytes:400}});
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:picture});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const tile=id=>page.locator(`.gal figure[data-asset="${id}"]`);
  const settle=()=>page.evaluate(()=>document.getAnimations().forEach(animation=>{try{animation.finish()}catch{}}));
  const opacity=locator=>locator.evaluate(node=>getComputedStyle(node).opacity);

  await page.goto(`${base}/on/09-07`);
  await tile(1).waitFor();
  assert.equal(await page.locator('.b.play').count(),0,'no play badge');
  assert.equal(await tile(1).locator('.dur').innerText(),'0:41');
  assert.equal(await tile(1).locator('.dur').getAttribute('aria-label'),'Video, 0:41');
  assert.equal(await tile(2).locator('.dur').innerText(),'1:02:05');
  assert.equal(await tile(3).locator('.dur .icon').count(),1,'a clip not read yet still shows it is a video');
  assert.equal(await tile(3).locator('.dur').getAttribute('aria-label'),'Video');
  assert.equal(await tile(4).locator('.dur').count(),0,'a photo has none');

  // Bottom right, inside the tile.
  await page.mouse.move(5,5);
  await settle();
  const [box,dur]=await Promise.all([tile(1).boundingBox(),tile(1).locator('.dur').boundingBox()]);
  assert.ok(box.x+box.width-(dur.x+dur.width)<=16&&box.x+box.width-(dur.x+dur.width)>=6,'near the right edge');
  assert.ok(box.y+box.height-(dur.y+dur.height)<=16&&box.y+box.height-(dur.y+dur.height)>=6,'near the bottom edge');
  assert.equal(await opacity(tile(1).locator('.dur')),'1');
  if(shots)await page.screenshot({path:`${shots}/video-duration.png`,clip:{x:256,y:180,width:1024,height:420}});

  // The actions take the corner on hover.
  await tile(1).hover();
  await settle();
  assert.equal(await opacity(tile(1).locator('.dur')),'0');
  assert.equal(await opacity(tile(2).locator('.dur')),'1','other tiles keep theirs');

  // On a phone the actions always show, and nothing sits under the heart.
  await page.setViewportSize({width:375,height:760});
  await page.mouse.move(5,5);
  await settle();
  const overlap=(a,b)=>a.x<b.x+b.width&&b.x<a.x+a.width&&a.y<b.y+b.height&&b.y<a.y+a.height;
  for(const id of [1,2,3]){
    const [heart,time,when]=await Promise.all([tile(id).locator('.act.fav').boundingBox(),tile(id).locator('.dur').boundingBox(),tile(id).locator('.when').boundingBox()]);
    assert.equal(await opacity(tile(id).locator('.dur')),'1');
    assert.ok(!overlap(heart,time),`tile ${id}: the running time clears the heart`);
    assert.ok(!overlap(when,time),`tile ${id}: the running time clears the capture time`);
    assert.ok(!overlap(heart,when),`tile ${id}: the capture time clears the heart`);
  }
  const photoWhen=await tile(4).locator('.when').boundingBox(),photoHeart=await tile(4).locator('.act.fav').boundingBox();
  assert.ok(!overlap(photoWhen,photoHeart),'a photo\'s capture time clears the heart');
  await tile(1).scrollIntoViewIfNeeded();
  await settle();
  if(shots)await page.screenshot({path:`${shots}/video-duration-phone.png`});
  await browser.close();
  console.log('video duration: ok');
})().catch(error=>{console.error(error);process.exit(1)});
