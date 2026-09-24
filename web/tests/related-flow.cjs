const {chromium}=require('playwright');
const assert=require('node:assert/strict');
(async()=>{
 const b=await chromium.launch({channel:'chrome',headless:true});const page=await b.newPage();
 const assets=['DSCF0421.JPG','DSCF0421 (2).JPG'].map((n,i)=>({id:i+1,path:'/archive/2000/2000-01-03/'+n,capturedAt:946857600,kind:'image',size:i?6106689:3627911,status:'unreviewed',favourite:false,revision:0,source:'archive',alternativeCount:0,relatedCount:1}));
 const saves=[];
 await page.route('**/api/**',r=>{const p=new URL(r.request().url()).pathname;
 if(p==='/api/stats')return r.fulfill({json:{total:2,synthetic:true,candidates:0}});
 if(p==='/api/assets')return r.fulfill({json:{assets:[assets[0]],next:''}});
 if(p.endsWith('/related'))return r.fulfill({json:assets});
 if(p==='/api/decisions/batch'){saves.push(...r.request().postDataJSON());return r.fulfill({json:[{revision:1,previousStatus:'unreviewed',previousFavourite:false}]})}
 if(p.startsWith('/api/media/'))return r.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="800" height="600"/>'});throw Error(p);
 });
 await page.goto('http://127.0.0.1:8840/queue.html');await page.locator('.tile').first().waitFor();assert.equal(await page.getByText('Possible duplicate / related copy · 2 files',{exact:true}).count(),1);
 await page.locator('.tile').first().click();await page.getByRole('heading',{name:'Compare related copies',exact:true}).waitFor();await page.locator('.compare-grid article').nth(1).waitFor();assert.equal(await page.locator('.compare-grid article').count(),2);
 await page.getByRole('button',{name:'Keep this file',exact:true}).first().click();await page.getByText('Choice: keep',{exact:true}).waitFor();assert.equal(saves.length,1);assert.equal(saves[0].assetId,1);assert.equal(await page.getByText('Choice: Not decided',{exact:true}).count(),1);
 await b.close();console.log(JSON.stringify({screenshotFilenames:true,unequalFileSizes:true,visibleCandidateBadges:true,oneClickComparison:true,perFileDecisionIsolation:true,scope:'synthetic browser test; not live deployment'},null,2));
})().catch(e=>{console.error(e);process.exit(1)});
