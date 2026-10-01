const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const base=process.env.APP_URL||'http://127.0.0.1:8842';
const shots=process.env.SHOTS;

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  for(const [width,theme] of [[1440,'night'],[390,'day']]){
    const page=await browser.newPage({viewport:{width,height:900}});
    const errors=[];page.on('pageerror',error=>errors.push(error.message));
    await page.addInitScript(chosen=>localStorage.setItem('cull-theme',chosen),theme);
    await page.route('**/api/**',async route=>{
      const path=new URL(route.request().url()).pathname;
      if(path==='/api/stats')return route.fulfill({json:{total:10,calendarDates:366,reviewedDates:22,bin:0}});
      if(path==='/api/log'){await new Promise(done=>setTimeout(done,800));return route.fulfill({json:[]})}
      if(path==='/api/trash')return route.fulfill({json:[]});
      if(path==='/api/trash/deleting')return route.fulfill({json:{items:[],graceDays:0}});
      if(path==='/api/tasks')return route.fulfill({json:{active:2,tasks:[
        {id:'known',kind:'bin.restore',label:'Restore photos',state:'running',total:4,done:1,failed:0,cancelled:0,failures:[]},
        {id:'unknown',kind:'bin.restore',label:'Count photos',state:'queued',total:0,done:0,failed:0,cancelled:0,failures:[]},
      ]}});
      if(path==='/api/year'){
        await new Promise(done=>setTimeout(done,1200));
        return route.fulfill({json:{months:[{name:'January',cells:[{md:'01-01',dom:1,years:1,files:2,done:0,waiting:2,state:'todo'}]}],prog:{dates:366,done:22,part:0,filesDone:0,files:10},streak:0}});
      }
      return route.fulfill({status:404,json:{error:'not mocked'}});
    });
    await page.goto(`${base}/log`);
    await page.locator('.pageload .busy.big').waitFor();
    assert.equal(await page.locator('.pageload .busy-mark .tile').count(),3);
    assert.equal(await page.locator('.pageload .progress-track.indeterminate').count(),1);
    await page.getByRole('heading',{name:'Log',exact:true}).waitFor();
    await page.getByRole('button',{name:/^Tasks,/}).click();
    const known=page.getByRole('progressbar',{name:'Restore photos'});
    const unknown=page.getByRole('progressbar',{name:'Count photos'});
    await known.waitFor();
    assert.equal(await known.getAttribute('aria-valuenow'),'1');
    assert.equal(await known.getAttribute('aria-valuemax'),'4');
    assert.equal(await known.locator('.progress-fill').evaluate(node=>node.style.width),'25%');
    assert.equal(await unknown.getAttribute('aria-valuenow'),null,'unknown totals never claim zero percent');
    assert.equal(await unknown.getAttribute('class'),'progress-track indeterminate meter');
    const taskStyle=await known.evaluate(node=>{const css=getComputedStyle(node);return [css.height,css.borderRadius,getComputedStyle(node.firstElementChild).backgroundColor]});
    await page.waitForTimeout(250);
    if(shots)await page.screenshot({path:`${shots}/loading-tasks-${theme}.png`});
    await page.keyboard.press('Escape');
    await page.getByRole('navigation',{name:'Main navigation'}).getByRole('link',{name:'Year',exact:true}).click();
    const loader=page.locator('.navigationload');
    await page.waitForFunction(()=>{const status=document.querySelector('.navigationload');return status&&Number(getComputedStyle(status).opacity)>.99});
    assert.equal(await page.locator('.pagestage h1').textContent(),'Log','loading retains the old page');
    assert.equal(await loader.locator('canvas').count(),0);
    const box=await loader.boundingBox();assert.ok(box.x>=0&&box.x+box.width<=width);
    if(shots)await page.screenshot({path:`${shots}/loading-navigation-${theme}.png`});
    await page.emulateMedia({reducedMotion:'reduce'});
    assert.equal(await loader.evaluate(node=>node.getAnimations({subtree:true}).filter(animation=>animation.playState==='running').length),0,'reduced motion stops tiles and rails');
    await page.locator('.yearview').waitFor();
    const yearStyle=await page.locator('.yearview .progress-track').evaluate(node=>{const css=getComputedStyle(node);return [css.height,css.borderRadius,getComputedStyle(node.firstElementChild).backgroundColor]});
    assert.deepEqual(yearStyle,taskStyle,'calendar and task rails share dimensions and colour');
    assert.deepEqual(errors,[]);
    await page.close();
  }
  await browser.close();console.log('loading consistency: ok');
})().catch(error=>{console.error(error);process.exit(1)});
