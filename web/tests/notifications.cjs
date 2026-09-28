const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Graduation files new photos on dates at night, some of them dates already
// reviewed. The bell lists what arrived, each date it landed on is back in
// play with a red dot, opening the bell marks it seen, and opening a date
// takes its dot away.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS_DIR;
const photo=(id,year,extra={})=>({id,path:`/archive/${year}/${year}-09/${year}-09-25/IMG_${String(id).padStart(4,'0')}.JPG`,capturedAt:Date.parse(`${year}-09-25T12:00:00Z`)/1000+id,kind:'image',source:'archive',size:100,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:`${year}-09-25`,...extra});
const lengths=[31,29,31,30,31,30,31,31,30,31,30,31];
const names=['January','February','March','April','May','June','July','August','September','October','November','December'];
const cell=(month,dom)=>{
  const md=`${String(month+1).padStart(2,'0')}-${String(dom).padStart(2,'0')}`;
  if(md==='09-25')return {md,dom,years:2,files:3,done:0,waiting:3,state:'todo',today:false,fresh:[1,3].filter(id=>!seen.has(id)).length};
  if(md==='09-27')return {md,dom,years:1,files:1,done:0,waiting:1,state:'todo',today:false,fresh:seen.has(4)?0:1};
  if(md==='09-24')return {md,dom,years:1,files:4,done:1,waiting:0,state:'done',today:false};
  return {md,dom,years:0,files:0,done:0,waiting:0,state:'none',today:md==='09-28'};
};
const seen=new Set();
const year=()=>({months:lengths.map((days,month)=>({name:names[month],cells:Array.from({length:days},(_,index)=>cell(month,index+1))})),prog:{dates:2,done:1,part:0,filesDone:4,files:7},today:'09-28',streak:1,week:{days:1,seconds:60},refreshed:'2026-09-28 01:00:00'});
const now=new Date('2026-09-28T09:30:00Z');
const stamp=date=>date.toISOString().replace('T',' ').replace('Z','');
const list=()=>({unread,items:[
  {id:7,kind:'arrivals',createdAt:stamp(new Date(now.getTime()-5*3600e3)),files:4,bytes:9_400_000,read:unread===0,days:[
    {day:'2020-09-25',md:'09-25',files:1,reopened:false,fresh:seen.has(3)?0:1},
    {day:'2023-09-25',md:'09-25',files:1,reopened:true,fresh:seen.has(1)?0:1},
    {day:'2024-09-24',md:'09-24',files:1,reopened:true,fresh:0},
    {day:'2026-09-27',md:'09-27',files:1,reopened:false,fresh:seen.has(4)?0:1}]},
  {id:6,kind:'phone-deletions',createdAt:stamp(new Date(now.getTime()-30*3600e3)),files:2,bytes:5_100_000,read:unread<2,days:[]},
  {id:3,kind:'arrivals',createdAt:'2026-08-02 04:10:00.000',files:1,bytes:2_000_000,read:true,days:[{day:'2019-08-01',md:'08-01',files:1,reopened:false,fresh:0}]}]});
let unread=2,reads=[],seenReports=[],failNotifications=false;

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const context=await browser.newContext({viewport:{width:1440,height:900}});
  await context.addInitScript(()=>{try{localStorage.setItem('cull-theme','night')}catch{}});
  const page=await context.newPage();
  await page.clock.install({time:now});
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/catalogue')return route.fulfill({json:{generation:1}});
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:7,synthetic:false,snapshotAt:'2026-09-28 01:00:00',candidates:0,calendarDays:2,reviewedDays:1,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0,notifications:unread}});
    if(url.pathname==='/api/notifications')return failNotifications?route.fulfill({status:503,body:'unavailable'}):route.fulfill({json:list()});
    if(url.pathname==='/api/notifications/read'){reads.push(route.request().postDataJSON());unread=0;return route.fulfill({status:204,body:''})}
    if(url.pathname==='/api/arrivals/seen'){const body=route.request().postDataJSON();seenReports.push(body);body.ids.forEach(id=>seen.add(id));return route.fulfill({status:204,body:''})}
    if(url.pathname==='/api/year')return route.fulfill({json:year()});
    if(url.pathname==='/api/today/09-25')return route.fulfill({json:{md:'09-25',label:'25 September',previous:'09-24',next:'09-26',memories:3,bytes:300,years:[
      {day:'2023-09-25',year:2023,files:2,bytes:200,status:'pending',fresh:seen.has(1)?0:1,assets:[photo(1,2023,{new:!seen.has(1)}),photo(2,2023)]},
      {day:'2020-09-25',year:2020,files:1,bytes:100,status:'pending',fresh:seen.has(3)?0:1,assets:[photo(3,2020,{new:!seen.has(3)})]}]}});
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/streak'))return route.fulfill({status:404,body:''});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const settle=async()=>{await page.clock.runFor(400);await page.waitForTimeout(50)};

  // 1. The day page says which files are new, and the bell counts what is unseen.
  const seenReport=page.waitForRequest(request=>new URL(request.url()).pathname==='/api/arrivals/seen');
  await page.goto(`${base}/on/09-25`);
  await page.locator('.gal figure').first().waitFor();
  const bell=page.getByRole('button',{name:'Notifications, 2 new'});
  await bell.waitFor();
  assert.equal(await page.locator('.bellcount').textContent(),'2');
  assert.deepEqual(await page.locator('.yhead .tag.fresh').allTextContents(),['1 new','1 new']);
  assert.equal(await page.locator('.gal figure .b.new').count(),2,'each new file carries a badge');
  // Opening the date saw them: the page says so once, and keeps the badges.
  await seenReport;
  assert.deepEqual(seenReports,[{ids:[1,3]}]);

  // 2. Opening it lists the arrivals, newest first, and marks them seen.
  await bell.click();
  const panel=page.getByRole('dialog',{name:'Notifications'});
  await panel.waitFor();
  const items=panel.locator('.noteitem');
  await items.first().waitFor();
  assert.equal(await items.count(),3);
  assert.equal(await items.nth(0).locator('.notetitle').textContent(),'4 new files reached the archive');
  assert.match(await items.nth(0).locator('.notemeta').textContent(),/^Today, \d\d:\d\d.*9\.4 MB.*4 days, 2 reopened$/);
  const chips=items.nth(0).locator('a.noteday');
  assert.deepEqual(await chips.evaluateAll(links=>links.map(link=>link.getAttribute('href'))),['/on/09-25','/on/09-25','/on/09-24','/on/09-27']);
  assert.equal(await chips.nth(3).locator('.freshdot').count(),1,'a date not opened since its files arrived carries the dot');
  assert.equal(await chips.nth(0).locator('.freshdot').count(),0,'the date just opened does not');
  assert.equal(await chips.nth(2).locator('.freshdot').count(),0,'nor does a date reviewed again since');
  assert.equal(await items.nth(1).locator('.notetitle').textContent(),'2 photos deleted on a phone were marked for the Bin');
  assert.equal(await items.nth(1).getByRole('link',{name:'Open the Bin'}).getAttribute('href'),'/bin');
  assert.match(await items.nth(1).locator('.notemeta').textContent(),/^Yesterday, /);
  assert.equal(await page.locator('.noteitem.unseen').count(),2,'what was unseen stays marked while the list is open');
  await page.waitForFunction(()=>!document.querySelector('.bellcount'));
  assert.deepEqual(reads,[{through:7}]);
  assert.equal(await page.locator('.bell').getAttribute('aria-label'),'Notifications');
  if(shots)await panel.screenshot({path:`${shots}/notifications-night-1440.png`});

  // 3. Escape closes it and gives focus back to the bell.
  await page.keyboard.press('Escape');
  await panel.waitFor({state:'hidden'});
  assert.equal(await page.evaluate(()=>document.activeElement?.classList.contains('bell')),true);

  // 4. Opened again, nothing is new and nothing is marked again.
  await page.locator('.bell').click();
  await items.first().waitFor();
  await settle();
  assert.equal(await page.locator('.noteitem.unseen').count(),0);
  assert.equal(reads.length,1);
  await page.keyboard.press('Escape');

  // 5. The date picker and the calendar show the date with a red dot.
  await page.locator('.datepill').click();
  const picker=page.getByRole('dialog',{name:'Choose a date'});
  await picker.waitFor();
  const pickerDay=picker.locator('a.d',{hasText:/^27$/});
  await pickerDay.locator('.freshdot').waitFor();
  assert.match(await pickerDay.getAttribute('aria-label'),/1 newly arrived file\b/);
  assert.equal(await picker.locator('a.d',{hasText:/^25$/}).locator('.freshdot').count(),0,'the date just opened has no dot');
  assert.equal(await picker.locator('a.d .freshdot').count(),1);
  assert.match(await picker.locator('.callegend').textContent(),/New files/);
  if(shots)await picker.screenshot({path:`${shots}/notifications-picker-night.png`});
  await page.keyboard.press('Escape');
  await page.goto(`${base}/year`);
  const yearCell=page.locator('a.cell[href="/on/09-27"]');
  await yearCell.waitFor();
  assert.equal(await yearCell.locator('.freshdot').count(),1);
  assert.match(await yearCell.getAttribute('aria-label'),/1 newly arrived file$/);
  assert.equal(await page.locator('a.cell .freshdot').count(),1,'only the date not opened since has a dot');
  if(shots)await page.locator('.cmonth').nth(8).screenshot({path:`${shots}/notifications-year-night.png`});

  // 6. Opened again, the date has nothing new and says nothing more.
  await page.goto(`${base}/on/09-25`);
  await page.locator('.gal figure').first().waitFor();
  await settle();
  assert.equal(await page.locator('.gal figure .b.new').count(),0);
  assert.equal(await page.locator('.yhead .tag.fresh').count(),0);
  assert.equal(seenReports.length,1);

  // 7. A list that does not load says so and can be asked again.
  failNotifications=true;
  await page.locator('.bell').click();
  await page.getByRole('alert').filter({hasText:'Notifications did not load.'}).waitFor();
  failNotifications=false;
  await page.getByRole('button',{name:'Try again'}).click();
  await items.first().waitFor();
  await page.keyboard.press('Escape');

  // 8. On a phone, in the day theme, the list fits the screen.
  const phone=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true});
  await phone.addInitScript(()=>{try{localStorage.setItem('cull-theme','day')}catch{}});
  const small=await phone.newPage();
  await small.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:7,synthetic:false,snapshotAt:'2026-09-28 01:00:00',candidates:0,calendarDays:2,reviewedDays:1,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0,notifications:2}});
    if(url.pathname==='/api/notifications'){unread=2;return route.fulfill({json:list()})}
    if(url.pathname==='/api/notifications/read')return route.fulfill({status:204,body:''});
    if(url.pathname==='/api/catalogue')return route.fulfill({json:{generation:1}});
    if(url.pathname==='/api/year')return route.fulfill({json:year()});
    if(url.pathname.startsWith('/api/streak'))return route.fulfill({status:404,body:''});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await small.goto(`${base}/year`);
  await small.locator('a.cell').first().waitFor();
  await small.getByRole('button',{name:'Notifications, 2 new'}).click();
  const smallPanel=small.getByRole('dialog',{name:'Notifications'});
  await smallPanel.locator('.noteitem').first().waitFor();
  const box=await smallPanel.boundingBox();
  assert.ok(box.x>=8&&box.x+box.width<=390-8,`the list runs off the screen: ${JSON.stringify(box)}`);
  assert.equal(await small.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'no sideways scroll');
  if(shots){await small.waitForTimeout(300);await small.screenshot({path:`${shots}/notifications-day-390.png`})}
  await small.keyboard.press('Escape');
  await smallPanel.waitFor({state:'hidden'});
  if(shots)await small.screenshot({path:`${shots}/year-day-390.png`});

  await browser.close();
  console.log('notifications: ok');
})().catch(error=>{console.error(error);process.exit(1)});
