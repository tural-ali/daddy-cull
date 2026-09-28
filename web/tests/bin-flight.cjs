const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// A removed photo draws in to a dot where it was and is thrown in an arc into
// the Bin in the sidebar, which gives a little bump as it lands. A selection
// sends one dot for each. With reduced motion, or from inside the viewer
// where the sidebar is covered, nothing flies. Every name is a synthetic fixture.
const photo=(id,name)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,
  status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07',width:4032,height:3024});
const picture='<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300"><rect width="400" height="300" fill="#6b7f95"/></svg>';
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;

async function open(browser,options={}){
  const page=await browser.newPage({viewport:{width:1280,height:800},...options});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  await page.route('**/api/**',route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:4,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0,bin:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:4,bytes:400,status:'pending',assets:[photo(1,'ONE.JPG'),photo(2,'TWO.JPG'),photo(3,'THREE.JPG'),photo(4,'FOUR.JPG')]}],memories:4,bytes:400}});
    if(url.pathname==='/api/decisions'){const body=request.postDataJSON();return route.fulfill({json:{revision:body.expectedRevision+1,previousStatus:'unreviewed',previousFavourite:false}})}
    if(url.pathname==='/api/decisions/batch'){const body=request.postDataJSON();return route.fulfill({json:body.map(change=>({revision:change.expectedRevision+1,previousStatus:'unreviewed',previousFavourite:false}))})}
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:picture});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await page.goto(`${base}/on/09-07`);
  await page.waitForFunction(()=>[...document.querySelectorAll('main figure img')].every(img=>img.complete&&img.naturalWidth>0));
  // Every dot's path is watched from the start: where it flies, how high,
  // and whether the Bin bumped.
  await page.evaluate(()=>{
    window.flights=[];window.bumps=0;
    const icon=document.querySelector('#side a[href="/bin"] .icon');
    new MutationObserver(records=>{for(const record of records)for(const node of record.addedNodes){
      if(!(node instanceof HTMLElement)||!node.classList.contains('binfly'))continue;
      const path=[];window.flights.push(path);
      const look=()=>{if(!node.isConnected)return;const box=node.getBoundingClientRect();path.push({x:box.left+box.width/2,y:box.top+box.height/2,w:box.width});requestAnimationFrame(look)};
      look();
    }}).observe(document.body,{childList:true});
    const animate=icon.animate.bind(icon);
    icon.animate=(...args)=>{window.bumps++;return animate(...args)};
  });
  return page;
}
const tile=(page,id)=>page.locator(`main figure[data-asset="${id}"]`);
const gone=page=>page.waitForFunction(()=>!document.querySelector('.binfly'),null,{timeout:3000});

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await open(browser);
  const bin=await page.locator('#side a[href="/bin"] .icon').boundingBox();
  const target={x:bin.x+bin.width/2,y:bin.y+bin.height/2};

  // X on one photo: one dot, from the tile to the Bin, over a low arc.
  const from=await tile(page,2).boundingBox();
  await tile(page,2).focus();
  await page.keyboard.press('x');
  await page.waitForTimeout(260);
  if(shots)await page.screenshot({path:`${shots}/bin-flight.png`});
  await gone(page);
  let flights=await page.evaluate(()=>window.flights);
  assert.equal(flights.length,1,'one dot for one photo');
  const path=flights[0];
  assert.ok(Math.abs(path[0].x-(from.x+from.width/2))<from.width/2,'it starts on the tile');
  assert.ok(path.some(point=>point.w<=16),'it draws in to a dot');
  const nearest=Math.min(...path.map(point=>Math.hypot(point.x-target.x,point.y-target.y)));
  assert.ok(nearest<14,`it reaches the Bin, within ${nearest}px`);
  const start=path.find(point=>point.w<=16);
  assert.ok(Math.min(...path.map(point=>point.y))<Math.min(start.y,target.y)-4,'it rises before it drops in');
  assert.ok(await page.evaluate(()=>window.bumps)>=1,'the Bin bumps as it lands');
  assert.equal(await tile(page,2).getAttribute('class').then(name=>name.includes('culled')),true,'the photo is removed at once');

  // A selection of two sends two dots.
  await tile(page,3).hover();
  await tile(page,3).getByRole('checkbox').click();
  await tile(page,4).getByRole('checkbox').click();
  await page.getByRole('toolbar',{name:'Selection'}).getByRole('button',{name:'Remove'}).click();
  await gone(page);
  flights=await page.evaluate(()=>window.flights);
  assert.equal(flights.length,3,'one dot for each selected photo');

  // From the viewer, which covers the sidebar, nothing flies.
  await tile(page,1).click();
  await page.getByRole('dialog',{name:'Photo review'}).waitFor();
  await page.waitForTimeout(400);
  await page.keyboard.press('x');
  await page.waitForTimeout(500);
  assert.equal((await page.evaluate(()=>window.flights)).length,3,'no dot from inside the viewer');
  await page.close();

  // Reduced motion: the photo is removed with no dot.
  const still=await open(browser,{reducedMotion:'reduce'});
  await tile(still,2).focus();
  await still.keyboard.press('x');
  await still.waitForTimeout(500);
  assert.equal((await still.evaluate(()=>window.flights)).length,0,'no dot with reduced motion');
  assert.equal(await tile(still,2).getAttribute('class').then(name=>name.includes('culled')),true);
  await browser.close();
  console.log('bin flight: ok');
})().catch(error=>{console.error(error);process.exit(1)});
