const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Opening the Apple Photos page checks Photos at once when Cull Sync is
// online and nothing is under way, and the headline counts what that check
// found rather than everything Cull removed. A plan the server renews by
// itself keeps what the reviewer unticked. A current Cull Sync offers no setup
// button; one that is not answering does, and nothing is checked.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const now='2026-09-28T10:00:00Z';
const row=(n,name)=>({id:`delete:asset:${n}`,action:'delete',keys:[`asset:${n}`],name,day:'2026-09-20',original:`/archive/2026/2026-09/2026-09-20/${name}`,kind:'image',ext:'heic',state:'bin',how:'exact',photos:[{id:`P${n}`,name,created:'2026-09-20T10:00:00Z',favourite:false}]});
const rows=[row(1,'IMG_0817.HEIC'),row(2,'IMG_4233.HEIC'),row(3,'IMG_5120.HEIC')];
const missing=Array.from({length:1464},(_,index)=>({action:'delete',name:`IMG_${7000+index}.MOV`,day:'2026-09-01'}));
let agent={online:true,lastSeen:now,version:'1.1',access:'authorized'};
let job={id:'J1',state:'checking',rev:1,stage:'matching',done:10,total:1467};
let checks=0;
const view=()=>({...job,created:now,updated:now,toCheck:1467,delete:job.state==='planned'?rows:[],favourite:[],missing:job.state==='planned'?missing:[],held:[],undated:0,selected:[],skipped:0});

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:900}});
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:4,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/photos')return route.fulfill({json:{configured:true,now,agent,job:{id:job.id,state:job.state,rev:job.rev,stage:job.stage,done:job.done??0,total:job.total??0}}});
    if(url.pathname==='/api/photos/overview')return route.fulfill({json:{delete:1460,favourite:0,held:128,undated:0,restored:[],synced:{deleted:130,favourited:0,last:'2026-09-27 18:00:00'}}});
    if(url.pathname==='/api/photos/check'&&route.request().method()==='POST'){
      checks++;
      job={id:`C${checks}`,state:'queued_check',rev:1};
      return route.fulfill({json:view()});
    }
    if(url.pathname===`/api/photos/jobs/${job.id}`)return route.fulfill({json:view()});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const headline=()=>page.locator('.photoshead .ysum').innerText();
  const helper=page.locator('.phelper');

  // A check already under way is not started again, and says what it looks for.
  await page.goto(`${base}/photos`);
  await page.getByText('Finding each photograph in Photos…').first().waitFor();
  await page.waitForFunction(()=>document.querySelector('.photoshead .ysum')?.textContent?.startsWith('Looking for'));
  assert.match(await headline(),/^Looking for 1,467 files in Photos… · last synced 27 Sept, \d\d:00$/);
  await page.waitForTimeout(300);
  assert.equal(checks,0,'no second check while one runs');
  assert.equal(await helper.getByRole('button').count(),0,'no setup button while Cull Sync is current');

  // Opening the page with only an old plan checks Photos at once.
  job={id:'J1',state:'planned',rev:2};
  await page.reload();
  await page.waitForFunction(()=>document.querySelector('.photoshead .ysum')?.textContent?.startsWith('Looking for'));
  assert.equal(checks,1,'opening the page checks Photos');
  job={...job,state:'planned',rev:2};
  await page.locator('.prow').first().waitFor();
  assert.match(await headline(),/^3 still in Photos to delete · 0 favourites to set · 1,464 already gone from it · last synced 27 Sept, \d\d:00$/);
  if(shots)await page.screenshot({path:`${shots}/photos-auto.png`});

  // Something unticked stays unticked when the server renews the plan.
  await page.getByRole('checkbox',{name:'Include IMG_4233.HEIC'}).uncheck();
  const renewed=page.waitForResponse(response=>response.url().endsWith('/api/photos/jobs/R1'),{timeout:10000});
  job={id:'R1',state:'planned',rev:1};
  await renewed;
  await page.waitForTimeout(200);
  const ticked=await page.locator('.prow input[type=checkbox]').evaluateAll(boxes=>boxes.map(box=>box.checked));
  assert.deepEqual(ticked,[true,false,true],'the renewed plan keeps the untick');
  assert.equal(checks,1,'the page checks once per visit');

  // Cull Sync not answering: the setup button, and no check.
  agent={online:false,lastSeen:'2026-09-28T08:00:00Z',version:'1.1',access:'authorized'};
  job={id:'J1',state:'planned',rev:2};
  await page.reload();
  await page.getByRole('dialog').first().waitFor();
  await page.keyboard.press('Escape');
  await helper.getByRole('button',{name:'Set up Cull Sync'}).waitFor();
  assert.equal(checks,1,'nothing is checked without the helper');
  await browser.close();
  console.log('photos auto: ok');
})().catch(error=>{console.error(error);process.exit(1)});
