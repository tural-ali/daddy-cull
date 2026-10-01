const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Moving between pages is a motion, not a fresh load: the page left fades as
// it goes and cannot be pressed meanwhile, and the next rises in, or fades in
// if it is long, with no motion left on it after. A date pressed in the year opens out of its own
// square, which grows to fill the panel before the day's photographs come in
// one after another; from the day, the year zooms back out onto that square.
// With less motion asked for, pages change at once.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const make=id=>({id,path:`/archive/2010/2010-09/2010-09-07/IMG_${id}.JPG`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,
  status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07',width:4032,height:3024});
const assets=Array.from({length:40},(_,index)=>make(index+1));
const long=Array.from({length:1500},(_,index)=>make(index+1));
const cell=(md,dom,files)=>({md,dom,years:1,files,done:0,waiting:files,state:'todo'});
const month=(name,number,days)=>({name,cells:Array.from({length:days},(_,index)=>{
  const dom=index+1,md=`${String(number).padStart(2,'0')}-${String(dom).padStart(2,'0')}`;
  return cell(md,dom,(dom*7+number*13)%140);
})});
const names=['January','February','March','April','May','June','July','August','September','October','November','December'];
const lengths=[31,29,31,30,31,30,31,31,30,31,30,31];
const year={months:names.map((name,index)=>month(name,index+1,lengths[index])),prog:{dates:366,done:0,part:0,filesDone:0,files:9000},today:'09-07',streak:0,week:{days:0,seconds:0}};
const colours=['#6b7f95','#9a7b5c','#5c8a72','#8a5c7b'];
const square=id=>`<svg xmlns="http://www.w3.org/2000/svg" width="64" height="48"><rect width="64" height="48" fill="${colours[id%colours.length]}"/></svg>`;

async function open(browser,{reduced=false}={}){
  const page=await browser.newPage({viewport:{width:1280,height:800},reducedMotion:reduced?'reduce':'no-preference'});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  await page.addInitScript(()=>{window.__loaded=(window.__loaded??0)+1});
  await page.route('**/api/**',async route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:40,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:366,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname.startsWith('/api/today/')){
      // Slower than the square takes to grow, as a day read over the network is.
      await new Promise(resolve=>setTimeout(resolve,120));
      const md=url.pathname.split('/')[3];
      // Any other day is a long one, many screens of photographs.
      const day=md==='09-07'?assets:long;
      return route.fulfill({json:{md,label:'A day',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:day.length,bytes:day.length*100,status:'pending',assets:day}],memories:day.length,bytes:day.length*100}});
    }
    if(url.pathname==='/api/year')return route.fulfill({json:year});
    if(url.pathname==='/api/trash')return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/')){const id=+url.pathname.split('/')[3]||0;return route.fulfill({contentType:'image/svg+xml',body:square(id)})}
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  return page;
}
const stageClass=page=>page.evaluate(()=>document.querySelector('.pagestage')?.className);
// Every class the stage takes, and whether it could be pressed meanwhile, as
// the phases are too short to catch by asking after a click.
const watch=page=>page.evaluate(()=>{
  window.__phases=[];
  const stage=document.querySelector('.pagestage');
  const note=()=>stage.className!==window.__phases.at(-1)?.className&&window.__phases.push({className:stage.className,inert:stage.inert,current:document.querySelector('nav [aria-current="page"]')?.textContent});
  new MutationObserver(note).observe(stage,{attributes:true,attributeFilter:['class']});
});
const phases=page=>page.evaluate(()=>window.__phases);
const settled=async page=>{
  await page.waitForFunction(()=>document.querySelector('.pagestage')?.className==='pagestage'&&!document.querySelector('.zoomtile'),null,{timeout:4000});
  const moving=await page.evaluate(()=>document.getAnimations().filter(a=>a.playState==='running'&&(a.animationName??'').startsWith('page-')).map(a=>a.animationName));
  assert.deepEqual(moving,[],'no motion is left on the page');
  assert.equal(await page.evaluate(()=>getComputedStyle(document.querySelector('.pagestage')).transform),'none','nothing inside is drawn relative to a moved box');
};

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await open(browser);
  const side=page.getByRole('navigation',{name:'Main navigation'});

  // Any move: the page left fades and cannot be pressed, the next rises in.
  await page.goto(`${base}/bin`);
  await page.getByRole('heading',{name:'Bin',exact:true}).waitFor();
  await watch(page);
  await side.getByRole('link',{name:'Year'}).click();
  await settled(page);
  const seen=await phases(page);
  assert.deepEqual(seen.filter(p=>p.className!=='pagestage waiting').map(p=>p.className),['pagestage leaving','pagestage entering','pagestage'],'the page left goes, the next comes in');
  assert.deepEqual(seen.filter(p=>p.className!=='pagestage waiting').map(p=>p.inert),[true,false,false],'and the page left cannot be pressed as it goes');
  assert.equal(seen[0].current,'Year','the sidebar says where it is going at once');
  assert.ok(await page.locator('main .cell').count()>300,'the year comes in');

  // A date: its square grows to fill the panel, then the day comes in.
  const target=page.locator('main .cell[href="/on/09-07"]');
  const before=await target.boundingBox();
  await watch(page);
  await target.click();
  const tile=page.locator('.zoomtile');
  await tile.waitFor();
  // The square is read at set moments of its own growth rather than after
  // waits, which a slow machine can outlast before the day replaces it.
  const [start,end,early,middle,label]=await page.evaluate(()=>{
    const grown=document.querySelector('.zoomtile'),grow=grown.getAnimations()[0],was=grow.currentTime;
    const at=time=>{grow.currentTime=time;return grown.getBoundingClientRect().width};
    const sizes=[at(60),at(140)];
    grow.currentTime=was;
    const frames=grow.effect.getKeyframes();
    return [frames[0],frames.at(-1),...sizes,grown.innerText];
  });
  for(const edge of ['left','top','width','height'])assert.ok(Math.abs(parseFloat(start[edge])-before[{left:'x',top:'y'}[edge]??edge])<0.5,`the square starts where the date is: ${edge} ${start[edge]}`);
  assert.ok(early>before.width*2&&middle>early,`it grows: ${before.width}, ${early}, then ${middle}`);
  assert.match(label,/^7$/,'with the date on it');
  if(shots)await page.screenshot({path:`${shots}/zoom-grow.png`});
  await page.locator('main .jgrid figure').first().waitFor();
  assert.equal(new URL(page.url()).pathname,'/on/09-07');
  const panel=await page.locator('.panel').boundingBox();
  assert.ok(Math.abs(parseFloat(end.width)-panel.width)<2&&Math.abs(parseFloat(end.left)-panel.x)<2,`the square grows to fill the panel: ${end.left} ${end.width}`);
  await page.waitForFunction(()=>/\bentering zoom\b/.test(document.querySelector('.pagestage')?.className??''));
  const waits=await page.evaluate(()=>[...document.querySelectorAll('main .jgrid figure')].slice(0,3).map(f=>getComputedStyle(f).animationDelay));
  assert.deepEqual(waits,['0.06s','0.072s','0.084s'],`the photographs come in one after another: ${waits}`);
  await page.waitForTimeout(160);
  if(shots)await page.screenshot({path:`${shots}/zoom-reveal.png`});
  await settled(page);
  assert.deepEqual((await phases(page)).map(p=>p.className),['pagestage leaving zoom','pagestage entering zoom','pagestage'],'the year zooms in on the date, then the day comes in');
  if(shots)await page.screenshot({path:`${shots}/zoom-end.png`});

  // Back to the year zooms out onto the day's square.
  await page.goBack();
  await page.waitForFunction(()=>/\bentering fromday\b/.test(document.querySelector('.pagestage')?.className??''));
  const [origin,cellAt]=await page.evaluate(()=>{
    const stage=document.querySelector('.pagestage'),box=stage.getBoundingClientRect(),rect=document.querySelector('.cell[href="/on/09-07"]').getBoundingClientRect();
    return [getComputedStyle(stage).transformOrigin,`${rect.left-box.left+rect.width/2}px ${rect.top-box.top+rect.height/2}px`];
  });
  const [ox,oy]=origin.split(' ').map(parseFloat),[sx,sy]=cellAt.split(' ').map(parseFloat);
  assert.ok(Math.abs(ox-sx)<1&&Math.abs(oy-sy)<1,`the year zooms out onto 7 September: ${origin} against ${cellAt}`);
  await page.waitForTimeout(160);
  if(shots)await page.screenshot({path:`${shots}/zoom-back.png`});
  await settled(page);
  assert.equal(await page.evaluate(()=>window.__loaded),1,'all without a page load');

  // From one day straight to another, the top bar keeps what the day puts
  // there, though the day going takes its own away.
  await page.goForward();
  await settled(page);
  await side.getByRole('link',{name:'Today'}).click();
  await page.waitForURL(/\/on\/(?!09-07)/);
  await settled(page);
  const bar=page.locator('header.gbar');
  await bar.getByRole('button',{name:/^Mark A day reviewed/}).waitFor();
  await bar.getByRole('button',{name:/^A day/}).waitFor();

  // Keys meant for the page left do nothing while it goes.
  await page.evaluate(()=>{
    window.__keys=0;
    window.addEventListener('keydown',()=>{window.__keys++});
    const stage=document.querySelector('.pagestage');
    const observer=new MutationObserver(()=>{
      if(!stage.classList.contains('leaving'))return;
      observer.disconnect();
      // Where a key pressed goes: whatever has the focus.
      (document.activeElement??document.body).dispatchEvent(new KeyboardEvent('keydown',{key:'x',bubbles:true}));
    });
    observer.observe(stage,{attributes:true,attributeFilter:['class']});
  });
  await side.getByRole('link',{name:'Year'}).click();
  await page.waitForFunction(()=>!document.querySelector('.pagestage.leaving'));
  assert.equal(await page.evaluate(()=>window.__keys),0,'a key pressed as the page goes reaches nothing');
  await settled(page);

  // The page left fades where it is, and a page many screens long fades in
  // where it is too, as moving a page has the browser paint all of it, which
  // for a long one holds everything for a second or more. A short page rises.
  const motion=()=>page.evaluate(()=>new Promise(resolve=>{
    const stage=document.querySelector('.pagestage'),got={};
    const observer=new MutationObserver(()=>{
      const phase=stage.classList.contains('leaving')?'leaving':stage.classList.contains('entering')?'entering':null;
      if(!phase||got[phase])return;
      const moving=stage.getAnimations().find(a=>(a.animationName??'').startsWith('page-'));
      got[phase]={name:moving?.animationName,transform:!!moving?.effect.getKeyframes().some(frame=>frame.transform&&frame.transform!=='none'),tall:Math.round(stage.scrollHeight/innerHeight)};
      if(phase==='entering'){observer.disconnect();resolve(got)}
    });
    observer.observe(stage,{attributes:true,attributeFilter:['class']});
  }));
  let moved=motion();
  await side.getByRole('link',{name:'Today'}).click();
  const toLong=await moved;
  assert.deepEqual(toLong.leaving,{name:'page-leave',transform:false,tall:toLong.leaving.tall},`the year fades where it is: ${JSON.stringify(toLong)}`);
  assert.ok(toLong.entering.tall>4,`the day is long: ${toLong.entering.tall} screens`);
  assert.deepEqual([toLong.entering.name,toLong.entering.transform],['page-fade-in',false],`so it fades in where it is: ${JSON.stringify(toLong)}`);
  await settled(page);
  assert.equal(await page.evaluate(()=>document.querySelector('.pagestage').style.animationName),'','and keeps no motion of its own after');
  moved=motion();
  await side.getByRole('link',{name:/^Bin/}).click();
  const toShort=await moved;
  assert.deepEqual([toShort.leaving.name,toShort.leaving.transform],['page-leave',false],`the long day fades where it is: ${JSON.stringify(toShort)}`);
  assert.deepEqual([toShort.entering.name,toShort.entering.transform],['page-enter',true],`and the Bin rises in: ${JSON.stringify(toShort)}`);
  await settled(page);
  await page.close();

  // With less motion: no square, no fading, the page simply changes.
  const still=await open(browser,{reduced:true});
  await still.goto(`${base}/year`);
  await still.locator('main .cell').first().waitFor();
  await still.locator('main .cell[href="/on/09-07"]').click();
  assert.equal(await still.locator('.zoomtile').count(),0,'no square grows');
  await still.locator('main .jgrid figure').first().waitFor();
  assert.equal(await stageClass(still),'pagestage','and nothing moves');
  await browser.close();
  console.log('page motion: ok');
})().catch(error=>{console.error(error);process.exit(1)});
