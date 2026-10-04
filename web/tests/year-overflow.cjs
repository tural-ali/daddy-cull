const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// The year's calendar fills a desktop without a sideways scroll. The red dot
// on a date with new files, the ring on today and a date grown under the
// pointer all reach past the date's square, so the edge ones must still fit.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const length=[31,29,31,30,31,30,31,31,30,31,30,31];
const months=length.map((days,month)=>({name:new Date(Date.UTC(2000,month,1)).toLocaleString('en',{month:'long',timeZone:'UTC'}),cells:Array.from({length:31},(_day,index)=>index>=days?null:{
  md:`${String(month+1).padStart(2,'0')}-${String(index+1).padStart(2,'0')}`,dom:index+1,years:3,files:40,done:0,waiting:20+index*4,state:'todo',
  // New files on the last date of August and on the first of every month.
  ...(month===7&&index===30||index===0?{fresh:2}:{})})}));

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1440,height:900}});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:10,synthetic:false,snapshotAt:'',candidates:0,calendarDays:366,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/year')return route.fulfill({json:{months,prog:{dates:366,done:0,part:0,filesDone:0,files:14640},today:'08-31',streak:0,week:{days:0,seconds:0}}});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const scroll=()=>page.locator('.calendar').evaluate(node=>({width:node.scrollWidth-node.clientWidth,height:node.scrollHeight-node.clientHeight}));
  for(const width of [2000,1440,1100,1000]){
    await page.setViewportSize({width,height:900});
    await page.goto(`${base}/year`);
    // Loading the year plays the opening, whose dates fly in from past the
    // edges; the calendar is measured once they have landed.
    await page.locator('.yearview[class="yearview"] .cmonth').nth(11).waitFor({timeout:8000});
    assert.deepEqual(await scroll(),{width:0,height:0},`no scroll at ${width}px`);
    // A date grown under the pointer at the far edge still fits.
    await page.locator('.cmonth').nth(7).locator('.cell').last().hover();
    await page.waitForTimeout(300);
    assert.deepEqual(await scroll(),{width:0,height:0},`no scroll with the pointer on the edge at ${width}px`);
    // The month's name sits in view at the left, not cut by the edge.
    const [calendar,label]=await Promise.all([page.locator('.calendar').boundingBox(),page.locator('.mlabel').first().boundingBox()]);
    assert.ok(label.x>=calendar.x,'the month name is not cut');
    // The calendar runs the panel's full width, its squares growing to fill it.
    const [main,cell]=await Promise.all([page.locator('main').evaluate(node=>{const box=node.getBoundingClientRect(),style=getComputedStyle(node);return box.width-parseFloat(style.paddingLeft)-parseFloat(style.paddingRight)}),page.locator('.cell:not(.blank)').first().boundingBox()]);
    assert.ok(Math.abs(calendar.width-16-main)<=2,`the calendar fills the panel at ${width}px: ${calendar.width} for ${main}`);
    if(width===2000)assert.ok(cell.width>=44,`big squares on a wide screen: ${cell.width}`);
    if(shots)await page.screenshot({path:`${shots}/year-${width}.png`});
  }
  // A phone scrolls sideways, which is the design there.
  await page.setViewportSize({width:390,height:844});
  await page.goto(`${base}/year`);
  await page.locator('.yearview[class="yearview"] .cmonth').nth(11).waitFor({timeout:8000});
  assert.ok((await scroll()).width>0,'a phone scrolls the year sideways');
  await browser.close();
  console.log('year overflow: ok');
})().catch(error=>{console.error(error);process.exit(1)});
