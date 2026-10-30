#!/usr/bin/env python3
"""Check the packaged dashboard in a real browser against a local API fixture.

Requires agent-browser and an AgentLab client binary. No real host is contacted.
"""
import argparse
import http.server
import json
import os
import pathlib
import re
import secrets
import socket
import subprocess
import tempfile
import threading
import time
import urllib.parse


def smoke(client_path, saved_credentials=False):
    temporary = tempfile.TemporaryDirectory(prefix='agentlab-dashboard-browser-')
    root = pathlib.Path(temporary.name)
    state = {'identity': 'server_' + 'a' * 64, 'capacity_blocked': False, 'profiles_empty': False, 'revoked': False, 'created': [], 'canceled': []}
    token = secrets.token_urlsafe(24)
    class API(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_): pass
        def reply(self, data, code=200):
            raw = json.dumps(data).encode()
            self.send_response(code)
            self.send_header('Content-Type','application/json')
            self.send_header('Content-Length',str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)
        def do_GET(self):
            if self.headers.get('Authorization') != 'Bearer '+token or state['revoked']: return self.reply({'error':'credential revoked','code':'fixture_revoked'},401)
            if self.headers.get('X-AgentLab-Server-Identity') is not None and self.headers.get('X-AgentLab-Server-Identity') != state['identity']: return self.reply({'error':'server identity changed','code':'v1/auth/server_identity_mismatch'},409)
            if self.path == '/v1/capabilities': return self.reply({'server_identity':state['identity'],'protocol_version':1,'api_schema_version':1,'features':{'request_server_identity':True,'execution_inspection':True},'authorization':{'method':'ssh-token','issuer':'fixture-signing-authority','commands':state.get('commands',['*']),'scope':[],'expires_at':int(time.time())+3600}})
            if self.path == '/v1/host': return self.reply({'server_identity':state['identity']})
            if self.path == '/v1/profiles': return self.reply({'profiles':[] if state['profiles_empty'] else [{'name':'small','type':'vm'}]})
            if self.path == '/v1/status': return self.reply({'sandboxes':{'RUNNING':len(state['created'])},'jobs':{'RUNNING':1,'COMPLETED':1},'job_timelines':{'job-complete':{'job_id':'job-complete','status':'COMPLETED'},'job-running':{'job_id':'job-running','status':'RUNNING'}}})
            if self.path == '/v1/sandboxes': return self.reply({'sandboxes':state['created']})
            if self.path.startswith('/v1/jobs/'):
                job = self.path.split('/')[3]
                if self.path.split('?')[0].endswith('/execution'):
                    index = {'job_id':job,'server_identity':state['identity'],'state':'verified',
                             'manifest_sha256':'c'*64,'archive_sha256':'d'*64,
                             'entries':[{'path':'agent-runner.log','size_bytes':24000,'text':True},
                                        {'path':'report.json','size_bytes':2,'text':True}]}
                    query = urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query)
                    if 'path' in query:
                        name = query['path'][0]
                        text = ('<script>window.executionLogRan = true</script>\n'+'guest output '*2000)[:24000] if name == 'agent-runner.log' else '{}'
                        offset = int(query.get('offset',['0'])[0])
                        end = min(offset+16384,len(text))
                        index['chunk'] = {'path':name,'offset':offset,'next_offset':end,'eof':end==len(text),'text':text[offset:end]}
                    return self.reply(index)
                if self.path.endswith('/artifacts'): return self.reply({'artifacts':[{'name':'execution.tar.gz','path':'repository-results/fixture/execution.tar.gz','size_bytes':123}]})
                return self.reply({'id':job,'status':'COMPLETED' if job == 'job-complete' else ('FAILED' if job in state['canceled'] else state.get('running_status','RUNNING')),'profile':'small','task':'Run tests','created_at':'2026-10-30T10:00:00Z','updated_at':'2026-10-30T10:01:00Z','sandbox_vmid':1001,'result':{'repository_result':{'state':'verified','manifest_sha256':'c'*64,'cleanup_status':'retained','execution':{'state':'verified','tests':'failed','test_runs':[{'command':'make test','state':'failed','exit_code':1}]},'resources':[{'kind':'sandbox','id':'1001','state':'retained','expires_at':'2026-10-30T12:00:00Z'}]}}})
            if self.path.startswith('/v1/messages?'): return self.reply({'messages':[]})
            if self.path == '/v1/workspaces': return self.reply({'workspaces':[]})
            if self.path == '/v1/exposures': return self.reply({'exposures':[]})
            return self.reply({'error':'not found'},404)
        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers.get('Content-Length',0))) or '{}')
            if self.headers.get('Authorization') != 'Bearer '+token or state['revoked']: return self.reply({'error':'credential revoked'},401)
            if self.headers.get('X-AgentLab-Server-Identity') != state['identity']: return self.reply({'error':'server identity changed','code':'v1/auth/server_identity_mismatch'},409)
            if self.path == '/v1/sandboxes/validate-plan':
                if state['capacity_blocked']: return self.reply({'ok':False,'errors':[{'message':'No capacity is available. Release resources or select another profile.'}]})
                plan = dict(body, keepalive=body.get('keepalive',False),ttl_minutes=body.get('ttl_minutes',60),capacity={'checked':True,'cores':2,'memory_mb':2048})
                return self.reply({'ok':True,'plan':plan,'warnings':[]})
            if self.path == '/v1/sandboxes':
                sb = dict(body,vmid=1001+len(state['created']),state='RUNNING',created_at='2026-10-30T10:00:00Z')
                state['created'].append(sb)
                return self.reply(sb,201)
            if self.path.endswith('/cancel'):
                state['canceled'].append(self.path.split('/')[3])
                return self.reply({'state':'stopped','cleanup_status':'retained'})
            return self.reply({'error':'not found'},404)
    server = http.server.ThreadingHTTPServer(('127.0.0.1',0),API)
    threading.Thread(target=server.serve_forever,daemon=True).start()
    with socket.socket() as port:
        port.bind(('127.0.0.1',0))
        listen = '127.0.0.1:'+str(port.getsockname()[1])
    env = dict(os.environ,AGENTLAB_CONFIG_DIR=str(root/'config'),AGENTLAB_CREDENTIAL_STORE='file',AGENTLAB_ENDPOINT='',AGENTLAB_TOKEN=token)
    endpoint = 'http://127.0.0.1:' + str(server.server_port)
    env['AGENTLAB_CONTEXT'] = ''
    def save_credential():
        result = subprocess.run([str(client_path),'connect','--context','task','--endpoint',endpoint,'--token-stdin','--credential-store','file','--json'],
                                input=token.encode(),env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=15)
        if result.returncode:
            raise RuntimeError('The fixture could not save its verified context')
        if token.encode() in result.stdout + result.stderr:
            raise RuntimeError('Context save exposed a credential')
    if saved_credentials:
        env['AGENTLAB_TOKEN'] = ''
        save_credential()
    log = (root/'dashboard.log').open('wb')
    target = ['--context','task'] if saved_credentials else ['--endpoint',endpoint]
    client = subprocess.Popen([str(client_path),*target,'dashboard','--listen',listen],env=env,stdout=log,stderr=log)

    session = 'agentlab-browser-' + secrets.token_hex(6)
    def browser(*args, script=None):
        # The CLI's wait --fn uses string evaluation when a condition is pending.
        # Poll through CDP evaluation without weakening the dashboard policy.
        if args[:2] == ('wait', '--fn'):
            deadline = time.monotonic() + 20
            while time.monotonic() < deadline:
                if browser('eval', 'Boolean(' + args[2] + ')').strip() == 'true':
                    return 'condition verified'
                time.sleep(0.2)
            raise RuntimeError('Browser condition did not become true: ' + args[2])
        command = ['agent-browser', '--session', session, *args]
        result = subprocess.run(command, input=script, text=True, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, timeout=30)
        if result.returncode:
            raise RuntimeError('Browser check failed: ' + result.stdout + result.stderr)
        return result.stdout
    try:
        deadline = time.monotonic() + 10
        code = None
        while time.monotonic() < deadline:
            if client.poll() is not None:
                raise RuntimeError('Dashboard stopped before browser enrollment')
            match = re.search(r'expires in five minutes\): ([A-Za-z0-9_-]+)', (root/'dashboard.log').read_text())
            if match:
                code = match.group(1)
                break
            time.sleep(0.02)
        if not code:
            raise RuntimeError('Dashboard enrollment is unavailable')
        browser('open', 'http://' + listen)
        script = 'fetch("/auth/enroll",{method:"POST",headers:{"X-Requested-With":"XMLHttpRequest",Authorization:"Bearer "+' + json.dumps(code) + '}}).then(r=>{if(r.status!==204)throw new Error("Enrollment failed")})'
        browser('eval', '--stdin', script=script)
        browser('click', '#btn-refresh')
        browser('wait', '--fn', 'document.getElementById("connection-state").textContent === "Connected"')
        browser('eval', 'if(document.cookie.includes("dashboard_token"))throw new Error("Session cookie is readable")')
        if saved_credentials:
            browser('eval', 'window.prompt = () => { window.unexpectedPrompt = true; return null; }')
            previous_token = token
            token = secrets.token_urlsafe(24)
            save_credential()
            browser('click', '#btn-refresh')
            browser('wait', '--fn', 'document.getElementById("connection-state").textContent === "Connected"')
            browser('eval', 'if(window.unexpectedPrompt || !document.getElementById("connection-permissions").textContent.includes("saved context task"))throw new Error("Saved credential refresh required another session")')
            state['commands'] = ['schema.read','status.read']
            token = secrets.token_urlsafe(24)
            save_credential()
            browser('click', '#btn-refresh')
            browser('wait', '--text', 'Credential refresh blocked')
            browser('eval', 'if(!document.getElementById("btn-new-sandbox").disabled)throw new Error("Changed authority permitted creation")')
            state['commands'] = ['*']
            token = secrets.token_urlsafe(24)
            save_credential()
            browser('click', '#btn-refresh')
            browser('wait', '--fn', 'document.getElementById("connection-state").textContent === "Connected"')
            if client.poll() is not None:
                raise RuntimeError('The dashboard stopped during credential refresh')
            check = browser('eval', 'document.documentElement.outerHTML + JSON.stringify(localStorage) + JSON.stringify(sessionStorage)')
            if token in check or previous_token in check:
                raise RuntimeError('The browser received a daemon credential')

        state['profiles_empty'] = True
        browser('click', '#btn-new-sandbox')
        browser('wait', '--text', 'No profiles are available')
        browser('eval', 'if(!document.getElementById("btn-check-sandbox").disabled)throw new Error("Missing profiles permitted validation")')
        browser('press', 'Escape')
        state['profiles_empty'] = False
        browser('click', '#btn-new-sandbox')
        browser('wait', '--fn', '!document.getElementById("select-profile").disabled')
        browser('fill', '#form-new-sandbox [name="name"]', 'browser-trial')
        state['capacity_blocked'] = True
        browser('click', '#btn-check-sandbox')
        browser('wait', '--text', 'No capacity is available')
        browser('eval', 'if(!document.getElementById("btn-create-sandbox").disabled)throw new Error("Capacity failure permitted creation")')
        if state['created']:
            raise RuntimeError('A blocked plan created a sandbox')
        state['capacity_blocked'] = False
        browser('click', '#btn-check-sandbox')
        browser('wait', '--fn', '!document.getElementById("btn-create-sandbox").disabled')
        browser('fill', '#form-new-sandbox [name="name"]', 'browser-trial-changed')
        browser('eval', 'if(!document.getElementById("btn-create-sandbox").disabled || document.getElementById("sandbox-plan-review").textContent)throw new Error("Changed settings retained a reviewed plan")')
        browser('focus', '#form-new-sandbox [name="name"]')
        browser('press', 'Shift+Tab')
        browser('eval', 'if(document.activeElement.textContent!=="Cancel")throw new Error("Focus escaped the dialog")')
        browser('press', 'Tab')
        browser('eval', 'if(document.activeElement.name!=="name")throw new Error("Focus did not wrap")')
        browser('set', 'viewport', '375', '812')
        browser('eval', 'if(document.documentElement.scrollWidth>window.innerWidth || document.querySelector("#modal-new-sandbox .modal-content").scrollWidth > document.querySelector("#modal-new-sandbox .modal-content").clientWidth)throw new Error("Narrow layout overflows")')
        browser('click', '#btn-check-sandbox')
        browser('wait', '--fn', '!document.getElementById("btn-create-sandbox").disabled')
        browser('click', '#btn-create-sandbox')
        browser('wait', '--text', 'browser-trial-changed')
        if len(state['created']) != 1:
            raise RuntimeError('Creation did not produce exactly one sandbox')
        browser('click', 'a[data-view="jobs"]')
        browser('click', '#job-list tr:first-child button')
        browser('wait', '--text', 'Download execution logs and report')
        browser('click','button:has-text("View execution logs")')
        browser('wait','--fn','document.querySelector(".execution-log-text") && document.querySelector(".execution-log-text").textContent.startsWith("<script>")')
        browser('eval','if(window.executionLogRan || document.querySelector(".execution-log-text").children.length)throw new Error("Guest log markup executed")')
        browser('click','button:has-text("Next chunk")')
        browser('wait','--text','End of log.')
        browser('eval','if(!Array.from(document.querySelectorAll("button")).find(el=>el.textContent==="Next chunk").disabled)throw new Error("Log cursor passed EOF")')
        browser('click','button:has-text("Previous chunk")')
        browser('wait','--fn','document.querySelector(".execution-log-text").textContent.startsWith("<script>")')
        browser('select','select[aria-label="Execution log"]','report.json')
        browser('wait','--fn','document.querySelector(".execution-log-text").textContent === "{}"')
        browser('eval','document.querySelector(".execution-log-text").focus(); if(document.documentElement.scrollWidth>innerWidth)throw new Error("Inline log overflows the viewport")')
        browser('eval', 'if(!document.getElementById("job-results").textContent.includes("make test: failed (exit 1)") || !document.getElementById("job-results").textContent.includes("sandbox 1001: retained"))throw new Error("Result evidence is missing")')
        browser('press', 'Escape')
        browser('eval', 'if(document.querySelector("main").inert)throw new Error("Main view remains inert")')
        browser('eval', 'window.confirm = text => { window.lastConfirmation = text; return true; }')
        browser('click', '#job-list tr:nth-child(2) button')
        browser('wait', '--text', 'Cancel job')
        state['running_status'] = 'QUEUED'
        browser('wait', '--fn', 'document.getElementById("detail-summary").textContent.includes("QUEUED")')
        browser('click', '#job-results button:has-text("Cancel job")')
        browser('wait', '--fn', 'document.getElementById("detail-summary").textContent.includes("FAILED")')
        browser('eval', 'if(!window.lastConfirmation.includes("job-running") || !window.lastConfirmation.includes(' + json.dumps(state['identity']) + '))throw new Error("Cancellation confirmation lacks its target")')
        if state['canceled'] != ['job-running']:
            raise RuntimeError('Cancellation did not target the selected job')
        browser('press', 'Escape')
        browser('eval', 'window.prompt = () => { window.unexpectedPrompt = true; return null; }')
        state['revoked'] = True
        browser('click', '#btn-refresh')
        browser('wait', '--text', 'Authentication required' if saved_credentials else 'Credential refresh blocked')
        browser('eval', 'if(window.unexpectedPrompt)throw new Error("Upstream revocation requested browser enrollment")')
        state['revoked'] = False
        state['identity'] = 'changed-server'
        browser('click', '#btn-refresh')
        browser('wait', '--text', 'Server identity changed' if saved_credentials else 'Credential refresh blocked')
        browser('eval', 'if(!document.getElementById("btn-new-sandbox").disabled)throw new Error("Changed server permitted creation")')
        print(json.dumps({'guided_sandbox':'verified','capacity_denial':'verified',
                          'changed_settings':'verified','keyboard_dialog':'verified',
                          'narrow_layout':'verified','job_results':'verified',
                          'job_cancellation':'verified','job_progress':'verified','execution_logs':'verified','revocation':'verified',
                          'server_identity':'verified','credential_refresh':'verified' if saved_credentials else 'fixed startup credential'}), flush=True)
    finally:
        try:
            browser('close')
        finally:
            client.terminate()
            try: client.wait(timeout=5)
            except subprocess.TimeoutExpired: client.kill(); client.wait()
            server.shutdown()
            server.server_close()
            log.close()
            temporary.cleanup()


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--client', type=pathlib.Path, required=True)
    parser.add_argument('--saved-credentials', action='store_true', help='also verify context credential refresh without restarting the dashboard')
    arguments = parser.parse_args()
    smoke(arguments.client.resolve(strict=True), arguments.saved_credentials)
