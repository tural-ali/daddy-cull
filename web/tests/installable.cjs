const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const os=require('node:os');
const path=require('node:path');

// Chrome offers to install the app from its address bar once the page links
// a manifest that names the app, opens it standalone and carries its icons.
// Chrome is asked directly what still stands in the way; it answers only for
// a secure origin and outside incognito, so this runs a profile on localhost.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');

(async()=>{
  const profile=fs.mkdtempSync(path.join(os.tmpdir(),'cull-install-'));
  const context=await chromium.launchPersistentContext(profile,{channel:'chrome',headless:true,viewport:{width:1280,height:800}});
  const page=await context.newPage();
  await page.goto(`${base}/`);
  const cdp=await context.newCDPSession(page);
  const {url,errors,parsed}=await cdp.send('Page.getAppManifest');
  assert.equal(url,`${base}/manifest.json`,'the page links its manifest');
  assert.deepEqual(errors,[],'the manifest parses');
  assert.equal(parsed.scope,`${base}/`);
  const manifest=await page.evaluate(async src=>(await fetch(src)).json(),url);
  assert.equal(manifest.name,'Daddy, Cull!');
  assert.equal(manifest.display,'standalone');
  for(const icon of manifest.icons){
    const status=await page.evaluate(async src=>(await fetch(src)).status,icon.src);
    assert.equal(status,200,`${icon.src} is served`);
  }
  let errorsLeft=[];
  for(let i=0;i<40;i++){
    ({installabilityErrors:errorsLeft}=await cdp.send('Page.getInstallabilityErrors'));
    if(errorsLeft.length===0)break;
    await page.waitForTimeout(250);
  }
  assert.deepEqual(errorsLeft,[],'Chrome finds nothing in the way of installing');
  await context.close();
  fs.rmSync(profile,{recursive:true,force:true});
  console.log('installable: ok');
})().catch(error=>{console.error(error);process.exit(1)});
