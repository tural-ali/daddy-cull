const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const path=require('node:path');

// A Live Photo is one tile, its photo, badged Live. The badge plays the
// video over the tile, muted, without opening the photo; the viewer plays it
// over the still, at the still's size, on its Live button or L, and shows the
// still again when it ends. A link to the video, as the catalogue once listed
// it on its own, opens the photo. Every file is a synthetic fixture.
const clip=fs.readFileSync(path.join(__dirname,'fixtures','clip.webm'));
const dir='/archive/2026/2026-09/2026-09-26/';
const file=(id,name,extra={})=>({id,path:dir+name,capturedAt:Date.parse('2026-09-26T12:00:00Z')/1000+id,kind:'image',source:'archive',size:3e6,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2026-09-26',...extra});
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const colours={1:'#7a5b3a',2:'#2f4f6f'};

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  const played=[];
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:2,synthetic:false,snapshotAt:'2026-09-26 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-26')return route.fulfill({json:{md:'09-26',label:'26 September',previous:'09-25',next:'09-27',years:[{day:'2026-09-26',year:2026,files:2,bytes:6e6,status:'pending',assets:[
      file(1,'IMG_5472.HEIC',{live:true}),
      file(2,'IMG_5473.HEIC'),
    ]}],memories:2,bytes:6e6,clips:{9:1}}});
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(/^\/api\/assets\/\d+\/details$/.test(url.pathname))return route.fulfill({json:{camera:'Apple iPhone 15 Pro',width:600,height:600,located:false,modified:'2026-09-26T12:00:00Z'}});
    const live=url.pathname.match(/^\/api\/media\/(\d+)\/live$/);
    if(live){played.push(Number(live[1]));return route.fulfill({contentType:'video/webm',body:clip})}
    const media=url.pathname.match(/^\/api\/media\/(\d+)\//);
    if(media)return route.fulfill({contentType:'image/svg+xml',body:`<svg xmlns="http://www.w3.org/2000/svg" width="600" height="600"><rect width="600" height="600" fill="${colours[media[1]]}"/><text x="300" y="315" font-size="48" fill="#fff" text-anchor="middle" font-family="sans-serif">file ${media[1]}</text></svg>`});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });

  await page.goto(`${base}/on/09-26`);
  const tile=page.locator('[data-asset="1"]');
  await tile.waitFor();
  const badge=tile.getByRole('button',{name:'Play the Live Photo'});
  assert.equal(await badge.innerText(),'LIVE');
  assert.equal(await page.locator('[data-asset="2"] .b.live').count(),0,'a photo with no video has no badge');

  // The badge plays the video over the tile, muted, and leaves the grid be.
  await badge.click();
  const inTile=tile.locator('video.liveclip');
  await inTile.waitFor();
  assert.equal(await inTile.evaluate(video=>video.muted),true);
  assert.equal(new URL(page.url()).pathname,'/on/09-26','the badge does not open the photo');
  const [tileBox,videoBox]=await Promise.all([tile.boundingBox(),inTile.boundingBox()]);
  assert.deepEqual(videoBox,tileBox,'the video fills the tile');
  await page.waitForFunction(()=>{const video=document.querySelector('video.liveclip');return video&&video.currentTime>0});
  if(shots)await page.screenshot({path:`${shots}/live-tile.png`});
  await tile.getByRole('button',{name:'Stop the Live Photo'}).click();
  await inTile.waitFor({state:'detached'});

  // In the viewer it plays over the still, with sound, and the still comes
  // back when it ends.
  await tile.click();
  const viewer=page.getByRole('dialog',{name:'Photo review'});
  await viewer.waitFor();
  const play=viewer.getByRole('button',{name:'Play the Live Photo'});
  await play.waitFor();
  await page.locator('.rv.on:not(.flight)').waitFor();
  await page.keyboard.press('l');
  const onStage=viewer.locator('video.rvlive');
  await onStage.waitFor();
  assert.equal(await onStage.evaluate(video=>video.muted),false);
  await page.waitForFunction(()=>{const video=document.querySelector('video.rvlive');return video&&video.currentTime>0});
  const still=await viewer.locator('.rvstage img').boundingBox();
  assert.deepEqual(await onStage.boundingBox(),still,'the video covers the still exactly');
  assert.equal(await viewer.getByRole('button',{name:'Stop the Live Photo'}).getAttribute('aria-pressed'),'true');
  if(shots)await page.screenshot({path:`${shots}/live-viewer.png`});
  await onStage.waitFor({state:'detached',timeout:6000});
  assert.equal(await play.getAttribute('aria-pressed'),'false','the still is back once the video ends');
  // A click stops it early.
  await play.click();
  await onStage.waitFor();
  await onStage.click();
  await onStage.waitFor({state:'detached'});
  assert.equal(await viewer.count(),1,'stopping the video leaves the viewer open');
  // The next photo has no video to play.
  await page.keyboard.press('ArrowRight');
  await viewer.locator('.rvstage img[src*="/api/media/2/"]').waitFor();
  assert.equal(await viewer.locator('.rvact.live').count(),0);
  await page.keyboard.press('l');
  assert.equal(await viewer.locator('video.rvlive').count(),0);
  assert.deepEqual(played.filter((id,index)=>played.indexOf(id)===index),[1]);

  // A link to the video opens its photo.
  await page.goto(`${base}/on/09-26/photo/9`);
  await viewer.locator('.rvstage img[src*="/api/media/1/"]').waitFor();
  await viewer.getByRole('button',{name:'Play the Live Photo'}).waitFor();

  // On a phone the badge fits the tile's corner.
  await page.setViewportSize({width:390,height:844});
  await page.goto(`${base}/on/09-26`);
  await badge.waitFor();
  const [small,corner]=await Promise.all([badge.boundingBox(),tile.boundingBox()]);
  assert.ok(small.x+small.width<=corner.x+corner.width&&small.y>=corner.y,'the badge sits inside its tile');
  if(shots)await page.screenshot({path:`${shots}/live-phone.png`});

  await browser.close();
  console.log('live photos: ok');
})().catch(error=>{console.error(error);process.exit(1)});
