const test = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, '../static/assets/app.js'), 'utf8')
  .replace('document.addEventListener("DOMContentLoaded", init);', 'globalThis.subject = {api, loadStatus, loadEvents};');
function fixture(fetch, prompt = () => 'token') {
  const elements = new Map();
  const context = {fetch, window: {prompt}, sessionStorage: {getItem: () => '', setItem: () => {}},
    document: {getElementById: id => {
      if (!elements.has(id)) elements.set(id, {style: {}, textContent: '', innerHTML: ''});
      return elements.get(id);
    }}, console};
  vm.createContext(context);
  vm.runInContext(source, context);
  return {subject: context.subject, elements};
}
const response = (body, status = 200) => ({status, ok: status < 400, json: async () => body, text: async () => JSON.stringify(body)});
test('concurrent requests share one token prompt and retain the token in memory', async () => {
  let prompts = 0;
  const {subject} = fixture(async (_, opts) => opts.headers.Authorization === 'Bearer token' ? response({}) : response({}, 401), () => {prompts++; return 'token';});
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
