const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// A RAW and the JPEG and HEIC exported beside it are one tile, the JPEG,
// badged RAW, JPG and HEIC. Removing it removes every file, one undo brings
// them all back, the viewer shows each file, and a wrong stack can be shown as
// separate photos and joined again with undo.
const dir='/archive/2010/2010-09/2010-09-07/';
const file=(id,name,kind,extra={})=>({id,path:dir+name,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind,source:'archive',size:20e6,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07',...extra});
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const colours={1:'#7a5b3a',2:'#2f4f6f',3:'#4f6f2f',4:'#6f2f4f',5:'#3a6b6b',6:'#6b3a6b'};

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.clock.install({time:new Date('2026-09-07T10:00:00')});
  const decisions=[];
  const pairs=[];
  const revisions={};
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:6,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:2,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:6,bytes:120e6,status:'pending',assets:[
      file(1,'A7404251.ARW','raw',{stack:[2,6],relatedCount:2}),
      file(2,'A7404251.JPG','image',{stack:[1,6],relatedCount:2}),
      file(3,'A7404252.JPG','image'),
      file(4,'A7404253.ARW','raw',{stack:[5],relatedCount:1,status:'keep'}),
      file(5,'A7404253.JPG','image',{stack:[4],relatedCount:1}),
      file(6,'A7404251.HEIC','image',{stack:[1,2],relatedCount:2}),
    ]}],memories:6,bytes:120e6}});
    if(url.pathname==='/api/decisions'){
      const body=route.request().postDataJSON();
      decisions.push(body);
      revisions[body.assetId]=(revisions[body.assetId]??0)+1;
      return route.fulfill({json:{revision:revisions[body.assetId],previousStatus:'unreviewed',previousFavourite:false}});
    }
    if(url.pathname==='/api/pairs'){pairs.push(route.request().postDataJSON());return route.fulfill({json:{paired:route.request().postDataJSON().paired}})}
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    const media=url.pathname.match(/^\/api\/media\/(\d+)\//);
    if(media)return route.fulfill({contentType:'image/svg+xml',body:`<svg xmlns="http://www.w3.org/2000/svg" width="600" height="400"><rect width="600" height="400" fill="${colours[media[1]]}"/><text x="300" y="215" font-size="48" fill="#fff" text-anchor="middle" font-family="sans-serif">file ${media[1]}</text></svg>`});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const tiles=page.locator('.gal figure');
  const tileIDs=()=>tiles.evaluateAll(nodes=>nodes.map(node=>Number(node.dataset.asset)));
  const flush=async()=>{await page.clock.runFor(300)};
  // The queue saves one choice at a time, so wait for the count to arrive.
  const saved=async count=>{for(let tries=0;decisions.length<count&&tries<50;tries++)await page.waitForTimeout(40);assert.equal(decisions.length,count)};

  await page.goto(`${base}/on/09-07`);
  await tiles.first().waitFor();
  // The stack shows as its JPEG; a RAW decided differently from its JPEG
  // stays in sight.
  assert.deepEqual(await tileIDs(),[2,3,4,5]);
  const pairTile=page.locator('[data-asset="2"]');
  assert.deepEqual(await pairTile.locator('.b.pair').allInnerTexts(),['RAW','JPG','HEIC']);
  assert.equal(await pairTile.locator('.b.dupe').count(),0,'the RAW and HEIC are not called duplicates');
  assert.equal(await page.locator('[data-asset="5"] .b.pair').count(),0);
  await page.getByText('4 memories from 6 files').waitFor();
  assert.equal(await page.locator('.dline .count').innerText(),'4 memories','a stack counts once');
  if(shots)await page.screenshot({path:`${shots}/raw-stack-grid.png`});

  // Remove takes every file; one undo brings them all back.
  await pairTile.focus();
  await page.keyboard.press('x');
  await saved(3);
  assert.deepEqual(decisions.map(d=>[d.assetId,d.status]),[[2,'cull'],[6,'cull'],[1,'cull']]);
  assert.deepEqual(await tileIDs(),[2,3,4,5],'the removed stack stays one tile');
  await page.keyboard.press(process.platform==='darwin'?'Meta+z':'Control+z');
  await saved(6);
  assert.deepEqual(decisions.slice(3).map(d=>[d.assetId,d.status]).sort((a,b)=>a[0]-b[0]),[[1,'unreviewed'],[2,'unreviewed'],[6,'unreviewed']]);
  await page.getByRole('status').filter({hasText:'Undone: removed A7404251.JPG and its HEIC and RAW.'}).waitFor();

  // The viewer shows the JPEG and, on request, each file behind it.
  await pairTile.click();
  const viewer=page.getByRole('dialog',{name:'Photo review'});
  await viewer.waitFor();
  const stage=viewer.locator('.rvstage img');
  await page.waitForFunction(()=>document.querySelector('.rvstage img')?.getAttribute('src')?.includes('/api/media/2/'));
  const pairSwitch=viewer.getByRole('group',{name:/Show which file of this photo/});
  assert.deepEqual(await pairSwitch.getByRole('button').allInnerTexts(),['JPG','HEIC','RAW']);
  assert.equal(await pairSwitch.getByRole('button',{name:'JPG'}).getAttribute('aria-pressed'),'true');
  await pairSwitch.getByRole('button',{name:'RAW'}).click();
  assert.match(await stage.getAttribute('src'),/\/api\/media\/1\//);
  assert.equal(await stage.getAttribute('alt'),'A7404251.ARW');
  await page.keyboard.press('r');
  assert.match(await stage.getAttribute('src'),/\/api\/media\/2\//,'R steps round to the JPEG');
  await page.keyboard.press('r');
  assert.match(await stage.getAttribute('src'),/\/api\/media\/6\//,'then to the HEIC');
  await page.keyboard.press('r');
  await viewer.getByRole('button',{name:'Info',exact:true}).click();
  await viewer.getByText('RAW, kept or removed with this photo').waitFor();
  await viewer.getByText('HEIC, kept or removed with this photo').waitFor();
  assert.equal(await viewer.getByRole('button',{name:'Compare similar photos'}).count(),0,'the files of the stack are not similar photos');
  await page.evaluate(()=>document.getAnimations().forEach(animation=>animation.finish()));
  if(shots)await page.screenshot({path:`${shots}/raw-stack-viewer.png`});
  await page.keyboard.press('i');
  // Moving on shows the next photo's own file.
  await page.keyboard.press('ArrowRight');
  assert.match(await stage.getAttribute('src'),/\/api\/media\/3\//);
  assert.equal(await pairSwitch.count(),0,'a lone JPEG has no switch');
  await page.keyboard.press('ArrowLeft');
  assert.equal(await pairSwitch.getByRole('button',{name:'JPG'}).getAttribute('aria-pressed'),'true','each photo opens on its JPEG');

  // A keep in the viewer keeps every file.
  const before=decisions.length;
  await page.keyboard.press('k');
  await saved(before+3);
  assert.deepEqual(decisions.slice(before).map(d=>[d.assetId,d.status]),[[2,'keep'],[6,'keep'],[1,'keep']]);
  await page.keyboard.press('ArrowLeft');

  // Showing the files separately makes each a photo of its own and keeps the
  // viewer on the JPEG.
  await viewer.getByRole('button',{name:'More'}).click();
  await viewer.getByRole('menuitem',{name:'Show the JPG, HEIC and RAW separately'}).click();
  await flush();
  assert.deepEqual(pairs.map(p=>[p.rawId,p.partnerId,p.paired]).sort((a,b)=>a[1]-b[1]),[[1,2,false],[1,6,false]]);
  assert.match(await stage.getAttribute('src'),/\/api\/media\/2\//,'the viewer stays on the JPEG');
  assert.equal(await pairSwitch.count(),0);
  await page.keyboard.press('ArrowLeft');
  assert.match(await stage.getAttribute('src'),/\/api\/media\/1\//,'the RAW is now a photo in the walk');
  await page.keyboard.press('Escape');
  await viewer.waitFor({state:'hidden'});
  assert.deepEqual(await tileIDs(),[1,2,3,4,5,6]);
  const kept=decisions.length;
  await page.locator('[data-asset="2"]').focus();
  await page.keyboard.press('x');
  await flush();
  assert.deepEqual(decisions.slice(kept).map(d=>d.assetId),[2],'a split JPEG is removed alone');

  // Undo takes back the remove, then joins the stack again.
  const mod=process.platform==='darwin'?'Meta':'Control';
  await page.keyboard.press(`${mod}+z`);
  await flush();
  await page.keyboard.press(`${mod}+z`);
  await flush();
  assert.deepEqual(pairs.slice(-2).map(p=>[p.rawId,p.partnerId,p.paired]).sort((a,b)=>a[1]-b[1]),[[1,2,true],[1,6,true]]);
  assert.deepEqual(await tileIDs(),[2,3,4,5]);

  // On a phone the viewer's bar still fits with the switch in it.
  await page.setViewportSize({width:375,height:760});
  await pairTile.click();
  await viewer.waitFor();
  await pairSwitch.waitFor();
  await page.clock.runFor(1000);
  const switchBox=await pairSwitch.boundingBox();
  assert.ok(switchBox.y>=64&&switchBox.x>=0&&switchBox.x+switchBox.width<=375,`the switch floats under the bar: ${JSON.stringify(switchBox)}`);
  const bar=await viewer.locator('.rvtop').evaluate(node=>({scroll:node.scrollWidth,width:node.clientWidth}));
  assert.ok(bar.scroll<=bar.width,`the bar fits the phone: ${JSON.stringify(bar)}`);
  for(const name of ['More','Remove','RAW']){
    const box=await viewer.getByRole('button',{name,exact:true}).boundingBox();
    assert.ok(box&&box.x>=0&&box.x+box.width<=375,`${name} is on screen: ${JSON.stringify(box)}`);
  }
  await page.evaluate(()=>document.getAnimations().forEach(animation=>animation.finish()));
  if(shots)await page.screenshot({path:`${shots}/raw-stack-phone.png`});
  await browser.close();
  console.log('raw stacks: ok');
})().catch(error=>{console.error(error);process.exit(1)});
