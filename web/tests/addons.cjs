const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const {builtIns,yours,common}=require('./lib/addons.cjs');

// The Addons page turns each addon on and off, and the sidebar follows at
// once: here, and from another tab through the event stream. An addon of
// your own says what it may do before its key is written. Every addon here
// is a synthetic fixture.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  const errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  const addons=[...builtIns(),...yours()];
  const posts=[];
  let failNext='';
  // The stream is held open until the test says something changed, as the
  // server would, and then says so once.
  const streams=[];
  const announce=()=>{for(const route of streams.splice(0))void route.fulfill({contentType:'text/event-stream',body:'retry: 600000\nevent: addons\ndata: \n\n'})};
  await page.route('**/api/**',route=>{
    const request=route.request(),url=new URL(request.url());
    const usual=common(url);
    if(usual)return route.fulfill(usual);
    if(url.pathname==='/api/events'){streams.push(route);return}
    if(url.pathname==='/api/addons')return route.fulfill({json:addons});
    if(url.pathname.startsWith('/api/addons/')&&request.method()==='POST'){
      const id=url.pathname.split('/').pop(),body=request.postDataJSON();
      posts.push({id,body});
      if(failNext===id){failNext='';return route.fulfill({status:500,json:{error:'The choice could not be saved. Try again.'}})}
      const addon=addons.find(item=>item.id===id);
      addon.on=body.on;addon.chosen=true;
      return route.fulfill({json:addon});
    }
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const nav=page.getByRole('navigation',{name:'Main navigation'});
  const sidebar=async()=>nav.locator('.sidesec').evaluateAll(sections=>sections.map(section=>`${section.querySelector('.sidetitle')?.textContent??''}: ${[...section.querySelectorAll('a')].map(link=>link.textContent.trim()).join(', ')}`));

  await page.goto(`${base}/addons`);
  await page.getByRole('heading',{name:'Addons',level:1}).waitFor();
  await page.getByRole('switch',{name:'Screenshots'}).waitFor();
  assert.equal(await page.title(),'Addons · Daddy, Cull!');
  assert.deepEqual(await sidebar(),[': Today, Year, Find photos','Collections: Duplicates, Screenshots','Sync: Apple Photos','History: Log, Bin'],'only the pages of addons that are on');
  assert.equal(await page.getByRole('navigation',{name:'Settings'}).getByRole('link',{name:'Addons'}).getAttribute('aria-current'),'page');

  // Each row says whether it is ready, and why not.
  const row=id=>page.locator(`li.addon:has(#addon-${id})`);
  const status=id=>row(id).locator('.addonstatus').textContent();
  assert.equal(await status('screenshots'),'Ready. 12 screenshots wait for review.');
  assert.equal(await status('social'),'Off. No detection report has been imported yet.');
  assert.equal(await status('apple-photos'),'Needs setting up. Cull Sync is not set up yet.');
  assert.equal(await status('broken'),'Problem. addon.json could not be read.');
  assert.equal(await page.getByRole('switch',{name:'Broken'}).isDisabled(),true,'a broken addon cannot be turned on');
  assert.equal(await page.locator('li.addon .addonver').allInnerTexts().then(list=>list.join(' ')),'0.1.0 0.0.1','only yours show a version');

  // Its details: what it adds, who made it and where it lives.
  const hello=row('hello-cull');
  await hello.getByText('About Hello, Cull').click();
  const about=await hello.locator('dl.kv').innerText();
  for(const part of ['Adds','Hello','May','Read the catalogue, choices and settings.','Delete files in the Bin for good.','Folder','/state/addons/hello-cull','By','Test Author','https://example.test/hello'])assert.ok(about.includes(part),`the details name ${part}`);
  assert.equal(await hello.getByRole('link',{name:'Hello',exact:true}).count(),0,'a page is not linked while its addon is off');

  // Turning Screenshots off takes its page away at once.
  await page.getByRole('switch',{name:'Screenshots'}).click();
  await page.waitForFunction(()=>document.querySelector('[role=switch][aria-labelledby=addon-screenshots]')?.getAttribute('aria-checked')==='false');
  assert.deepEqual(posts.shift(),{id:'screenshots',body:{on:false}});
  assert.deepEqual(await sidebar(),[': Today, Year, Find photos','Collections: Duplicates','Sync: Apple Photos','History: Log, Bin']);
  assert.equal(await status('screenshots'),'Off. 12 screenshots wait for review.');

  // Its address now says it is off, and how to turn it on.
  await page.goto(`${base}/screenshots`);
  const missing=page.locator('.addonframe.missing');
  await missing.waitFor();
  assert.equal(await missing.innerText(),'Screenshots is turned off. Turn it on in Addons to see it.');
  await missing.getByRole('link',{name:'Turn it on in Addons'}).click();
  await page.getByRole('switch',{name:'Screenshots'}).waitFor();

  // An addon of your own says what it may do before it is turned on, and
  // Cancel leaves it off.
  await page.getByRole('switch',{name:'Hello, Cull'}).click();
  const ask=page.getByRole('dialog',{name:'Turn on Hello, Cull?'});
  await ask.waitFor();
  assert.equal(await ask.locator('.addonperms').innerText(),'Read Read the catalogue, choices and settings.\nReview Save choices and hearts.\nDelete Delete files in the Bin for good.');
  assert.match(await ask.getByRole('button',{name:/^Turn on/}).getAttribute('class'),/danger/,'asking to delete makes the button a warning');
  if(shots){await page.waitForTimeout(300);await page.screenshot({path:`${shots}/addons-ask.png`})}
  await page.keyboard.press('Escape');
  await ask.waitFor({state:'hidden'});
  assert.equal(posts.length,0,'Cancel sends nothing');
  await page.getByRole('switch',{name:'Hello, Cull'}).click();
  await ask.getByRole('button',{name:/^Turn on/}).click();
  await nav.getByRole('link',{name:'Hello'}).waitFor();
  assert.deepEqual(posts.shift(),{id:'hello-cull',body:{on:true}});
  assert.deepEqual(await sidebar(),[': Today, Year, Find photos','Collections: Duplicates','Sync: Apple Photos','Tools: Hello','History: Log, Bin'],'a page of your own joins its section');
  assert.equal(await nav.getByRole('link',{name:'Hello'}).getAttribute('href'),'/addons/hello-cull/hello');

  // A failure says so on the row and leaves the switch where it was.
  failNext='social';
  await page.getByRole('switch',{name:'Saved from social'}).click();
  const social=row('social');
  await social.getByRole('alert').waitFor();
  assert.equal(await social.getByRole('alert').innerText(),'The choice could not be saved. Try again.');
  assert.equal(await page.getByRole('switch',{name:'Saved from social'}).getAttribute('aria-checked'),'false');
  posts.shift();

  // Turned off somewhere else: the stream says so and the page follows.
  addons.find(item=>item.id==='apple-photos').on=false;
  announce();
  await page.waitForFunction(()=>![...document.querySelectorAll('nav[aria-label="Main navigation"] a')].some(link=>link.textContent.trim()==='Apple Photos'));
  assert.equal(await page.getByRole('switch',{name:'Apple Photos'}).getAttribute('aria-checked'),'false');
  assert.deepEqual(await sidebar(),[': Today, Year, Find photos','Collections: Duplicates','Tools: Hello','History: Log, Bin'],'an empty section is left out');

  // Day theme and a phone: nothing runs off the side.
  await page.evaluate(()=>{localStorage.setItem('cull-theme','day');window.dispatchEvent(new StorageEvent('storage',{key:'cull-theme'}))});
  await page.waitForFunction(()=>document.documentElement.dataset.theme==='day');
  if(shots){await page.waitForTimeout(300);await page.screenshot({path:`${shots}/addons-day.png`,fullPage:true})}
  await page.setViewportSize({width:390,height:800});
  await page.waitForTimeout(300);
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'no sideways scroll on a phone');
  const first=await page.locator('li.addon').first().boundingBox(),toggle=await page.getByRole('switch',{name:'Screenshots'}).boundingBox();
  assert.ok(toggle.x+toggle.width<=first.x+first.width,'the switch stays inside its row');
  if(shots){await page.screenshot({path:`${shots}/addons-phone.png`})}
  assert.deepEqual(errors,[]);
  await browser.close();
  console.log('addons: ok');
})().catch(error=>{console.error(error);process.exit(1)});
