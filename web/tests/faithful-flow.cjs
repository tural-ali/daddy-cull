const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

const asset=(id,name,day)=>({id,path:`/archive/${day.slice(0,4)}/${day.slice(0,7)}/${day}/${name}`,capturedAt:Date.parse(`${day}T12:00:00Z`)/1000,kind:'image',source:'archive',size:100,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:1,day});
const first=asset(1,'FAMILY.JPG','2000-09-07');
const copy=asset(2,'FAMILY (2).JPG','2010-09-07');
const third={...asset(3,'THIRD.JPG','2010-09-07'),relatedCount:0};
const fourth={...asset(4,'FOURTH.JPG','2010-09-07'),relatedCount:0};

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1440,height:900}});
  // "/" opens today's date, and the fixtures are for 7 September.
  await page.clock.setFixedTime(new Date('2026-09-07T10:00:00'));
  const writes=[],individualWrites=[],fetched=[];
  // A choice's write is held unanswered until the test lets it go, so the
  // viewer can be seen to move on without waiting for it.
  let answered=0,release;
  const held=new Promise(resolve=>{release=resolve});
  await page.route('**/api/**',async route=>{
    const request=route.request(),url=new URL(request.url());
    fetched.push(url.pathname);
    if(url.pathname==='/api/catalogue')return route.fulfill({json:{generation:1}});
    // Without the list of addons the sidebar shows Cull's usual pages, and
    // without a setup the app opens where it was asked to.
    if(url.pathname==='/api/addons'||url.pathname==='/api/events'||url.pathname==='/api/setup')return route.fulfill({status:404,json:{error:'not mocked'}});
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:61208,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:1,calendarDays:2749,reviewedDays:0,decisions:80,favourites:2,evidence:4277,fullHashes:1357,marked:2}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2000-09-07',year:2000,files:1,bytes:100,status:'pending',assets:[first]},{day:'2010-09-07',year:2010,files:3,bytes:300,status:'pending',assets:[copy,third,fourth]}],memories:4,bytes:400}});
    if(url.pathname==='/api/duplicates')return route.fulfill({json:[{hash:'abcdef0123456789abcdef0123456789',size:100,reclaimable:100,members:[first,copy]}]});
    if(url.pathname==='/api/year')return route.fulfill({json:{months:Array.from({length:12},(_,month)=>({name:new Date(Date.UTC(2000,month,1)).toLocaleString('en',{month:'long',timeZone:'UTC'}),cells:Array.from({length:31},(_day,index)=>({md:`${String(month+1).padStart(2,'0')}-${String(index+1).padStart(2,'0')}`,dom:index+1,years:month===8&&index===6?2:0,files:month===8&&index===6?2:0,done:0,waiting:month===8&&index===6?2:0,state:month===8&&index===6?'todo':'none'}))})),prog:{dates:1,done:0,part:0,filesDone:0,files:2},today:'09-07',streak:0,week:{days:0,seconds:0}}});
    if(url.pathname==='/api/decisions/batch'){writes.push(request.postDataJSON());return route.fulfill({json:[{revision:1,previousStatus:'unreviewed',previousFavourite:false},{revision:1,previousStatus:'unreviewed',previousFavourite:false}]})}
    if(url.pathname==='/api/decisions'){const body=request.postDataJSON();individualWrites.push(body);await held;answered++;return route.fulfill({json:{revision:body.expectedRevision+1,previousStatus:'unreviewed',previousFavourite:false}})}
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="800" height="800"><rect width="800" height="800" fill="#526b52"/></svg>'});
    if(url.pathname==='/api/tasks')return route.fulfill({json:{tasks:[],active:0}});
    // Info reads the file itself; a file that cannot be read shows without it.
    if(/^\/api\/assets\/\d+\/details$/.test(url.pathname))return route.fulfill({status:404,json:{error:'The file could not be read.'}});
    throw new Error(`${request.method()} ${url.pathname}`);
  });
  await page.goto(process.env.APP_URL||'http://127.0.0.1:8842/');
  await page.getByRole('heading',{name:/7 September/}).waitFor();
  await page.getByRole('link',{name:/^Bin 2 files$/}).waitFor();
  assert.equal(fetched.filter(path=>path==='/api/today/09-07').length,1,'the page loaded its data more than once');
  assert.equal(await page.locator('.xgroup').count(),1);
  // The page's own action sits at the top right, as in Google Photos, not in a bar at the bottom.
  await page.locator('.gbar .pageacts').getByRole('button',{name:/^Mark .+ reviewed$/}).waitFor();
  assert.equal(await page.locator('.fbar button').count(),0);
  assert.match(await page.locator('.xdupes').innerText(),/byte-identical, verified by full hash/);
  // Each copy is a row of the facts that tell it apart, not a big thumbnail.
  const rows=page.locator('.xtable tbody tr');
  assert.equal(await rows.count(),2);
  assert.deepEqual((await rows.nth(1).locator('td').allInnerTexts()).slice(1,6),['FAMILY (2).JPG','JPG','0.1 KB','7 Sept 2010, '+new Date(Date.parse('2010-09-07T12:00:00Z')).toLocaleTimeString('en-GB',{hour:'2-digit',minute:'2-digit'}),'/archive/2010/2010-09/2010-09-07']);
  assert.match(await rows.nth(0).locator('.xpath').innerText(),/2000\/2000-09\/2000-09-07$/);
  assert.equal(await rows.nth(0).locator('.xpath .dim').innerText(),'/archive/','the folders both copies share are dimmed');
  if(process.env.SHOTS)await page.locator('.xdupes').screenshot({path:`${process.env.SHOTS}/copy-group.png`});
  await page.setViewportSize({width:390,height:844});
  // The page lays itself out again after the resize, so the check waits for
  // that, and still fails on a group that never fits.
  await page.waitForFunction(()=>![...document.querySelectorAll('.xdupes *')].some(node=>node.getBoundingClientRect().right>391),null,{timeout:3000}).catch(()=>{});
  const wide=await page.evaluate(()=>[...document.querySelectorAll('.xdupes *')].filter(node=>node.getBoundingClientRect().right>391).slice(0,6).map(node=>`${node.tagName}.${node.className} ${Math.round(node.getBoundingClientRect().right)}`));
  assert.deepEqual(wide,[],`the group fits a phone: ${JSON.stringify(wide)}`);
  if(process.env.SHOTS)await page.locator('.xdupes').screenshot({path:`${process.env.SHOTS}/copy-group-phone.png`});
  await page.setViewportSize({width:1440,height:900});
  // A copy opens full size, and can be chosen from there.
  await rows.nth(1).getByRole('button',{name:'Open FAMILY (2).JPG'}).click();
  const box=page.getByRole('dialog',{name:'Preview of FAMILY (2).JPG'});
  await box.waitFor();
  assert.match(await box.locator('.lbtitle').innerText(),/JPG · 0\.1 KB · 7 Sept 2010/);
  if(process.env.SHOTS)await page.screenshot({path:`${process.env.SHOTS}/copy-group-open.png`});
  await page.keyboard.press('k');
  await box.locator('.xstays').waitFor();
  assert.equal(await rows.nth(1).getAttribute('class'),'keeper','K in the preview chose that copy');
  await page.keyboard.press('Escape');
  await box.waitFor({state:'hidden'});
  // A click on a row chooses too.
  await rows.nth(0).locator('.xname').click();
  assert.equal(await rows.nth(0).getAttribute('class'),'keeper');
  await page.getByRole('radio',{name:'Keep FAMILY (2).JPG'}).check();
  await page.getByRole('button',{name:/Keep the selected copy/}).click();
  await page.getByText(/verified copy marked for the Bin/).waitFor();
  assert.equal(writes.length,1);
  assert.equal(writes[0].find(item=>item.assetId===2).status,'keep');
  assert.equal(writes[0].find(item=>item.assetId===1).status,'cull');
  await page.getByRole('link',{name:'Year',exact:true}).click();
  await page.getByRole('heading',{name:'Your archive'}).waitFor();
  assert.equal(await page.locator('.cmonth').count(),12);
  await page.getByRole('link',{name:'Daddy, Cull!'}).click();
  await page.getByRole('heading',{name:/7 September/}).waitFor();
  assert.match(page.url(),/\/on\/09-07$/);
  // Review starts from a photograph, as in Google Photos.
  await page.locator('.yhead ~ .gal .mo').first().click();
  await page.getByRole('dialog',{name:'Photo review'}).waitFor();
  assert.equal(new URL(page.url()).pathname,'/on/09-07/photo/1','the preview has its own address');
  await page.keyboard.press('k');
  await page.locator('.rvpos').filter({hasText:'2 / 4'}).waitFor();
  assert.equal(answered,0,'viewer waited for the network before advancing');
  release();
  assert.equal(new URL(page.url()).pathname,'/on/09-07/photo/2','the address follows the photo');
  // Info, once open, stays open from photo to photo.
  await page.keyboard.press('i');
  await page.locator('.rv.info').waitFor();
  await page.keyboard.press('k');
  await page.locator('.rvpos').filter({hasText:'3 / 4'}).waitFor();
  assert.equal(await page.locator('.rv.info').count(),1,'Info closed when the photo changed');
  await page.keyboard.press('i');
  await page.waitForFunction(()=>Object.keys(localStorage).filter(key=>key.startsWith('cull.pending.')).every(key=>JSON.parse(localStorage[key]).length===0));
  assert.deepEqual(individualWrites.map(item=>item.assetId),[1,2]);
  // A click on the photograph only hides the chrome; a click beside it leaves.
  const viewer=page.getByRole('dialog',{name:'Photo review'});
  await page.locator('.rvstage img').click();
  assert.equal(await viewer.isVisible(),true,'a click on the photograph closed the review');
  const stage=await page.locator('.rvstage').boundingBox();
  await page.mouse.click(stage.x+stage.width-20,stage.y+20);
  await viewer.waitFor({state:'hidden'});
  assert.equal(new URL(page.url()).pathname,'/on/09-07','closing the preview left its address behind');
  // A shared address opens the same photo.
  await page.goto((process.env.APP_URL||'http://127.0.0.1:8842/').replace(/\/$/,'')+'/on/09-07/photo/3');
  await viewer.waitFor();
  assert.match(await page.locator('.rvinfo').innerText(),/THIRD\.JPG/);
  await browser.close();
  console.log(JSON.stringify({faithfulToday:true,binCountShown:true,pageLoadedOnce:true,verifiedDuplicates:true,keeperChoiceAtomic:true,calendar:true,logoReturnsToday:true,instantKeyboardAdvance:true,durableQueueDrained:true,lightboxClosesOutside:true,photoAddress:true,infoStaysOpen:true},null,2));
})().catch(error=>{console.error(error);process.exit(1)});
