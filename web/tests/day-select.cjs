const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Many photos at once on a date, as in Google Photos: a tick shows on the
// tile under the pointer, and once one is ticked every tile shows its tick and
// a click adds a tile rather than opening it. The bar then turns, favourites
// and removes them together, each one step to undo. A photo can be turned in
// the viewer too, and the turn is Cull's own. Every name is a synthetic fixture.
const photo=(id,name)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,
  status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07',width:4032,height:3024});
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const picture='<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300"><rect width="400" height="300" fill="#8c6e5a"/><rect width="400" height="60" fill="#d9c3a5"/></svg>';

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  const batches=[],turns=[],turned=new Map();
  await page.route('**/api/**',route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:3,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:3,bytes:300,status:'pending',assets:[photo(1,'ONE.JPG'),photo(2,'TWO.JPG'),{...photo(3,'THREE.JPG'),relatedCount:1}]}],memories:3,bytes:300}});
    if(url.pathname==='/api/decisions/batch'){const body=request.postDataJSON();batches.push(body);return route.fulfill({json:body.map(change=>({revision:change.expectedRevision+1,previousStatus:'unreviewed',previousFavourite:false}))})}
    if(url.pathname==='/api/turns'){
      const body=request.postDataJSON();turns.push(body);
      for(const id of body.ids)turned.set(id,(((turned.get(id)??0)+body.quarters)%4+4)%4);
      return route.fulfill({json:{turns:Object.fromEntries(body.ids.map(id=>[id,turned.get(id)]))}});
    }
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:picture});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const tile=id=>page.locator(`main figure[data-asset="${id}"]`);
  const tick=id=>tile(id).getByRole('checkbox');
  const opacity=locator=>locator.evaluate(node=>getComputedStyle(node).opacity);
  const settle=()=>page.evaluate(()=>document.getAnimations().forEach(animation=>{try{animation.finish()}catch{}}));
  const bar=page.getByRole('toolbar',{name:'Selection'});
  const wait=async(list,n)=>{for(let i=0;i<100&&list.length<n;i++)await page.waitForTimeout(30);assert.equal(list.length,n)};
  const shape=async id=>{const box=await tile(id).boundingBox();return box.width/box.height};

  await page.goto(`${base}/on/09-07`);
  await page.waitForFunction(()=>[...document.querySelectorAll('main figure img')].every(img=>img.complete&&img.naturalWidth>0));

  // A tile has no Remove button now; its heart shows in the bottom-left
  // corner under the pointer only.
  assert.equal(await tile(1).getByRole('button',{name:'Remove'}).count(),0);
  await page.mouse.move(5,400);
  await settle();
  assert.equal(await opacity(tick(1)),'0','no tick before the pointer comes');
  assert.equal(await opacity(tile(1).getByRole('button',{name:'Favourite'})),'0','nor a heart');
  await tile(1).hover();
  await settle();
  assert.equal(await opacity(tick(1)),'1','the tick shows on the tile under the pointer');
  assert.equal(await opacity(tick(2)),'0','and only there');
  const [corner,heart]=await Promise.all([tile(1).boundingBox(),tile(1).getByRole("button",{name:"Favourite"}).boundingBox()]);
  assert.equal(await opacity(tile(1).getByRole('button',{name:'Favourite'})),'1');
  assert.ok(heart.x-corner.x<12&&corner.y+corner.height-(heart.y+heart.height)<12,'the heart sits in the bottom-left corner');
  // A badge has the top-right corner, clear of the tick, and stays put on hover.
  const badge=tile(3).locator('.b.dupe');
  const place=async()=>{const [frame,box]=await Promise.all([tile(3).boundingBox(),badge.boundingBox()]);return {right:Math.round(frame.x+frame.width-(box.x+box.width)),top:Math.round(box.y-frame.y)}};
  const resting=await place();
  assert.ok(resting.right<=12&&resting.top<=12,`the duplicate badge sits top right: ${JSON.stringify(resting)}`);
  await tile(3).hover();
  await settle();
  assert.deepEqual(await place(),resting,'and does not move for the tick');
  await tile(1).hover();
  await settle();
  if(shots)await page.screenshot({path:`${shots}/day-select-hover.png`,clip:{x:256,y:120,width:1024,height:420}});

  // One tick starts a selection: every tick shows, and a click on a tile adds it.
  await tick(1).click();
  await bar.waitFor();
  await settle();
  assert.equal(await opacity(tick(3)),'1','every tile shows its tick while selecting');
  await tile(2).click();
  assert.equal(await page.getByRole('dialog',{name:'Photo review'}).count(),0,'a click adds the tile rather than opening it');
  assert.equal(await tick(2).getAttribute('aria-checked'),'true');
  assert.match(await bar.innerText(),/2 selected/);
  for(const name of ['Rotate clockwise','Favourite','Remove'])await bar.getByRole('button',{name}).waitFor();
  await page.waitForTimeout(300);
  assert.ok((await tile(2).locator('.actual-media').boundingBox()).width<(await tile(2).boundingBox()).width-10,'a picked tile draws its picture inside a frame');
  if(shots)await page.screenshot({path:`${shots}/day-select-bar.png`,clip:{x:0,y:0,width:1280,height:520}});

  // Rotate turns both, and the tiles take the turned shape.
  assert.ok(await shape(1)>1,'a landscape tile to begin with');
  await bar.getByRole('button',{name:'Rotate clockwise'}).click();
  await wait(turns,1);
  assert.deepEqual(turns[0],{ids:[1,2],quarters:1});
  await page.waitForFunction(()=>document.querySelector('main figure[data-asset="1"]')?.dataset.turn==='1');
  assert.ok(await shape(1)<1,'the turned tile stands upright');
  assert.ok(await shape(3)>1,'a tile not selected is left as it was');
  const media=await tile(1).locator('.actual-media').evaluate(node=>{const box=node.getBoundingClientRect(),own=node.parentElement.getBoundingClientRect();return {w:box.width,h:box.height,tw:own.width,th:own.height}});
  // Picked tiles draw their picture a little smaller, inside the frame.
  assert.ok(Math.abs(media.w-media.tw*.86)<1.5&&Math.abs(media.h-media.th*.86)<1.5,`the turned picture fills its tile: ${JSON.stringify(media)}`);
  if(shots)await page.screenshot({path:`${shots}/day-select-turned.png`,clip:{x:0,y:0,width:1280,height:520}});
  if(shots)await page.screenshot({path:`${shots}/day-select-tick.png`,clip:{x:450,y:250,width:80,height:60},scale:'device'});

  // Favourite both, one step to undo.
  await bar.getByRole('button',{name:'Favourite'}).click();
  await wait(batches,1);
  assert.deepEqual(batches[0].map(change=>[change.assetId,change.favourite]),[[1,true],[2,true]]);
  await bar.getByRole('button',{name:'Remove from favourites'}).waitFor();
  assert.equal(await tile(1).locator('.tfav').getAttribute('aria-pressed'),'true');
  // Pressed at once, before the server has confirmed the batch, it still counts.
  await page.keyboard.press('Meta+z');
  await wait(batches,2);
  assert.deepEqual(batches[1].map(change=>[change.assetId,change.favourite]),[[1,false],[2,false]],'one undo takes both hearts back');
  // Undo before that takes the turn back.
  await page.keyboard.press('Meta+z');
  await wait(turns,2);
  assert.deepEqual(turns[1],{ids:[1,2],quarters:-1});
  await page.waitForFunction(()=>!document.querySelector('main figure[data-asset="1"]')?.dataset.turn);

  // Remove both from the bar, and the selection ends.
  await bar.getByRole('button',{name:'Remove'}).click();
  await wait(batches,3);
  assert.deepEqual(batches[2].map(change=>[change.assetId,change.status]),[[1,'cull'],[2,'cull']]);
  await bar.waitFor({state:'detached'});
  await page.keyboard.press('Meta+z');
  await wait(batches,4);
  assert.deepEqual(batches[3].map(change=>[change.assetId,change.status]),[[1,'unreviewed'],[2,'unreviewed']]);

  // Esc ends a selection without doing anything.
  await tile(3).hover();
  await tick(3).click();
  await bar.waitFor();
  await page.keyboard.press('Escape');
  await bar.waitFor({state:'detached'});

  // The viewer turns the photo on show with its button and with ] and [.
  await tile(3).click();
  const viewer=page.getByRole('dialog',{name:'Photo review'});
  await viewer.waitFor();
  await viewer.getByRole('button',{name:'Rotate clockwise'}).click();
  await wait(turns,3);
  assert.deepEqual(turns[2],{ids:[3],quarters:1});
  const stage=viewer.locator('.rvstage img');
  await page.waitForFunction(()=>document.querySelector('.rvstage img')?.dataset.turn==='1');
  const [shown,room]=await Promise.all([stage.boundingBox(),viewer.locator('.rvstage').boundingBox()]);
  assert.ok(shown.height>shown.width,'the turned photo stands upright');
  assert.ok(shown.height<=room.height+1&&shown.width<=room.width+1,'and fits the stage');
  if(shots){await page.waitForTimeout(1600);await page.screenshot({path:`${shots}/day-select-viewer.png`})}
  await page.keyboard.press(']');
  await wait(turns,4);
  await page.keyboard.press('[');
  await wait(turns,5);
  assert.deepEqual(turns.slice(3).map(turn=>turn.quarters),[1,-1]);
  await page.waitForFunction(()=>document.querySelector('.rvstage img')?.dataset.turn==='1');
  await page.keyboard.press('Escape');
  await viewer.waitFor({state:'detached'});
  assert.equal(await tile(3).getAttribute('data-turn'),'1','the grid shows the turn too');
  await browser.close();
  console.log('day select: ok');
})().catch(error=>{console.error(error);process.exit(1)});
