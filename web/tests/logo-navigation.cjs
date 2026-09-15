const {chromium}=require('playwright');
const assert=require('node:assert/strict');
(async()=>{
 const browser=await chromium.launch({channel:'chrome',headless:true});const page=await browser.newPage();
 const assets=['PAIR.JPG','PAIR (2).JPG','SINGLE.JPG'].map((name,i)=>({id:i+1,path:'/fixture/'+name,capturedAt:1600000000,kind:'image',source:'archive',size:100,status:'unreviewed',favourite:false,revision:0,relatedCount:i<2?1:0,alternativeCount:0}));
 await page.route('**/api/**',route=>{
  assert.equal(route.request().method(),'GET','Navigation must not change decisions');
  const path=new URL(route.request().url()).pathname;
  if(path==='/api/stats')return route.fulfill({json:{total:3,synthetic:true,candidates:0}});
  if(path==='/api/assets')return route.fulfill({json:{assets:[assets[0],assets[2]],next:''}});
  if(path.endsWith('/related'))return route.fulfill({json:assets.slice(0,2)});
  if(path==='/api/bin')return route.fulfill({json:[]});
  if(path.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="800" height="600"/>'});
  throw Error(path);
 });
 const grid=async()=>{await page.getByRole('heading',{name:'Your next 2 memories',exact:true}).waitFor();assert.equal(await page.locator('.comparison,.review,.bin-workspace').count(),0)};
 const logo=async()=>{await page.getByRole('link',{name:/Daddy, Cull!/}).click();await grid()};
 await page.goto('http://127.0.0.1:8840/');await grid();
 await page.locator('.tile').first().click();await page.locator('.comparison').waitFor();await logo();
 await page.reload();await grid();
 await page.locator('.tile').last().click();await page.locator('.review').waitFor();
 await page.reload();await page.locator('.review').waitFor();await logo();
 await page.locator('.tile').first().click();await page.locator('.comparison').waitFor();
 await page.getByRole('button',{name:'Marked files & Bin',exact:true}).click();await page.getByRole('heading',{name:'Marked files & Bin',exact:true}).waitFor();await logo();
 assert.equal(await page.getByRole('combobox',{name:'Review status'}).inputValue(),'unreviewed');
 await browser.close();console.log(JSON.stringify({logoReturnsFromComparison:true,logoReturnsFromViewer:true,logoReturnsFromBin:true,gridSurvivesRefresh:true,normalViewerResumePreserved:true,noDecisionWrites:true},null,2));
})().catch(error=>{console.error(error);process.exit(1)});
