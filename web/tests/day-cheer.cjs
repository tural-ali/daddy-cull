const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Marking a day reviewed brings up a card over the blurred grid with what the
// review cleared, how much room it made and how many favourites it kept, with
// paper falling; Escape or a click outside takes it down, and undoing the mark
// takes it down too.
const make=(id,name,status,favourite,size)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size,status,favourite,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07'});
const assets=[make(1,'ONE.JPG','keep',true,100),make(2,'TWO.JPG','cull',false,1024*1024),make(3,'THREE.JPG','cull',false,2*1024*1024),make(4,'FOUR.JPG','unreviewed',false,100)];
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  const progress=[];
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:4,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:2,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:4,bytes:3145800,status:'pending',assets}],memories:4,bytes:3145800}});
    if(url.pathname==='/api/day-progress'){progress.push(route.request().postDataJSON());return route.fulfill({json:{}})}
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const card=page.getByRole('dialog',{name:'7 September reviewed'});

  await page.goto(`${base}/on/09-07`);
  await page.locator('.gal figure').first().waitFor();
  await page.getByRole('button',{name:'Mark 7 September reviewed'}).click();
  await card.waitFor();
  assert.equal(await card.locator('canvas.confetti').count(),1,'paper falls');
  await card.locator('dd').filter({hasText:'3.0 MB'}).waitFor();
  await card.locator('dd').filter({hasText:/^2 of 4$/}).waitFor();
  assert.equal(await card.locator('dd').nth(2).innerText(),'1');
  assert.equal(await card.getByRole('link',{name:'8 September →'}).getAttribute('href'),'/on/09-08');
  await page.keyboard.press('Escape');
  await card.waitFor({state:'hidden'});
  await page.locator('.yr.settled').waitFor();
  assert.deepEqual(progress.map(p=>[p.day,p.status]),[['2010-09-07','done']]);

  // Undone while the card is up, the mark and the card both go.
  await page.keyboard.press('Meta+z');
  await page.locator('.yr:not(.settled)').waitFor();
  await page.getByRole('button',{name:'Mark 7 September reviewed'}).click();
  await card.waitFor();
  await page.keyboard.press('Meta+z');
  await card.waitFor({state:'hidden'});
  await page.locator('.yr:not(.settled)').waitFor();

  // A click outside the card closes it.
  await page.getByRole('button',{name:'Mark 7 September reviewed'}).click();
  await card.waitFor();
  await page.mouse.click(20,400);
  await card.waitFor({state:'hidden'});
  await browser.close();
  console.log('day cheer: ok');
})().catch(error=>{console.error(error);process.exit(1)});
