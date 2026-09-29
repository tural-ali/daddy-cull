const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
// The Duplicates page laid out as Apple Photos: date headings newest first,
// every copy on its own tile with its size, one preview read per set, and a
// merge that keeps exactly the ticked copy.
const member=(id,path,day,kind='image')=>({id,path,day,capturedAt:Date.parse(day)/1000,kind,size:2516582,status:'unreviewed',favourite:false,revision:0,source:'archive',relatedCount:0,alternativeCount:0});
const report={candidates:6,hashed:6,settled:true,unproven:[],groups:[
  {hash:'aaaa',size:2516582,reclaimable:2516582,members:[member(91,'/archive/2025/2025-08/2025-08-05/IMG_1000.HEIC','2025-08-05'),member(92,'/archive/2025/2025-08/2025-08-05/IMG_1000 (1).HEIC','2025-08-05')]},
  {hash:'bbbb',size:3565158,reclaimable:3565158,members:[member(93,'/archive/2026/2026-07/2026-07-26/IMG_2000.HEIC','2026-07-26'),member(94,'/archive/2026/2026-09/2026-09-05/IMG_2000.HEIC','2026-09-05')]},
  {hash:'cccc',size:1363148,reclaimable:1363148,members:[member(95,'/archive/2024/2024-01/2024-01-02/CLIP.MOV','2024-01-02','video'),member(96,'/archive/2024/2024-01/2024-01-02/.culled/CLIP.MOV','2024-01-02','video')]},
]};
const svg=(w,h,fill)=>`<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}"><rect width="100%" height="100%" fill="${fill}"/></svg>`;
(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1400,height:1000}});
  const previews=[];let posted=null;
  await page.route('**/api/duplicate-report**',route=>route.fulfill({json:report}));
  await page.route(/\/api\/media\/9\d\/preview/,route=>{const id=Number(/media\/(\d+)/.exec(route.request().url())[1]);previews.push(id);return route.fulfill({contentType:'image/svg+xml',body:id===93?svg(600,800,'#6b8f71'):svg(800,533,'#4d6f94')})});
  await page.route('**/api/decisions/batch',route=>{posted=JSON.parse(route.request().postData());return route.fulfill({json:posted.map(change=>({...change,revision:1}))})});
  // A set of one, which the server should never send, is not offered.
  report.groups.push({hash:'dddd',size:1000,reclaimable:0,members:[member(97,'/archive/2026/2026-07/2026-07-25/A7401914-2.ARW','2026-07-25','raw')]});
  await page.goto((process.env.APP_URL||'http://127.0.0.1:8850').replace(/\/$/,'')+'/duplicates');
  await page.locator('.dupegroup').first().waitFor();
  await page.waitForFunction(()=>[...document.querySelectorAll('.dupefig')].filter(f=>f.querySelector('img')).every(f=>f.style.aspectRatio));
  const headings=await page.locator('.dupegrouphead h2').allInnerTexts();
  assert.equal(headings.length,3);
  assert.match(headings[0],/2026.*&.*2026/,'two dates are joined, newest set first');
  assert.match(headings[1],/2025/);
  assert.match(headings[2],/2024/);
  assert.deepEqual(await page.locator('.dupegroup').nth(0).locator('.dupesize').allInnerTexts(),['3.4 MB','3.4 MB']);
  assert.deepEqual([...new Set(previews)].sort((a,b)=>a-b),[91,93,95],'one preview per set, however many copies');
  // A portrait set keeps its shape inside the square cell.
  const portrait=await page.locator('.dupegroup').nth(0).locator('.dupefig').first().boundingBox();
  const cell=await page.locator('.dupegroup').nth(0).locator('.dupecell').first().boundingBox();
  assert.ok(Math.abs(portrait.height-cell.height)<1&&portrait.width<cell.width*0.8,`portrait fits the cell height: ${JSON.stringify({portrait,cell})}`);
  // Same names, so the folders tell the copies apart; different names show the names.
  assert.deepEqual(await page.locator('.dupegroup').nth(0).locator('.dupelabel').allInnerTexts(),['2026-07/2026-07-26','2026-09/2026-09-05']);
  assert.deepEqual(await page.locator('.dupegroup').nth(1).locator('.dupelabel').allInnerTexts(),['IMG_1000.HEIC','IMG_1000 (1).HEIC']);
  // A landscape set's label sits right under the picture.
  const landscape=await page.locator('.dupegroup').nth(1).locator('.dupefig').first().boundingBox();
  const label=await page.locator('.dupegroup').nth(1).locator('.dupelabel').first().boundingBox();
  assert.ok(label.y-(landscape.y+landscape.height)<14,`label hugs the picture: ${JSON.stringify({landscape,label})}`);
  // A copy in .culled differs from the other only by that folder.
  assert.deepEqual(await page.locator('.dupegroup').nth(2).locator('.dupelabel > span:first-child').allInnerTexts(),['2024-01-02','.culled']);
  await page.screenshot({path:process.env.SHOT||'/tmp/dupes.png',fullPage:true});
  // The plainer name is kept by default; clicking the other copy moves the tick.
  const second=page.locator('.dupegroup').nth(1);
  assert.equal(await second.locator('.dupetile.keeper .dupelabel').innerText(),'IMG_1000.HEIC');
  await second.locator('.dupechoose').nth(1).click();
  assert.equal(await second.locator('.dupetile.keeper').count(),1);
  assert.equal(await second.locator('.dupetile.keeper .dupelabel').innerText(),'IMG_1000 (1).HEIC');
  await second.getByRole('button',{name:'Merge 2 copies'}).click();
  await page.getByText(/1 copy marked for the Bin/).waitFor();
  assert.deepEqual(posted.map(change=>[change.assetId,change.status]).sort(),[[91,'cull'],[92,'keep']]);
  assert.equal(await page.locator('.dupegroup').count(),2,'the merged set leaves the page');
  // A copy already in .culled is never the default keeper.
  assert.equal(await page.locator('.dupegroup').nth(1).locator('.dupetile.keeper .dupeflag').count(),0);
  await browser.close();
  console.log(JSON.stringify({dateHeadings:true,sizeCaptions:true,onePreviewPerSet:true,portraitShape:true,keeperMoves:true,mergeKeepsTicked:true},null,2));
})().catch(error=>{console.error(error);process.exit(1)});
