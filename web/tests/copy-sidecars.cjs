const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Removing a copy takes its own sidecars to the Bin with it, so the copy kept
// by default is the one whose sidecars record the most: who is in it, then
// keywords, rating and caption, then having a sidecar at all. That comes after
// the place and before the name, and the poorer copies say what they lack. A
// group with a copy whose sidecars were not read is ranked by name alone.
// Every name is a synthetic fixture.
const day='2022-06-15';
const facts=(people=0,extra={})=>({files:1,people,keywords:0,rating:0,captioned:false,...extra});
const clip=(id,name,located,sidecars)=>({id,path:`/archive/2022/2022-06/${day}/${name}`,capturedAt:Date.parse(`${day}T00:00:00Z`)/1000,kind:'video',source:'archive',size:4000000+id,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day,located,...(sidecars?{sidecars}:{})});
const group=(hash,members)=>({hash,proof:'bytes',size:4000000,reclaimable:4000000*(members.length-1),members});
const tagged=group('aa',[clip(1,'IMG_0021.MOV',false,facts(1)),clip(2,'IMG_0021 (2022-06-15).MOV',false,facts(2,{files:2,keywords:1,rating:4})),clip(3,'IMG_0021 (1).MOV',false,facts(0,{files:0}))]);
const unread=group('bb',[clip(4,'IMG_0030.MOV',false),clip(5,'IMG_0030 (1).MOV',false,facts(3))]);
const placed=group('cc',[clip(6,'IMG_0040 (1).MOV',true,facts(0)),clip(7,'IMG_0040.MOV',false,facts(2))]);
const groups=[tagged,unread,placed];
const all=groups.flatMap(entry=>entry.members);
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const svg='<svg xmlns="http://www.w3.org/2000/svg" width="96" height="54"><rect width="96" height="54" fill="#4d6f94"/></svg>';

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:900}});
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/addons')return route.fulfill({json:[]});
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:all.length,synthetic:true,snapshotAt:'2026-09-30 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/06-15')return route.fulfill({json:{md:'06-15',label:'15 June',previous:'06-14',next:'06-16',years:[{day,year:2022,files:all.length,bytes:all.length*4000000,status:'pending',assets:all}],memories:all.length,bytes:all.length*4000000}});
    if(url.pathname==='/api/duplicates')return route.fulfill({json:groups});
    if(url.pathname==='/api/duplicate-report')return route.fulfill({json:{candidates:0,hashed:0,settled:true,unproven:[],groups}});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:svg});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });

  await page.goto(`${base}/on/06-15`);
  const copies=page.locator('.xcopy');
  await copies.nth(2).waitFor();
  const table=index=>copies.nth(index).locator('.xtable tbody tr').evaluateAll(rows=>rows.map(row=>[Number(row.dataset.asset),row.className,[...row.querySelectorAll('.dupeflag')].map(flag=>flag.textContent)]));
  assert.deepEqual(await table(0),[[1,'binned',['fewer people tags']],[2,'keeper',[]],[3,'binned',['no people tags']]],'the copy whose sidecars name the most people is kept, whatever its name');
  assert.deepEqual(await table(1),[[4,'keeper',[]],[5,'binned',[]]],'a copy not read yet is not ranked as having nothing');
  assert.deepEqual(await table(2),[[6,'keeper',['no people tags']],[7,'binned',['no location']]],'the place still comes first');
  if(shots)await copies.first().screenshot({path:`${shots}/copy-sidecars-day.png`});

  // On a phone the flags wrap inside the group.
  await page.setViewportSize({width:390,height:844});
  await page.waitForFunction(()=>![...document.querySelectorAll('.xdupes *')].some(node=>node.getBoundingClientRect().right>391),null,{timeout:3000}).catch(()=>{});
  assert.deepEqual(await page.evaluate(()=>[...document.querySelectorAll('.xdupes *')].filter(node=>node.getBoundingClientRect().right>391).map(node=>node.className)),[],'the groups fit a phone');
  await page.setViewportSize({width:1280,height:900});

  await page.goto(`${base}/duplicates`);
  const sets=page.locator('.dupegroup');
  await sets.nth(2).waitFor();
  assert.equal(await sets.nth(0).locator('.dupetile.keeper .dupelabel > span:first-child').innerText(),'IMG_0021 (2022-06-15).MOV');
  assert.deepEqual(await sets.nth(0).locator('.dupetile:not(.keeper) .dupeflag').allInnerTexts(),['fewer people tags','no people tags']);
  assert.match(await sets.nth(0).getByRole('button',{name:'Merge 3 copies'}).getAttribute('title'),/because its sidecars record the most: 2 people, 1 keyword, rated 4, and marks/);
  assert.equal(await sets.nth(1).locator('.dupetile.keeper .dupelabel > span:first-child').innerText(),'IMG_0030.MOV');
  assert.match(await sets.nth(2).getByRole('button',{name:'Merge 2 copies'}).getAttribute('title'),/because it records where it was taken/);
  // A rule by date still yields to what the sidecars record.
  await page.locator('select:has(option[value=oldest])').selectOption('oldest');
  assert.equal(await sets.nth(0).locator('.dupetile.keeper .dupelabel > span:first-child').innerText(),'IMG_0021 (2022-06-15).MOV');
  if(shots)await sets.first().screenshot({path:`${shots}/copy-sidecars-page.png`});
  await browser.close();
  console.log('copy sidecars: ok');
})().catch(error=>{console.error(error);process.exit(1)});
