const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const taskQueue=require('./lib/taskQueue.cjs');

// ⌘Z on Screenshots takes back what was done, one step at a time and not
// just the last, from the grid or from the viewer, and ⇧⌘Z does it again.
// Each move is queued as a task and leaves the grid at once; every undo is
// the server's own, bringing back from the Bin what the task moved. Every
// name is a synthetic fixture.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const shot=id=>({id,path:`/screenshots/IMG_${id}.PNG`,name:`IMG_${id}.PNG`,day:`2024-05-0${id}`,state:'waiting',capturedAt:1714000000+id,kind:'image',source:'screenshots',size:1000,
  status:'unreviewed',favourite:false,revision:0,alternativeCount:0,relatedCount:0,width:1179,height:2556});

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  // The holding area as the server keeps it, and its queue.
  const waiting=new Set([1,2,3,4,5,6]);
  const queue=taskQueue({
    apply:(kind,body)=>{
      assert.equal(kind,'screenshots.remove');
      for(const id of body.assetIds){assert.ok(waiting.has(id),'only a waiting screenshot is moved');waiting.delete(id)}
      return {total:body.assetIds.length,moved:body.assetIds};
    },
    undo:source=>{for(const id of source.moved){assert.ok(!waiting.has(id),'only a moved screenshot comes back');waiting.add(id)}return {total:source.moved.length}},
  });
  await page.route('**/api/**',route=>{
    const request=route.request(),url=new URL(request.url());
    if(queue.handle(route,url,request))return;
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:6,synthetic:false,snapshotAt:'2026-09-06 01:49:00',screenshots:waiting.size,bin:6-waiting.size}});
    if(url.pathname==='/api/screenshots'){const items=[...waiting].sort((a,b)=>a-b).map(shot);return route.fulfill({json:{items,total:items.length,bytes:items.length*1000,unreviewed:items.length,reviewed:0,stills:items.length,recordings:0}})}
    if(url.pathname.startsWith('/api/media/'))return route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="118" height="256"><rect width="118" height="256" fill="#557"/></svg>'});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await page.goto(`${base}/screenshots`);
  const tile=id=>page.locator(`main figure[data-asset="${id}"]`);
  const shown=()=>page.evaluate(()=>[...document.querySelectorAll('main figure[data-asset]')].map(figure=>Number(figure.dataset.asset)));
  const until=async(want,what)=>{for(let i=0;i<100;i++){if(JSON.stringify(await shown())===JSON.stringify(want))return;await page.waitForTimeout(30)}assert.deepEqual(await shown(),want,what)};
  const flash=page.locator('.flash');
  await tile(6).waitFor();

  // Two removals with X: one screenshot, then two.
  await tile(1).hover();
  await tile(1).getByRole('checkbox').click();
  await page.keyboard.press('x');
  await until([2,3,4,5,6],'the first is in the Bin');
  await tile(2).hover();
  await tile(2).getByRole('checkbox').click();
  await page.keyboard.down('Shift');await tile(3).click();await page.keyboard.up('Shift');
  await page.keyboard.press('x');
  await until([4,5,6],'the next two are in the Bin');

  // ⌘Z brings back the last two, then the first.
  await page.keyboard.press('ControlOrMeta+z');
  await until([2,3,4,5,6],'the last removal is undone');
  await flash.getByText('Undone: moved 2 screenshots to the Bin.').waitFor();
  if(shots)await page.screenshot({path:`${shots}/screenshot-undo.png`});
  await page.keyboard.press('ControlOrMeta+z');
  await until([1,2,3,4,5,6],'and then the one before');
  await flash.getByText('Undone: moved 1 screenshot to the Bin.').waitFor();
  await page.keyboard.press('ControlOrMeta+z');
  await flash.getByText('Nothing to undo.').waitFor();

  // ⇧⌘Z does the first again, and the Redo button the second.
  await page.keyboard.press('ControlOrMeta+Shift+z');
  await until([2,3,4,5,6],'redo moves it to the Bin again');
  await flash.getByRole('button',{name:'Redo'}).click();
  await until([4,5,6],'and the Redo button the next');

  // From inside the viewer too, where X removes the one showing.
  await tile(4).click();
  await page.getByRole('dialog',{name:'Photo review'}).waitFor();
  await page.keyboard.press('x');
  await until([5,6],'X in the viewer moves it to the Bin');
  await page.keyboard.press('ControlOrMeta+z');
  await until([4,5,6],'and ⌘Z brings it back');
  assert.ok(queue.posts.every(post=>post.path!=='/api/tasks/bin'),'nothing is ever deleted');
  assert.deepEqual(queue.posts.filter(post=>post.path==='/api/tasks/screenshots').map(post=>post.body.assetIds),[[1],[2,3],[1],[2,3],[4]],'each move is one task');
  await browser.close();
  console.log('screenshot undo: ok');
})().catch(error=>{console.error(error);process.exit(1)});
