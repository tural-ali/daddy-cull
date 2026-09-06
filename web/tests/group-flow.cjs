const {chromium}=require('playwright');
const assert=require('node:assert/strict');
(async()=>{
 const b=await chromium.launch({channel:'chrome',headless:true});const context=await b.newContext();
 const assets=['DSCF0421.JPG','DSCF0421 (2).JPG','NEXT.JPG'].map((n,i)=>({id:i+1,path:'/archive/2000/2000-01-03/'+n,capturedAt:946857600,kind:'image',size:i?6106689:3627911,status:'unreviewed',favourite:false,revision:0,source:'archive',alternativeCount:0,relatedCount:i<2?1:0}));const saves=[];
 await context.route('**/api/**',r=>{const u=new URL(r.request().url()),p=u.pathname;
 if(p==='/api/stats')return r.fulfill({json:{total:3,synthetic:true,candidates:0}});
 if(p==='/api/assets'){const eligible=assets.filter(a=>!u.searchParams.get('status')||a.status===u.searchParams.get('status'));return r.fulfill({json:{assets:[eligible.find(a=>a.id<3),eligible.find(a=>a.id===3)].filter(Boolean),next:''}})}
 if(p.endsWith('/related'))return r.fulfill({json:assets.slice(0,2)});
 if(p==='/api/decisions/batch'){const ds=r.request().postDataJSON();saves.push(ds);const out=ds.map(d=>{const a=assets.find(a=>a.id===d.assetId);assert.equal(d.expectedRevision,a.revision);const old={previousStatus:a.status,previousFavourite:a.favourite};a.status=d.status;a.revision++;return {...old,revision:a.revision}});return r.fulfill({json:out})}
 if(p.startsWith('/api/media/'))return r.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="800" height="600"><rect width="800" height="600" fill="#657"/></svg>'});throw Error(p);
 });
 let page=await context.newPage();await page.goto('http://127.0.0.1:8840/');await page.locator('.tile').first().waitFor();assert.equal(await page.locator('.tile').count(),2);
 await page.locator('.tile').first().click();await page.locator('.compare-grid article').nth(1).waitFor();assert.equal(await page.locator('.intro').isVisible(),false);
 await page.keyboard.press('k');await page.getByText('Choice: keep',{exact:true}).waitFor();assert.equal(saves.length,1);assert.equal(saves[0][0].assetId,1);assert.equal(assets[1].status,'unreviewed');
 await page.getByRole('combobox',{name:'Linked preview zoom'}).selectOption('2');assert.equal(await page.locator('.zoom-content').first().evaluate(e=>e.style.transform),'translate(0px, 0px) scale(2)');
 await page.getByRole('button',{name:'Keep both files',exact:true}).click();assert.equal(assets[1].status,'unreviewed');await page.getByRole('button',{name:'Save these 2 choices',exact:true}).click();await page.getByRole('button',{name:/Next memory/}).waitFor();await page.waitForFunction(()=>document.querySelector('.comparison').textContent.includes('0 undecided'));assert.match(await page.locator('.session').textContent(),/2 \/ 20/);
 await page.getByRole('button',{name:/Undo group decision/}).click();await page.waitForFunction(()=>document.querySelector('.comparison').textContent.includes('1 undecided'));assert.equal(assets[1].status,'unreviewed');
 await page.getByRole('button',{name:'Keep both files',exact:true}).click();await page.getByRole('button',{name:'Save these 2 choices',exact:true}).click();await page.waitForFunction(()=>document.querySelector('.comparison').textContent.includes('0 undecided'));
 await page.getByRole('button',{name:/Next memory/}).click();await page.locator('.details h2').filter({hasText:'NEXT.JPG'}).waitFor();
 await page.close();page=await context.newPage();await page.goto('http://127.0.0.1:8840/');await page.locator('.details h2').filter({hasText:'NEXT.JPG'}).waitFor();assert.equal(await page.locator('.comparison').count(),0);
 await b.close();console.log(JSON.stringify({oneQueueCardPerGroup:true,focusedKeyboardChoice:true,linkedZoom:true,explicitGroupConfirmation:true,atomicGroupUndo:true,resumeAfterTabClosure:true,scope:'synthetic browser workflow'},null,2));
})().catch(e=>{console.error(e);process.exit(1)});
