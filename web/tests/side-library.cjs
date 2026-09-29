const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const {builtIns,common}=require('./lib/addons.cjs');

// The sidebar shows what the library holds under the review meter: photos
// and videos, how many and how much space, with a bar for each split. Folded
// to a rail it is one icon whose tooltip says the same. Every count is a
// synthetic fixture.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const library={photos:{files:38412,bytes:581_300_000_000},videos:{files:19807,bytes:2_843_000_000_000}};

(async()=>{
  let stats={...common(new URL(`${base}/api/stats`)).json,calendarDates:366,reviewedDates:122,library};
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:900},timezoneId:'Europe/London'});
  await page.addInitScript(()=>{
    const now=new Date();
    localStorage.setItem('cull.streak-intro',`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`);
    localStorage.setItem('cull-guides-hidden',JSON.stringify(['log']));
  });
  const errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:stats});
    if(url.pathname==='/api/addons')return route.fulfill({json:builtIns()});
    if(url.pathname==='/api/log')return route.fulfill({json:[]});
    if(url.pathname==='/api/trash/deleting')return route.fulfill({json:{graceDays:30,items:[],lastRun:'',lastDeleted:0,lastError:'',checkIntervalMinutes:15}});
    const found=common(url);
    if(found)return route.fulfill(found);
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });

  await page.goto(`${base}/log`);
  const widget=page.locator('.sidelibrary');
  await widget.waitFor();
  const rows=await widget.locator('tr').allInnerTexts();
  assert.deepEqual(rows.map(row=>row.replace(/\s+/g,' ').trim()),['Kind Files Space','Photos 38,412 581 GB','Videos 19,807 2.8 TB','Total 58,219 3.4 TB']);
  assert.equal(await widget.getByRole('img',{name:'Files: 66% photos, 34% videos'}).count(),1);
  assert.equal(await widget.getByRole('img',{name:'Space: 17% photos, 83% videos'}).count(),1);
  const meter=await page.locator('.sideprogress').boundingBox(),box=await widget.boundingBox(),foot=await page.locator('.sidefoot').boundingBox();
  assert.ok(box.y>=meter.y+meter.height&&box.y+box.height<=foot.y,'it sits between the review meter and Settings');
  if(shots)await page.locator('#side').screenshot({path:`${shots}/side-library.png`});

  await page.evaluate(()=>document.documentElement.setAttribute('data-theme','day'));
  if(shots)await page.locator('#side').screenshot({path:`${shots}/side-library-day.png`});
  await page.evaluate(()=>document.documentElement.removeAttribute('data-theme'));

  // Folded to a rail, the icon's tooltip carries the numbers.
  await page.setViewportSize({width:900,height:900});
  const head=widget.locator('.sidelibraryhead');
  await page.waitForFunction(()=>getComputedStyle(document.querySelector('.libkey')).display==='none');
  assert.equal(await widget.getAttribute('data-tip'),'38,412 photos, 581 GB. 19,807 videos, 2.8 TB.');
  assert.ok((await head.boundingBox()).width<=48);
  if(shots)await page.locator('#side').screenshot({path:`${shots}/side-library-rail.png`});

  // An empty library shows nothing rather than a row of zeros.
  stats={...stats,library:{photos:{files:0,bytes:0},videos:{files:0,bytes:0}}};
  await page.setViewportSize({width:1280,height:900});
  await page.reload();
  await page.locator('.sideprogress').waitFor();
  assert.equal(await widget.count(),0);

  assert.deepEqual(errors,[]);
  await browser.close();
})().catch(error=>{console.error(error);process.exit(1)});
