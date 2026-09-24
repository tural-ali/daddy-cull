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
  let bin=fixture();
  const posts=[];
  await page.route('**/api/**',async route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:10,synthetic:false,snapshotAt:'',candidates:0,calendarDays:0,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:1,bin:bin.length}});
    if(url.pathname==='/api/trash')return route.fulfill({json:bin});
    if(url.pathname.startsWith('/api/trash/')){
      const body=request.postDataJSON();posts.push({path:url.pathname,body});
      const groups=new Set(bin.filter(item=>(body.keys||bin.map(other=>other.key)).includes(item.key)).map(item=>item.group));
      const hit=bin.filter(item=>groups.has(item.group));
      bin=bin.filter(item=>!groups.has(item.group));
      return route.fulfill({json:{done:hit.length,bytes:hit.reduce((sum,item)=>sum+item.size,0),failures:[]}});
    }
    if(/^\/api\/(media|bin-media|binned-media)\//.test(url.pathname))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="400" height="400"><rect width="400" height="400" fill="#6b5252"/></svg>'});
    throw new Error(`${request.method()} ${url.pathname}`);
  });
  await page.goto((process.env.APP_URL||'http://127.0.0.1:8842')+'/bin');
  await page.getByRole('heading',{name:'Bin',exact:true}).waitFor();
  await page.locator('.bingal figure').nth(4).waitFor();
  assert.equal(await page.locator('.bingal figure').count(),5,'both tools\' files are in one gallery');
  assert.match(await page.locator('.binhead .ysum').innerText(),/5 files · 5\.0 MB/);
  await page.getByRole('link',{name:/^Bin 5$/}).waitFor();
  const images=await page.locator('.bingal img').evaluateAll(list=>list.map(image=>image.getAttribute('src')));
  assert.ok(images.includes('/api/bin-media/7/preview?size=grid')&&images.includes('/api/binned-media/shot/bb/0/preview?size=grid'),'every card shows its picture');

  // Selecting one card of a batch selects the batch; shift-click selects a run.
  await page.getByRole('checkbox',{name:'Select BATCH-ONE.JPG'}).click();
  assert.equal(await page.locator('#selcount').innerText(),'2');
  await page.getByRole('checkbox',{name:'Select MARKED.JPG'}).click();
  await page.getByRole('checkbox',{name:'Select OLD-TOOL.MOV'}).click({modifiers:['Shift']});
  assert.equal(await page.locator('#selcount').innerText(),'4');

  // Restore needs no question; it only puts files back.
  await page.getByRole('button',{name:'Restore selected'}).click();
  await page.getByText('4 files put back where they came from.').waitFor();
  assert.deepEqual(posts[0],{path:'/api/trash/restore',body:{keys:['marked:11','bin:aa:12','bin:aa:13','legacy:7']}});
  assert.equal(await page.locator('.bingal figure').count(),1);
  await page.getByRole('link',{name:/^Bin 1$/}).waitFor();

  // Deletion always asks first, and Cancel sends nothing.
  bin=fixture();
  await page.goto((process.env.APP_URL||'http://127.0.0.1:8842')+'/bin');
  await page.locator('.bingal figure').nth(4).waitFor();
  await page.getByLabel('Select all').check();
  assert.equal(await page.locator('#selcount').innerText(),'5');
  await page.getByLabel('Select all').uncheck();
  await page.getByRole('checkbox',{name:'Select BATCH-TWO.JPG'}).click();
  await page.getByRole('button',{name:'Delete selected for good'}).click();
  const dialog=page.getByRole('dialog',{name:'Delete 2 files for good?'});
  await dialog.waitFor();
  assert.match(await dialog.innerText(),/2 files \(2\.0 MB\) will be permanently deleted/);
  await dialog.getByRole('button',{name:'Cancel'}).click();
  await dialog.waitFor({state:'hidden'});
  assert.equal(posts.length,1,'Cancel sent a deletion');
  await page.getByRole('button',{name:'Delete selected for good'}).click();
  await dialog.getByRole('button',{name:'Delete 2 files'}).click();
  await page.getByText('2 files permanently deleted, freeing 2.0 MB.').waitFor();
  assert.deepEqual(posts[1],{path:'/api/trash/delete',body:{keys:['bin:aa:12','bin:aa:13'],confirmation:'DELETE 2'}});

  // Empty the Bin names the count it was shown.
  await page.getByRole('button',{name:'Empty the Bin'}).click();
  const empty=page.getByRole('dialog',{name:'Empty the Bin?'});
  await empty.waitFor();
  assert.match(await empty.innerText(),/All 3 files in the Bin/);
  await empty.getByRole('button',{name:'Empty the Bin'}).click();
  await page.getByText(/The Bin was emptied: 3 files permanently deleted/).waitFor();
  assert.deepEqual(posts[2],{path:'/api/trash/empty',body:{confirmation:'DELETE 3'}});
  await page.getByText(/The Bin is empty/).waitFor();
  assert.equal(await page.locator('.fbar').count(),0);
  await page.screenshot({path:process.env.SHOT||'/tmp/faithful-bin.png'});
  await browser.close();
  console.log(JSON.stringify({oneGallery:true,previewsForBothTools:true,batchSelectedTogether:true,shiftRange:true,restoreSelected:true,deleteAsksFirst:true,cancelSendsNothing:true,emptyNamesCount:true},null,2));
})().catch(error=>{console.error(error);process.exit(1)});
