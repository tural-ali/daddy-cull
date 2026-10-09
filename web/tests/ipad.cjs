const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Cull on an iPad held sideways, driven by a finger: a hold selects a photo
// and its lift opens nothing, a tap then adds one, and with nothing selected a
// tap opens the review, where a swipe turns to the next photo and a pull down
// goes back. The hints say tap, the ticks stay out of the way until something
// is selected, and the keyboard's ring is left to the keyboard. Upright, the
// app asks to be turned. A Log tile keeps its verb clear of its action. Every
// name is a synthetic fixture.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const photo=id=>({id,path:`/archive/2010/2010-09/2010-09-07/P${id}.JPG`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,
  status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07',width:4032,height:3024});
const ids=[1,2,3,4,5,6];
const event=(index,where)=>({requestId:`r${index}`,status:index%2?'cull':'keep',favourite:false,previousStatus:'unreviewed',previousFavourite:false,
  createdAt:new Date(Date.UTC(2026,8,27,9,0,0)-index*60000).toISOString(),...(where?{where}:{}),
  asset:{id:index+1,path:`/archive/2026/2026-09/2026-09-27/IMG_${index}.JPG`,capturedAt:1,kind:'image',source:'archive',size:100,status:index%2?'cull':'keep',favourite:false,revision:1,alternativeCount:0,relatedCount:0}});

async function open(browser,path,{touch=true,viewport={width:1180,height:820}}={}){
  const context=await browser.newContext({viewport,deviceScaleFactor:2,hasTouch:touch,isMobile:touch});
  const page=await context.newPage();
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  const writes=[];
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(route.request().method()!=='GET'){writes.push(url.pathname);return route.fulfill({json:{}})}
    if(url.pathname==='/api/catalogue')return route.fulfill({json:{generation:1}});
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:6,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:6,bytes:600,status:'pending',assets:ids.map(photo)}],memories:6,bytes:600}});
    if(url.pathname==='/api/log')return route.fulfill({json:[event(0),event(1,'deleting'),event(2),event(3,'bin'),event(4),event(5)]});
    if(url.pathname==='/api/trash/deleting')return route.fulfill({json:{graceDays:30,items:[],lastRun:'',lastDeleted:0,lastError:'',checkIntervalMinutes:15}});
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300"><rect width="400" height="300" fill="#5b6b7d"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await page.goto(`${base}${path}`);
  return {page,writes};
}

// A finger, through the same touch events an iPad sends.
async function finger(page){
  const cdp=await page.context().newCDPSession(page);
  const send=(type,points)=>cdp.send('Input.dispatchTouchEvent',{type,touchPoints:points.map(([x,y])=>({x,y}))});
  const centre=async locator=>{await locator.scrollIntoViewIfNeeded();const box=await locator.boundingBox();return [box.x+box.width/2,box.y+box.height/2]};
  return {
    async tap(locator){const [x,y]=await centre(locator);await page.touchscreen.tap(x,y)},
    async hold(locator){const at=await centre(locator);await send('touchStart',[at]);await page.waitForTimeout(700);await send('touchEnd',[])},
    async drag(locator,dx,dy){
      const [x,y]=await centre(locator);
      await send('touchStart',[[x,y]]);
      for(let step=1;step<=12;step++){await send('touchMove',[[x+dx*step/12,y+dy*step/12]]);await page.waitForTimeout(16)}
      await send('touchEnd',[]);
    },
  };
}

async function day(browser){
  const {page,writes}=await open(browser,'/on/09-07');
  const tile=id=>page.locator(`main figure[data-asset="${id}"]`);
  await tile(6).waitFor();
  assert.deepEqual(await page.evaluate(()=>[matchMedia('(hover: none)').matches,matchMedia('(pointer: coarse)').matches]),[true,true],'the page is driven by a finger');
  assert.match(await page.locator('.snack').innerText(),/^Tap a photo to review it\. Swipe to the next, pull down to go back, and hold a photo to select it\./,'the hint is for a finger');
  assert.equal(await tile(1).locator('.pick').evaluate(pick=>getComputedStyle(pick).pointerEvents),'none','with nothing selected the ticks are out of reach');
  const picked=()=>page.evaluate(()=>[...document.querySelectorAll('main figure.picked')].map(figure=>Number(figure.dataset.asset)));
  const viewing=()=>page.locator('.rv.on').count();
  const touch=await finger(page);
  const start=page.url();

  await touch.hold(tile(2));
  await page.waitForTimeout(400);
  assert.deepEqual(await picked(),[2],'a hold selects the photo');
  assert.equal(await viewing(),0,'and its lift opens nothing');
  assert.equal(await page.locator('.snack',{hasText:'Tap a photo'}).count(),0,'the tip on what a tap does goes, as a tap now adds');
  await touch.tap(tile(4));
  await page.waitForTimeout(300);
  assert.deepEqual(await picked(),[2,4],'while selecting, a tap adds a photo');
  assert.equal(await viewing(),0);
  assert.equal(page.url(),start);
  assert.equal(await tile(4).evaluate(figure=>getComputedStyle(figure).outlineStyle),'none','a tapped tile carries no keyboard ring');
  assert.ok(await tile(4).evaluate(figure=>getComputedStyle(figure,'::after').content!=='none'||figure.querySelector('.pick')),'its frame and tick stay');
  if(shots)await page.screenshot({path:`${shots}/ipad-selecting.png`});
  await page.getByRole('button',{name:'Clear the selection'}).click();
  await page.waitForTimeout(300);
  assert.deepEqual(await picked(),[]);

  await touch.tap(tile(3));
  await page.locator('.rv.on').waitFor();
  const shown=()=>page.locator('.rvstage > img:not(.rvpeek)').first().getAttribute('src');
  await page.waitForTimeout(500);
  assert.match(await shown(),/\/api\/media\/3\//,'with nothing selected a tap opens the photo');
  await touch.drag(page.locator('.rvstage'),-420,0);
  await page.waitForTimeout(600);
  assert.match(await shown(),/\/api\/media\/4\//,'a swipe to the left turns to the next photo');
  await touch.drag(page.locator('.rvstage'),360,0);
  await page.waitForTimeout(600);
  assert.match(await shown(),/\/api\/media\/3\//,'and to the right turns back');
  await touch.drag(page.locator('.rvstage'),0,300);
  await page.waitForFunction(()=>!document.querySelector('.rv.on'),null,{timeout:3000});
  assert.equal(await page.evaluate(()=>window.visualViewport?.scale??1),1,'nothing zoomed the page');
  assert.deepEqual(writes,[],'looking decides nothing');
  await page.context().close();
}

async function upright(browser){
  const {page}=await open(browser,'/on/09-07',{viewport:{width:820,height:1180}});
  const prompt=page.locator('.landscape',{hasText:'Turn your iPad sideways'});
  await prompt.waitFor();
  assert.ok(await prompt.isVisible(),'upright, the app asks to be turned');
  assert.equal(await page.evaluate(()=>getComputedStyle(document.getElementById('root')).visibility),'hidden');
  if(shots)await page.screenshot({path:`${shots}/ipad-upright.png`});
  await page.setViewportSize({width:1180,height:820});
  await page.locator('main figure[data-asset="6"]').waitFor();
  assert.equal(await prompt.isVisible(),false,'sideways, it is out of the way');
  await page.context().close();
}

async function log(browser){
  const {page}=await open(browser,'/log');
  await page.locator('.logtile[data-asset="6"]').waitFor();
  const boxes=await page.evaluate(()=>[...document.querySelectorAll('.logtile')].map(tile=>{
    const verb=tile.querySelector('.bdg').getBoundingClientRect();
    const act=tile.querySelector('.acts')?.getBoundingClientRect();
    const caption=tile.querySelector('figcaption').getBoundingClientRect();
    return {id:tile.dataset.asset,verb:[verb.left,verb.top,verb.right,verb.bottom],act:act&&[act.left,act.top,act.right,act.bottom],caption:caption.top,
      clipped:tile.querySelector('.bdg .b').scrollWidth>tile.querySelector('.bdg .b').clientWidth};
  }));
  const overlap=(a,b)=>a[0]<b[2]&&b[0]<a[2]&&a[1]<b[3]&&b[1]<a[3];
  for(const box of boxes){
    assert.equal(box.clipped,false,`tile ${box.id}: the verb is shown in full`);
    assert.ok(box.act,`tile ${box.id}: a finger sees its action without hovering`);
    assert.equal(overlap(box.verb,box.act),false,`tile ${box.id}: the action sits clear of the verb`);
    assert.ok(box.act[3]<=box.caption,`tile ${box.id}: and clear of the caption`);
  }
  if(shots)await page.screenshot({path:`${shots}/ipad-log.png`});
  await page.context().close();
}

// With a keyboard on a desktop the place still shows, as a ring that sits
// over the photo rather than under it.
async function keyboard(browser){
  const {page}=await open(browser,'/on/09-07',{touch:false,viewport:{width:1280,height:800}});
  await page.locator('main figure[data-asset="6"]').waitFor();
  await page.locator('main').click({position:{x:5,y:5}});
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('ArrowRight');
  const ring=await page.evaluate(()=>{const figure=document.querySelector('main figure.sel');const style=figure&&getComputedStyle(figure);return figure&&{id:figure.dataset.asset,focused:document.activeElement===figure,outline:style.outlineStyle,width:style.outlineWidth,offset:style.outlineOffset}});
  assert.ok(ring,'an arrow key gives the keyboard a place');
  assert.equal(ring.focused,true,'focus goes with it');
  assert.deepEqual([ring.outline,ring.width,ring.offset],['solid','3px','-3px'],'shown as a ring over the photo');
  await page.context().close();
}

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  await day(browser);
  await upright(browser);
  await log(browser);
  await keyboard(browser);
  await browser.close();
  console.log('ipad: ok');
})().catch(error=>{console.error(error);process.exit(1)});
