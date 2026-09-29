const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const {builtIns,common}=require('./lib/addons.cjs');

// Settings connects Jellyfin: an address and an API key, saved like the setup,
// after which Cull starts again. The key is never shown again, and once
// connected the page says when Jellyfin was last asked to scan. Every address
// and key is a synthetic fixture.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const home='/Users/sam/Pictures/Daddy Cull';

(async()=>{
  const state={
    configurable:true,
    config:{library:`${home}/Library`,import:`${home}/Import`,takeoutInbox:'',shared:false,icloud:{on:false,appleId:'',since:''},immich:{url:'',pathPrefix:''},jellyfin:{url:''},done:true},
    immichKeySet:false,jellyfinKeySet:false,tools:[],
    icloud:{signedIn:false,lastRun:'',ok:false,message:''},
    applePhotos:{signedIn:false,lastRun:'',ok:false,message:''},
    free:0,
  };
  const jellyfin=()=>state.config.jellyfin.url&&state.jellyfinKeySet
    ?{state:'ready',detail:'Jellyfin was last asked to scan at 22:01 on 29 September.'}
    :{state:'setup',detail:'JELLYFIN_URL and JELLYFIN_KEY are not both set.'};
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
      state.config={...change.config,jellyfin:{url:change.config.jellyfin.url.replace(/\/+$/,'')}};
      if(change.jellyfinKey!==undefined)state.jellyfinKeySet=change.jellyfinKey!=='';
      down=2;
      return route.fulfill({status:202,json:{...state,restarting:true}});
    }
    if(url.pathname==='/api/stats')return route.fulfill({json:{total:10,synthetic:false,snapshotAt:'2026-09-06 01:49:00',candidates:0,calendarDays:1,reviewedDays:0,decisions:0,favourites:3,evidence:0,fullHashes:0,marked:0,legacyBin:0,shadowGroups:0,screenshots:0,social:0,upgradesAccepted:0,upgradeCandidates:0,bin:0,notifications:0,immichSynced:0,immichPending:0,immichFailed:0}});
    if(url.pathname==='/api/intake')return route.fulfill({json:{configured:true,status:null}});
    if(url.pathname==='/api/trash/deleting')return route.fulfill({json:{graceDays:30,items:[],lastRun:'',lastDeleted:0,lastError:'',checkIntervalMinutes:15}});
    if(url.pathname==='/api/addons')return route.fulfill({json:[...builtIns(),
      {id:'jellyfin',name:'Jellyfin',version:'1',summary:'Have Jellyfin scan the library again whenever files leave it or come back.',icon:'videocam',
        builtIn:true,on:true,chosen:false,status:jellyfin(),routes:0}]});
    const found=common(url);
    if(found)return route.fulfill(found);
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });

  await page.goto(`${base}/settings`);
  await page.waitForSelector('#jellyfin-url');
  const form=page.locator('form:has(#jellyfin-url)');
  const save=form.getByRole('button',{name:'Save'});
  assert.equal(await save.isDisabled(),true,'nothing to save yet');
  assert.equal(await page.getByText('Scans',{exact:true}).count(),0,'no scan status before Jellyfin is connected');
  assert.equal(await form.getByRole('button',{name:'Disconnect'}).count(),0);

  await page.fill('#jellyfin-url','http://jellyfin.example.test:8096/');
  assert.equal(await save.isDisabled(),false);
  await page.fill('#jellyfin-key','fedcba9876543210fedcba9876543210');
  await save.click();
  await page.getByText('Saved. Jellyfin will be asked to scan when files leave or come back.').waitFor({timeout:10000});
  assert.equal(saves.length,1);
  assert.deepEqual(saves[0].config.jellyfin,{url:'http://jellyfin.example.test:8096/'},'the address goes out as typed; Cull tidies it');
  assert.equal(saves[0].jellyfinKey,'fedcba9876543210fedcba9876543210');
  assert.equal('immichKey' in saves[0],false,'Immich\'s key is left alone');
  assert.deepEqual(saves[0].config.immich,{url:'',pathPrefix:''},'the rest of the setup is sent unchanged');
  assert.equal(await page.inputValue('#jellyfin-url'),'http://jellyfin.example.test:8096');
  assert.equal(await page.inputValue('#jellyfin-key'),'','the key is not shown again');
  assert.match(await page.getAttribute('#jellyfin-key','placeholder'),/^Kept/);
  const scans=page.locator('#jellyfin ~ .kv').first();
  await scans.waitFor();
  assert.match(await scans.innerText(),/last asked to scan at 22:01 on 29 September/);
  if(shots)await page.locator('#jellyfin').evaluate(heading=>heading.scrollIntoView());
  if(shots)await page.screenshot({path:`${shots}/settings-jellyfin.png`});

  await form.getByRole('button',{name:'Disconnect'}).click();
  await page.getByText('Jellyfin is disconnected.').waitFor({timeout:10000});
  assert.deepEqual(saves[1].config.jellyfin,{url:''});
  assert.equal(saves[1].jellyfinKey,'','disconnecting forgets the key');
  assert.equal(await form.getByRole('button',{name:'Disconnect'}).count(),0);
  assert.equal(await page.getByText('Scans',{exact:true}).count(),0);

  // Started with its settings given where it runs, as in Docker, the fields
  // cannot be changed here, but the status still shows.
  state.configurable=false;
  state.config.jellyfin={url:'http://jellyfin.example.test:8096'};
  state.jellyfinKeySet=true;
  await page.reload();
  await page.waitForSelector('#jellyfin-url');
  assert.equal(await page.locator('#jellyfin-url').isDisabled(),true);
  await page.locator('#jellyfin ~ .kv').first().waitFor();

  assert.deepEqual(errors,[]);
  await browser.close();
})().catch(error=>{console.error(error);process.exit(1)});
