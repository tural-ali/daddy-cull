const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const base=process.env.APP_URL||'http://127.0.0.1:8842';
const asset=(id,extra={})=>({id,path:`/archive/2020-01-02/DSC_${id}.JPG`,capturedAt:1577966400+id,kind:'image',source:'archive',size:100,status:'unreviewed',favourite:false,revision:0,relatedCount:0,alternativeCount:0,comparisonReason:'Nearby capture and similar appearance',...extra});
(async()=>{
 const browser=await chromium.launch({channel:'chrome',headless:true});
 const page=await browser.newPage({viewport:{width:1440,height:900},reducedMotion:'reduce'});
 const files=[asset(1,{stack:[2]}),asset(2,{path:'/archive/2020-01-02/DSC_1.ARW',kind:'raw',stack:[1]}),asset(3),asset(4)];
 const errors=[],batches=[];let reads=0,failPreview=true,holdSave,saveStarted=false;
 page.on('pageerror',error=>errors.push(error.message));
 await page.route('**/api/**',async route=>{
  const request=route.request(),url=new URL(request.url()),p=url.pathname;
  if(p==='/api/stats')return route.fulfill({json:{total:4,calendarDays:1,reviewedDays:0,decisions:0,favourites:0,marked:0,bin:0}});
  if(p==='/api/today/01-02')return route.fulfill({json:{md:'01-02',label:'2 January',previous:'01-01',next:'01-03',years:[{day:'2020-01-02',year:2020,files:4,bytes:400,status:'pending',assets:files}],memories:4,bytes:400}});
  if(p==='/api/assets/1/burst'){reads++;return route.fulfill({json:reads===1?[{...files[0],comparisonPending:true}]:files})}
  if(p.startsWith('/api/duplicates'))return route.fulfill({json:[]});
  if(p==='/api/decisions/batch'){
   batches.push(request.postDataJSON());saveStarted=true;
   await new Promise(resolve=>{holdSave=resolve});
   return route.fulfill({json:batches.at(-1).map(()=>({revision:1}))});
  }
  if(p.startsWith('/api/media/')){
   if(p==='/api/media/4/preview'&&url.searchParams.get('size')==='large'&&failPreview)return route.fulfill({status:503,body:'unavailable'});
   return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="800" height="600"><rect width="800" height="600" fill="#456b78"/><circle cx="400" cy="300" r="120" fill="#ed9865"/></svg>'});
  }
  return route.fulfill({status:404,json:{error:'not mocked'}});
 });
 await page.goto(`${base}/on/01-02`);
 await page.locator('[data-asset="1"]').click();
 const viewer=page.getByRole('dialog',{name:'Photo review',exact:true});
 await viewer.getByRole('button',{name:'Compare similar photos'}).click();
 const compare=page.getByRole('dialog',{name:'Compare nearby photos',exact:true});
 await compare.getByRole('status').filter({hasText:'Finding nearby shots'}).waitFor();
 assert.equal(await compare.getByRole('button',{name:'Keep keeper, remove others'}).count(),0,'analysis cannot offer decisions on an unfinished group');
 await compare.locator('.comparestrip button').nth(2).waitFor();
 const removeOthers=compare.getByRole('button',{name:'Keep keeper, remove others'});
 assert.equal(await removeOthers.isDisabled(),true);
 assert.match(await compare.innerText(),/Suggestions, not exact duplicates/);
 await compare.getByRole('button',{name:'Zoom in',exact:true}).click();
 await page.keyboard.press('ArrowRight');
 let transforms=await compare.locator('.compareimage img').evaluateAll(nodes=>nodes.map(n=>n.style.transform));
 assert.equal(transforms[0],transforms[1]);assert.match(transforms[0],/-5%/);
 await compare.getByRole('button',{name:'Fit',exact:true}).click();
 await compare.locator('.comparestrip button').nth(1).click();
 await compare.getByRole('button',{name:'Use inspected photo as reference'}).click();
 assert.match(await compare.locator('.comparecaption').first().innerText(),/DSC_3.JPG/);
 await compare.getByRole('button',{name:'Choose as keeper',exact:true}).first().click();
 assert.equal(batches.length,0,'choosing a keeper alone never writes decisions');
 await compare.locator('.comparestrip button').nth(2).click();
 await compare.getByText('Preview unavailable for DSC_4.JPG.',{exact:true}).waitFor();
 failPreview=false;await compare.getByRole('button',{name:'Retry preview'}).click();
 await page.waitForFunction(()=>[...document.querySelectorAll('.compareimage img')].every(n=>n.complete&&n.naturalWidth>0));
 assert.equal(await compare.getByRole('button',{name:'Keeper chosen',exact:true}).count(),1,'keeper stays pinned while other candidates are inspected');
 for(const width of [320,768,1024,1440]){
  await page.setViewportSize({width,height:width===320?650:900});
  await removeOthers.scrollIntoViewIfNeeded();
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),`comparison fits ${width}`);
  const box=await removeOthers.boundingBox();assert.ok(box.x>=0&&box.x+box.width<=width,`keeper action fits ${width}`);
 }
 if(process.env.SHOTS)await page.screenshot({path:`${process.env.SHOTS}/explicit-burst-keeper.png`});
 await compare.getByRole('button',{name:'Close compare'}).focus();
 await page.keyboard.press('Shift+Tab');
 assert.equal(await page.evaluate(()=>document.activeElement?.textContent),'Fit','reverse tab navigation stays within the modal');
 await removeOthers.click();
 await page.waitForFunction(()=>document.querySelector('[aria-label="Saving comparison choice"]'));
 assert.ok(saveStarted);
 assert.equal(await compare.getByRole('button',{name:'Close compare'}).isDisabled(),true);
 await page.keyboard.press('Escape');assert.equal(await compare.isVisible(),true,'cannot close a decision while it is being committed');
 assert.deepEqual(batches[0].map(job=>[job.assetId,job.status]),[[1,'cull'],[2,'cull'],[3,'keep'],[4,'cull']]);
 holdSave();await compare.waitFor({state:'hidden'});
 assert.deepEqual(errors,[]);
 await browser.close();console.log('burst review: background analysis, explicit keeper, pinned reference, linked keyboard pan, preview retry, phone layout and atomic stack decisions verified');
})().catch(error=>{console.error(error);process.exit(1)});
