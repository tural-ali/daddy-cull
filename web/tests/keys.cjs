const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const taskQueue=require('./lib/taskQueue.cjs');

// Every action shows its key the same way: a tooltip with the name then the
// key for a control, a chip on a question's buttons, and the key works. Keys
// typed into a field stay in the field. Every name here is a synthetic fixture.
const card=(key,group,name)=>({key,group,source:group.split(':')[0],name,original:`/archive/2020/${name}`,kind:'image',size:1048576,sidecars:0,removedAt:'2026-09-06T10:00:00Z',preview:`/api/media/${key.replace(/\D/g,'')||1}`});
const fixture=()=>[card('marked:11','marked:11','MARKED.JPG'),card('bin:aa:12','bin:aa','BATCH-ONE.JPG'),card('marked:14','marked:14','SINGLE.JPG')];
const shots=process.env.SHOTS;
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1440,height:900}});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  let bin=fixture();
  // Restoring and deleting are queued as tasks, which do their work at once.
  const queue=taskQueue({apply:(kind,body)=>{
    const hit=bin.filter(item=>!body.keys||body.keys.includes(item.key));
    bin=bin.filter(item=>!hit.includes(item));
    return {total:hit.length,bytes:hit.length*1048576};
  }});
  const posts=queue.posts;
  await page.route('**/api/**',async route=>{
    const request=route.request(),url=new URL(request.url());
    if(queue.handle(route,url,request))return;
    if(url.pathname==='/api/catalogue')return route.fulfill({json:{generation:1}});
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:10,synthetic:false,snapshotAt:'',candidates:0,calendarDays:0,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:1,bin:bin.length,notifications:0}});
    if(url.pathname==='/api/trash')return route.fulfill({json:bin});
    if(url.pathname==='/api/trash/deleting')return route.fulfill({json:{graceDays:0,items:[],lastRun:'',lastDeleted:0,lastError:'',checkIntervalMinutes:15}});
    if(url.pathname.startsWith('/api/trash/')){
      const body=request.postDataJSON();posts.push({path:url.pathname,body});
      const hit=bin.filter(item=>!body.keys||body.keys.includes(item.key));
      bin=bin.filter(item=>!hit.includes(item));
      return route.fulfill({json:{done:hit.length,bytes:hit.length*1048576,failures:[]}});
    }
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="400" height="400"><rect width="400" height="400" fill="#6b5252"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const tip=page.getByRole('tooltip');
  const tipText=async()=>(await tip.innerText()).replace(/\s+/g,' ').trim();
  // The app draws Mac key symbols on a Mac and key names elsewhere.
  const mac=await page.evaluate(()=>/Mac|iPhone|iPad/.test(navigator.platform));
  await page.goto(`${base}/bin`);
  await page.locator('.bingrid figure').nth(2).waitFor();

  // A page action names its key when the pointer rests on it.
  const empty=page.getByRole('button',{name:'Empty Bin',exact:true});
  assert.equal(await empty.getAttribute('aria-keyshortcuts'),'Shift+Delete');
  await empty.hover();
  await tip.waitFor();
  assert.equal(await tipText(),mac?'Empty Bin ⇧ ⌫':'Empty Bin Shift Del','keys are drawn the way this machine labels them');
  const [box,tipBox]=[await empty.boundingBox(),await tip.boundingBox()];
  assert.ok(tipBox.y>=box.y+box.height&&tipBox.y-(box.y+box.height)<12,'the tip sits just under the button');
  assert.ok(tipBox.x+tipBox.width<=1440-8,'and stays on screen at the right edge');
  if(shots)await page.screenshot({path:`${shots}/keys-tip.png`,clip:{x:900,y:0,width:540,height:140}});
  await page.mouse.move(700,500);
  await tip.waitFor({state:'detached'});

  // / goes to the search, and a key typed there is text, not a shortcut.
  await page.keyboard.press('/');
  const search=page.getByRole('search').getByRole('combobox');
  assert.equal(await search.evaluate(input=>input===document.activeElement),true,'/ focuses the search');
  await page.keyboard.type('R');
  assert.equal(await search.inputValue(),'R');
  await page.keyboard.press('Escape');
  await page.keyboard.press('Escape');
  assert.equal(await search.evaluate(input=>input===document.activeElement),false,'Esc in an empty field leaves it');
  assert.equal(posts.length,0,'nothing was restored by typing');

  // The key opens the question; its buttons show Esc and Enter, and Enter
  // answers yes although the focus starts on Cancel.
  await page.keyboard.press('Shift+Backspace');
  const question=page.getByRole('dialog',{name:'Empty the Bin?'});
  await question.waitFor();
  assert.equal(await question.getByRole('button',{name:'Cancel'}).locator('kbd').innerText(),'Esc');
  assert.equal(await question.getByRole('button',{name:'Empty the Bin'}).locator('kbd').innerText(),mac?'↵':'Enter');
  assert.equal(await question.getByRole('button',{name:'Cancel'}).evaluate(button=>button===document.activeElement),true);
  if(shots)await page.screenshot({path:`${shots}/keys-dialog.png`});
  // Page keys wait while the question is open.
  await page.keyboard.press('Shift+Backspace');
  assert.equal(posts.length,0);
  await page.keyboard.press('Escape');
  await question.waitFor({state:'hidden'});
  await page.keyboard.press('Shift+Backspace');
  await question.waitFor();
  await page.keyboard.press('Enter');
  await page.getByText(/The Bin was emptied: 3 files permanently deleted/).waitFor();
  assert.deepEqual(posts.map(post=>post.body.action),['empty']);

  // The selection bar's actions have keys too.
  bin=fixture();
  await page.goto(`${base}/bin`);
  await page.locator('.bingrid figure').nth(2).waitFor();
  await page.getByRole('checkbox',{name:/^Select MARKED\.JPG/}).click();
  const bar=page.getByRole('toolbar',{name:'Selection'});
  await bar.getByRole('button',{name:'Restore'}).hover();
  await tip.waitFor();
  assert.equal(await tipText(),'Restore R');
  // Moving along the row shows the next tip at once.
  await bar.getByRole('button',{name:'Delete for good'}).hover();
  await page.waitForTimeout(150);
  assert.equal(await tipText(),mac?'Delete for good ⌫':'Delete for good Del');
  if(shots)await page.screenshot({path:`${shots}/keys-selection.png`,clip:{x:900,y:0,width:540,height:140}});
  await page.mouse.move(700,500);
  await page.keyboard.press('r');
  await page.getByText(/1 file put back where they came from/).waitFor();
  assert.deepEqual(posts.at(-1),{path:'/api/tasks/bin',body:{action:'restore',keys:['marked:11']}});

  // The lightbox's own button carries its key on it, and the key works.
  await page.locator('.bingrid figure').first().click();
  const preview=page.getByRole('dialog',{name:/^Preview of/});
  await preview.waitFor();
  assert.equal(await preview.locator('.rvbot button kbd').innerText(),'R');
  const before=posts.length;
  await page.keyboard.press('r');
  for(let step=0;step<50&&posts.length===before;step++)await page.waitForTimeout(50);
  assert.equal(posts.length,before+1,'R restored the file on show');
  assert.equal(posts.at(-1).path,'/api/trash/restore-file');

  await browser.close();
  console.log('keys: ok');
})().catch(error=>{console.error(error);process.exit(1)});
