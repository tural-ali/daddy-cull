const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
(async()=>{
 const browser=await chromium.launch({channel:'chrome',headless:true});const page=await browser.newPage();let plan=null;const executions=[];
 const asset={id:1,path:'/archive/2020/A.jpg',capturedAt:1600000000,kind:'image',source:'archive',size:100,status:'cull',favourite:false,revision:1,alternativeCount:0};
 await page.route('**/api/**',async route=>{const u=new URL(route.request().url());
  if(u.pathname==='/api/stats')return route.fulfill({json:{total:1,synthetic:true,candidates:0}});
  if(u.pathname==='/api/assets')return route.fulfill({json:{assets:u.searchParams.get('status')==='cull'?[asset]:[],next:''}});
  if(u.pathname==='/api/bin')return route.fulfill({json:plan&&plan.state!=='planned'?[plan]:[]});
  if(u.pathname==='/api/bin/preview'){assert.deepEqual(route.request().postDataJSON().ids,[1]);plan={id:'fixture-plan',state:'planned',created:'2026-09-06T00:00:00Z',assets:[asset],files:[{original:'2020/A.jpg',size:100,sidecar:false,phase:'planned'},{original:'2020/A.jpg.xmp',size:20,sidecar:true,phase:'planned'}],warnings:[]};return route.fulfill({json:plan})}
  if(u.pathname==='/api/bin/execute'){const d=route.request().postDataJSON();executions.push(d);plan.state={quarantine:'bin',restore:'restored',purge:'purged'}[d.action];return route.fulfill({json:plan})}
  throw Error('Unexpected API: '+u.pathname);
 });
 await page.goto(process.env.APP_URL||'http://127.0.0.1:8840/');await page.getByRole('button',{name:'Marked files & Bin',exact:true}).click();
 async function move(){await page.locator('.marked-list input').check();await page.getByRole('button',{name:'Review 1 selected',exact:true}).click();await page.getByRole('button',{name:'Move 2 files to Bin',exact:true}).click();await page.getByRole('button',{name:'Review restore',exact:true}).waitFor()}
 await move();await page.getByRole('button',{name:'Review restore',exact:true}).click();await page.getByRole('button',{name:'Restore 2 files',exact:true}).click();await page.getByText('Files restored. They are back in the undecided queue.',{exact:true}).waitFor();
 await move();await page.getByRole('button',{name:'Review permanent deletion',exact:true}).click();const remove=page.getByRole('button',{name:'Permanently delete 2 files',exact:true});assert.equal(await remove.isDisabled(),true);await page.getByLabel('Permanent deletion confirmation').fill('DELETE 1');assert.equal(await remove.isDisabled(),true);await page.getByLabel('Permanent deletion confirmation').fill('DELETE 2');await remove.click();await page.getByText('Selected Bin files permanently deleted.',{exact:true}).waitFor();
 assert.deepEqual(executions.map(e=>e.action),['quarantine','restore','quarantine','purge']);assert.equal(executions.at(-1).confirmation,'DELETE 2');console.log(JSON.stringify({selectedManifest:true,sidecarsVisible:true,moveRestorePurgeControls:true,wrongDeleteCountBlocked:true,scope:'mock browser workflow; no originals affected'},null,2));await browser.close();
})().catch(e=>{console.error(e);process.exit(1)});
