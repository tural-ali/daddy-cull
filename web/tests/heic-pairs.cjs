const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// A HEIC and a JPEG of one shot are one tile, the HEIC, badged HEIC and JPG,
// and the day offers them as one shot saved twice, keeping the HEIC unless
// the JPEG is chosen, and taking the JPEG's sidecars. Unlike a RAW's stack,
// they are listed with the copies.
// Shown separately from the viewer, they are two photos, and undo joins them.
const dir='/archive/2022/2022-10/2022-10-02/';
const file=(id,name,extra={})=>({id,path:dir+name,capturedAt:Date.parse('2022-10-02T10:11:12Z')/1000+id,kind:'image',source:'archive',size:name.endsWith('.JPG')?3.1e6:1.6e6,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2022-10-02',...extra});
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const colours={1:'#3a5b7a',2:'#3a5b7a',3:'#6b4f2f'};

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:900}});
  await page.clock.install({time:new Date('2026-10-02T10:00:00')});
  const pairs=[];
  const batches=[];
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:3,synthetic:true,snapshotAt:'2026-10-01 01:00:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0,hiddenGuides:['today','dupes']}});
    if(url.pathname==='/api/today/10-02')return route.fulfill({json:{md:'10-02',label:'2 October',previous:'10-01',next:'10-03',years:[{day:'2022-10-02',year:2022,files:3,bytes:6.3e6,status:'pending',assets:[
      file(1,'IMG_2355.HEIC',{stack:[2]}),
      file(2,'IMG_2355.JPG',{stack:[1]}),
      file(3,'IMG_2356.HEIC'),
    ]}],memories:3,bytes:6.3e6}});
    if(url.pathname==='/api/duplicates')return route.fulfill({json:[{hash:'exposure:1',proof:'exposure',size:3.1e6,reclaimable:3.1e6,members:[
      {...file(1,'IMG_2355.HEIC'),located:false,converted:false,retagged:false,sidecars:{files:0,people:0,keywords:0,rating:0,captioned:false}},
      {...file(2,'IMG_2355.JPG'),located:false,converted:false,retagged:false,sidecars:{files:1,people:1,keywords:0,rating:0,captioned:false}},
    ]}]});
    if(url.pathname==='/api/pairs'){pairs.push(route.request().postDataJSON());return route.fulfill({json:{paired:route.request().postDataJSON().paired}})}
    if(url.pathname==='/api/decisions/batch'){
      const body=route.request().postDataJSON();
      batches.push(body);
      return route.fulfill({json:body.map(()=>({revision:1}))});
    }
    const media=url.pathname.match(/^\/api\/media\/(\d+)\//);
    if(media)return route.fulfill({contentType:'image/svg+xml',body:`<svg xmlns="http://www.w3.org/2000/svg" width="600" height="400"><rect width="600" height="400" fill="${colours[media[1]]}"/><text x="300" y="215" font-size="48" fill="#fff" text-anchor="middle" font-family="sans-serif">file ${media[1]}</text></svg>`});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const tiles=page.locator('.gal figure');
  const tileIDs=()=>tiles.evaluateAll(nodes=>nodes.map(node=>Number(node.dataset.asset)));
  const flush=async()=>{await page.clock.runFor(300)};
  // The grid draws the day before it knows its stacks, so wait for them.
  const shows=async(want,why)=>{
    for(let tries=0;tries<50&&JSON.stringify(await tileIDs())!==JSON.stringify(want);tries++)await page.waitForTimeout(40);
    assert.deepEqual(await tileIDs(),want,why);
  };

  await page.goto(`${base}/on/10-02`);
  await tiles.first().waitFor();
  await shows([1,3],'the pair shows as its HEIC');
  assert.deepEqual(await page.locator('[data-asset="1"] .b.pair').allInnerTexts(),['HEIC','JPG']);
  await page.getByText('2 memories from 3 files').waitFor();

  // The day lists the pair as one shot saved twice, the HEIC kept.
  const copies=page.locator('.xdupes');
  await copies.waitFor();
  assert.equal((await copies.locator('h2').innerText()).replace(/\s+/g,' '),'Same shot, two formats 1 pair · a HEIC and a JPEG that record the same moment, to a fraction of a second, and camera');
  assert.equal((await copies.locator('.xmeta').innerText()).replace(/\s+/g,' '),'Same shot HEIC and JPG · 3.0 MB reclaimable');
  // The HEIC is kept even though only the JPEG's sidecars name a person, as
  // the Bin copies them beside it.
  assert.equal(await copies.locator('tr.keeper .xfile').innerText(),'IMG_2355.HEIC');
  assert.equal(await copies.locator('tr.keeper .dupeflag').count(),0);
  const resolve=copies.getByRole('button',{name:'Keep the HEIC, mark the JPG for the Bin'});
  await resolve.waitFor();
  await copies.getByRole('radio',{name:'Keep IMG_2355.JPG'}).check();
  await copies.getByRole('button',{name:'Keep the JPG, mark the HEIC for the Bin'}).waitFor();
  await copies.getByRole('radio',{name:'Keep IMG_2355.HEIC'}).check();
  await page.evaluate(()=>document.getAnimations().forEach(animation=>animation.finish()));
  if(shots)await copies.screenshot({path:`${shots}/heic-pair-copies.png`});

  // The viewer switches between the two, and can show them separately.
  await page.locator('.gal [data-asset="1"]').click();
  const viewer=page.getByRole('dialog',{name:'Photo review'});
  await viewer.waitFor();
  const pairSwitch=viewer.getByRole('group',{name:/Show which file of this photo/});
  assert.deepEqual(await pairSwitch.getByRole('button').allInnerTexts(),['HEIC','JPG']);
  await viewer.getByRole('button',{name:'More'}).click();
  await viewer.getByRole('menuitem',{name:'Show the HEIC and JPG separately'}).click();
  await flush();
  assert.deepEqual(pairs.map(p=>[p.rawId,p.partnerId,p.paired]),[[1,2,false]],'the JPEG is taken out of the HEIC\'s stack');
  await page.getByRole('status').filter({hasText:'IMG_2355.HEIC and its JPG are separate photos now.'}).waitFor();
  await page.keyboard.press('Escape');
  await viewer.waitFor({state:'hidden'});
  await shows([1,2,3],'shown apart');
  await page.keyboard.press(process.platform==='darwin'?'Meta+z':'Control+z');
  await flush();
  assert.deepEqual(pairs.at(-1),{rawId:1,partnerId:2,paired:true});
  await shows([1,3],'undo makes them one photo again');

  // Keeping the HEIC marks the JPEG for the Bin.
  await resolve.click();
  await page.getByRole('status').filter({hasText:'Marked IMG_2355.JPG for the Bin. IMG_2355.HEIC stays. No original has moved.'}).waitFor();
  assert.deepEqual(batches.flat().map(change=>[change.assetId,change.status]),[[1,'keep'],[2,'cull']]);
  assert.equal(await copies.count(),0,'the pair is settled');
  await browser.close();
  console.log('heic-pairs ok');
})().catch(error=>{console.error(error);process.exit(1)});
