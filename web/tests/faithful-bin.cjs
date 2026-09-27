const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// One Bin for files from both tools. Every name here is a synthetic fixture.
const card=(key,group,source,name,extra={})=>({key,group,source,name,original:`/archive/2020/${name}`,kind:'image',size:1048576,sidecars:0,removedAt:'2026-09-06T10:00:00Z',preview:`/api/media/${key.replace(/\D/g,'')||1}`,...extra});
const fixture=()=>[
  card('marked:11','marked:11','marked','MARKED.JPG'),
  card('bin:aa:12','bin:aa','bin','BATCH-ONE.JPG'),
  card('bin:aa:13','bin:aa','bin','BATCH-TWO.JPG'),
  card('legacy:7','legacy:batch-a','legacy','OLD-TOOL.MOV',{kind:'video',sidecars:1,disk:'disk1',preview:'/api/bin-media/7',removedAt:'2025-01-01T00:00:00Z'}),
  card('shot:bb','shot:bb','screenshot','2020-01-02_SHOT.PNG',{preview:'/api/binned-media/shot/bb/0'}),
];

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1440,height:900}});
  let bin=fixture(),grace=0;
  const posts=[];
  await page.route('**/api/**',async route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.pathname==='/api/catalogue')return route.fulfill({json:{generation:1}});
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:10,synthetic:false,snapshotAt:'',candidates:0,calendarDays:0,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:1,bin:bin.length}});
    if(url.pathname==='/api/trash')return route.fulfill({json:bin});
    if(url.pathname==='/api/trash/deleting')return route.fulfill({json:{graceDays:grace,items:[],lastRun:'',lastDeleted:0,lastError:'',checkIntervalMinutes:15}});
    if(url.pathname.startsWith('/api/trash/')){
      const body=request.postDataJSON();posts.push({path:url.pathname,body});
      const groups=new Set(bin.filter(item=>(body.keys||bin.map(other=>other.key)).includes(item.key)).map(item=>item.group));
      const hit=bin.filter(item=>groups.has(item.group));
      bin=bin.filter(item=>!groups.has(item.group));
      return route.fulfill({json:{done:hit.length,bytes:hit.reduce((sum,item)=>sum+item.size,0),failures:[],...(grace&&url.pathname!=='/api/trash/restore'?{keptDays:grace}:{})}});
    }
    if(/^\/api\/(media|bin-media|binned-media)\//.test(url.pathname))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="400" height="400"><rect width="400" height="400" fill="#6b5252"/></svg>'});
    throw new Error(`${request.method()} ${url.pathname}`);
  });
  await page.goto((process.env.APP_URL||'http://127.0.0.1:8842')+'/bin');
  await page.getByRole('heading',{name:'Bin',exact:true}).waitFor();
  await page.locator('.bingrid figure').nth(4).waitFor();
  assert.equal(await page.locator('.bingrid figure').count(),5,'both tools\' files are in one gallery');
  assert.match(await page.locator('.binhead .ysum').innerText(),/5 files · 5\.0 MB/);
  await page.getByRole('link',{name:/^Bin 5 files$/}).waitFor();
  const images=await page.locator('.bingrid img').evaluateAll(list=>list.map(image=>image.getAttribute('src')));
  assert.ok(images.includes('/api/bin-media/7/preview?size=grid')&&images.includes('/api/binned-media/shot/bb/0/preview?size=grid'),'every card shows its picture');

  // Selecting one card of a batch selects the batch; shift-click selects a run.
  // A click previews, as in Google Photos, and the preview has its own address.
  await page.locator('.bingrid figure').first().click();
  await page.getByRole('dialog',{name:'Preview of MARKED.JPG'}).waitFor();
  assert.equal(new URL(page.url()).pathname,'/bin/photo/marked%3A11');
  await page.keyboard.press('ArrowRight');
  await page.getByRole('dialog',{name:'Preview of BATCH-ONE.JPG'}).waitFor();
  assert.equal(new URL(page.url()).pathname,'/bin/photo/bin%3Aaa%3A12','the address follows the preview');
  await page.goBack();
  await page.getByRole('dialog').waitFor({state:'hidden'});
  assert.equal(new URL(page.url()).pathname,'/bin','Back closes the preview');
  await page.goto((process.env.APP_URL||'http://127.0.0.1:8842')+'/bin/photo/legacy%3A7');
  await page.getByRole('dialog',{name:'Preview of OLD-TOOL.MOV'}).waitFor();
  await page.keyboard.press('Escape');
  await page.getByRole('dialog').waitFor({state:'hidden'});
  assert.equal(new URL(page.url()).pathname,'/bin','closing a shared address leaves the page');
  assert.equal(await page.locator('.gbar.selecting').count(),0,'previewing selected something');

  // Selecting one card of a batch selects the batch; shift-click selects a run.
  const bar=page.getByRole('toolbar',{name:'Selection'});
  await page.getByRole('checkbox',{name:/^Select BATCH-ONE\.JPG/}).click();
  assert.equal(await page.locator('.selcount').innerText(),'2 selected');
  await page.getByRole('checkbox',{name:/^Select MARKED\.JPG/}).click();
  await page.getByRole('checkbox',{name:/^Select OLD-TOOL\.MOV/}).click({modifiers:['Shift']});
  assert.equal(await page.locator('.selcount').innerText(),'4 selected');

  // Restore needs no question; it only puts files back.
  await bar.getByRole('button',{name:'Restore'}).click();
  await page.getByText('4 files put back where they came from.').waitFor();
  assert.deepEqual(posts[0],{path:'/api/trash/restore',body:{keys:['marked:11','bin:aa:12','bin:aa:13','legacy:7']}});
  assert.equal(await page.locator('.bingrid figure').count(),1);
  await page.getByRole('link',{name:/^Bin 1 file$/}).waitFor();

  // Deletion always asks first, and Cancel sends nothing.
  bin=fixture();
  await page.goto((process.env.APP_URL||'http://127.0.0.1:8842')+'/bin');
  await page.locator('.bingrid figure').nth(4).waitFor();
  await page.getByRole('checkbox',{name:/^Select MARKED\.JPG/}).click();
  await bar.getByRole('button',{name:'Select all 5'}).click();
  assert.equal(await page.locator('.selcount').innerText(),'5 selected');
  await bar.getByRole('button',{name:'Clear the selection'}).first().click();
  await page.getByRole('checkbox',{name:/^Select BATCH-TWO\.JPG/}).click();
  await bar.getByRole('button',{name:'Delete for good'}).click();
  const dialog=page.getByRole('dialog',{name:'Delete 2 files for good?'});
  await dialog.waitFor();
  assert.match(await dialog.innerText(),/2 files \(2\.0 MB\) will be permanently deleted/);
  await dialog.getByRole('button',{name:'Cancel'}).click();
  await dialog.waitFor({state:'hidden'});
  assert.equal(posts.length,1,'Cancel sent a deletion');
  await bar.getByRole('button',{name:'Delete for good'}).click();
  await dialog.getByRole('button',{name:'Delete 2 files'}).click();
  await page.locator('.flash .busy').waitFor();
  await page.getByText('2 files permanently deleted, freeing 2.0 MB.').waitFor();
  assert.deepEqual(posts[1],{path:'/api/trash/delete',body:{keys:['bin:aa:12','bin:aa:13'],confirmation:'DELETE 2'}});

  // Empty the Bin names the count it was shown.
  // Empty Bin is the page's one action, top right.
  await page.getByRole('button',{name:'Empty Bin',exact:true}).click();
  const empty=page.getByRole('dialog',{name:'Empty the Bin?'});
  await empty.waitFor();
  assert.match(await empty.innerText(),/All 3 files in the Bin/);
  await empty.getByRole('button',{name:'Empty the Bin'}).click();
  // The orb shows in the button that was pressed, even when the answer is instant.
  await page.locator('.headrow .textbtn .busy').waitFor();
  await page.getByText(/The Bin was emptied: 3 files permanently deleted/).waitFor();
  assert.deepEqual(posts[2],{path:'/api/trash/empty',body:{confirmation:'DELETE 3'}});
  await page.getByText(/The Bin is empty/).waitFor();
  assert.equal(await page.locator('.gbar.selecting').count(),0);

  // With a grace period, deleting only schedules: the wording says the files
  // stay on disk and can be restored from the Log until they go.
  bin=fixture();grace=30;
  await page.goto((process.env.APP_URL||'http://127.0.0.1:8842')+'/bin');
  await page.locator('.bingrid figure').nth(4).waitFor();
  await page.getByRole('checkbox',{name:/^Select MARKED\.JPG/}).click();
  await bar.getByRole('button',{name:'Delete',exact:true}).click();
  const kept=page.getByRole('dialog',{name:'Delete 1 file?'});
  await kept.waitFor();
  assert.match(await kept.innerText(),/stay on disk for 30 days, restorable from the Log, and are then deleted automatically/);
  await kept.getByRole('button',{name:'Delete 1 file'}).click();
  await page.getByText(/1 file deleted from the Bin\. They stay on disk until .+ and can be restored from the Log until then\./).waitFor();
  assert.deepEqual(posts[3],{path:'/api/trash/delete',body:{keys:['marked:11'],confirmation:'DELETE 1'}});
  assert.equal(await page.locator('.bingrid figure').count(),4);
  await page.screenshot({path:process.env.SHOT||'/tmp/faithful-bin.png'});
  await browser.close();
  console.log(JSON.stringify({oneGallery:true,clickPreviews:true,previewAddress:true,backCloses:true,previewsForBothTools:true,batchSelectedTogether:true,shiftRange:true,restoreSelected:true,deleteAsksFirst:true,cancelSendsNothing:true,emptyNamesCount:true,graceSchedules:true},null,2));
})().catch(error=>{console.error(error);process.exit(1)});
