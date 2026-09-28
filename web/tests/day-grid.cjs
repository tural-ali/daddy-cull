const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// A date's photos sit in rows at their own shapes, as in Google Photos: every
// full row runs edge to edge with no picture cropped, rows stay near one
// height, and the last row is left short. A file the server has not measured
// takes its shape from its picture. The years can be shown oldest or newest
// first from the search bar, and the viewer walks them in that order.
const make=(year,id,width,height)=>({id,path:`/archive/${year}/${year}-09/${year}-09-07/IMG_${id}.JPG`,capturedAt:Date.parse(`${year}-09-07T12:00:00Z`)/1000+id,kind:'image',source:'archive',size:100,
  status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:`${year}-09-07`,...(width?{width,height}:{})});
const shapes=[[4032,3024],[3024,4032],[1920,1080],[3024,4032],[4000,3000],[1080,1920],[4032,3024],[3024,4032],[5472,3648],[3000,4000],[4032,3024],[3024,4032],[4032,3024],[3024,4032]];
const early=[make(2000,1,4032,3024),make(2000,2,3024,4032),make(2000,3)];
const late=shapes.map(([width,height],index)=>make(2010,10+index,width,height));
const shots=process.env.SHOTS;
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
// Tall pictures for the file with no known shape, square for the rest.
const tall='<svg xmlns="http://www.w3.org/2000/svg" width="90" height="160"><rect width="90" height="160" fill="#526b52"/></svg>';
const square='<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#6b7f95"/></svg>';

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:900}});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:17,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:2,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[
      {day:'2000-09-07',year:2000,files:3,bytes:300,status:'pending',assets:early},
      {day:'2010-09-07',year:2010,files:late.length,bytes:late.length*100,status:'pending',assets:late}],memories:3+late.length,bytes:(3+late.length)*100}});
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:url.pathname.startsWith('/api/media/3/')?tall:square});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const settled=()=>page.evaluate(()=>new Promise(resolve=>{
    let last='',still=0;
    const look=()=>{
      const now=[...document.querySelectorAll('main .jrow')].map(row=>row.getBoundingClientRect().height).join();
      still=now===last?still+1:0;last=now;
      if(still>=3)resolve();else requestAnimationFrame(look);
    };
    look();
  }));
  const grids=()=>page.evaluate(()=>[...document.querySelectorAll('main section.yr')].map(section=>{
    const grid=section.querySelector('.jgrid');
    const width=grid.getBoundingClientRect().width;
    const target=grid.querySelector('.jruler').getBoundingClientRect().height;
    return {year:section.querySelector('h2').firstChild.textContent,width,target,rows:[...grid.querySelectorAll('.jrow')].map(row=>{
      const tiles=[...row.children].map(tile=>{const box=tile.getBoundingClientRect(),img=tile.querySelector('img');return {id:tile.dataset.asset,width:box.width,height:box.height,left:box.left,natural:img.naturalWidth/img.naturalHeight}});
      return {height:row.getBoundingClientRect().height,tiles};
    })};
  }));

  await page.goto(`${base}/on/09-07`);
  await page.locator('main .jrow').first().waitFor();
  await page.waitForFunction(()=>[...document.querySelectorAll('main .jgrid img')].every(img=>img.complete&&img.naturalWidth>0));
  await settled();
  let [first,second]=await grids();
  assert.deepEqual([first.year,second.year],['2000','2010'],'the oldest year first, as the server lists them');
  assert.ok(second.rows.length>=3,'the later year fills several rows');
  const shape=Object.fromEntries(late.map(asset=>[String(asset.id),asset.width/asset.height]));
  for(const [index,row] of second.rows.entries()){
    const last=index===second.rows.length-1;
    const right=row.tiles.at(-1).left+row.tiles.at(-1).width-row.tiles[0].left;
    if(!last)assert.ok(Math.abs(right-second.width)<=1,`row ${index} runs edge to edge: ${right} of ${second.width}`);
    else assert.ok(right<=second.width+1&&row.height<=second.target+1,'the last row is not stretched');
    assert.ok(row.height>second.target*.6&&row.height<second.target*1.5,`row ${index} is near the target: ${row.height} for ${second.target}`);
    for(const tile of row.tiles)assert.ok(Math.abs(tile.width/tile.height-shape[tile.id])<.03,`tile ${tile.id} keeps its shape`);
  }
  // The file with no shape known took its picture's.
  const unknown=first.rows.flatMap(row=>row.tiles).find(tile=>tile.id==='3');
  assert.ok(Math.abs(unknown.width/unknown.height-90/160)<.03,`an unmeasured file takes its picture's shape: ${unknown.width}x${unknown.height}`);

  if(shots)await page.screenshot({path:`${shots}/day-grid.png`});

  // The order is in the filter menu, under the filters.
  const search=page.getByRole('search');
  await search.getByRole('button',{name:'Filters'}).click();
  const menu=page.getByRole('dialog',{name:'Show only'});
  const order=menu.getByRole('radiogroup',{name:'Order'});
  if(shots)await page.screenshot({path:`${shots}/day-grid-menu.png`});
  assert.equal(await order.getByRole('radio',{name:'Oldest year first'}).getAttribute('aria-checked'),'true');
  await order.getByRole('radio',{name:'Newest year first'}).click();
  await page.keyboard.press('Escape');
  const pill=search.getByRole('button',{name:'Show oldest year first again'});
  await pill.waitFor();
  assert.deepEqual((await grids()).map(grid=>grid.year),['2010','2000'],'newest first');
  // The viewer walks the years in the order shown.
  await page.locator('main section.yr').first().locator('figure').first().click();
  await page.getByRole('dialog',{name:'Photo review'}).waitFor();
  assert.equal(new URL(page.url()).pathname,'/on/09-07/photo/10');
  await page.keyboard.press('Escape');
  await page.getByRole('dialog',{name:'Photo review'}).waitFor({state:'detached'});

  // Kept on this device.
  await page.reload();
  await pill.waitFor();
  assert.deepEqual((await grids()).map(grid=>grid.year),['2010','2000']);
  // The pill puts the usual order back; typing offers the other one.
  await pill.click();
  await pill.waitFor({state:'detached'});
  assert.deepEqual((await grids()).map(grid=>grid.year),['2000','2010']);
  await search.getByRole('combobox').fill('newest');
  await search.getByRole('option',{name:/Show newest year first/}).waitFor();
  await page.keyboard.press('Enter');
  await pill.waitFor();
  // Backspace in the empty field takes the order pill off first.
  await search.getByRole('combobox').press('Backspace');
  await pill.waitFor({state:'detached'});

  // Between a desktop and a phone the sidebar folds to a rail by itself.
  await page.setViewportSize({width:900,height:800});
  await settled();
  const side=await page.evaluate(()=>({rail:document.querySelector('.shell').classList.contains('side-hidden'),menu:!!document.querySelector('.iconbtn.menu')}));
  assert.deepEqual(side,{rail:true,menu:false},'a rail and no menu button');
  if(shots)await page.screenshot({path:`${shots}/day-grid-900.png`});

  // A phone gets shorter rows, still edge to edge.
  await page.setViewportSize({width:390,height:844});
  await settled();
  [,second]=await grids();
  assert.ok(second.target<150,'shorter rows on a phone');
  const row=second.rows[0];
  assert.ok(Math.abs(row.tiles.at(-1).left+row.tiles.at(-1).width-row.tiles[0].left-second.width)<=1,'a phone row runs edge to edge');
  if(shots)await page.screenshot({path:`${shots}/day-grid-phone.png`});
  await browser.close();
  console.log('day grid: ok');
})().catch(error=>{console.error(error);process.exit(1)});
