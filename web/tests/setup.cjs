const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const {builtIns,common}=require('./lib/addons.cjs');

// A Cull set up from a config file opens on the setup wizard until it is
// finished. Each step saves as it is left, Cull starts again to take the
// change up, and the page waits for it to answer. iCloud stays on its step
// until Terminal has signed in, and Google Photos turns its addon on. Every
// folder and account is a synthetic fixture.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;
const home='/Users/sam/Pictures/Daddy Cull';

function server({configurable=true}={}){
  const state={
    configurable,
    config:{library:`${home}/Library`,import:`${home}/Import`,takeoutInbox:'',shared:false,icloud:{on:false,appleId:'',since:''},immich:{url:'',pathPrefix:''},done:false},
    immichKeySet:false,jellyfinKeySet:false,
    tools:[
      {name:'exiftool',found:true,for:'Reads when each photo was taken'},
      {name:'ffmpeg',found:true,for:'Makes previews of videos'},
      {name:'icloudpd',found:false,for:'Downloads photos from iCloud'},
      {name:'osxphotos',found:false,for:'Exports photos from Apple Photos on this Mac'},
    ],
    icloud:{signedIn:false,lastRun:'',ok:false,message:''},
    applePhotos:{signedIn:false,lastRun:'',ok:false,message:''},
    free:512*1024**3,
  };
  const saves=[],addons=[];
  // Reads while Cull starts again fail, as they do while launchd brings it back.
  let down=0;
  function handle(route,url,request){
    if(url.pathname==='/api/setup'&&request.method()==='GET'){
      if(down>0){down--;return route.abort('connectionrefused')}
      return route.fulfill({json:state});
    }
    if(url.pathname==='/api/setup'&&request.method()==='POST'){
      const {config}=request.postDataJSON();
      saves.push(config);
      if(!state.configurable)return route.fulfill({status:409,json:{error:'Cull was started without a config file, so its folders are set where it is started.'}});
      if(config.icloud.on&&!/@/.test(config.icloud.appleId))return route.fulfill({status:400,json:{error:'The Apple Account is the email address or phone number you sign in to iCloud with, such as sam@example.com.'}});
      // As Cull does, folders are saved without a trailing slash.
      const tidy=folder=>folder.trim().replace(/(.)\/+$/,'$1');
      state.config={...config,library:tidy(config.library),import:tidy(config.import),takeoutInbox:tidy(config.takeoutInbox)};
      down=2;
      return route.fulfill({status:202,json:{...state,restarting:true}});
    }
    if(url.pathname==='/api/addons')return route.fulfill({json:builtIns()});
    if(url.pathname.startsWith('/api/addons/')&&request.method()==='POST'){
      const id=decodeURIComponent(url.pathname.split('/')[3]);
      addons.push({id,...request.postDataJSON()});
      return route.fulfill({json:{id,name:'Google Photos',version:'1',summary:'',builtIn:true,on:true,chosen:true,pages:[],status:{state:'ready',detail:''},routes:0}});
    }
    if(url.pathname.startsWith('/api/today/')){const md=url.pathname.slice(-5);return route.fulfill({json:{md,label:md,previous:md,next:md,years:[],memories:0,bytes:0}})}
    const found=common(url);
    if(found)return route.fulfill(found);
    return route.fulfill({status:404,json:{error:'not mocked'}});
  }
  return {handle,state,saves,addons};
}

async function open(browser,options,viewport={width:1280,height:860},theme='night'){
  const page=await browser.newPage({viewport});
  await page.addInitScript(([key,chosen])=>{
    const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`);
    localStorage.setItem('cull-theme',chosen);
  },['cull.streak-intro',theme]);
  const mock=server(options);
  const errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  await page.route('**/api/**',route=>{const request=route.request();return mock.handle(route,new URL(request.url()),request)});
  return {page,mock,errors};
}

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const {page,mock,errors}=await open(browser,{});

  // Any address opens on the wizard until setup is done.
  await page.goto(`${base}/year`);
  await page.getByRole('heading',{name:'Set up Daddy Cull'}).waitFor();
  assert.equal(new URL(page.url()).pathname,'/setup');
  assert.equal(new URL(page.url()).searchParams.get('step'),'welcome');
  const tools=page.locator('.setuptools');
  assert.match(await tools.textContent(),/icloudpd.*Not installed, only needed if you use it/,'an optional tool is not an alarm');
  assert.equal(await tools.locator('code',{hasText:'daddy-cull icloud install'}).count(),1,'a missing tool says how to install it');
  assert.match(await tools.textContent(),/512\.0 GB/);
  if(shots)await page.screenshot({path:`${shots}/setup-welcome.png`,fullPage:true});

  // Folders: an empty library cannot be saved; the tree follows the name.
  await page.getByRole('button',{name:'Begin'}).click();
  await page.getByLabel('Library',{exact:true}).waitFor();
  assert.equal(await page.getByRole('button',{name:'Apple Photos'}).isDisabled(),false);
  await page.getByLabel('Library',{exact:true}).fill('');
  assert.equal(await page.getByRole('button',{name:'Continue'}).isDisabled(),true,'no library, no going on');
  assert.equal(await page.getByRole('button',{name:'Apple Photos'}).isDisabled(),true,'nor skipping ahead');
  await page.getByLabel('Library',{exact:true}).fill('/Users/sam/Family Photos');
  assert.match(await page.locator('.setuptree').textContent(),/^Family Photos\/\n\s+2019\/\n\s+2019-08\/\n\s+2019-08-14\//);
  await page.getByRole('button',{name:'Save and continue'}).click();
  await page.getByText('Daddy Cull is starting again with the change…').waitFor();
  await page.getByText('Download my iCloud Photos').waitFor();
  assert.equal(mock.saves.at(-1).library,'/Users/sam/Family Photos');
  assert.equal(new URL(page.url()).searchParams.get('step'),'icloud');

  // iCloud: a mistyped account is refused where it was typed.
  await page.getByText('Download my iCloud Photos').click();
  await page.getByLabel('Apple Account').fill('sam at example');
  await page.getByRole('button',{name:'Save and sign in'}).click();
  await page.getByRole('alert').filter({hasText:'email address or phone number'}).waitFor();
  await page.getByLabel('Apple Account').fill('sam@example.com');
  await page.getByText('From a day on').click();
  await page.getByLabel('First day to download').fill('2024-01-01');
  await page.getByRole('button',{name:'Save and sign in'}).click();
  // It stays on the step, and says how to sign in, until Terminal has.
  await page.getByText('Waiting for you to sign in in Terminal…').waitFor();
  assert.deepEqual(mock.saves.at(-1).icloud,{on:true,appleId:'sam@example.com',since:'2024-01-01'});
  assert.equal(new URL(page.url()).searchParams.get('step'),'icloud');
  assert.equal(await page.locator('.setupsteps code',{hasText:'daddy-cull icloud sign-in'}).count(),1);
  assert.equal(await page.getByLabel(/password/i).count(),0,'the page never asks for the password');
  if(shots)await page.screenshot({path:`${shots}/setup-icloud.png`,fullPage:true});
  mock.state.icloud={signedIn:true,lastRun:new Date(Date.now()-5*60000).toISOString(),ok:true,message:'Downloaded 12 photos.'};
  await page.getByText(/^Signed in\./).waitFor({timeout:8000});
  await page.getByText('Last run 5 minutes ago: Downloaded 12 photos.').waitFor();

  // Apple Photos: the import command and Cull Sync, each optional.
  await page.getByRole('button',{name:'Continue'}).click();
  await page.locator('.setupsteps code',{hasText:'daddy-cull apple-photos import'}).waitFor();
  assert.equal(await page.getByRole('button',{name:'Set up Cull Sync'}).count(),1);

  // Google Photos: a Takeout folder turns the addon on. It is suggested beside
  // the Import folder, and typed with a trailing slash, which Cull drops as it
  // saves; the page goes on as soon as Cull answers with the folder as saved.
  await page.getByRole('button',{name:'Continue'}).click();
  assert.equal(await page.getByLabel('Takeout folder').getAttribute('placeholder'),`${home}/Takeout`);
  await page.getByLabel('Takeout folder').fill('/Users/sam/Pictures/Takeout/');
  const saving=Date.now();
  await page.getByRole('button',{name:'Save and continue'}).click();
  await page.getByRole('button',{name:'Start culling'}).waitFor({timeout:10000});
  assert.ok(Date.now()-saving<8000,'a tidied folder does not leave the page waiting for Cull');
  assert.deepEqual(mock.addons,[{id:'google-photos',on:true}]);
  const summary=await page.locator('.setup .kv').textContent();
  assert.match(summary,/Library\/Users\/sam\/Family Photos/);
  assert.match(summary,/iCloud Photossam@example\.com, from 2024-01-01/);
  assert.match(summary,/Google Takeout\/Users\/sam\/Pictures\/Takeout$/);
  if(shots)await page.screenshot({path:`${shots}/setup-done.png`,fullPage:true});

  // Done: saved as done, and the app opens on today, with no step left over.
  const saved=mock.saves.length;
  await page.getByRole('button',{name:'Start culling'}).click();
  await page.waitForURL(url=>url.pathname.startsWith('/on/'));
  assert.equal(mock.saves.length,saved+1);
  assert.equal(mock.saves.at(-1).done,true);
  assert.equal(new URL(page.url()).search,'','the step does not follow the app out');
  assert.deepEqual(errors,[]);
  await page.close();

  // Started with flags, as in Docker: the folders are shown, not changed.
  {
    const {page:phone,errors:problems}=await open(browser,{configurable:false},{width:390,height:844},'day');
    await phone.goto(`${base}/setup?step=folders`);
    await phone.getByLabel('Library',{exact:true}).waitFor();
    assert.equal(await phone.getByLabel('Library',{exact:true}).isDisabled(),true);
    assert.equal(await phone.getByRole('button',{name:'Save and continue'}).count(),0,'nothing to save');
    const width=await phone.evaluate(()=>document.documentElement.scrollWidth);
    assert.ok(width<=390,`the page fits a phone, ${width}px`);
    if(shots)await phone.screenshot({path:`${shots}/setup-fixed-phone-day.png`,fullPage:true});
    await phone.goto(`${base}/setup?step=welcome`);
    await phone.getByText(/started with its folders set where it runs/).waitFor();
    assert.deepEqual(problems,[]);
    await phone.close();
  }

  await browser.close();
  console.log('setup: ok');
})().catch(error=>{console.error(error);process.exit(1)});
