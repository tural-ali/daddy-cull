const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Screenshots and Saved from social keep their filters in the search bar, as
// Today does, and load more as the list is scrolled instead of paging.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shot=(id,kept)=>({id,path:`/screenshots/2024-05-${String(id%28+1).padStart(2,'0')}_shot${id}.${id%10===0?'mov':'png'}`,capturedAt:0,kind:id%10===0?'video':'image',source:'screenshots',size:1000,
  status:kept?'keep':'unreviewed',favourite:false,revision:kept?1:0,alternativeCount:0,relatedCount:0,day:`2024-05-${String(id%28+1).padStart(2,'0')}`,name:`shot${id}.png`,state:'waiting'});
const shots=[...Array.from({length:250},(_,index)=>shot(index+1,false)),...Array.from({length:5},(_,index)=>shot(1001+index,true))];
const clip=(id,band)=>({id,path:`/archive/2023/2023-01/2023-01-02/clip${id}.mp4`,capturedAt:0,kind:'video',source:'archive',size:5000,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,
  day:'2023-01-02',name:`clip${id}.mp4`,score:band==='likely'?9:2,band,evidence:'',width:720,height:1280,duration:12,letterbox:false,poster:true});
const clips=Array.from({length:130},(_,index)=>clip(index+1,index<4?'likely':'possible'));
const svg='<svg xmlns="http://www.w3.org/2000/svg" width="90" height="160"><rect width="90" height="160" fill="#6b7f95"/></svg>';

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  const asked=[];
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:1,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/screenshots'){
      const review=url.searchParams.get('review'),kind=url.searchParams.get('kind'),from=Number(url.searchParams.get('from'));
      asked.push(`shots ${review}/${kind}/${from}`);
      const ofKind=shots.filter(item=>!kind||item.kind===kind);
      const onSide=item=>review==='all'||(review==='reviewed')===(item.status==='keep');
      const matching=ofKind.filter(onSide);
      return route.fulfill({json:{items:matching.slice(from,from+120),total:matching.length,bytes:matching.length*1000,
        unreviewed:ofKind.filter(item=>item.status!=='keep').length,reviewed:ofKind.filter(item=>item.status==='keep').length,
        stills:shots.filter(onSide).filter(item=>item.kind==='image').length,recordings:shots.filter(onSide).filter(item=>item.kind==='video').length}});
    }
    if(url.pathname==='/api/social'){
      const band=url.searchParams.get('band'),from=Number(url.searchParams.get('from'));
      asked.push(`social ${band}/${from}`);
      const matching=clips.filter(item=>!band||(band==='social')===(item.band==='likely'));
      const social=clips.filter(item=>item.band==='likely').length;
      return route.fulfill({json:{items:matching.slice(from,from+120),total:clips.length,shown:matching.length,bytes:clips.length*5000,likely:social,possible:clips.length-social,watch:0,letterboxed:0,social,unsure:clips.length-social,kept:0,marked:0}});
    }
    if(url.pathname.startsWith('/api/media/')||url.pathname.startsWith('/api/social-poster/'))return route.fulfill({contentType:'image/svg+xml',body:svg});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const search=page.getByRole('search');
  const tiles=page.locator('main .rt');
  const pill=label=>search.getByRole('button',{name:`Remove the ${label} filter`});
  const bottom=async()=>{await page.evaluate(()=>window.scrollTo(0,document.documentElement.scrollHeight))};
  const count=async n=>{for(let i=0;i<60&&await tiles.count()!==n;i++){await bottom();await page.waitForTimeout(50)}assert.equal(await tiles.count(),n,'tiles loaded')};

  await page.goto(`${base}/screenshots`);
  await tiles.first().waitFor();
  assert.equal(await page.locator('main .pager').count(),0,'no page buttons under the heading or the grid');
  assert.equal(await page.getByText(/Next \d+/).count(),0,'no Next button');
  await pill('Not reviewed').waitFor();
  assert.match(await page.locator('.ysum').innerText(),/^250 to review/);
  assert.equal(await tiles.count(),120,'one page to start');
  await count(250);
  assert.deepEqual(asked.splice(0),['shots //0','shots //120','shots //240'],'each page read once, at the length loaded');

  // The filter button lists every filter with its count.
  await search.getByRole('button',{name:'Filters, 1 on'}).click();
  const menu=page.getByRole('dialog',{name:'Show only'});
  const counts=await menu.locator('.filteropt').evaluateAll(options=>options.map(option=>`${option.querySelector('.label').textContent} ${option.querySelector('.n').textContent}`));
  assert.deepEqual(counts,['Not reviewed 250','Reviewed 5','Stills 225','Recordings 25']);
  await page.keyboard.press('Escape');

  // Taking the pill off shows both sides, in place, and the address follows.
  await pill('Not reviewed').click();
  await page.waitForFunction(()=>(document.querySelector('.ysum')?.textContent??'').startsWith('255 screenshots'));
  assert.equal(new URL(page.url()).searchParams.get('review'),'all');
  assert.equal(asked.at(-1),'shots all//0');

  // Typing a filter's name offers it; Enter puts it on.
  await search.getByRole('combobox').fill('record');
  await search.getByRole('option',{name:/recordings/i}).waitFor();
  await page.keyboard.press('Enter');
  await pill('Recordings').waitFor();
  await page.waitForFunction(()=>(document.querySelector('.ysum')?.textContent??'').startsWith('25 screenshots'));
  assert.equal(new URL(page.url()).searchParams.get('show'),'video');
  assert.equal(await tiles.count(),25);

  // The actions follow what is selected: a kept file offers Mark not
  // reviewed and filing, not Keep.
  await pill('Recordings').click();
  await search.getByRole('button',{name:'Filters'}).click();
  await menu.getByRole('button',{name:/^Reviewed/}).click();
  await page.keyboard.press('Escape');
  await page.waitForFunction(()=>(document.querySelector('.ysum')?.textContent??'').startsWith('5 reviewed'));
  await tiles.first().getByRole('checkbox').click();
  const bar=page.getByRole('toolbar',{name:'Selection'});
  await bar.getByRole('button',{name:'Mark not reviewed'}).waitFor();
  assert.equal(await bar.getByRole('button',{name:'Keep',exact:true}).count(),0);
  await bar.getByRole('button',{name:/Copy into the archive/}).waitFor();

  // A reload keeps the filters the address holds.
  await page.reload();
  await pill('Reviewed').waitFor();
  assert.match(await page.locator('.ysum').innerText(),/^5 reviewed/);

  asked.length=0;
  await page.goto(`${base}/social`);
  await tiles.first().waitFor();
  assert.equal(await page.locator('main .pager').count(),0);
  assert.equal(await tiles.count(),120);
  await count(130);
  assert.deepEqual(asked.splice(0),['social /0','social /120']);
  await search.getByRole('button',{name:'Filters'}).click();
  const bands=await menu.locator('.filteropt').evaluateAll(options=>options.map(option=>`${option.querySelector('.label').textContent} ${option.querySelector('.n').textContent}`));
  assert.deepEqual(bands,['Likely social 4','Not sure 126']);
  await menu.getByRole('button',{name:/^Likely social/}).click();
  await page.keyboard.press('Escape');
  await pill('Likely social').waitFor();
  await page.waitForFunction(()=>document.querySelectorAll('main .rt').length===4);
  assert.equal(new URL(page.url()).searchParams.get('band'),'social');
  assert.match(await page.locator('.ysum').innerText(),/130 undecided.*4 likely social/);
  await browser.close();
  console.log('search filters and scroll: ok');
})().catch(error=>{console.error(error);process.exit(1)});
