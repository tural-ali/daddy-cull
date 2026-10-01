const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const {common}=require('./lib/addons.cjs');

// Each page opens with a guide to how it works. Got it hides that page's
// guide in every browser, as the catalogue keeps it, the question mark brings
// it back, and Settings shows every hidden guide again. On a phone the guide still shows, without
// the page scrolling sideways. Every name is a synthetic fixture.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const photo=(id,name)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,
  status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07',width:4032,height:3024});
const picture='<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300"><rect width="400" height="300" fill="#6b7f95"/></svg>';
const stats={total:2,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0,legacyBin:0,shadowGroups:0,screenshots:0,upgradesAccepted:0,upgradeCandidates:0,bin:0,notifications:0};

// The catalogue's own copy of which guides are hidden, shared by every
// browser the test opens, as one Cull is by every device.
const server=new Set();

async function open(browser,viewport={width:1280,height:800},theme='night',stored){
  const page=await browser.newPage({viewport});
  await page.addInitScript(([key,chosen,hidden])=>{
    const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`);
    localStorage.setItem('cull-theme',chosen);
    if(hidden&&!sessionStorage.getItem('seeded')){localStorage.setItem('cull-guides-hidden',JSON.stringify(hidden));sessionStorage.setItem('seeded','1')}
  },['cull.streak-intro',theme,stored]);
  const errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  await page.route('**/api/**',route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{...stats,hiddenGuides:[...server].sort((a,b)=>a.localeCompare(b))}});
    if(url.pathname==='/api/settings/guides'){
      const body=request.postDataJSON();
      if(!body.page)server.clear();else if(body.hidden)server.add(body.page);else server.delete(body.page);
      return route.fulfill({json:body});
    }
    if(url.pathname==='/api/catalogue')return route.fulfill({json:{generation:1}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:2,bytes:200,status:'pending',assets:[photo(1,'ONE.JPG'),photo(2,'TWO.JPG')]}],memories:2,bytes:200}});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:picture});
    if(url.pathname==='/api/addons')return route.fulfill({json:[]});
    const found=common(url);
    if(found)return route.fulfill(found);
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  return {page,errors};
}

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const {page,errors}=await open(browser);

  // A first visit shows the page's guide, keys written as keys and read as words.
  await page.goto(`${base}/on/09-07`);
  const guide=page.getByRole('complementary',{name:'How a day works'});
  await guide.waitFor();
  assert.equal(await guide.locator('li').count(),5);
  assert.match(await guide.textContent(),/K\s*K\s*keeps/,'a key is drawn and spoken');
  const help=page.getByRole('button',{name:'How this page works'});
  assert.equal(await help.getAttribute('aria-pressed'),'true');
  if(shots)await page.screenshot({path:`${shots}/guide-today.png`});

  // Got it hides it, for this page, across a reload.
  await guide.getByRole('button',{name:'Got it'}).click();
  await guide.waitFor({state:'detached'});
  assert.equal(await help.getAttribute('aria-pressed'),'false');
  await page.reload();
  await page.locator('.gal, .rows, [data-asset]').first().waitFor();
  assert.equal(await page.locator('.guide').count(),0,'a hidden guide stays hidden');

  // The question mark brings it back, and hides it again.
  await page.getByRole('button',{name:'How this page works'}).click();
  await page.getByRole('complementary',{name:'How a day works'}).waitFor();
  await page.getByRole('button',{name:'How this page works'}).click();
  await page.getByRole('complementary',{name:'How a day works'}).waitFor({state:'detached'});

  // Another page has its own guide, still showing.
  await page.getByRole('link',{name:'Addons'}).click();
  await page.getByRole('complementary',{name:'How addons work'}).waitFor();

  // Settings shows every hidden guide again.
  await page.getByRole('link',{name:'Settings'}).click();
  await page.getByRole('complementary',{name:'What Settings holds'}).waitFor();
  const again=page.getByRole('button',{name:'Show all guides again (1 hidden)'});
  await again.click();
  await page.getByText('1 guide will show again.').waitFor();
  assert.equal(await page.getByRole('button',{name:'Every guide is showing'}).isDisabled(),true);
  await page.getByRole('link',{name:'Today'}).click();
  await page.getByRole('complementary',{name:'How a day works'}).waitFor();
  assert.deepEqual(errors,[]);
  await page.close();

  // Hidden at one address, the guide stays hidden at another, or on another
  // device: a browser that has never seen Cull before never shows it at all.
  {
    const {page:one,errors:problems}=await open(browser);
    await one.goto(`${base}/on/09-07`);
    await one.getByRole('complementary',{name:'How a day works'}).getByRole('button',{name:'Got it'}).click();
    await one.waitForFunction(()=>!document.querySelector('.guide'));
    for(const end=Date.now()+5000;!server.has('today')&&Date.now()<end;)await one.waitForTimeout(25);
    assert.deepEqual([...server],['today'],'the catalogue keeps it');
    const {page:other}=await open(browser);
    let flashed=false;
    await other.exposeFunction('guideShown',()=>{flashed=true});
    await other.addInitScript(()=>new MutationObserver(()=>{if(document.querySelector('.guide'))window.guideShown()}).observe(document,{childList:true,subtree:true}));
    await other.goto(`${base}/on/09-07`);
    await other.locator('.gal figure').first().waitFor();
    await other.waitForTimeout(300);
    assert.equal(flashed,false,'another browser never shows a guide hidden elsewhere');
    assert.equal(await other.getByRole('button',{name:'How this page works'}).getAttribute('aria-pressed'),'false');
    // Another page's guide still shows there.
    await other.getByRole('link',{name:'Addons'}).click();
    await other.getByRole('complementary',{name:'How addons work'}).waitFor();
    assert.deepEqual(problems,[]);
    await one.close();await other.close();
  }

  // A browser that hid a guide before the catalogue kept them sends it there.
  {
    const {page:old}=await open(browser,undefined,'night',['addons']);
    await old.goto(`${base}/addons`);
    await old.getByRole('heading',{name:'Addons'}).first().waitFor();
    for(const end=Date.now()+5000;!server.has('addons')&&Date.now()<end;)await old.waitForTimeout(25);
    assert.deepEqual([...server].sort((a,b)=>a.localeCompare(b)),['addons','today'],'its own hidden guide reaches the catalogue');
    assert.equal(await old.locator('.guide').count(),0);
    await old.close();
    server.clear();
  }

  // A phone shows the guide in the width it has.
  {
    const {page:phone,errors:problems}=await open(browser,{width:375,height:812},'day');
    await phone.goto(`${base}/on/09-07`);
    await phone.getByRole('complementary',{name:'How a day works'}).waitFor();
    const width=await phone.evaluate(()=>document.documentElement.scrollWidth);
    assert.equal(width,375,'no sideways scroll');
    assert.equal(await phone.getByRole('button',{name:'How this page works'}).isVisible(),false,'the top bar has no room for it');
    if(shots)await phone.screenshot({path:`${shots}/guide-today-phone-day.png`});
    assert.deepEqual(problems,[]);
    await phone.close();
  }

  await browser.close();
  console.log('guides: ok');
})().catch(error=>{console.error(error);process.exit(1)});
