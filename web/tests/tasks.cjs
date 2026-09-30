const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');

// Deleting from the Bin never waits: the cards leave at once and the work
// shows under Tasks beside the bell, whose ring fills as it goes. The panel
// lists each task with its progress, stops one, tries its leftovers again
// and clears what finished. Every name is a synthetic fixture.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const card=(key,name)=>({key,group:key,source:'marked',name,original:`/archive/2020/${name}`,kind:'image',size:1048576,sidecars:0,removedAt:'2026-09-06T10:00:00Z',preview:`/api/media/${key.replace(/\D/g,'')}`});
const at='2026-09-28T09:00:00Z';

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  let bin=[card('marked:11','FIRST.JPG'),card('marked:12','SECOND.JPG'),card('marked:13','THIRD.JPG')];
  // One task that finished with two files it could not move, and whatever
  // the page queues, which moves on only when the test says.
  const tasks=[{id:'old',kind:'screenshots.remove',label:'Move 5 screenshots to the Bin',state:'failed',total:5,done:3,failed:2,cancelled:0,bytes:0,
    failures:[{name:'IMG_0001.PNG',error:'The file is no longer where the catalogue says.'},{name:'IMG_0002.PNG',error:'The file is no longer where the catalogue says.'}],createdAt:at,finishedAt:at,undoable:true}];
  const posts=[];
  const list=()=>({tasks,active:tasks.filter(task=>task.state==='queued'||task.state==='running').length});
  const advance=()=>{const task=tasks.find(item=>item.state==='queued'||item.state==='running');task.state='running';task.startedAt=at;task.done++;if(task.done===task.total){task.state='done';task.finishedAt=at}};
  await page.route('**/api/**',route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:10,synthetic:false,snapshotAt:'',screenshots:0,bin:bin.length,notifications:0}});
    if(url.pathname==='/api/notifications')return route.fulfill({json:{unread:0,items:[]}});
    if(url.pathname==='/api/trash')return route.fulfill({json:bin});
    if(url.pathname==='/api/trash/deleting')return route.fulfill({json:{graceDays:0,items:[],lastRun:'',lastDeleted:0,lastError:'',checkIntervalMinutes:15}});
    if(url.pathname==='/api/tasks')return route.fulfill({json:list()});
    if(url.pathname.startsWith('/api/tasks/')){
      const body=request.postDataJSON();posts.push({path:url.pathname,body});
      if(url.pathname==='/api/tasks/bin'){
        const hit=bin.filter(item=>body.keys.includes(item.key));
        bin=bin.filter(item=>!hit.includes(item));
        const task={id:`t${tasks.length}`,kind:'bin.delete',label:`Delete ${hit.length} files from the Bin`,state:'queued',total:hit.length,done:0,failed:0,cancelled:0,bytes:0,failures:[],createdAt:at,undoable:false};
        tasks.unshift(task);
        return route.fulfill({status:202,json:task});
      }
      if(url.pathname==='/api/tasks/clear'){tasks.splice(0,tasks.length,...tasks.filter(task=>task.state==='queued'||task.state==='running'));return route.fulfill({json:list()})}
      const [,,,id,action]=url.pathname.split('/'),task=tasks.find(item=>item.id===id);
      if(action==='cancel'){task.cancelled=task.total-task.done;task.bytes=task.done*1048576;task.state='cancelled';task.finishedAt=at;return route.fulfill({json:task})}
      if(action==='retry'){
        const again={...task,id:`t${tasks.length}`,label:`${task.label}, tried again`,state:'queued',total:task.failed+task.cancelled,done:0,failed:0,cancelled:0,failures:[],finishedAt:undefined};
        tasks.unshift(again);
        return route.fulfill({status:202,json:again});
      }
    }
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="400" height="400"><rect width="400" height="400" fill="#6b5252"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await page.goto(`${base}/bin`);
  await page.locator('.bingrid figure').nth(2).waitFor();
  const button=page.getByRole('button',{name:/^Tasks/});
  const bell=page.locator('.bellwrap');
  assert.equal(await button.getAttribute('aria-label'),'Tasks','nothing is running');
  await page.locator('.taskflag').waitFor();
  const [bellBox,taskBox]=[await bell.boundingBox(),await button.boundingBox()];
  assert.ok(Math.abs(taskBox.y-bellBox.y)<1&&taskBox.x>bellBox.x&&taskBox.x-(bellBox.x+bellBox.width)<8,'Tasks sits right beside the bell');

  // Deleting three files: the cards leave at once and Tasks starts its ring.
  const bar=page.getByRole('toolbar',{name:'Selection'});
  await page.getByRole('checkbox',{name:/^Select FIRST\.JPG/}).click();
  await bar.getByRole('button',{name:'Select all 3'}).click();
  await bar.getByRole('button',{name:'Delete for good'}).click();
  await page.getByRole('dialog',{name:'Delete 3 files for good?'}).getByRole('button',{name:'Delete 3 files'}).click();
  await page.getByText('Permanently deleting 3 files. It carries on in the background under Tasks.').waitFor();
  assert.equal(await page.locator('.bingrid figure').count(),0,'the cards left without waiting');
  assert.deepEqual(posts[0],{path:'/api/tasks/bin',body:{action:'delete',keys:['marked:11','marked:12','marked:13'],confirmation:'DELETE 3'}});
  await page.locator('.taskring').waitFor();
  // The icon stays centred inside the ring while a task runs.
  const [ringBox,iconBox]=[await page.locator('.taskring').boundingBox(),await button.locator('.icon').boundingBox()];
  assert.ok(Math.abs(ringBox.x+ringBox.width/2-(iconBox.x+iconBox.width/2))<0.5&&Math.abs(ringBox.y+ringBox.height/2-(iconBox.y+iconBox.height/2))<0.5,'the icon is centred in the ring');
  advance();
  await page.waitForFunction(()=>/1 task running, 33% done/.test(document.querySelector('.tasks')?.getAttribute('aria-label')??''));

  // T opens the panel: the running task with its progress and Stop, and the
  // one that failed with its files and Try again.
  await page.keyboard.press('t');
  const panel=page.getByRole('dialog',{name:'Tasks'});
  await panel.waitFor();
  const running=panel.locator('.taskitem').first();
  assert.equal(await running.locator('.notetitle').innerText(),'Delete 3 files from the Bin');
  assert.equal(await running.locator('.taskstate').innerText(),'1 of 3');
  assert.equal(await running.getByRole('progressbar').getAttribute('aria-valuenow'),'1');
  const failed=panel.locator('.taskitem.failed');
  assert.equal(await failed.locator('.taskstate').innerText(),'3 moved, 2 could not be');
  assert.match(await failed.locator('.taskfailures').innerText(),/IMG_0001\.PNG The file is no longer where the catalogue says\./);
  if(shots){await page.waitForTimeout(300);await page.screenshot({path:`${shots}/tasks-panel.png`})}

  // Stop leaves what is left alone, and Try again queues just that.
  await running.getByRole('button',{name:'Stop'}).click();
  await running.getByText('Stopped after 1 of 3').waitFor();
  await running.getByRole('button',{name:'Try the 2 files again'}).click();
  await panel.getByText('Delete 3 files from the Bin, tried again').waitFor();
  assert.deepEqual(posts.slice(1).map(post=>post.path),['/api/tasks/t1/cancel','/api/tasks/t1/retry']);
  await page.getByText('1 file permanently deleted, freeing 1.0 MB. The other 2 were left in the Bin.').waitFor();
  advance();advance();
  await panel.locator('.taskitem.done').first().waitFor();
  await page.waitForFunction(()=>document.querySelector('.tasks')?.getAttribute('aria-label')==='Tasks');
  assert.equal(await page.locator('.taskring').count(),0,'the ring goes when nothing runs');

  // Clear finished empties the list.
  await panel.getByRole('button',{name:'Clear finished'}).click();
  await panel.getByText(/^Nothing running\./).waitFor();
  assert.equal(await page.locator('.taskflag').count(),0,'a cleared failure leaves no dot');
  await page.keyboard.press('Escape');
  await panel.waitFor({state:'hidden'});

  // On a phone the panel fills the width under the bar.
  await page.setViewportSize({width:390,height:800});
  await button.click();
  await panel.waitFor();
  await page.waitForTimeout(600);
  assert.equal(await page.getByRole('tooltip').count(),0,'no tip covers the open panel');
  const box=await panel.boundingBox();
  assert.ok(box.x>=7&&box.x+box.width<=390-7,'the panel stays on screen');
  if(shots){await page.waitForTimeout(300);await page.screenshot({path:`${shots}/tasks-phone.png`})}
  await browser.close();
  console.log('tasks: ok');
})().catch(error=>{console.error(error);process.exit(1)});
