const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const {builtIns,yours,common}=require('./lib/addons.cjs');

// An addon's own page shows inside Cull's frame, sandboxed at its own
// address. Cull tells it the theme, and it may ask Cull to go to one of
// Cull's own pages, but nothing else and from nowhere else. Turned off
// elsewhere, the page gives way at once. Every addon is a synthetic fixture.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const framePage=`<!doctype html><meta charset="utf-8"><title>Hello frame</title>
<style>body{margin:0;padding:24px;font:15px system-ui;background:var(--surface,#fff);color:var(--text,#000)}</style>
<h1>Hello from the frame</h1>
<script>
  window.heard=[];
  addEventListener('message',event=>{if(event.origin==='${base}')window.heard.push(event.data)});
  window.ask=path=>parent.postMessage({type:'cull:navigate',path},'${base}');
</script>`;

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  // The script runs in the frames too, where a sandboxed page has no storage.
  await page.addInitScript(key=>{try{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`);localStorage.setItem('cull-theme','night')}catch{/* a sandboxed frame */}},'cull.streak-intro');
  const errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  const addons=[...builtIns(),...yours()];
  const hello=addons.find(addon=>addon.id==='hello-cull');
  hello.on=true;
  // An addon whose page is served from Cull's own address.
  addons.push({...hello,id:'same-place',name:'Same place',pages:[{id:'page',label:'Same place',icon:'extension',section:'tools',path:'/addons/same-place/page',url:`${base}/frame-fixture/`}]});
  const streams=[];
  const announce=()=>{for(const route of streams.splice(0))void route.fulfill({contentType:'text/event-stream',body:'retry: 600000\nevent: addons\ndata: \n\n'})};
  await page.route('http://localhost:8842/hello-frame/**',route=>route.fulfill({contentType:'text/html',body:framePage}));
  await page.route(`${base}/frame-fixture/**`,route=>route.fulfill({contentType:'text/html',body:framePage}));
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    const usual=common(url);
    if(usual)return route.fulfill(usual);
    if(url.pathname==='/api/events'){streams.push(route);return}
    if(url.pathname==='/api/addons')return route.fulfill({json:addons});
    if(url.pathname==='/api/trash')return route.fulfill({json:[]});
    if(url.pathname==='/api/trash/deleting')return route.fulfill({json:{graceDays:0,items:[],lastRun:'',lastDeleted:0,lastError:'',checkIntervalMinutes:15}});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });

  await page.goto(`${base}/addons/hello-cull/hello`);
  const iframe=page.locator('.addonframe iframe');
  await iframe.waitFor();
  assert.equal(await page.title(),'Hello · Daddy, Cull!');
  assert.equal(await page.locator('.framehead h1').textContent(),'HelloHello, Cull');
  assert.equal(await iframe.getAttribute('sandbox'),'allow-scripts allow-forms allow-popups allow-downloads allow-same-origin');
  assert.equal(await iframe.getAttribute('referrerpolicy'),'no-referrer');
  assert.equal(await iframe.getAttribute('title'),'Hello, from Hello, Cull');
  assert.equal(await page.getByRole('link',{name:'Open on its own'}).getAttribute('href'),'http://localhost:8842/hello-frame/');
  const nav=page.getByRole('navigation',{name:'Main navigation'});
  assert.equal(await nav.getByRole('link',{name:'Hello',exact:true}).getAttribute('aria-current'),'page');

  // The page is told who it is and Cull's theme, with the colours to match.
  const frame=page.frameLocator('.addonframe iframe');
  await frame.getByRole('heading',{name:'Hello from the frame'}).waitFor();
  const inner=page.frames().find(item=>item.url().startsWith('http://localhost:8842/hello-frame/'));
  await inner.waitForFunction(()=>window.heard.length>0);
  const [said]=await inner.evaluate(()=>window.heard);
  assert.deepEqual({...said,colors:undefined},{type:'cull:hello',version:1,addon:'hello-cull',page:'hello',theme:'dark',colors:undefined});
  for(const name of ['background','surface','text','muted','line','accent','onAccent'])assert.ok(said.colors[name],`the ${name} colour is given`);

  // A change of theme is passed on.
  await page.evaluate(()=>{localStorage.setItem('cull-theme','day');window.dispatchEvent(new StorageEvent('storage',{key:'cull-theme'}))});
  await inner.waitForFunction(()=>window.heard.some(message=>message.type==='cull:theme'));
  const theme=(await inner.evaluate(()=>window.heard)).find(message=>message.type==='cull:theme');
  assert.equal(theme.theme,'light');
  assert.notEqual(theme.colors.background,said.colors.background,'with the day colours');

  // Asking for anything but one of Cull's pages is ignored, and so is the
  // same message from anywhere but the frame.
  await inner.evaluate(()=>{window.ask('/api/stats');window.ask('https://example.test/');window.ask('//example.test/bin')});
  await page.evaluate(()=>window.postMessage({type:'cull:navigate',path:'/bin'},location.origin));
  await page.waitForTimeout(300);
  assert.equal(new URL(page.url()).pathname,'/addons/hello-cull/hello','nothing moved');
  await inner.evaluate(()=>window.ask('/bin'));
  await page.getByRole('heading',{name:'Bin',level:1}).waitFor();
  assert.equal(new URL(page.url()).pathname,'/bin');

  // Back returns to the frame. Turned off in another tab, it gives way.
  await page.goBack();
  await iframe.waitFor();
  hello.on=false;
  announce();
  const missing=page.locator('.addonframe.missing');
  await missing.waitFor();
  assert.equal(await missing.innerText(),'Hello is part of Hello, Cull, which is turned off. Turn it on in Addons to see it.');
  assert.equal(await page.locator('.addonframe iframe').count(),0,'the frame is gone');
  assert.equal(await nav.getByRole('link',{name:'Hello',exact:true}).count(),0,'and so is its place in the sidebar');
  if(shots){await page.waitForTimeout(300);await page.screenshot({path:`${shots}/frame-off.png`})}

  // A page no addon has.
  await page.goto(`${base}/addons/nobody/page`);
  await missing.waitFor();
  assert.equal(await missing.innerText(),'No addon has this page. It may have been taken out of the addons folder. See your addons');

  // A page served from Cull's own address is never given Cull's origin.
  await page.goto(`${base}/addons/same-place/page`);
  await iframe.waitFor();
  assert.equal(await iframe.getAttribute('sandbox'),'allow-scripts allow-forms allow-popups allow-downloads');
  await frame.getByRole('heading',{name:'Hello from the frame'}).waitFor();
  const own=page.frames().find(item=>item.url().startsWith(`${base}/frame-fixture/`));
  assert.equal(await own.evaluate(()=>{try{return String(parent.document.title)}catch{return 'kept out'}}),'kept out');

  // On a phone the frame fills what is left of the screen.
  await page.setViewportSize({width:390,height:800});
  await page.waitForTimeout(300);
  const box=await iframe.boundingBox();
  assert.ok(box.x+box.width<=390&&box.y+box.height>=780,'the frame reaches the bottom and stays on screen');
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'no sideways scroll on a phone');
  if(shots){await page.screenshot({path:`${shots}/frame-phone.png`})}
  assert.deepEqual(errors,[]);
  await browser.close();
  console.log('addon-frame: ok');
})().catch(error=>{console.error(error);process.exit(1)});
