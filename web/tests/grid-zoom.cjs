const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Cmd+ and Cmd- change the thumbnails, not the page, on pages with a grid;
// Cmd 0 puts them back, the size is remembered, and the tile in the middle of
// the screen stays there. Settings has no grid and keeps the browser's zoom.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shot=(id,kept)=>({id,path:`/screenshots/2024-05-${String(id%28+1).padStart(2,'0')}_shot${id}.${id%10===0?'mov':'png'}`,capturedAt:0,kind:id%10===0?'video':'image',source:'screenshots',size:1000,
  status:kept?'keep':'unreviewed',favourite:false,revision:kept?1:0,alternativeCount:0,relatedCount:0,day:`2024-05-${String(id%28+1).padStart(2,'0')}`,name:`shot${id}.png`,state:'waiting',...(id%3===0?{width:1920,height:1080}:{width:1170,height:2532})});
const shots=[...Array.from({length:250},(_,index)=>shot(index+1,false)),...Array.from({length:5},(_,index)=>shot(1001+index,true))];
const clips=[];
const svg='<svg xmlns="http://www.w3.org/2000/svg" width="160" height="90"><rect width="160" height="90" fill="#6b7f95"/></svg>';

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  const asked=[];
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:1,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/screenshots'){
      const review=url.searchParams.get('review'),kind=url.searchParams.get('kind'),from=Number(url.searchParams.get('from'));
      asked.push(`shots ${review}/${kind}/${from}`);
      const ofKind=shots.filter(item=>!kind||item.kind===kind);
      const onSide=item=>review==='all'||(review==='reviewed')===(item.status==='keep');
      const matching=ofKind.filter(onSide);
      return route.fulfill({json:{items:matching.slice(from,from+120),total:matching.length,bytes:matching.length*1000,
        unreviewed:ofKind.filter(item=>item.status!=='keep').length,reviewed:ofKind.filter(item=>item.status==='keep').length,
        stills:shots.filter(onSide).filter(item=>item.kind==='image').length,recordings:shots.filter(onSide).filter(item=>item.kind==='video').length}});
    }
    if(url.pathname==='/api/social'){
      const band=url.searchParams.get('band'),from=Number(url.searchParams.get('from'));
      asked.push(`social ${band}/${from}`);
      const matching=clips.filter(item=>!band||(band==='social')===(item.band==='likely'));
      const social=clips.filter(item=>item.band==='likely').length;
      return route.fulfill({json:{items:matching.slice(from,from+120),total:clips.length,shown:matching.length,bytes:clips.length*5000,likely:social,possible:clips.length-social,watch:0,letterboxed:0,social,unsure:clips.length-social,kept:0,marked:0}});
    }
    if(url.pathname.startsWith('/api/media/')||url.pathname.startsWith('/api/social-poster/'))return route.fulfill({contentType:'image/svg+xml',body:svg});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const mod=process.platform==='darwin'?'Meta':'Control';
  const tiles=page.locator('main .rt');
  // How tall the rows are: the grid's first full row.
  // The grid lays itself out again a frame or two after the zoom changes, so
  // the height is read once it has held still for a few frames.
  const width=async()=>page.evaluate(()=>new Promise(resolve=>{
    let last=-1,still=0;
    const look=()=>{
      const height=document.querySelector('main .jrow').getBoundingClientRect().height;
      still=height===last?still+1:0;last=height;
      if(still>=3)resolve(height);else requestAnimationFrame(look);
    };
    look();
  }));
  const snack=page.locator('#snacks .snack');
  // Whether the browser was left to handle the key, read after every listener.
  await page.addInitScript(()=>{window.addEventListener('keydown',event=>{if(event.metaKey||event.ctrlKey)window.lastZoomKey=event})});

  await page.goto(`${base}/screenshots`);
  await tiles.nth(5).waitFor();
  await page.waitForTimeout(300);
  const normal=await width();
  await page.keyboard.press(`${mod}+Equal`);
  await page.waitForFunction(()=>getComputedStyle(document.documentElement).getPropertyValue('--zoom').trim()==='1.25');
  assert.ok(await page.evaluate(()=>window.lastZoomKey?.defaultPrevented),'the browser does not zoom the page');
  assert.equal(await page.evaluate(()=>window.devicePixelRatio),1,'the page itself is not zoomed');
  const bigger=await width();
  assert.ok(bigger>normal*1.1,`rows grow: ${normal} to ${bigger}`);
  assert.equal(await snack.innerText(),`Thumbnails at 125% · ${mod==='Meta'?'⌘0':'Ctrl+0'} resets`);
  await page.keyboard.press(`${mod}+Equal`);
  await page.keyboard.press(`${mod}+Equal`);
  await page.waitForFunction(()=>getComputedStyle(document.documentElement).getPropertyValue('--zoom').trim()==='1.75');

  await page.reload();
  await tiles.nth(5).waitFor();
  assert.equal(await page.evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--zoom').trim()),'1.75','remembered');
  await page.keyboard.press(`${mod}+Digit0`);
  await page.waitForFunction(()=>getComputedStyle(document.documentElement).getPropertyValue('--zoom').trim()==='1');
  assert.equal(await snack.innerText(),'Thumbnails at 100%');
  await page.waitForTimeout(200);
  assert.ok(Math.abs(await width()-normal)<2,'Cmd 0 puts the size back');
  for(let i=0;i<8;i++)await page.keyboard.press(`${mod}+Minus`);
  await page.waitForFunction(()=>getComputedStyle(document.documentElement).getPropertyValue('--zoom').trim()==='0.5');
  assert.equal(await snack.innerText(),'Smallest thumbnails · '+(mod==='Meta'?'⌘0':'Ctrl+0')+' resets');
  assert.ok(await width()<normal*.7,'rows shrink');
  await page.keyboard.press(`${mod}+Digit0`);

  // The tile in the middle of the screen stays in the middle.
  await page.evaluate(()=>window.scrollTo(0,1600));
  await page.waitForTimeout(200);
  const middle=await page.evaluate(()=>{
    const box=document.querySelector('main').getBoundingClientRect(),x=box.left+box.width/2,y=innerHeight/2;
    const near=rect=>Math.max(rect.left-x,0,x-rect.right)**2+Math.max(rect.top-y,0,y-rect.bottom)**2;
    const tile=[...document.querySelectorAll('main figure')].toSorted((a,b)=>near(a.getBoundingClientRect())-near(b.getBoundingClientRect()))[0];
    tile.dataset.mark='1';
    return tile.getBoundingClientRect().top;
  });
  await page.keyboard.press(`${mod}+Equal`);
  await page.waitForFunction(()=>getComputedStyle(document.documentElement).getPropertyValue('--zoom').trim()==='1.25');
  await page.waitForTimeout(100);
  const after=await page.evaluate(()=>document.querySelector('[data-mark]').getBoundingClientRect().top);
  assert.ok(Math.abs(after-middle)<2,`the middle tile stays put: ${middle} then ${after}`);
  await page.keyboard.press(`${mod}+Digit0`);

  // A photo open over the grid is looked at, not the grid: the browser zooms it.
  await tiles.nth(2).locator('img').first().click();
  await page.getByRole('dialog',{name:/Photo review|Preview of/}).waitFor();
  await page.keyboard.press(`${mod}+Equal`);
  await page.waitForTimeout(100);
  assert.equal(await page.evaluate(()=>window.lastZoomKey?.defaultPrevented),false,'the viewer leaves Cmd+ to the browser');
  assert.equal(await page.evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--zoom').trim()),'1');
  await page.keyboard.press('Escape');

  // No grid, no takeover.
  await page.goto(`${base}/settings`);
  await page.locator('a[href="/settings"][aria-current="page"]').waitFor();
  await page.keyboard.press(`${mod}+Equal`);
  await page.waitForTimeout(100);
  assert.equal(await page.evaluate(()=>window.lastZoomKey?.defaultPrevented),false,'Settings leaves Cmd+ to the browser');
  assert.equal(await snack.count(),0);
  await browser.close();
  console.log('grid zoom: ok');
})().catch(error=>{console.error(error);process.exit(1)});
