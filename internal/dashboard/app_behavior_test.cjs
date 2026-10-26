const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
function fixture(fetch,storage = new Map()) {
 const elements=new Map();
 function element(tag='div') {return {tag,children:[],style:{},value:'',textContent:'',innerHTML:'',listeners:{},appendChild(child){this.children.push(child);return child;},addEventListener(name,fn){this.listeners[name]=fn;},showModal(){},close(){},remove(){},focus(){}};}
 const body=element('body');
 const document={body,createElement:element,querySelectorAll(){return [];},getElementById(id){if(!elements.has(id))elements.set(id,element());return elements.get(id);},addEventListener(){}};
 const ctx={document,fetch,sessionStorage:{getItem(key){return storage.get(key) || null;},setItem(key,value){storage.set(key,value);},removeItem(key){storage.delete(key);}},crypto:require("node:crypto"),console,setInterval(){},clearInterval(){},URLSearchParams};
 vm.createContext(ctx);
 let source=fs.readFileSync(__dirname+'/static/assets/app.js','utf8');
 source=source.replace('  document.addEventListener("DOMContentLoaded", init);','globalThis.app={loadStatus,loadEvents,api,refreshAll,submitCreation,initNewSandboxForm,destroySandbox,initBulkActions,loadJobs,showDetail,showJobDetail,showWorkspaceDetail,loadWorkspaces};');
 vm.runInContext(source,ctx);
 return {ctx,document,body,app:ctx.app};
}
const response=(body,status=200,auth=false)=>({ok:status<400,status,headers:{get(){return auth?'dashboard':null}},async json(){return body;},async text(){return JSON.stringify(body)}});
test('status sums canonical states and separates destroyed history',async()=>{
 const f=fixture(async path=>response(path.includes('/status')?{sandboxes:{RUNNING:2,READY:1,DESTROYED:100,FUTURE:1},jobs:{RUNNING:1,QUEUED:2}}:null));
 await f.app.loadStatus();
 assert.equal(f.document.getElementById('status-sandboxes').textContent,'Sandboxes: 4 current (3 active), 100 destroyed');
 assert.equal(f.document.getElementById('status-jobs').textContent,'Jobs: 3 active');
});
test('events work without scope and retain failures when messages fail',async()=>{
 const paths=[];
 const f=fixture(async path=>{paths.push(path);if(path.includes('messages'))throw Error('messages unavailable');return response({recent_failure_digest:[{ts:'2026-01-01',error:'fixture failure',job_id:'job-1'}]});});
 await f.app.loadEvents();
 assert.equal(paths.length,1);
 assert.match(f.document.getElementById('events-list').innerHTML,/fixture failure/);
 f.document.getElementById('event-scope-type').value='job';
 f.document.getElementById('event-scope-id').value='a b';
 await f.app.loadEvents();
 assert.ok(paths.some(p=>p.includes('scope_type=job&scope_id=a%20b')));
 assert.match(f.document.getElementById('events-list').innerHTML,/fixture failure/);
 assert.match(f.document.getElementById('events-status').textContent,/messages unavailable/);
});
test('concurrent authentication uses one dialog and current token',async()=>{
 let retried=0;
 const f=fixture(async(path,opts)=>{if(opts.headers.Authorization==='Bearer entered'){retried++;return response({});}return response({error:'token required'},401,true);});
 const requests=[f.app.api('/v1/status'),f.app.api('/v1/sandboxes'),f.app.api('/v1/jobs')];
 await new Promise(resolve=>setImmediate(resolve));
 assert.equal(f.body.children.length,1);
 const form=f.body.children[0].children[0];
 form.children[0].children[0].value='entered';
 form.listeners.submit({preventDefault(){}});
 await Promise.all(requests);
 assert.equal(retried,3);
});
test('daemon unauthorized does not ask for browser credentials',async()=>{
 const f=fixture(async()=>response({error:'daemon denied'},401,false));
 await assert.rejects(f.app.api('/v1/status'),/daemon denied/);
 assert.equal(f.body.children.length,0);
});
test('empty status shows zero active resources',async()=>{
 const f=fixture(async()=>response({}));
 await f.app.loadStatus();
 assert.equal(f.document.getElementById('status-sandboxes').textContent,'Sandboxes: 0 current (0 active), 0 destroyed');
 assert.equal(f.document.getElementById('status-jobs').textContent,'Jobs: 0 active');
});
test('dropped create response preserves its key and payload across page reload',async()=>{
 const storage=new Map();
 const payload=JSON.stringify({name:'fixture',profile:'default',prompt:'quoted task'});
 const first=fixture(async()=>{throw Error('connection dropped');},storage);
 await first.app.submitCreation(payload,null);
 const saved=JSON.parse(storage.get('agentlab_create_recovery'));
 assert.equal(saved.payload,payload);
 assert.ok(saved.key);
 assert.match(first.document.getElementById('create-progress').textContent,/outcome unknown/);
 let requests=0;
 const reloaded=fixture(async(path,opts)=>{
  if(opts && opts.method==='POST'){
   requests++;
   assert.equal(opts.headers['Idempotency-Key'],saved.key);
   assert.equal(opts.body,payload);
   return response({id:'same-operation',state:'accepted'});
  }
  if(path.includes('/operations/'))return response({id:'same-operation',state:'unknown',vmid:123});
  return response({sandboxes:[]});
 },storage);
 reloaded.app.initNewSandboxForm();
 assert.equal(reloaded.document.getElementById('create-resume').hidden,false);
 await reloaded.document.getElementById('create-resume').listeners.click();
 assert.equal(requests,1);
 const after=JSON.parse(storage.get('agentlab_create_recovery'));
 assert.equal(after.key,saved.key);
 assert.equal(after.operation,'same-operation');
});
test('destroy confirmation identifies workload and uses normal cleanup',async()=>{
 const requests=[];let confirmation='';
 const f=fixture(async(path,opts)=>{
  requests.push({path,opts});
  if(path==='/api/v1/sandboxes/123')return response({vmid:123,name:'named-vm',state:'RUNNING',job_id:'job-linked',workspace_id:'ws-linked',session_id:'session-linked'});
  if(opts && opts.method==='POST'){assert.equal(JSON.parse(opts.body).force,false);return response({});}
  return response({sandboxes:[]});
 });
 f.ctx.confirm=text=>{confirmation=text;return true;};
 f.ctx.alert=text=>assert.fail(text);
 await f.app.destroySandbox(123);
 for(const text of ['named-vm','RUNNING','job-linked','ws-linked','session-linked'])assert.ok(confirmation.includes(text));
 assert.ok(requests.some(r=>r.path==='/api/v1/sandboxes/123/destroy'));
});
test('prune previews timed-out targets and reports retryable failures',async()=>{
 const paths=[];let message='';
 const f=fixture(async(path,opts)=>{
  paths.push(path);
  if(path.includes('dry_run=true'))return response({outcomes:[{vmid:12,name:'timed-out'}]});
  if(path.includes('prune?vmid=12'))return response({count:0,outcomes:[{vmid:12,outcome:'failed',error:'fixture backend failure',retryable:true}]});
  return response({sandboxes:[]});
 });
 f.ctx.confirm=text=>{assert.match(text,/timed-out/);assert.match(text,/12/);return true;};
 f.ctx.alert=text=>{message=text;};
 f.app.initBulkActions();
 await f.document.getElementById('btn-prune').listeners.click();
 assert.ok(paths.includes('/api/v1/sandboxes/prune?dry_run=true'));
 assert.ok(paths.includes('/api/v1/sandboxes/prune?vmid=12'));
 assert.match(message,/12.*failed.*fixture backend failure.*retryable/);
});

test('job details preserve missing outcomes and link related workspaces',async()=>{
 const paths=[];
 const f=fixture(async path=>{paths.push(path);return response(path.includes('/workspaces/')?{id:'ws/a',name:'Example workspace',size_gb:10,attached_vmid:1001}:{id:'job/a',repo_url:'repo',status:'COMPLETED',sandbox_vmid:1001,workspace_id:'ws/a',outcome:{exit_code:0,duration_seconds:0}});});
 await f.app.showJobDetail('job/a');
 assert.equal(paths[0],'/api/v1/jobs/job%2Fa');
 const summary=f.document.getElementById('detail-summary');
 assert.match(summary.innerHTML,/Exit code/);
 assert.match(summary.innerHTML,/detail-value">0/);
 const workspace=summary.children.find(child=>child.textContent==='Workspace ws/a');
 assert.ok(workspace);
 workspace.listeners.click();
 await new Promise(resolve=>setImmediate(resolve));
 assert.ok(paths.includes('/api/v1/workspaces/ws%2Fa'));
 assert.match(f.document.getElementById('detail-title').textContent,/Example workspace/);
 const old=fixture(async()=>response({id:'old',status:'COMPLETED'}));
 await old.app.showJobDetail('old');
 assert.match(old.document.getElementById('detail-summary').innerHTML,/Not recorded/);
});

test('recovery details show future expiry and recorded workspace ownership',async()=>{
 const f=fixture(async path=>response(path.includes('/workspaces/')?{id:'workspace',size_gb:10,lease_owner:'job:fixture',lease_expires_at:'2100-01-01T00:00:00Z'}:{vmid:1001,state:'RUNNING',lease_expires_at:'2100-01-01T00:00:00Z'}));
 await f.app.showDetail(1001);
 assert.match(f.document.getElementById('detail-summary').innerHTML,/2100-01-01T00:00:00Z \(in \d+d\)/);
 await f.app.showWorkspaceDetail('workspace');
 assert.ok(f.document.getElementById('detail-summary').children.some(item=>item.textContent==='Lease owner: job:fixture'));
});

test('events retain timestamped stale sources without reusing another message scope',async()=>{
 let failing=false;
 const f=fixture(async path=>{if(failing)throw Error('fixture offline');return response(path.includes('messages')?{messages:[{ts:'2026-01-01',scope_type:'job',scope_id:'one',text:'private scope one'}]}:{recent_failure_digest:[{ts:'2026-01-01',error:'retained digest'}]});});
 f.document.getElementById('event-scope-type').value='job';
 f.document.getElementById('event-scope-id').value='one';
 await f.app.loadEvents();
 failing=true;
 await f.app.loadEvents();
 assert.match(f.document.getElementById('events-list').innerHTML,/private scope one/);
 assert.match(f.document.getElementById('events-list').innerHTML,/retained digest/);
 assert.match(f.document.getElementById('events-status').textContent,/Showing stale messages from.*failure digest from/);
 f.document.getElementById('event-scope-id').value='two';
 await f.app.loadEvents();
 assert.doesNotMatch(f.document.getElementById('events-list').innerHTML,/private scope one/);
 assert.match(f.document.getElementById('events-list').innerHTML,/retained digest/);
});
