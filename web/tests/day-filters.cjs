const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// A day's filters live in the search bar. The filter button lists them with
// counts: one from a group keeps only its files, two from the same group keep
// either, and groups combine. Each filter that is on is a pill after the date
// that takes itself off; typing a filter's name offers it, and Backspace in
// the empty field takes the last one off. The arrow keys and the viewer walk
// the narrowed list, and the choice follows the reviewer to the next day.
const make=(id,name,kind,status,favourite)=>({id,path:`/archive/2010/2010-09/2010-09-07/${name}`,capturedAt:Date.parse('2010-09-07T12:00:00Z')/1000+id,kind,source:'archive',size:100,status,favourite,revision:0,alternativeCount:0,relatedCount:0,day:'2010-09-07'});
const assets=[
  make(1,'ONE.JPG','image','unreviewed',false),
  make(2,'TWO.MOV','video','keep',true),
  make(3,'THREE.JPG','image','cull',false),
  make(4,'FOUR.ARW','raw','keep',false),
  make(5,'FIVE.MOV','video','unreviewed',true),
];
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  // A two-digit streak, already played today, crowds the bar the most.
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  const apart=async()=>{
    const boxes=await page.locator('.gbar').evaluate(bar=>[...bar.querySelectorAll('.brand, .streakpill, .searchgo, .search, .datepill, .filterbtn, .pageacts')].filter(node=>node.offsetParent).map(node=>{const box=node.getBoundingClientRect();return {name:node.className.split(' ')[0],left:box.left,right:box.right}}));
    const order=['brand','streakpill','search','pageacts'].map(name=>boxes.find(box=>box.name===name)).filter(Boolean);
    for(let index=1;index<order.length;index++)assert.ok(order[index].left>=order[index-1].right-0.5,`${order[index].name} clears ${order[index-1].name}`);
    assert.ok(order.at(-1).right<=page.viewportSize().width,'the bar fits');
  };
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/search')return route.fulfill({json:{assets:[],next:''}});
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:5,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:2,reviewedDays:0,decisions:0,favourites:0,evidence:0,fullHashes:0,marked:0,calendarDates:366,reviewedDates:40,streak:13,reviewedToday:true}});
    if(url.pathname==='/api/streak')return route.fulfill({json:{streak:13,reviewedToday:true,best:13,days:[]}});
    if(url.pathname==='/api/today/09-07')return route.fulfill({json:{md:'09-07',label:'7 September',previous:'09-06',next:'09-08',years:[{day:'2010-09-07',year:2010,files:5,bytes:500,status:'pending',assets}],memories:5,bytes:500}});
    if(url.pathname==='/api/today/09-08')return route.fulfill({json:{md:'09-08',label:'8 September',previous:'09-07',next:'09-09',years:[{day:'2010-09-08',year:2010,files:1,bytes:100,status:'pending',assets:[{...make(6,'SIX.JPG','image','unreviewed',false),day:'2010-09-08'}]}],memories:1,bytes:100}});
    if(url.pathname.startsWith('/api/duplicates'))return route.fulfill({json:[]});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="#526b52"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  const search=page.getByRole('search');
  const button=search.locator('.filterbtn');
  const menu=page.getByRole('dialog',{name:'Show only'});
  const open=async()=>{if(!await menu.isVisible()){await button.click();await menu.waitFor()}};
  const chip=async name=>{await open();return menu.getByRole('button',{name:new RegExp(`^${name}`)})};
  const toggle=async name=>(await chip(name)).click();
  const pills=async()=>search.locator('.filterpill').evaluateAll(nodes=>nodes.map(node=>node.getAttribute('aria-label')));
  const tiles=async()=>(await page.locator('.gal figure').evaluateAll(nodes=>nodes.map(node=>Number(node.dataset.asset))));
  const box=page.getByRole('combobox',{name:'Filter, or go to a date'});

  await page.goto(`${base}/on/09-07`);
  await page.locator('.gal figure').first().waitFor();
  assert.equal(await page.locator('main [role=progressbar]').count(),0,'the progress bar is gone');
  assert.equal(await page.locator('.dfilters, .fchip').count(),0,'no chip row above the grid');
  assert.equal(await button.getAttribute('aria-label'),'Filters');
  await open();
  assert.deepEqual(await menu.getByRole('group').evaluateAll(nodes=>nodes.map(node=>node.getAttribute('aria-label'))).then(groups=>groups.length),3,'three groups');
  assert.deepEqual((await menu.locator('.filteropt').allInnerTexts()).map(text=>text.replace(/\s+/g,' ').trim()),['Favourites 2','Videos 2','Photos 3','Undecided 2','Kept 2','Removed 1']);
  assert.equal(await menu.locator('.filteropt').first().evaluate(node=>node===document.activeElement),true,'the first filter takes focus');
  assert.deepEqual(await tiles(),[1,2,3,4,5]);
  await page.keyboard.press('Escape');
  await apart();

  await toggle('Videos');
  assert.deepEqual(await tiles(),[2,5],'videos alone');
  assert.equal(await (await chip('Videos')).getAttribute('aria-pressed'),'true');
  assert.equal(await button.getAttribute('aria-label'),'Filters, 1 on');
  await toggle('Photos');
  assert.deepEqual(await tiles(),[1,2,3,4,5],'videos or photos is everything');
  await toggle('Videos');
  assert.deepEqual(await tiles(),[1,3,4],'photos, RAW included');
  await toggle('Kept');
  assert.deepEqual(await tiles(),[4],'kept photos');
  await toggle('Removed');
  assert.deepEqual(await tiles(),[3,4],'kept or removed photos');
  assert.deepEqual(await pills(),['Remove the Photos filter','Remove the Kept filter','Remove the Removed filter']);
  await page.evaluate(()=>document.getAnimations().forEach(animation=>animation.finish()));
  if(process.env.SHOTS)await page.screenshot({path:`${process.env.SHOTS}/filters-menu.png`});
  await toggle('Favourites');
  assert.equal(await page.locator('.gal figure').count(),0);
  await page.getByText('Nothing on this date matches the filters.').waitFor();
  assert.equal(await page.locator('.yr').count(),0,'a year with nothing to show is left out');
  await page.keyboard.press('Escape');
  await menu.waitFor({state:'hidden'});
  assert.equal(await button.evaluate(node=>node===document.activeElement),true,'Escape hands focus back to the button');

  // A pill takes its own filter off.
  await search.getByRole('button',{name:'Remove the Photos filter'}).click();
  await search.getByRole('button',{name:'Remove the Kept filter'}).click();
  await search.getByRole('button',{name:'Remove the Removed filter'}).click();
  assert.deepEqual(await tiles(),[2,5],'favourites alone');

  // Typing a filter's name offers it, and Enter puts it on.
  await box.fill('');
  await box.pressSequentially('vid');
  const offers=page.getByRole('listbox',{name:'Filters'});
  await offers.waitFor();
  assert.deepEqual((await offers.getByRole('option').allInnerTexts()).map(text=>text.replace(/\s+/g,' ').trim()),['Show only videos 2']);
  assert.equal(await box.getAttribute('aria-expanded'),'true');
  await page.keyboard.press('Enter');
  await offers.waitFor({state:'hidden'});
  assert.equal(await box.inputValue(),'');
  assert.deepEqual(await pills(),['Remove the Favourites filter','Remove the Videos filter']);
  assert.deepEqual(await tiles(),[2,5],'favourite videos');
  // An offer for a filter that is on takes it off.
  await box.pressSequentially('fav');
  assert.match(await offers.getByRole('option').first().innerText(),/^Stop showing only/);
  await page.keyboard.press('Escape');
  assert.equal(await box.inputValue(),'','Escape clears the field');
  // Backspace in the empty field takes the last filter off.
  await page.keyboard.press('Backspace');
  assert.deepEqual(await pills(),['Remove the Favourites filter']);
  assert.deepEqual(await tiles(),[2,5],'favourites alone again');
  // A date still goes to the date.
  await box.pressSequentially('zzz');
  await page.keyboard.press('Enter');
  await page.waitForURL(/\/library\?q=zzz$/);
  await page.getByRole('heading',{name:'Library',exact:true}).waitFor();
  await page.goBack();
  await page.locator('.gal figure').first().waitFor();

  // The keys and the viewer walk only what is shown.
  await page.locator('.gal figure').first().click();
  const viewer=page.getByRole('dialog',{name:'Photo review'});
  await viewer.waitFor();
  await page.waitForURL(/\/photo\/2$/);
  await page.keyboard.press('ArrowRight');
  await page.waitForURL(/\/photo\/5$/);
  await page.keyboard.press('ArrowRight');
  await page.waitForTimeout(200);
  assert.match(page.url(),/\/photo\/(2|5)$/,'the viewer stays among the shown files');
  await page.keyboard.press('Escape');
  await viewer.waitFor({state:'hidden'});
  await page.locator('.dline').click();
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('ArrowRight');
  await page.locator('.gal figure.sel[data-asset="5"]').waitFor();
  await page.keyboard.press('ArrowLeft');
  await page.locator('.gal figure.sel[data-asset="2"]').waitFor();

  // The choice follows to the next day in this tab.
  await page.getByRole('button',{name:/^7 September/}).click();
  await page.getByRole('navigation',{name:'Nearby dates'}).getByRole('link',{name:'8 Sep'}).click();
  await page.getByText('Nothing on this date matches the filters.').waitFor();
  assert.deepEqual(await pills(),['Remove the Favourites filter']);
  assert.equal(await (await chip('Favourites')).getAttribute('aria-pressed'),'true');
  await page.keyboard.press('Escape');
  await page.getByRole('button',{name:'Clear filters'}).click();
  await page.locator('.gal figure[data-asset="6"]').waitFor();
  assert.deepEqual(await pills(),[]);

  // On a phone the button's count stands in for the pills, and the bar fits.
  await page.setViewportSize({width:375,height:760});
  await toggle('Videos');
  await page.keyboard.press('Escape');
  assert.equal(await search.locator('.filterpills').isVisible(),false);
  assert.equal(await button.locator('.n').innerText(),'1');
  const phone=await button.boundingBox();
  assert.ok(phone.x+phone.width<=375,'the button fits the bar');
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth),375,'no sideways scroll');
  await apart();
  assert.equal(await page.locator('.brand .brandmark').isVisible(),true,'the logo keeps only its mark');
  assert.equal(await search.locator('.searchgo').isVisible(),false,'the folded field gives way to the date and the funnel');
  await open();
  const sheet=await menu.boundingBox();
  assert.ok(sheet.x>=8&&sheet.x+sheet.width<=367,'the menu spans the phone with a margin');
  await page.evaluate(()=>document.getAnimations().forEach(animation=>animation.finish()));
  if(process.env.SHOTS)await page.screenshot({path:`${process.env.SHOTS}/filters-phone.png`});
  await browser.close();
  console.log('day filters: ok');
})().catch(error=>{console.error(error);process.exit(1)});
