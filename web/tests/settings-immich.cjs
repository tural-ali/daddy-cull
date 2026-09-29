const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const {builtIns,common}=require('./lib/addons.cjs');

// Settings connects Immich: the address, the folder Immich reads the library
// from and an API key, saved like the setup, after which Cull starts again.
// Cull tidies what it saves, so the page waits for the setup as saved, not as
// typed. The key is never shown again, and the sync counts appear only while
// Immich is connected. Every address, folder and key is a synthetic fixture.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const home='/Users/sam/Pictures/Daddy Cull';

(async()=>{
  const state={
    configurable:true,
    config:{library:`${home}/Library`,import:`${home}/Import`,takeoutInbox:'',shared:false,icloud:{on:false,appleId:'',since:''},immich:{url:'',pathPrefix:''},done:true},
    immichKeySet:false,tools:[],
    icloud:{signedIn:false,lastRun:'',ok:false,message:''},
    applePhotos:{signedIn:false,lastRun:'',ok:false,message:''},
    free:0,
  };
  const saves=[];
  let down=0;
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:860},timezoneId:'Europe/London'});
  await page.addInitScript(()=>{
    const now=new Date();
    localStorage.setItem('cull.streak-intro',`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`);
    localStorage.setItem('cull-guides-hidden',JSON.stringify(['settings']));
  });
  const errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  await page.route('**/api/**',route=>{
    const request=route.request(),url=new URL(request.url());
    if(url.pathname==='/api/setup'&&request.method()==='GET'){
      if(down>0){down--;return route.abort('connectionrefused')}
      return route.fulfill({json:state});
    }
    if(url.pathname==='/api/setup'&&request.method()==='POST'){
      const change=request.postDataJSON();
      saves.push(change);
      // As Cull does: the address loses its trailing slash.
      state.config={...change.config,immich:{...change.config.immich,url:change.config.immich.url.replace(/\/+$/,'')}};
      if(change.immichKey!==undefined)state.immichKeySet=change.immichKey!=='';
      down=2;
      return route.fulfill({status:202,json:{...state,restarting:true}});
    }
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:10,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:3,evidence:0,fullHashes:0,marked:0,legacyBin:0,shadowGroups:0,screenshots:0,social:0,upgradesAccepted:0,upgradeCandidates:0,bin:0,notifications:0,immichSynced:2,immichPending:1,immichFailed:0}});
    if(url.pathname==='/api/intake')return route.fulfill({json:{configured:true,status:null}});
    if(url.pathname==='/api/trash/deleting')return route.fulfill({json:{graceDays:30,items:[],lastRun:'',lastDeleted:0,lastError:'',checkIntervalMinutes:15}});
    if(url.pathname==='/api/addons')return route.fulfill({json:builtIns()});
    const found=common(url);
    if(found)return route.fulfill(found);
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });

  await page.goto(`${base}/settings`);
  await page.waitForSelector('#immich-url');
  assert.equal((await page.locator('.kv div',{hasText:'Last refreshed'}).locator('dd').textContent()).trim(),'6 September 2026 at 02:49','the index date is written out in full, in the viewer\'s time');
  assert.equal(await page.getByText('Favourites in Immich').count(),0,'no sync counts before Immich is connected');
  assert.equal(await page.getByText('Files with imported evidence').count(),0,'an empty legacy count is not shown');
  assert.equal(await page.getByRole('heading',{name:'Google Takeout'}).count(),0,'no upgrades section without upgrades');
  const save=page.locator('form.immichform').getByRole('button',{name:'Save'});
  assert.equal(await save.isDisabled(),true,'nothing to save yet');

  await page.fill('#immich-url','http://immich.example.test:2283/');
  await page.fill('#immich-prefix','/mnt/photos');
  await page.fill('#immich-key','0123456789abcdef0123456789abcdef');
  const started=Date.now();
  await save.click();
  await page.getByText('Saved. Favourites will be sent to Immich.').waitFor({timeout:10000});
  assert.ok(Date.now()-started<8000,'the page takes the tidied address as saved rather than waiting it out');
  assert.equal(saves.length,1);
  assert.deepEqual(saves[0].config.immich,{url:'http://immich.example.test:2283/',pathPrefix:'/mnt/photos'},'the address goes out as typed; Cull tidies it');
  assert.equal(saves[0].immichKey,'0123456789abcdef0123456789abcdef');
  assert.equal(saves[0].config.library,`${home}/Library`,'the rest of the setup is sent unchanged');
  assert.equal(await page.inputValue('#immich-url'),'http://immich.example.test:2283');
  assert.equal(await page.inputValue('#immich-key'),'','the key is not shown again');
  assert.match(await page.getAttribute('#immich-key','placeholder'),/^Kept/);
  await page.getByText('Favourites in Immich').waitFor();
  assert.match(await page.locator('#immich ~ .kv').first().innerText(),/2 synced · 1 waiting · 0 failed/);
  if(shots)await page.locator('#immich').screenshot({path:`${shots}/settings-immich-heading.png`});

  // Changing only the folder keeps the key.
  await page.fill('#immich-prefix','/mnt/family');
  await save.click();
  await page.getByText('Saved. Favourites will be sent to Immich.').waitFor({timeout:10000});
  assert.equal(saves.length,2);
  assert.equal('immichKey' in saves[1],false,'a blank key field leaves the kept key alone');

  await page.getByRole('button',{name:'Disconnect'}).click();
  await page.getByText('Immich is disconnected.').waitFor({timeout:10000});
  assert.deepEqual(saves[2].config.immich,{url:'',pathPrefix:''});
  assert.equal(saves[2].immichKey,'','disconnecting forgets the key');
  assert.equal(await page.getByRole('button',{name:'Disconnect'}).count(),0);
  assert.equal(await page.getByText('Favourites in Immich').count(),0);

  // Only a Cull started from its own setup downloads from iCloud; one started
  // with flags, as in Docker, cannot tell whether something else does.
  const icloud=page.locator('.kv div',{hasText:'iCloud Photos'}).locator('dd');
  assert.equal((await icloud.textContent()).trim(),'Off');
  state.configurable=false;
  await page.reload();
  await icloud.waitFor();
  assert.equal((await icloud.textContent()).trim(),'Not downloaded by Daddy Cull');

  assert.deepEqual(errors,[]);
  await browser.close();
})().catch(error=>{console.error(error);process.exit(1)});
