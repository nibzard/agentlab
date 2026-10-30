const test = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, '../static/assets/app.js'), 'utf8')
  .replace('document.addEventListener("DOMContentLoaded", init);', 'globalThis.subject = {api, loadStatus, loadEvents, loadConnection, loadProfiles, renderJobResults, renderExecutionLogs, validateSandboxBody};');
function element(tag = 'div') {
  return {tag, style: {}, children: [], attributes: {}, events: {}, textContent: '', innerHTML: '',
    appendChild(child) { this.children.push(child); return child; },
    setAttribute(name, value) { this.attributes[name] = value; },
    addEventListener(name, handler) { this.events[name] = handler; },
    fire(name) { return this.events[name].call(this); },
    contains(child) { return this === child || this.children.some(item => item.contains(child)); }};
}
function find(root, predicate) {
  if (predicate(root)) return root;
  for (const child of root.children) { const found = find(child, predicate); if (found) return found; }
}
function fixture(fetch, prompt = () => 'token') {
  const elements = new Map();
  const context = {fetch, TextEncoder, window: {prompt}, sessionStorage: {getItem: () => '', setItem: () => {}},
    document: {createElement: element, getElementById: id => {
      if (!elements.has(id)) elements.set(id, element());
      return elements.get(id);
    }}, console};
  vm.createContext(context);
  vm.runInContext(source, context);
  return {subject: context.subject, elements};
}
const response = (body, status = 200, enrollment = false) => ({status, ok: status < 400, headers: {get: key => enrollment && key === 'X-Agentlab-Dashboard-Auth' ? 'required' : null}, json: async () => body, text: async () => JSON.stringify(body)});
test('concurrent requests share one enrollment prompt and use a cookie session', async () => {
  let prompts = 0;
  let enrolled = false;
  const {subject} = fixture(async (path, opts) => {
    if (path === '/auth/enroll') {
      assert.equal(opts.headers.Authorization, 'Bearer token');
      enrolled = true;
      return response({}, 204);
    }
    assert.equal(opts.headers.Authorization, undefined);
    return enrolled ? response({}) : response({}, 401, true);
  }, () => {prompts++; return 'token';});
  await Promise.all([subject.api('/one'), subject.api('/two'), subject.api('/three')]);
  await subject.api('/four');
  assert.equal(prompts, 1);
});
test('counts use API state names and exclude destroyed sandboxes', async () => {
  const {subject, elements} = fixture(async url => url.endsWith('/status') ? response({sandboxes:{RUNNING:2, READY:3, STOPPED:1, DESTROYED:9}, jobs:{QUEUED:4, RUNNING:2}}) : response(null,404));
  await subject.loadStatus();
  assert.equal(elements.get('status-sandboxes').textContent, 'Sandboxes: 6 (5 active)');
  assert.equal(elements.get('status-jobs').textContent, 'Jobs: 6 active');
});
test('events load scoped messages and the failure digest', async () => {
  const {subject, elements} = fixture(async url => {
    if (url.endsWith('/status')) return response({job_timelines:{job1:{}},recent_failure_digest:[{message:'failure fixture',ts:'2026-10-15T12:00:00Z'}]});
    if (url.endsWith('/workspaces')) return response({workspaces:[]});
    assert.match(url, /messages\?scope_type=job&scope_id=job1&limit=20$/);
    return response({messages:[{text:'message fixture',scope_type:'job',scope_id:'job1',ts:'2026-10-15T12:00:00Z'}]});
  });
  await subject.loadEvents();
  assert.match(elements.get('events-list').innerHTML, /message fixture/);
  assert.match(elements.get('events-list').innerHTML, /failure fixture/);
});

test('upstream revocation does not request another browser enrollment code', async () => {
  let prompts = 0;
  const {subject, elements} = fixture(async () => response({error:'revoked'},401), () => {prompts++; return 'unused';});
  assert.equal(await subject.loadConnection(), false);
  assert.equal(prompts, 0);
  assert.equal(elements.get('connection-state').textContent, 'Authentication required');
});
test('connection identity is pinned and a changed server blocks creation', async () => {
  let identity = 'first-server';
  let revoked = false;
  const {subject, elements} = fixture(async () => revoked ? response({error:'revoked'},401) : response({server_identity:identity,protocol_version:1,api_schema_version:1,authorization:{method:'ssh-token',commands:['sandbox','profile.read'],scope:[]}}));
  assert.equal(await subject.loadConnection(), true);
  assert.equal(elements.get('btn-new-sandbox').disabled, false);
  revoked = true;
  assert.equal(await subject.loadConnection(), false);
  assert.equal(elements.get('connection-state').textContent, 'Authentication required');
  revoked = false;
  identity = 'different-server';
  assert.equal(await subject.loadConnection(), false);
  assert.equal(elements.get('btn-new-sandbox').disabled, true);
  assert.match(elements.get('connection-action').textContent, /identity changed/);
});
test('missing profiles never creates an invented default profile', async () => {
  const {subject, elements} = fixture(async () => response({profiles:[]}));
  await subject.loadProfiles();
  assert.equal(elements.get('select-profile').disabled, true);
  assert.equal(elements.get('select-profile').innerHTML, '');
  assert.equal(elements.get('btn-check-sandbox').disabled, true);
  assert.match(elements.get('sandbox-plan-state').textContent, /No profiles/);
});
test('failed capacity checks block the plan', async () => {
  const {subject} = fixture(async (_url, opts) => {
    const body = JSON.parse(opts.body);
    assert.equal(body.prompt, undefined);
    return response({ok:false,errors:[{message:'capacity unavailable'}]});
  });
  await assert.rejects(subject.validateSandboxBody({profile:'small',prompt:'task'}), /capacity unavailable/);
});
test('an older server cannot skip the required plan check', async () => {
  const {subject} = fixture(async () => response({error:'not found'},404));
  await assert.rejects(subject.validateSandboxBody({profile:'small'}), /does not support sandbox plan checks/);
});
test('a sandbox-scoped credential cannot create an unrelated sandbox', async () => {
  const {subject, elements} = fixture(async () => response({server_identity:'scoped-server',protocol_version:1,api_schema_version:1,authorization:{method:'ssh-token',commands:['*'],scope:['sandbox:1001']}}));
  assert.equal(await subject.loadConnection(), true);
  assert.equal(elements.get('btn-new-sandbox').disabled, true);
  assert.match(elements.get('connection-action').textContent, /cannot create new sandboxes/);
});
test('credential store failure shows its corrective action without a new enrollment', async () => {
  let prompts = 0;
  const {subject, elements} = fixture(async () => response({code:'dashboard_credential_unavailable',error:'store unavailable',action:'Restore the saved credential store, then select Refresh.'},503), () => {prompts++; return 'unused';});
  assert.equal(await subject.loadConnection(), false);
  assert.equal(prompts, 0);
  assert.equal(elements.get('connection-state').textContent, 'Credential refresh blocked');
  assert.match(elements.get('connection-action').textContent, /saved credential store/);
  assert.equal(elements.get('btn-new-sandbox').disabled, true);
});
test('server-side identity rejection blocks the selected target', async () => {
  const {subject, elements} = fixture(async () => response({code:'v1/auth/server_identity_mismatch',error:'request targets another server',action:'Verify the original context before retrying.'},409));
  assert.equal(await subject.loadConnection(), false);
  assert.equal(elements.get('connection-state').textContent, 'Server identity changed');
  assert.equal(elements.get('btn-new-sandbox').disabled, true);
  assert.match(elements.get('connection-action').textContent, /original context/);
});

const executionIndex = () => ({job_id:'job-log',server_identity:'log-server',state:'verified',manifest_sha256:'m'.repeat(64),archive_sha256:'a'.repeat(64),entries:[{path:'agent-runner.log',size_bytes:20000,text:true},{path:'report.json',size_bytes:2,text:true}]});
const logCapabilities = enabled => ({server_identity:'log-server',protocol_version:1,api_schema_version:1,features:{execution_inspection:enabled},authorization:{commands:['job.artifacts'],scope:[]}});
const logJob = {id:'job-log',status:'COMPLETED',result:{repository_result:{state:'verified',manifest_sha256:'m'.repeat(64)}}};
function logPage(path, offset) {
  const size = path === 'report.json' ? 2 : 20000;
  const end = Math.min(offset+16384,size);
  return {...executionIndex(),chunk:{path,offset,next_offset:end,eof:end===size,text:path==='report.json'?'{}':('<img src=x onerror=alert(1)>'.padEnd(20000,'x')).slice(offset,end)}};
}
test('execution viewer pages text and keeps guest markup out of HTML', async () => {
  const calls = [];
  const {subject} = fixture(async url => {
    if (url.endsWith('/capabilities')) return response(logCapabilities(true));
    calls.push(url);
    if (!url.includes('?')) return response(executionIndex());
    const query = new URL(url,'https://fixture.invalid').searchParams;
    assert.equal(query.get('archive_sha256'),'a'.repeat(64));
    return response(logPage(query.get('path'),Number(query.get('offset'))));
  });
  await subject.loadConnection();
  const root=element();subject.renderExecutionLogs(logJob,root);
  await find(root,item=>item.textContent==='View execution logs').fire('click');
  const output=find(root,item=>item.tag==='pre');
  assert.match(output.textContent,/^<img/);assert.equal(output.innerHTML,'');
  const next=find(root,item=>item.textContent==='Next chunk');
  const previous=find(root,item=>item.textContent==='Previous chunk');
  assert.equal(previous.disabled,true);assert.equal(next.disabled,false);
  await next.fire('click');assert.equal(next.disabled,true);assert.equal(previous.disabled,false);
  assert.match(calls.at(-1),/offset=16384/);
  await previous.fire('click');assert.match(calls.at(-1),/offset=0/);
  const select=find(root,item=>item.tag==='select');select.value='report.json';await select.fire('change');
  assert.equal(output.textContent,'{}');assert.equal(next.disabled,true);assert.equal(previous.disabled,true);
  assert.equal(output.attributes['aria-label'],'Execution log text');assert.equal(output.tabIndex,0);
});
test('an older server leaves downloads available without requesting inline logs', async () => {
  const {subject}=fixture(async url=>{assert.match(url,/capabilities$/);return response(logCapabilities(false));});
  await subject.loadConnection();const root=element();subject.renderExecutionLogs(logJob,root);
  assert.equal(find(root,item=>item.textContent==='View execution logs'),undefined);
  assert.ok(find(root,item=>item.textContent.includes('Download the archive')));
});
test('execution viewer refuses changed archive identity before showing text', async () => {
  const {subject}=fixture(async url=>{
    if(url.endsWith('/capabilities'))return response(logCapabilities(true));
    if(!url.includes('?'))return response(executionIndex());
    return response({...logPage('agent-runner.log',0),archive_sha256:'b'.repeat(64)});
  });
  await subject.loadConnection();const root=element();subject.renderExecutionLogs(logJob,root);
  await find(root,item=>item.textContent==='View execution logs').fire('click');
  assert.equal(find(root,item=>item.tag==='pre').textContent,'');
  assert.ok(find(root,item=>item.textContent.includes('Execution log changed')));
  assert.equal(find(root,item=>item.textContent==='Next chunk').disabled,true);
});
test('a late response cannot replace the newly selected execution log', async () => {
  let completeOld;
  const {subject}=fixture(async url=>{
    if(url.endsWith('/capabilities'))return response(logCapabilities(true));
    if(!url.includes('?'))return response(executionIndex());
    const query=new URL(url,'https://fixture.invalid').searchParams;
    if(query.get('path')==='report.json') return new Promise(resolve=>{completeOld=resolve;});
    return response(logPage('agent-runner.log',Number(query.get('offset'))));
  });
  await subject.loadConnection();const root=element();subject.renderExecutionLogs(logJob,root);
  await find(root,item=>item.textContent==='View execution logs').fire('click');
  const select=find(root,item=>item.tag==='select');select.value='report.json';const pending=select.fire('change');
  select.value='agent-runner.log';await select.fire('change');
  completeOld(response(logPage('report.json',0)));await pending;
  assert.match(find(root,item=>item.tag==='pre').textContent,/^<img/);
});
