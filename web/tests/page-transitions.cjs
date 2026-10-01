const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Moving between pages keeps the frame: the top bar and the sidebar stay as
// they are, the panel shows a branded loading rail while the next page is read, and only the
// panel changes. Back returns to where the last page was scrolled, closing a
// photo does not read its page again, and a choice still being saved lands
// before the next page is read.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const make=id=>({id,path:`/archive/2010/2010-09/2010-09-07/IMG_${id}.JPG`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,
  status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07',width:4032,height:3024});
const assets=Array.from({length:80},(_,index)=>make(index+1));
const cell=(md,dom)=>({md,dom,years:1,files:3,done:0,waiting:3,state:'todo'});
const year={months:[{name:'September',cells:[cell('09-06',6),cell('09-07',7),cell('09-08',8)]}],prog:{dates:3,done:0,part:0,filesDone:0,files:9},today:'09-07',streak:0,week:{days:0,seconds:0}};
const square='<svg xmlns="http://www.w3.org/2000/svg" width="64" height="48"><rect width="64" height="48" fill="#6b7f95"/></svg>';

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  const log=[];
  let holdYear=null,holdDecision=null;
  await page.route('**/api/**',async route=>{
    const url=new URL(route.request().url());
    log.push(url.pathname);
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:80,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:3,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:assets.length,bytes:assets.length*100,status:'pending',assets}],memories:assets.length,bytes:assets.length*100}});
    if(url.pathname==='/api/year'){if(holdYear)await holdYear;return route.fulfill({json:year})}
    if(url.pathname==='/api/decisions'){
      const body=route.request().postDataJSON();
      if(holdDecision)await holdDecision;
      log.push('decision saved');
      return route.fulfill({json:{revision:body.expectedRevision+1,previousStatus:'unreviewed',previousFavourite:false}});
    }
    if(url.pathname==='/api/log')return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:square});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const side=page.getByRole('navigation',{name:'Main navigation'});
  const reloads=()=>page.evaluate(()=>window.__loaded);
  await page.addInitScript(()=>{window.__loaded=(window.__loaded??0)+1});

  await page.goto(`${base}/on/09-07`);
  await page.locator('main .jgrid figure').first().waitFor();
  await page.evaluate(()=>{document.querySelector('.gbar').dataset.frame='kept';document.querySelector('#side').dataset.frame='kept'});

  // The next page is read while the frame stays, with a branded loading rail in the panel.
  let release;
  holdYear=new Promise(resolve=>{release=resolve});
  await page.evaluate(()=>scrollTo(0,900));
  const scrolled=await page.evaluate(()=>scrollY);
  assert.ok(scrolled>800,'the day scrolls');
  await side.getByRole('link',{name:'Year'}).click();
  const loader=page.locator('.navigationload');
  await loader.getByText('Opening Year…').waitFor();
  assert.equal(new URL(page.url()).pathname,'/year');
  assert.equal(await page.locator('main .jgrid').count(),1,'the day stays visible while the next page loads');
  assert.equal(await page.locator('.pagestage').evaluate(node=>node.inert),true,'the previous page cannot receive stale actions');
  assert.equal(await loader.locator('.busy-mark .tile').count(),3,'the logo tiles replace the orb');
  assert.equal(await loader.locator('canvas').count(),0,'no canvas loader');
  assert.equal(await loader.locator('.progress-track.indeterminate').count(),1,'unknown progress uses the shared rail');
  assert.equal(await side.getByRole('link',{name:'Year'}).getAttribute('aria-current'),'page','the sidebar says where it is going at once');
  assert.equal(await page.evaluate(()=>scrollY),scrolled,'the old page keeps its place until the new one is ready');
  assert.equal(await page.locator('.pageacts button').first().isDisabled(),true,'old page actions stay disabled during the wait');
  await page.waitForFunction(()=>{const status=document.querySelector('.navigationload');return status&&Number(getComputedStyle(status).opacity)>.99});
  if(shots)await page.screenshot({path:`${shots}/page-loading.png`});
  release();holdYear=null;
  await page.locator('main .cell').first().waitFor();
  assert.equal(await page.title(),'Year · Daddy, Cull!');
  assert.deepEqual(await page.evaluate(()=>[document.querySelector('.gbar').dataset.frame,document.querySelector('#side').dataset.frame]),['kept','kept'],'the frame was not drawn again');
  assert.equal(await reloads(),1,'no page load');

  // Back returns to the day where it was scrolled.
  await page.goBack();
  await page.locator('main .jgrid figure').first().waitFor();
  await page.waitForFunction(want=>Math.abs(scrollY-want)<2,scrolled);
  assert.equal(new URL(page.url()).pathname,'/on/09-07');
  assert.equal(await side.getByRole('link',{name:'Today'}).getAttribute('aria-current'),'page');
  assert.equal(await reloads(),1);

  // Opening and closing a photo leaves the page as it is.
  await page.evaluate(()=>{document.querySelector('main .jgrid').dataset.mark='same'});
  await page.locator('main .jgrid figure').nth(2).click();
  await page.getByRole('dialog',{name:'Photo review'}).waitFor();
  await page.keyboard.press('Escape');
  await page.getByRole('dialog',{name:'Photo review'}).waitFor({state:'detached'});
  await page.waitForTimeout(300);
  assert.equal(await page.evaluate(()=>document.querySelector('main .jgrid')?.dataset.mark),'same','the day was not read again');

  // A choice still on its way lands before the next page is read.
  let saved;
  holdDecision=new Promise(resolve=>{saved=resolve});
  await page.evaluate(()=>scrollTo(0,0));
  await page.locator('main .jgrid figure').first().focus();
  await page.keyboard.press('x');
  log.length=0;
  await side.getByRole('link',{name:'Year'}).click();
  await page.waitForTimeout(400);
  assert.ok(!log.includes('/api/year'),`the year waits for the save: ${log.join(', ')}`);
  saved();holdDecision=null;
  await page.locator('main .cell').first().waitFor();
  assert.ok(log.indexOf('decision saved')<log.indexOf('/api/year'),`saved first: ${log.join(', ')}`);

  // Typing a date moves there the same way.
  await page.getByRole('search').getByRole('combobox').fill('7 Sep');
  await page.keyboard.press('Enter');
  await page.locator('main .jgrid figure').first().waitFor();
  assert.equal(new URL(page.url()).pathname,'/on/09-07');
  // A link with a modifier is the browser's, for a new tab.
  const [popup]=await Promise.all([page.context().waitForEvent('page'),side.getByRole('link',{name:'Year'}).click({modifiers:[process.platform==='darwin'?'Meta':'Control']})]);
  await popup.close();
  assert.equal(new URL(page.url()).pathname,'/on/09-07','the tab stayed where it was');
  assert.equal(await reloads(),1,'still the first page load');
  // A slow request from an abandoned destination must never replace the
  // later choice or leave the retained page inert.
  holdYear=new Promise(resolve=>{release=resolve});
  await side.getByRole('link',{name:'Year'}).click();
  await page.locator('.navigationload').getByText('Opening Year…').waitFor();
  await side.getByRole('link',{name:'Log',exact:true}).click();
  await page.getByRole('heading',{name:'Log',exact:true}).waitFor();
  release();holdYear=null;
  await page.waitForTimeout(650);
  assert.equal(new URL(page.url()).pathname,'/log');
  assert.equal(await page.locator('main .cell').count(),0,'the abandoned year never appears');
  assert.equal(await page.locator('.pagestage').evaluate(node=>node.inert),false);
  assert.equal(await page.locator('.navigationload').count(),0);
  await browser.close();
  console.log('page transitions: ok');
})().catch(error=>{console.error(error);process.exit(1)});
