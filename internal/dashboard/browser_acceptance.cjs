const assert=require('node:assert/strict');
const {chromium}=require(process.env.AGENTLAB_PLAYWRIGHT_MODULE);
(async()=>{
 const browser=await chromium.launch({executablePath:process.env.AGENTLAB_CHROME || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',headless:true});
 try {
  const page=await browser.newPage({viewport:{width:1280,height:900}});
  const errors=[];page.on('pageerror',error=>errors.push(error.message));
  await page.clock.install();
  await page.goto(process.argv[2]);
  await page.locator('dialog input[type=password]').waitFor();
  assert.equal(await page.locator('dialog').count(),1,'initial refresh must share login');
  await page.locator('dialog input').fill('browser-fixture');
  await page.getByRole('button',{name:'Sign in',exact:true}).click();
  await page.getByText('current-fixture',{exact:true}).waitFor();
  assert.match(await page.locator('#status-sandboxes').innerText(),/1 current.*1 active.*101 destroyed/);
  var destroyRequests = 0;
  page.on('request', request=>{if(request.method()==='POST' && request.url().endsWith('/destroy')) destroyRequests++;});
  const confirmation = page.waitForEvent('dialog');
  const destroyClick = page.getByRole('button',{name:'Destroy',exact:true}).click();
  const prompt = await confirmation;
  assert.match(prompt.message(), /1001/);
  await prompt.dismiss();
  await destroyClick;
  assert.equal(destroyRequests,0,'cancelled destruction must not mutate');
  await page.locator('#btn-new-sandbox').click();
  await page.locator('#modal-new-sandbox').waitFor({state:'visible'});
  await page.screenshot({path:'/tmp/agentlab-ax-dashboard-modal.png'});
  assert.ok(await page.locator('#modal-new-sandbox').evaluate(el=>el.contains(document.activeElement)),'modal must receive keyboard focus');
  await page.keyboard.press('Escape');
  await page.locator('#modal-new-sandbox').waitFor({state:'hidden'});
  assert.equal(await page.evaluate(()=>document.activeElement.id),'btn-new-sandbox','dismissal restores trigger focus');

  await page.locator('#sandbox-history').check();
  await page.locator('#sandbox-page-status').filter({hasText:'102 matching'}).waitFor();
  await page.locator('#sandbox-history').uncheck();
  await page.locator('#sandbox-search').fill('no-matching-resource');
  await page.locator('#sandbox-search').press('Tab');
  await page.locator('#sandbox-empty').waitFor({state:'visible'});
  assert.match(await page.locator('#sandbox-empty').innerText(),/filters.*Clear Search/);
  await page.locator('#sandbox-search').fill('');
  await page.locator('#sandbox-search').press('Tab');
  await page.getByText('current-fixture',{exact:true}).waitFor();
  await page.route('**/api/v1/sandboxes',route=>route.fulfill({status:503,contentType:'application/json',body:'{"error":"fixture unavailable"}'}));
  await page.locator('#btn-refresh').click();
  await page.locator('#data-status').filter({hasText:'Showing stale data'}).waitFor();
  assert.equal(await page.getByText('current-fixture',{exact:true}).count(),1);
  await page.unroute('**/api/v1/sandboxes');
  await page.locator('#btn-refresh').click();
  // Keyboard focus remains inside the modal in both tab directions.
  await page.locator('#btn-new-sandbox').click();
  await page.locator('#modal-new-sandbox [data-close-modal]').focus();
  await page.keyboard.press('Tab');
  assert.ok(await page.locator('#modal-new-sandbox').evaluate(el=>el.contains(document.activeElement)));
  await page.keyboard.press('Shift+Tab');
  assert.ok(await page.locator('#modal-new-sandbox').evaluate(el=>el.contains(document.activeElement)));
  await page.keyboard.press('Escape');

  await page.locator('#btn-new-sandbox').click();
  await page.locator('#select-profile').selectOption('default');
  await page.locator('#modal-new-sandbox button[type=submit]').click();
  await page.locator('#create-progress').filter({hasText:'succeeded'}).waitFor();
  assert.ok(await page.evaluate(()=>sessionStorage.getItem('agentlab_create_recovery'))===null,'completed creation must clear recovery state');

  await page.route('**/api/v1/status',route=>route.fulfill({status:401,contentType:'application/json',body:'{"error":"daemon permission denied"}'}));
  await page.locator('#btn-refresh').click();
  await page.locator('#status-daemon').filter({hasText:'daemon permission denied'}).waitFor();
  assert.equal(await page.locator('dialog').count(),0,'daemon denial must not request a browser token');
  await page.unroute('**/api/v1/status');
  // Expiration opens one login; cancellation pauses timer-triggered requests.
  await page.evaluate(()=>sessionStorage.setItem('agentlab_dashboard_token','expired'));
  await page.reload();
  await page.locator('dialog').waitFor();
  assert.equal(await page.locator('dialog').count(),1);
  await page.getByRole('button',{name:'Cancel',exact:true}).click();
  await page.locator('dialog').waitFor({state:'detached'});
  await page.clock.fastForward(11000);
  assert.equal(await page.locator('dialog').count(),0,'cancel must pause polling prompts');
  await page.locator('#btn-refresh').click();
  await page.locator('dialog input').fill('browser-fixture');
  await page.getByRole('button',{name:'Sign in',exact:true}).click();
  await page.getByText('current-fixture',{exact:true}).waitFor();
  await page.setViewportSize({width:390,height:844});
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),'narrow layout must not overflow the viewport');
  assert.equal(await page.locator('nav').count(),1);
  await page.screenshot({path:'/tmp/agentlab-ax-dashboard-mobile.png',fullPage:true});
  assert.deepEqual(errors,[]);
  console.log('PASS real Chrome: shared authentication, counts, current inventory, keyboard modal dismissal/focus, narrow viewport');
 } finally {await browser.close();}
})().catch(error=>{console.error(error);process.exit(1)});
