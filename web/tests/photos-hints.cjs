const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Once the Mac has sent Apple Photos' scores across, a day page marks the two
// judgements that stood apart from chance: a star where Photos would pick the
// picture for Memories, a cracked frame where it calls the shot failed. The
// filters offer each, and the viewer's Info says why. Every name is a
// synthetic fixture.
const photo=(id,name)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,
  status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07',width:4032,height:3024});
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const picture='<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300"><rect width="400" height="300" fill="#5a6e8c"/></svg>';
const assets=[
  {...photo(1,'ONE.JPG'),hint:{source:'apple-photos',how:'exact',overall:0.71,keep:true,reasons:['edited in Photos','Photos would pick it for Memories'],faces:2,labels:['people','beach'],caption:'two people on a beach'}},
  {...photo(2,'TWO.JPG'),hint:{source:'apple-photos',how:'near',overall:0.2,cull:true,reasons:['Photos marks it a failed shot','dark'],labels:[]}},
  photo(3,'THREE.JPG'),
];

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:3,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:3,bytes:300,status:'pending',assets}],memories:3,bytes:300}});
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.endsWith('/details'))return route.fulfill({json:{located:false,modified:'2026-01-01T00:00:00Z'}});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:picture});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const tile=id=>page.locator(`main figure[data-asset="${id}"]`);
  const tiles=async()=>page.locator('.gal figure').evaluateAll(nodes=>nodes.map(node=>Number(node.dataset.asset)));
  const search=page.getByRole('search');
  const menu=page.getByRole('dialog',{name:'Show only'});
  const open=async()=>{if(!await menu.isVisible()){await search.locator('.filterbtn').click();await menu.waitFor()}};
  const toggle=async name=>{await open();await menu.getByRole('button',{name:new RegExp(`^${name}`)}).click()};

  await page.goto(`${base}/on/09-07`);
  await page.waitForFunction(()=>[...document.querySelectorAll('main figure img')].every(img=>img.complete&&img.naturalWidth>0));

  assert.equal(await tile(1).locator('.b.pick').count(),1,'a Memories pick wears a star');
  assert.equal(await tile(1).locator('.b.fail').count(),0);
  assert.equal(await tile(1).locator('.b.pick').getAttribute('title'),'Photos would pick it for Memories · edited in Photos','the star says why on hover');
  assert.equal(await tile(2).locator('.b.fail').count(),1,'a failed shot wears a cracked frame');
  assert.equal(await tile(2).locator('.b.fail').getAttribute('title'),'Photos marks it a failed shot · dark');
  assert.equal(await tile(3).locator('.b.pick, .b.fail').count(),0,'a file Photos never scored wears nothing');

  await toggle('Photos would pick');
  assert.deepEqual(await tiles(),[1],'the filter keeps the picks');
  await toggle('Photos would pick');
  await toggle('Failed shots');
  assert.deepEqual(await tiles(),[2],'and the failed shots');
  await toggle('Failed shots');
  await page.keyboard.press('Escape');
  assert.deepEqual(await tiles(),[1,2,3]);

  await tile(1).click();
  const viewer=page.getByRole('dialog',{name:'Photo review'});
  await viewer.waitFor();
  await page.keyboard.press('i');
  const info=viewer.getByRole('complementary',{name:'Info'});
  await info.getByText('Photos would pick it for Memories').waitFor();
  const line=await info.locator('.irow',{hasText:'Photos would pick it for Memories'}).innerText();
  assert.ok(line.includes('edited in Photos')&&line.includes('scored 71')&&line.includes('2 faces')&&line.includes('people')&&line.includes('two people on a beach'),`the Info row says what Photos found: ${line}`);
  if(process.env.SHOTS)await page.screenshot({path:`${process.env.SHOTS}/photos-hints.png`});
  await page.keyboard.press('ArrowRight');
  await info.getByText('Photos marks it a failed shot').waitFor();
  await page.keyboard.press('ArrowRight');
  await page.waitForTimeout(200);
  assert.equal(await info.locator('.irow',{hasText:'Photos'}).count(),0,'a file Photos never scored has no such row');

  await browser.close();
  console.log('photos hints: ok');
})().catch(error=>{console.error(error);process.exit(1)});
