const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const assert=require('node:assert/strict');
const {builtIns,yours,common}=require('./lib/addons.cjs');

// The Developers page draws the API reference the server describes: each
// route with its parameters, body and answers, a curl line for an addon, and
// a Send button for the routes that only read. A link to a route opens it.
const base=(process.env.APP_URL||'http://127.0.0.1:8842').replace(/\/$/,'');
const shots=process.env.SHOTS;

(async()=>{
  const browser=await chromium.launch({channel:'chrome',headless:true});
  const page=await browser.newPage({viewport:{width:1280,height:800}});
  await page.addInitScript(key=>{const now=new Date();localStorage.setItem(key,`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`)},'cull.streak-intro');
  const errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  const sent=[];
  await page.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/stats'&&url.searchParams.get('tz')==='Europe/Paris'){sent.push(url.pathname+url.search);return route.fulfill({json:{total:10,bin:2}})}
    const usual=common(url);
    if(usual)return route.fulfill(usual);
    if(url.pathname==='/api/events')return route.fulfill({status:204});
    if(url.pathname==='/api/addons')return route.fulfill({json:[...builtIns(),...yours()]});
    return route.fulfill({status:404,json:{error:'not mocked'}});
  });

  // A link straight to a route opens it and brings it into view.
  await page.goto(`${base}/developers#op-getAsset`);
  const asset=page.locator('#op-getAsset');
  await asset.locator('.rbody').waitFor();
  assert.equal(await asset.evaluate(element=>element.open),true);
  const box=await asset.boundingBox();
  assert.ok(box.y>=0&&box.y<400,'the route is in view');
  assert.equal(await page.title(),'Developers · Daddy, Cull!');
  assert.match(await page.locator('.developers .ysum').innerText(),/Version 1, 5 routes\.$/);
  assert.deepEqual(await page.locator('.devnav a').allInnerTexts(),['Start here','Build an addon','Permissions','openapi.json','Library\n3','Screenshots\n1','Events\n1']);

  // A path parameter has to be filled in before it can be sent.
  const send=asset.getByRole('button',{name:'Send'});
  assert.equal(await send.isDisabled(),true);
  assert.equal(await asset.locator('.tryit .hint').innerText(),'Fill in id first.');
  assert.equal(await asset.locator('.codeblock pre').innerText(),'curl \\\n  -H "Authorization: Bearer $(cat key)" \\\n  "http://127.0.0.1:8842/api/assets/<id>"'.replace('http://127.0.0.1:8842',base),'an empty path parameter is a placeholder curl will not expand');
  await asset.getByRole('textbox',{name:'id'}).fill('42');
  assert.equal(await send.isDisabled(),false);
  assert.ok((await asset.locator('.codeblock pre').innerText()).endsWith(`"${base}/api/assets/42"`));

  // The permissions an addon may ask for, from the reference.
  assert.match(await page.locator('#permissions dl').innerText(),/delete\s+Delete files in the Bin for good\./);

  // A link on the page to another route opens that one too.
  await page.locator('#start').getByRole('link',{name:'GET /api/events'}).click();
  await page.locator('#op-getEvents .rbody').waitFor();
  assert.equal(await page.locator('#op-getEvents .tryit').count(),0,'a stream is not offered to Send');
  assert.equal(await page.locator('#op-getEvents').getByText('text/event-stream').count(),1,'its answer says what it is');

  // Opening a route that reads, filling in its query, and sending it.
  const stats=page.locator('#op-getStats');
  await stats.locator('summary').click();
  await stats.locator('.rbody').waitFor();
  const answers=await stats.locator('.answers').innerText();
  assert.match(answers,/total\s+integer/,'the answer’s fields are listed from its schema');
  assert.ok(answers.indexOf('total')<answers.indexOf('bin'),'in the order the schema gives');
  const tz=stats.getByRole('textbox',{name:'tz'});
  assert.equal(await tz.inputValue(),'Europe/London','the example is filled in');
  await tz.fill('Europe/Paris');
  await stats.getByRole('button',{name:'Send'}).click();
  await stats.locator('.answer').waitFor();
  assert.deepEqual(sent,['/api/stats?tz=Europe%2FParis']);
  assert.equal(await stats.locator('.answerhead').innerText(),'200\napplication/json','status and type');
  assert.equal(await stats.locator('.answer pre').innerText(),'{\n  "total": 10,\n  "bin": 2\n}');

  // A route that belongs to an addon that is off says so; one that changes
  // things says which permission it needs.
  const remove=page.locator('#op-postScreenshotsRemove summary');
  assert.equal(await remove.locator('.rchip.perm').innerText(),'bin');
  assert.equal(await remove.locator('.rchip.part').innerText(),'Saved from social · off');
  assert.equal(await page.locator('#op-postSettingsVideo .rchip.perm').innerText(),'settings');
  const heights=await page.locator('.route:not([open]) > summary').evaluateAll(rows=>[...new Set(rows.map(row=>Math.round(row.getBoundingClientRect().height)))]);
  assert.equal(heights.length,1,`every closed route's row is the same height, chips or not: ${heights}`);
  await remove.click();
  const removeBody=page.locator('#op-postScreenshotsRemove .rbody');
  await removeBody.waitFor();
  assert.match(await removeBody.locator('.codeblock pre').innerText(),/^curl -X POST \\\n.*\n  -H "Content-Type: application\/json" \\\n  -d '\{"ids":\[.*\]\}' \\\n/s,'a body is sent as JSON');
  assert.equal(await removeBody.locator('.tryit').count(),0,'a route that changes things is not offered to Send');
  await remove.click();

  // Finding a route narrows the list; finding nothing says so.
  const find=page.getByRole('searchbox',{name:'Find a route'});
  await find.fill('screenshots');
  await page.waitForFunction(()=>document.querySelectorAll('.route').length===1);
  assert.deepEqual(await page.locator('.devsec.reftag h2').allInnerTexts(),['Screenshots']);
  await find.fill('nothing like this');
  await page.getByText('No route matches “nothing like this”.').waitFor();
  await find.fill('');
  await page.waitForFunction(()=>document.querySelectorAll('.route').length===5);

  // Nothing runs off the side, even with a long answer open, and on a phone.
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'no sideways scroll');
  if(shots){await page.waitForTimeout(300);await page.screenshot({path:`${shots}/developers.png`})}
  await page.setViewportSize({width:390,height:800});
  await page.waitForTimeout(300);
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'no sideways scroll on a phone');
  assert.equal(await page.locator('.devnav').isVisible(),false,'the side list gives way on a phone');
  if(shots){await stats.scrollIntoViewIfNeeded();await page.screenshot({path:`${shots}/developers-phone.png`})}

  // A reference that cannot be read says what to do.
  const failing=await browser.newPage();
  await failing.route('**/api/**',route=>{
    const url=new URL(route.request().url());
    if(url.pathname==='/api/openapi.json')return route.fulfill({status:500,json:{error:'down'}});
    const usual=common(url);
    return usual?route.fulfill(usual):route.fulfill({status:404,json:{error:'not mocked'}});
  });
  await failing.goto(`${base}/developers`);
  await failing.getByRole('alert').waitFor();
  assert.equal(await failing.getByRole('alert').innerText(),'The API reference could not be read. Check that Cull is running, then try again.');
  assert.deepEqual(errors,[]);
  await browser.close();
  console.log('developers: ok');
})().catch(error=>{console.error(error);process.exit(1)});
