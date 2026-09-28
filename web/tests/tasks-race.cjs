const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const taskQueue=require('./lib/taskQueue.cjs');

// A read of the task list already on its way when a deletion is queued was
// sent before the task existed. The page reads again rather than trusting
// it, so the deletion is still announced the moment it is done, not at the
// next idle read half a minute later. Every name is a synthetic fixture.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const card=(key,name)=>({key,group:key,source:'marked',name,original:`/archive/2020/${name}`,kind:'image',size:1048576,sidecars:0,removedAt:'2026-09-06T10:00:00Z',preview:`/api/media/${key.replace(/\D/g,'')}`});

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  let bin=[card('marked:11','FIRST.JPG'),card('marked:12','SECOND.JPG')];
  const queue=taskQueue({apply:(kind,body)=>{
    const hit=bin.filter(item=>body.keys.includes(item.key));
    bin=bin.filter(item=>!hit.includes(item));
    return {total:hit.length,bytes:hit.length*1048576};
  }});
  // The page's first read of the list is held until the deletion is queued,
  // then answered with the list as it was before.
  let release,held=new Promise(resolve=>{release=resolve});
  let first=true;
  await page.route('**/api/**',async route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.pathname==='/api/tasks'&&first){first=false;await held;return route.fulfill({json:{tasks:[],active:0}})}
    if(queue.handle(route,url,request))return;
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:10,synthetic:false,snapshotAt:'',screenshots:0,bin:bin.length,notifications:0}});
    if(url.pathname==='/api/trash')return route.fulfill({json:bin});
    if(url.pathname==='/api/trash/deleting')return route.fulfill({json:{graceDays:0,items:[],lastRun:'',lastDeleted:0,lastError:'',checkIntervalMinutes:15}});
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="400" height="400"><rect width="400" height="400" fill="#6b5252"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await page.goto(`${base}/bin`);
  await page.locator('.bingrid figure').nth(1).waitFor();
  const bar=page.getByRole('toolbar',{name:'Selection'});
  await page.getByRole('checkbox',{name:/^Select FIRST\.JPG/}).click();
  await bar.getByRole('button',{name:'Select all 2'}).click();
  await bar.getByRole('button',{name:'Delete for good'}).click();
  await page.getByRole('dialog',{name:'Delete 2 files for good?'}).getByRole('button',{name:'Delete 2 files'}).click();
  await page.getByText('Permanently deleting 2 files. It carries on in the background under Tasks.').waitFor();
  release();
  await page.getByText('2 files permanently deleted, freeing 2.0 MB.').waitFor({timeout:4000});
  await browser.close();
  console.log('tasks race: ok');
})().catch(error=>{console.error(error);process.exit(1)});
