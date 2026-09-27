const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// A date's page names its date once, as a pill in the search bar. The pill
// opens a calendar of the year's dates drawn by how far each review has got;
// the arrow keys walk it across months, its keys never reach the photos
// behind it, and Escape hands focus back to the pill.
const photo=(id,name)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind:'image',source:'archive',size:100,status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07'});
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const pad=value=>String(value).padStart(2,'0');
// September: the 1st reviewed, the 2nd partly, the 3rd empty, the rest to do.
const months=Array.from({length:12},(_,month)=>({name:'',cells:Array.from({length:31},(_,index)=>{
  const days=new Date(2000,month+1,0).getDate();
  if(index>=days)return null;
  const md=`${pad(month+1)}-${pad(index+1)}`;
  const state=month===8&&index===0?'done':month===8&&index===1?'part':month===8&&index===2?'none':'todo';
  return {md,dom:index+1,years:state==='none'?0:3,files:9,done:state==='done'?3:state==='part'?1:0,waiting:state==='done'?0:6,state};
})}));

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.clock.install({time:new Date('2026-09-27T10:00:00')});
  let decisions=0;
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:3,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:2,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:2,bytes:200,status:'done',assets:[photo(1,'ONE.JPG'),photo(2,'TWO.JPG')]}],memories:2,bytes:200}});
    if(url.pathname==='/api/year')return route.fulfill({json:{months,prog:{dates:1,done:0,part:0,filesDone:0,files:0},today:'09-27',streak:0,week:{days:0,seconds:0}}});
    if(url.pathname.startsWith('/api/decisions')){decisions++;return route.fulfill({status:500,json:{}})}
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });

  await page.goto(`${base}/on/09-07`);
  await page.locator('.gal figure').first().waitFor();
  assert.equal(await page.locator('.dhead').count(),0,'the date line above the grid is gone');
  assert.equal(await page.getByRole('heading',{level:1,name:'7 September'}).count(),1,'the page keeps a heading for screen readers');
  const pill=page.getByRole('search').getByRole('button',{name:/^7 September, reviewed/});
  await pill.waitFor();
  assert.equal(await pill.innerText(),'7 September');
  assert.equal(await page.getByRole('combobox').getAttribute('placeholder'),'Filter, or go to another date');
  const searchBox=await page.getByRole('search').boundingBox();
  const pillBox=await pill.boundingBox();
  assert.ok(pillBox.y>searchBox.y&&pillBox.y+pillBox.height<searchBox.y+searchBox.height,'the pill sits inside the search bar');

  // Select a photo so a stray key would change it.
  await page.locator('.gal figure').first().click();
  await page.getByRole('dialog',{name:'Photo review'}).waitFor();
  await page.keyboard.press('Escape');
  await page.getByRole('dialog',{name:'Photo review'}).waitFor({state:'hidden'});

  await pill.click();
  const calendar=page.getByRole('dialog',{name:'Choose a date'});
  await calendar.waitFor();
  assert.equal(await pill.getAttribute('aria-expanded'),'true');
  await calendar.getByText('1 of 29 dates reviewed').waitFor();
  const cell=md=>calendar.locator(`[data-md="${md}"]`);
  assert.match(await cell('09-01').getAttribute('class'),/\bdone\b/);
  assert.match(await cell('09-02').getAttribute('class'),/\bpart\b/);
  assert.match(await cell('09-03').getAttribute('class'),/\bnone\b/);
  assert.equal(await cell('09-07').getAttribute('aria-current'),'page');
  assert.match(await cell('09-27').getAttribute('class'),/\btoday\b/);
  assert.equal(await cell('09-07').evaluate(node=>node===document.activeElement),true,'the page\'s date takes focus');
  // Monday first, on this year's weeks: 1 September 2026 is a Tuesday.
  const [dow,first]=await Promise.all([calendar.locator('.dow').first().boundingBox(),cell('09-01').boundingBox()]);
  assert.ok(first.x>dow.x+dow.width-2,'the 1st sits in the Tuesday column');
  await page.evaluate(()=>document.getAnimations().forEach(animation=>animation.finish()));
  if(shots)await page.screenshot({path:`${shots}/date-calendar.png`});

  // The arrow keys walk the days and run on into the next month.
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowDown');
  assert.equal(await page.evaluate(()=>document.activeElement?.dataset.md),'10-05');
  await calendar.locator('.calmonth').getByText('October').waitFor();
  // Keys inside the calendar stay there.
  await page.keyboard.press('x');
  await page.keyboard.press('f');
  await page.clock.runFor(500);
  assert.equal(decisions,0,'no key in the calendar reached the selected photo');
  await calendar.getByRole('button',{name:'Next month'}).click();
  await calendar.locator('.calmonth').getByText('November').waitFor();
  assert.equal(await calendar.getByRole('button',{name:'Next month'}).evaluate(node=>node===document.activeElement),true,'stepping a month leaves focus on the arrow');
  await page.keyboard.press('Escape');
  await calendar.waitFor({state:'hidden'});
  assert.equal(await pill.evaluate(node=>node===document.activeElement),true,'Escape hands focus back to the pill');

  // Typing a date never reaches the photo shortcuts either.
  await page.getByRole('combobox').fill('');
  await page.getByRole('combobox').pressSequentially('5 feb');
  await page.clock.runFor(500);
  assert.equal(decisions,0,'typing in the search field changed no photo');

  // A press outside closes it; the footer steps to the dates either side.
  await pill.click();
  await calendar.waitFor();
  const steps=calendar.getByRole('navigation',{name:'Nearby dates'});
  assert.deepEqual(await steps.getByRole('link').allInnerTexts(),['6 Sep','Today','8 Sep']);
  assert.equal(await steps.getByRole('link',{name:'Today'}).getAttribute('href'),'/on/09-27');
  await page.mouse.click(900,600);
  await calendar.waitFor({state:'hidden'});

  // On a phone the pill shortens and stays beside the folded search icon.
  await page.setViewportSize({width:375,height:760});
  await pill.waitFor();
  assert.equal(await pill.innerText(),'7 Sep');
  const phoneBox=await pill.boundingBox();
  assert.ok(phoneBox.x+phoneBox.width<=375,'the pill fits the bar');
  await pill.click();
  await calendar.waitFor();
  const phoneCal=await calendar.boundingBox();
  assert.ok(phoneCal.x>=8&&phoneCal.x+phoneCal.width<=367,'the calendar spans the phone with a margin');
  await page.evaluate(()=>document.getAnimations().forEach(animation=>animation.finish()));
  if(shots)await page.screenshot({path:`${shots}/date-calendar-phone.png`});
  await browser.close();
  console.log('date pill: ok');
})().catch(error=>{console.error(error);process.exit(1)});
