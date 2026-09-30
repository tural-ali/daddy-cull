const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// A RAW and the JPEG and HEIC exported beside it are one tile, the JPEG,
// badged RAW, JPG and HEIC. Removing it removes every file, one undo brings
// them all back, the viewer shows each file with its own Info, and a wrong
// stack can be shown as separate photos and joined again with undo. Remove
// in the viewer asks whether to take only the file shown or all of them.
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
    const details=url.pathname.match(/^\/api\/assets\/(\d+)\/details$/);
    if(details){
      const raw=details[1]==='1'||details[1]==='4';
      return route.fulfill({json:{camera:'SONY ILCE-7M4',lens:'FE 24-105mm F4 G OSS',shutter:'1/250',aperture:4.5,iso:100,focal:46,software:raw?'ILCE-7M4 v3.02':'Adobe Lightroom 9.5.1 (iOS)',
        width:raw?7168:3905,height:raw?5120:5858,located:!raw,modified:raw?'2010-09-07T12:00:00Z':'2026-08-08T22:41:44Z'}});
    }
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
  // Info describes the file on the stage: the RAW's own size, camera and
  // software, and the JPEG's once it is switched to, from Info itself.
  const panel=viewer.getByRole('complementary',{name:'Info'});
  const rows=()=>panel.locator('.irow').evaluateAll(nodes=>nodes.map(node=>node.innerText.replace(/\s+/g,' ').trim()));
  await panel.getByText('ILCE-7M4 v3.02').waitFor();
  const rawRows=await rows();
  assert.equal(rawRows[1],'A7404251.ARW 19.1 MB · RAW · 7168 × 5120');
  assert.equal(rawRows[2],'SONY ILCE-7M4 ƒ/4.5 · 1/250 s · 46 mm · ISO 100 FE 24-105mm F4 G OSS');
  assert.equal(rawRows[3],'ILCE-7M4 v3.02 File last saved 7 September 2010');
  assert.equal(rawRows[4],'No location recorded');
  assert.match(rawRows.at(-4),/A7404251\.ARW$/,'the folder row copies the RAW\'s path');
  const listed=panel.locator('.ifile');
  assert.deepEqual(await listed.evaluateAll(nodes=>nodes.map(node=>[node.innerText.replace(/\s+/g,' ').trim(),node.getAttribute('aria-pressed')])),
    [['A7404251.JPG 19.1 MB · JPG','false'],['A7404251.HEIC 19.1 MB · HEIC','false'],['A7404251.ARW 19.1 MB · RAW, shown','true']]);
  await listed.first().click();
  assert.match(await stage.getAttribute('src'),/\/api\/media\/2\//,'a file in Info goes on the stage');
  await panel.getByText('Adobe Lightroom 9.5.1 (iOS)').waitFor();
  assert.equal((await rows())[1],'A7404251.JPG 19.1 MB · JPG · 3905 × 5858');
  assert.equal((await rows())[4],'Records where it was taken');
  await page.keyboard.press('r');
  await page.keyboard.press('r');
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

  // Remove asks, starting on the file switched to. Esc changes nothing, O
  // takes the HEIC alone out of the stack and to the Bin, and undo puts it
  // back in the stack as it was.
  await pairTile.click();
  await viewer.waitFor();
  await page.keyboard.press('r');
  assert.match(await stage.getAttribute('src'),/\/api\/media\/6\//);
  const decided=decisions.length,paired=pairs.length;
  await page.keyboard.press('x');
  const prompt=viewer.getByRole('menu',{name:'Remove which files?'});
  await prompt.waitFor();
  assert.equal(await prompt.locator('p').innerText(),'This photo is 3 files: JPG, HEIC and RAW.');
  assert.deepEqual(await prompt.getByRole('menuitem').evaluateAll(nodes=>nodes.map(node=>node.innerText.replace(/\s+/g,' ').trim())),
    ['Only the HEIC A7404251.HEIC · 19.1 MB O','The whole photo All 3 files · 57.2 MB X']);
  assert.equal(await prompt.getByRole('menuitem',{name:/Only the HEIC/}).evaluate(node=>node===document.activeElement),true,'the file shown is offered first');
  await page.clock.runFor(2000);
  await page.evaluate(()=>document.getAnimations().forEach(animation=>animation.finish()));
  if(shots)await page.screenshot({path:`${shots}/raw-stack-remove-one.png`});
  await page.keyboard.press('Escape');
  await prompt.waitFor({state:'hidden'});
  assert.equal(decisions.length,decided,'Esc removes nothing');
  await page.keyboard.press('x');
  await page.keyboard.press('o');
  await saved(decided+1);
  assert.deepEqual(decisions.slice(decided).map(d=>[d.assetId,d.status]),[[6,'cull']]);
  assert.deepEqual(pairs.slice(paired).map(p=>[p.rawId,p.partnerId,p.paired]),[[1,6,false]]);
  assert.deepEqual(await pairSwitch.getByRole('button').allInnerTexts(),['JPG','RAW'],'the JPEG and RAW stay one photo');
  assert.match(await stage.getAttribute('src'),/\/api\/media\/2\//);
  assert.equal(await viewer.getByRole('button',{name:'Remove',exact:true}).count(),1,'the photo itself is not removed');
  await page.getByRole('status').filter({hasText:'Marked A7404251.HEIC for the Bin. The JPG and RAW stay.'}).waitFor();
  await page.keyboard.press('Escape');
  await viewer.waitFor({state:'hidden'});
  assert.deepEqual(await tileIDs(),[2,3,4,5,6],'the HEIC is a photo of its own, marked for the Bin');
  assert.equal(await page.locator('[data-asset="6"] .undo').count(),1);
  await page.keyboard.press(`${mod}+z`);
  await saved(decided+2);
  assert.deepEqual(decisions.at(-1).assetId,6);
  assert.deepEqual(pairs.slice(-1).map(p=>[p.rawId,p.partnerId,p.paired]),[[1,6,true]]);
  await page.getByRole('status').filter({hasText:'Undone: removed only the HEIC of A7404251.JPG.'}).waitFor();
  await page.waitForFunction(()=>!document.querySelector('.gal figure[data-asset="6"]'));
  assert.deepEqual(await tileIDs(),[2,3,4,5]);
  assert.deepEqual(await pairTile.locator('.b.pair').allInnerTexts(),['RAW','JPG','HEIC']);

  // Removing the JPEG that stands for the photo moves the viewer to what is
  // left, and X twice still removes the whole photo.
  await pairTile.click();
  await viewer.waitFor();
  await page.keyboard.press('x');
  assert.equal(await prompt.getByRole('menuitem',{name:/The whole photo/}).evaluate(node=>node===document.activeElement),true,'on the JPEG the whole photo is offered first');
  await prompt.getByRole('menuitem',{name:/Only the JPG/}).click();
  await saved(decided+3);
  assert.deepEqual(decisions.at(-1).assetId,2);
  await page.waitForFunction(()=>document.querySelector('.rvstage img')?.getAttribute('src')?.includes('/api/media/6/'));
  assert.deepEqual(await pairSwitch.getByRole('button').allInnerTexts(),['HEIC','RAW']);
  await page.keyboard.press('x');
  await page.keyboard.press('x');
  await saved(decided+5);
  assert.deepEqual(decisions.slice(-2).map(d=>[d.assetId,d.status]),[[6,'cull'],[1,'cull']]);
  await page.keyboard.press('Escape');
  await viewer.waitFor({state:'hidden'});
  await page.keyboard.press(`${mod}+z`);
  await saved(decided+7);
  await page.keyboard.press(`${mod}+z`);
  await saved(decided+8);
  await page.waitForFunction(()=>document.querySelectorAll('.gal figure').length===4);
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
