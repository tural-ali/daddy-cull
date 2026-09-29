const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const {builtIns,common}=require('./lib/addons.cjs');

// The Google Photos page reads a Takeout inbox. With nothing in it the steps
// for getting an export out of Google are open; with an export the photos
// are sorted into tabs by what checking them against the library came to.
// Photos the library lacks are added as a task and leave the page at once,
// skipped ones can be offered again, and the viewer says why each photo is
// where it is. Every name is a synthetic fixture.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const at='2026-09-29T08:00:00Z';
const colours=['#6b5252','#4f6b52','#52606b','#6b6452','#5d526b','#526b69'];

const google={id:'google-photos',name:'Google Photos',version:'1',summary:'Add what Google Photos holds and the library lacks.',icon:'photo_library',
  pages:[{id:'google',label:'Google Photos',icon:'photo_library',section:'sync',path:'/google-photos'}],builtIn:true,on:true,chosen:true,status:{state:'ready',detail:'4 photos can be added.'},routes:5};

function fixture(){
  const item=(id,name,outcome,extra={})=>({id,name,kind:/\.(mp4|mov)$/i.test(name)?'video':'image',size:2400000+id*1000,taken:'2019-08-14T12:00:00',outcome,
    reason:{missing:'The library has no copy of this.',alternative:'The library holds IMG_0010.JPG from the same day, but not these bytes.',uncertain:'Google did not say when this was taken.',
      represented:'The library already holds these bytes.',removed:'You removed the library’s copy in Cull.'}[outcome],
    state:'waiting',favourite:false,people:[],archive:'takeout-20260901-001.zip',copies:1,...extra});
  return [
    item(1,'IMG_0001.JPG','missing',{favourite:true}),
    item(2,'IMG_0002.JPG','missing'),
    item(3,'IMG_0003.JPG','missing'),
    item(4,'PXL_20190814_120000.MP4','missing'),
    item(10,'IMG_0010.JPG','alternative',{match:{assetId:710,path:'/archive/2019/2019-08/2019-08-14/IMG_0010.JPG',size:4800000}}),
    item(20,'Screenshot.png','uncertain',{taken:''}),
    item(30,'IMG_0030.JPG','represented',{match:{assetId:730,path:'/archive/2019/2019-08/2019-08-14/IMG_0030.JPG',size:2430000}}),
    item(40,'IMG_0040.JPG','removed'),
  ];
}

function server({inbox=true,archives=true}={}){
  const items=fixture(),posts=[],tasks=[];
  const tabOf=item=>item.state==='added'?'added':item.state==='skipped'?'skipped':item.queued?null:item.outcome;
  const counts=()=>{
    const out={missing:0,alternative:0,uncertain:0,represented:0,removed:0,added:0,skipped:0,checking:0};
    for(const item of items){const tab=tabOf(item);if(tab)out[tab]++}
    return out;
  };
  const view=item=>{const copy={...item};delete copy.queued;return copy};
  const read=tab=>({inbox,scanning:false,scannedAt:at,archives:archives?[{name:'takeout-20260901-001.zip',kind:'zip',size:52428800,media:items.length,scannedAt:at}]:[],
    counts:archives?counts():{missing:0,alternative:0,uncertain:0,represented:0,removed:0,added:0,skipped:0,checking:0},
    tab,items:archives?items.filter(item=>tabOf(item)===tab).map(view):[],next:0});
  function handle(route,url,request){
    if(url.pathname==='/api/addons')return route.fulfill({json:[...builtIns(),google]});
    if(url.pathname==='/api/tasks'){
      const answer={tasks:tasks.map(task=>({...task})),active:tasks.filter(task=>task.state==='queued').length};
      // The runner gets to what was queued by the next read.
      for(const task of tasks){
        if(task.state!=='queued')continue;
        for(const id of task.ids){const found=items.find(item=>item.id===id);found.queued=false;found.state='added';found.addedAs=`/archive/2019/2019-08/2019-08-14/${found.name}`;found.addedAt=at}
        Object.assign(task,{state:'done',done:task.total,finishedAt:at});
      }
      return route.fulfill({json:answer});
    }
    const found=common(url);
    if(found)return route.fulfill(found);
    if(url.pathname==='/api/google-photos')return route.fulfill({json:read(url.searchParams.get('tab')||'missing')});
    if(url.pathname.startsWith('/api/google-photos/media/')){
      const id=Number(url.pathname.split('/')[4]);
      return route.fulfill({contentType:'image/svg+xml',body:`<svg xmlns="http://www.w3.org/2000/svg" width="400" height="300"><rect width="400" height="300" fill="${colours[id%colours.length]}"/></svg>`});
    }
    if(url.pathname.startsWith('/api/google-photos/')&&request.method()==='POST'){
      const body=request.postDataJSON()||{};
      posts.push({path:url.pathname,body});
      if(url.pathname==='/api/google-photos/add'){
        for(const id of body.ids)items.find(item=>item.id===id).queued=true;
        const task={id:`task-${tasks.length+1}`,kind:'google-photos.add',label:`Add ${body.ids.length} photos from Google Photos to the library`,state:'queued',
          total:body.ids.length,done:0,failed:0,cancelled:0,bytes:0,failures:[],createdAt:at,undoable:false,ids:body.ids};
        tasks.unshift(task);
        const answer={...task};delete answer.ids;
        return route.fulfill({status:202,json:answer});
      }
      if(url.pathname==='/api/google-photos/skip'){
        for(const id of body.ids)items.find(item=>item.id===id).state=body.skip?'skipped':'waiting';
        return route.fulfill({json:{changed:body.ids.length}});
      }
      if(url.pathname==='/api/google-photos/scan')return route.fulfill({status:202,json:read('missing')});
    }
    return route.fulfill({status:404,json:{error:'not mocked'}});
  }
  return {handle,posts,items};
}

async function open(browser,options,viewport={width:1280,height:800},theme='night'){
  const page=await browser.newPage({viewport});
  await page.addInitScript(([key,chosen])=>{
    const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`);
    localStorage.setItem('cull-theme',chosen);
  },['cull.streak-intro',theme]);
  const state=server(options);
  const errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  await page.route('**/api/**',route=>{const request=route.request();return state.handle(route,new URL(request.url()),request)});
  return {page,state,errors};
}

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});

  // An empty inbox: the steps are open, and there are no tabs to show.
  {
    const {page,errors}=await open(browser,{archives:false});
    await page.goto(`${base}/google-photos`);
    await page.getByRole('heading',{name:'Google Photos'}).waitFor();
    assert.equal(await page.locator('details.gsteps').getAttribute('open'),'','the steps are open while nothing has arrived');
    assert.equal(await page.locator('.gsteps li').count(),6);
    assert.equal(await page.locator('.gsteps a[href="https://takeout.google.com"]').count(),1,'the steps link to Takeout');
    assert.equal(await page.locator('.gtabs').count(),0,'no tabs before an export');
    assert.ok(await page.getByRole('link',{name:'Google Photos'}).first().isVisible(),'the sidebar lists the page');
    if(shots)await page.screenshot({path:`${shots}/google-photos-empty.png`,fullPage:true});
    assert.deepEqual(errors,[]);
    await page.close();
  }

  // No inbox at all: the page says how to give Cull one.
  {
    const {page}=await open(browser,{inbox:false,archives:false},undefined,'day');
    await page.goto(`${base}/google-photos`);
    await page.getByText(/started without a Takeout inbox/).waitFor();
    assert.equal(await page.getByRole('button',{name:'Read the inbox now'}).count(),0,'nothing to read');
    if(shots)await page.screenshot({path:`${shots}/google-photos-no-inbox-day.png`,fullPage:true});
    await page.close();
  }

  // An export in the inbox.
  const {page,state,errors}=await open(browser,{});
  await page.goto(`${base}/google-photos`);
  const tiles=page.locator('.gphotos [data-item]');
  await tiles.nth(3).waitFor();
  assert.equal(await page.locator('details.gsteps').getAttribute('open'),null,'the steps fold away once an export arrives');
  const tab=label=>page.locator('.gtabs button',{hasText:label});
  const count=async label=>Number((await tab(label).locator('.n').textContent()).replace(/\D/g,''));
  assert.equal(await count('Not in the library'),4);
  assert.equal(await count('Different copies'),1);
  assert.equal(await count('Unsure'),1);
  assert.equal(await count('Already in the library'),1);
  assert.equal(await count('Removed in Cull'),1);
  assert.equal(await tab('Not in the library').getAttribute('aria-pressed'),'true');
  assert.match(await page.locator('.ysum').textContent(),/1 export.*50\.0 MB.*8.*photos and videos.*5.*can be added/);
  assert.equal(await page.locator('.gphotos .gfav').count(),1,'a starred photo shows its heart');
  if(shots)await page.screenshot({path:`${shots}/google-photos-night.png`});

  // The viewer says why a photo is here, and adds it from there.
  await tiles.first().click();
  const viewer=page.locator('.lb');
  await viewer.waitFor();
  assert.match(await viewer.locator('.greason').textContent(),/The library has no copy of this\./);
  assert.equal(await viewer.locator('.lbtitle b').textContent(),'IMG_0001.JPG');
  assert.equal(await viewer.locator('.rvpos').textContent(),'1 / 4');
  const [bar0,stage0]=[await viewer.locator('.rvbot').boundingBox(),await viewer.locator('.rvstage').boundingBox()];
  assert.ok(bar0.y>=stage0.y+stage0.height-1,'the actions sit under the photo, not over it');
  await page.waitForTimeout(300);
  if(shots)await page.screenshot({path:`${shots}/google-photos-viewer.png`});
  await page.keyboard.press('Escape');
  await viewer.waitFor({state:'detached'});

  // Two photos are added: they leave at once, and are under Added after the task.
  await page.getByRole('checkbox',{name:/^Select IMG_0002\.JPG/}).click();
  await page.getByRole('checkbox',{name:/^Select IMG_0003\.JPG/}).click();
  const bar=page.getByRole('toolbar',{name:'Selection'});
  await bar.getByRole('button',{name:'Add to the library'}).click();
  await page.getByText('Adding 2 photos to the library. It carries on in the background under Tasks.').waitFor();
  assert.deepEqual(state.posts.at(-1),{path:'/api/google-photos/add',body:{ids:[2,3]}});
  assert.equal(await tiles.count(),2,'the added photos left without waiting');
  assert.equal(await count('Not in the library'),2);
  await page.getByText('Added 2 photos to the library. They are under Added.').waitFor({timeout:10000});
  await tab('Added').click();
  await page.waitForFunction(()=>new URL(location.href).searchParams.get('tab')==='added');
  await page.locator('.gphotos [data-item="2"]').waitFor();
  assert.equal(await tiles.count(),2);
  assert.equal(await count('Added'),2);
  assert.equal(await bar.count(),0,'nothing is selected on a new tab');

  // Unsure photos cannot be added, only skipped; Skipped offers them again.
  await tab('Unsure').click();
  await page.locator('.gphotos [data-item="20"]').waitFor();
  assert.equal(await page.locator('.gphotos [data-item="20"] .b.warn').textContent(),'undated');
  await page.getByRole('checkbox',{name:/^Select Screenshot\.png/}).click();
  assert.equal(await bar.getByRole('button',{name:'Add to the library'}).count(),0,'an unsure photo cannot be added');
  await page.keyboard.press('s');
  await page.getByText(/^Skipped 1 photo\. It is under Skipped/).waitFor();
  assert.equal(await count('Unsure'),0);
  assert.equal(await count('Skipped'),1);
  await page.getByText('Cull could place every photo.').waitFor();
  await tab('Skipped').click();
  await page.locator('.gphotos [data-item="20"]').waitFor();
  await page.getByRole('checkbox',{name:/^Select Screenshot\.png/}).click();
  await bar.getByRole('button',{name:'Offer again'}).click();
  await page.getByText('1 photo is offered again.').waitFor();
  await page.waitForFunction(()=>document.querySelectorAll('.gphotos [data-item]').length===0);
  assert.equal(await count('Unsure'),1);
  assert.deepEqual(state.posts.filter(post=>post.path==='/api/google-photos/skip').map(post=>post.body),[{ids:[20],skip:true},{ids:[20],skip:false}]);

  // A different copy links to the library’s own.
  await tab('Different copies').click();
  await page.locator('.gphotos [data-item="10"]').click();
  await viewer.waitFor();
  assert.equal(await viewer.getByRole('link',{name:'Open the library’s copy'}).getAttribute('href'),'/api/media/710/original');
  await page.keyboard.press('Escape');

  // A reload keeps the tab.
  await page.reload();
  await page.locator('.gphotos [data-item="10"]').waitFor();
  assert.equal(await tab('Different copies').getAttribute('aria-pressed'),'true');
  assert.deepEqual(errors,[]);
  await page.close();

  // Day, and a phone.
  for(const {name,viewport,theme} of [{name:'day',viewport:{width:1280,height:800},theme:'day'},{name:'phone',viewport:{width:390,height:844},theme:'night'}]){
    const {page:view,errors:seen}=await open(browser,{},viewport,theme);
    await view.goto(`${base}/google-photos`);
    await view.locator('.gphotos [data-item]').nth(3).waitFor();
    const overflow=await view.evaluate(()=>document.documentElement.scrollWidth-document.documentElement.clientWidth);
    assert.ok(overflow<=0,`${name}: the page does not scroll sideways (${overflow}px)`);
    if(shots)await view.screenshot({path:`${shots}/google-photos-${name}.png`,fullPage:true});
    assert.deepEqual(seen,[]);
    await view.close();
  }

  await browser.close();
  console.log('google-photos: ok');
})().catch(error=>{console.error(error);process.exit(1)});
