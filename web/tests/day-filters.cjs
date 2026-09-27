const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// The chips over a day's grid narrow it: one from a group keeps only its
// files, two from the same group keep either, and groups combine. The arrow
// keys and the viewer walk the narrowed list, and the choice follows the
// reviewer to the next day in the same tab.
const make=(id,name,kind,status,favourite)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind,source:'archive',size:100,status,favourite,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07'});
const assets=[
  make(1,'ONE.JPG','image','unreviewed',false),
  make(2,'TWO.MOV','video','keep',true),
  make(3,'THREE.JPG','image','cull',false),
  make(4,'FOUR.ARW','raw','keep',false),
  make(5,'FIVE.MOV','video','unreviewed',true),
];
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:5,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:2,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:5,bytes:500,status:'pending',assets}],memories:5,bytes:500}});
    if(url.pathname==='/api/today/09-08')return route.fulfill({json:{md:'09-08',label:'8 September',previous:'09-07',next:'09-09',years:[{day:'2010-09-08',year:2010,files:1,bytes:100,status:'pending',assets:[{...make(6,'SIX.JPG','image','unreviewed',false),day:'2010-09-08'}]}],memories:1,bytes:100}});
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const chip=name=>page.getByRole('group',{name:'Show only'}).getByRole('button',{name:new RegExp(`^${name}`)});
  const tiles=async()=>(await page.locator('.gal figure').evaluateAll(nodes=>nodes.map(node=>Number(node.dataset.asset))));

  await page.goto(`${base}/on/09-07`);
  await page.locator('.gal figure').first().waitFor();
  assert.equal(await page.locator('[role=progressbar]').count(),0,'the progress bar is gone');
  assert.deepEqual((await page.getByRole('group',{name:'Show only'}).getByRole('button').allInnerTexts()).map(text=>text.replace(/\s+/g,' ')),['Favourites 2','Videos 2','Photos 3','Undecided 2','Kept 2','Removed 1']);
  assert.deepEqual(await tiles(),[1,2,3,4,5]);

  await chip('Videos').click();
  assert.deepEqual(await tiles(),[2,5],'videos alone');
  await chip('Photos').click();
  assert.deepEqual(await tiles(),[1,2,3,4,5],'videos or photos is everything');
  await chip('Videos').click();
  assert.deepEqual(await tiles(),[1,3,4],'photos, RAW included');
  await chip('Kept').click();
  assert.deepEqual(await tiles(),[4],'kept photos');
  await chip('Removed').click();
  assert.deepEqual(await tiles(),[3,4],'kept or removed photos');
  await chip('Favourites').click();
  assert.equal(await page.locator('.gal figure').count(),0);
  await page.getByText('Nothing on this date matches the filters.').waitFor();
  assert.equal(await page.locator('.yr').count(),0,'a year with nothing to show is left out');
  await chip('Photos').click();
  await chip('Kept').click();
  await chip('Removed').click();
  assert.deepEqual(await tiles(),[2,5],'favourites alone');

  // The keys and the viewer walk only what is shown.
  await page.locator('.gal figure').first().click();
  const viewer=page.getByRole('dialog',{name:'Photo review'});
  await viewer.waitFor();
  await page.waitForURL(/\/photo\/2$/);
  await page.keyboard.press('ArrowRight');
  await page.waitForURL(/\/photo\/5$/);
  await page.keyboard.press('ArrowRight');
  await page.waitForTimeout(200);
  assert.match(page.url(),/\/photo\/(2|5)$/,'the viewer stays among the shown files');
  await page.keyboard.press('Escape');
  await viewer.waitFor({state:'hidden'});
  await page.locator('.dline').click();
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('ArrowRight');
  await page.locator('.gal figure.sel[data-asset="5"]').waitFor();
  await page.keyboard.press('ArrowLeft');
  await page.locator('.gal figure.sel[data-asset="2"]').waitFor();

  // The choice follows to the next day in this tab.
  await page.getByRole('button',{name:/^7 September/}).click();
  await page.getByRole('navigation',{name:'Nearby dates'}).getByRole('link',{name:'8 Sep'}).click();
  await chip('Favourites').waitFor();
  assert.equal(await chip('Favourites').getAttribute('aria-pressed'),'true');
  await page.getByText('Nothing on this date matches the filters.').waitFor();
  await browser.close();
  console.log('day filters: ok');
})().catch(error=>{console.error(error);process.exit(1)});
