# Live AgentLab Test Log

## Test Run Start
- UTC: 2026-02-14T13:25:55Z
- Scope: live create/destroy/manipulate tests using installed `/usr/local/bin/agentlab`
- Host: pve

## Baseline Checks
### agentlab --version
```
version=v0.1.4-dirty commit=5843248 date=2026-02-14T13:17:44Z
```

### agentlabd --version
```
version=v0.1.4-dirty commit=5843248 date=2026-02-14T13:17:45Z
```

### systemctl is-active agentlabd.service
```
active
```

### agentlab status (snapshot)
```
Sandboxes:
STATE         COUNT
REQUESTED     0
PROVISIONING  0
BOOTING       0
READY         0
RUNNING       3
SUSPENDED     0
COMPLETED     0
FAILED        0
TIMEOUT       0
STOPPED       0
DESTROYED     61
Network Modes:
MODE       COUNT
off        0
nat        64
allowlist  0
Jobs:
STATUS     COUNT
QUEUED     0
RUNNING    1
COMPLETED  2
FAILED     7
TIMEOUT    0
Artifacts:
Root: /var/lib/agentlab/artifacts
Total Bytes: 944349380608
Free Bytes: 933760335872
Used Bytes: 10589044736
Error: -
Metrics:
Enabled: false
Recent Failures:
TIME                           KIND        JOB                   SANDBOX  MESSAGE
2026-01-31T19:12:29.87674595Z  job.failed  job_6908beea3f17ac57  -        template validation failed: failed to query template VM 9000: command qm config 9000 failed: exit status 255: ipcc_send_rec[1] failed: Unknown error -1
ipcc_send_rec[2] failed: Unknown error -1
ipcc_send_rec[3] failed: Unknown error -1
Unable to load access control list: Unknown error -1
2026-01-31T20:05:02.528359152Z  job.failed  job_e7deaa1bed9b96d4  -  template validation failed: failed to query template VM 9000: command qm config 9000 failed: exit status 255: ipcc_send_rec[1] failed: Unknown error -1
ipcc_send_rec[2] failed: Unknown error -1
ipcc_send_rec[3] failed: Unknown error -1
Unable to load access control list: Unknown error -1
2026-02-14T12:27:19.51578206Z   job.failed  job_51d87ecf0f92ecfb  -     template validation failed: request failed: Get "https://localhost:8006/api2/json/nodes": tls: failed to verify certificate: x509: certificate signed by unknown authority
2026-02-14T12:27:43.687921825Z  job.failed  job_1806a6c5b9473c43  1057  API error (status 400): invalid format - format error
net0.fwgroup: property is not defined in schema and the schema does not allow additional properties
2026-02-14T12:31:06.186773307Z  job.failed  job_56344d760f7842a8  -  template validation failed: failed to query template VM 9000: command qm config 9000 failed: exit status 255: ipcc_send_rec[1] failed: Unknown error -1
ipcc_send_rec[2] failed: Unknown error -1
ipcc_send_rec[3] failed: Unknown error -1
Unable to load access control list: Unknown error -1
2026-02-14T13:05:33.989Z  job.failed  job_8b5ed80a94e47385  1058  job cleanup: sandbox already DESTROYED while job stayed RUNNING
2026-02-14T13:05:33.989Z  job.failed  job_f80051b2f4ed82b6  1060  job cleanup: sandbox already DESTROYED while job stayed RUNNING
2026-02-14T13:19:00.892Z  job.failed  job_9ee731bf095808d8  1061  job cleanup: sandbox already DESTROYED while job stayed RUNNING
2026-02-14T13:19:00.892Z  job.failed  job_943ad77e343b497e  1062  job cleanup: sandbox already DESTROYED while job stayed RUNNING
```

## Lifecycle Test: live-lifecycle-132617
### Create Sandbox
- UTC: 2026-02-14T13:26:31Z
```bash
agentlab --json sandbox new --profile yolo-ephemeral --name live-lifecycle-132617 --ttl 45m
```
```
{
  "vmid": 1065,
  "name": "live-lifecycle-132617",
  "profile": "yolo-ephemeral",
  "state": "RUNNING",
  "ip": "10.77.0.177",
  "network": {
    "mode": "nat"
  },
  "keepalive": false,
  "lease_expires_at": "2026-02-14T14:11:17.898002011Z",
  "created_at": "2026-02-14T13:26:17.898002011Z",
  "updated_at": "2026-02-14T13:26:30.56622421Z"
}
```
- exit_code: 0
- vmid: 1065

### Show Sandbox
- UTC: 2026-02-14T13:26:31Z
```bash
agentlab sandbox show 1065
```
```
VMID: 1065
Name: live-lifecycle-132617
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:11:17.898002011Z
Last Used At: 2026-02-14T13:26:31.877298341Z
Created At: 2026-02-14T13:26:17.898002011Z
Updated At: 2026-02-14T13:26:31.877300508Z
```
- exit_code: 0

### Pause Sandbox
- UTC: 2026-02-14T13:26:31Z
```bash
agentlab sandbox pause 1065
```
```
sandbox 1065 paused (state=SUSPENDED)
```
- exit_code: 0

### Show Sandbox After Pause
- UTC: 2026-02-14T13:26:32Z
```bash
agentlab sandbox show 1065
```
```
VMID: 1065
Name: live-lifecycle-132617
Profile: yolo-ephemeral
State: SUSPENDED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:11:17.898002011Z
Last Used At: 2026-02-14T13:26:32.363678453Z
Created At: 2026-02-14T13:26:17.898002011Z
Updated At: 2026-02-14T13:26:32.363679878Z
```
- exit_code: 0

### Resume Sandbox
- UTC: 2026-02-14T13:26:32Z
```bash
agentlab sandbox resume 1065
```
```
sandbox 1065 resumed (state=READY)
```
- exit_code: 0

### Show Sandbox After Resume
- UTC: 2026-02-14T13:26:32Z
```bash
agentlab sandbox show 1065
```
```
VMID: 1065
Name: live-lifecycle-132617
Profile: yolo-ephemeral
State: READY
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:11:17.898002011Z
Last Used At: 2026-02-14T13:26:32.777150322Z
Created At: 2026-02-14T13:26:17.898002011Z
Updated At: 2026-02-14T13:26:32.77715211Z
```
- exit_code: 0

### Save Snapshot
- UTC: 2026-02-14T13:26:32Z
```bash
agentlab sandbox snapshot save --force 1065 livecheck1
```
```
Sandbox: 1065
Snapshot: livecheck1
Created At: 2026-02-14T13:26:33.858706084Z
```
- exit_code: 0

### List Snapshots
- UTC: 2026-02-14T13:26:33Z
```bash
agentlab sandbox snapshot list 1065
```
```
NAME        CREATED
clean       2026-02-14T13:26:30Z
livecheck1  2026-02-14T13:26:33Z
```
- exit_code: 0

### Stop Sandbox
- UTC: 2026-02-14T13:26:34Z
```bash
agentlab sandbox stop 1065
```
```
sandbox 1065 stopped (state=STOPPED)
```
- exit_code: 0

### Show Sandbox After Stop
- UTC: 2026-02-14T13:26:34Z
```bash
agentlab sandbox show 1065
```
```
VMID: 1065
Name: live-lifecycle-132617
Profile: yolo-ephemeral
State: STOPPED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:11:17.898002011Z
Last Used At: 2026-02-14T13:26:34.865708271Z
Created At: 2026-02-14T13:26:17.898002011Z
Updated At: 2026-02-14T13:26:34.865709747Z
```
- exit_code: 0

### Start Sandbox
- UTC: 2026-02-14T13:26:34Z
```bash
agentlab sandbox start 1065
```
```
sandbox 1065 started (state=RUNNING)
```
- exit_code: 0

### Show Sandbox After Start
- UTC: 2026-02-14T13:26:35Z
```bash
agentlab sandbox show 1065
```
```
VMID: 1065
Name: live-lifecycle-132617
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:11:17.898002011Z
Last Used At: 2026-02-14T13:26:35.791933388Z
Created At: 2026-02-14T13:26:17.898002011Z
Updated At: 2026-02-14T13:26:35.791934998Z
```
- exit_code: 0

### Renew Lease
- UTC: 2026-02-14T13:26:35Z
```bash
agentlab sandbox lease renew --ttl 90m 1065
```
```
error: cannot renew lease in RUNNING state. Valid states: RUNNING
next: agentlab sandbox --help
```
- exit_code: 1

### Tail Logs
- UTC: 2026-02-14T13:26:35Z
```bash
agentlab logs 1065 --tail 40
```
```
2026-02-14T13:26:18.314544293Z	sandbox.state	job=-	REQUESTED -> PROVISIONING
2026-02-14T13:26:20.503732713Z	sandbox.state	job=-	PROVISIONING -> BOOTING
2026-02-14T13:26:30.563758217Z	sandbox.state	job=-	BOOTING -> READY
2026-02-14T13:26:30.564908048Z	sandbox.slo.ready	job=-	ready in 12.666861123s
2026-02-14T13:26:30.567156261Z	sandbox.state	job=-	READY -> RUNNING
2026-02-14T13:26:31.839016823Z	sandbox.snapshot.created	job=-	snapshot clean created
2026-02-14T13:26:32.352729979Z	sandbox.state	job=-	RUNNING -> SUSPENDED
2026-02-14T13:26:32.354167496Z	sandbox.pause.completed	job=-	pause completed in 465.214754ms
2026-02-14T13:26:32.751502753Z	sandbox.slo.ssh_ready	job=-	ssh ready in 14.853483458s
2026-02-14T13:26:32.765880472Z	sandbox.state	job=-	SUSPENDED -> READY
2026-02-14T13:26:32.767076348Z	sandbox.resume.completed	job=-	resume completed in 392.60119ms
2026-02-14T13:26:34.854538503Z	sandbox.state	job=-	READY -> STOPPED
2026-02-14T13:26:34.855777588Z	sandbox.stop.completed	job=-	stop completed in 546.475387ms
2026-02-14T13:26:35.774627387Z	sandbox.state	job=-	STOPPED -> BOOTING
2026-02-14T13:26:35.777140936Z	sandbox.state	job=-	BOOTING -> READY
2026-02-14T13:26:35.77959389Z	sandbox.state	job=-	READY -> RUNNING
2026-02-14T13:26:35.780746562Z	sandbox.start.completed	job=-	start completed in 904.854815ms
```
- exit_code: 0

### Destroy Sandbox
- UTC: 2026-02-14T13:26:35Z
```bash
agentlab sandbox destroy --force 1065
```
```
sandbox 1065 destroyed (state=DESTROYED)
```
- exit_code: 0

### Show Sandbox After Destroy
- UTC: 2026-02-14T13:26:36Z
```bash
agentlab sandbox show 1065
```
```
VMID: 1065
Name: live-lifecycle-132617
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:11:17.898002011Z
Last Used At: 2026-02-14T13:26:36.779135492Z
Created At: 2026-02-14T13:26:17.898002011Z
Updated At: 2026-02-14T13:26:36.779136413Z
```
- exit_code: 0

## Lease Renew Focus Test: lease-renew-132658
### Create Sandbox
- UTC: 2026-02-14T13:27:12Z
```bash
agentlab --json sandbox new --profile yolo-ephemeral --name lease-renew-132658 --ttl 30m
```
```
{
  "vmid": 1066,
  "name": "lease-renew-132658",
  "profile": "yolo-ephemeral",
  "state": "RUNNING",
  "ip": "10.77.0.177",
  "network": {
    "mode": "nat"
  },
  "keepalive": false,
  "lease_expires_at": "2026-02-14T13:56:58.666838186Z",
  "created_at": "2026-02-14T13:26:58.666838186Z",
  "updated_at": "2026-02-14T13:27:10.889917143Z"
}
```
- exit_code: 0
- vmid: 1066

### Show Before Renew
- UTC: 2026-02-14T13:27:12Z
```bash
agentlab sandbox show 1066
```
```
VMID: 1066
Name: lease-renew-132658
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T13:56:58.666838186Z
Last Used At: 2026-02-14T13:27:12.190730273Z
Created At: 2026-02-14T13:26:58.666838186Z
Updated At: 2026-02-14T13:27:12.190736056Z
```
- exit_code: 0

### Lease Renew Attempt
- UTC: 2026-02-14T13:27:12Z
```bash
agentlab sandbox lease renew --ttl 120m 1066
```
```
error: cannot renew lease in RUNNING state. Valid states: RUNNING
next: agentlab sandbox --help
```
- exit_code: 1

### Show After Renew Attempt
- UTC: 2026-02-14T13:27:12Z
```bash
agentlab sandbox show 1066
```
```
VMID: 1066
Name: lease-renew-132658
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T13:56:58.666838186Z
Last Used At: 2026-02-14T13:27:12.213551652Z
Created At: 2026-02-14T13:26:58.666838186Z
Updated At: 2026-02-14T13:27:12.213553638Z
```
- exit_code: 0

### Destroy Lease Test Sandbox
- UTC: 2026-02-14T13:27:13Z
```bash
agentlab sandbox destroy --force 1066
```
```
sandbox 1066 destroyed (state=DESTROYED)
```
- exit_code: 0

### Show After Destroy (Lease Test)
- UTC: 2026-02-14T13:27:13Z
```bash
agentlab sandbox show 1066
```
```
VMID: 1066
Name: lease-renew-132658
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T13:56:58.666838186Z
Last Used At: 2026-02-14T13:27:13.249164409Z
Created At: 2026-02-14T13:26:58.666838186Z
Updated At: 2026-02-14T13:27:13.24916574Z
```
- exit_code: 0

## Summary
- UTC: 2026-02-14T13:27:30Z
- Tested with installed binaries:
  - `agentlab`: `v0.1.4-dirty` (commit `5843248`)
  - `agentlabd`: `v0.1.4-dirty` (commit `5843248`)

### Passed Operations
- Sandbox create (`sandbox new`)
- Read state/details (`sandbox show`, `logs`)
- Pause/resume (`sandbox pause`, `sandbox resume`)
- Snapshot save/list (`sandbox snapshot save`, `sandbox snapshot list`)
- Stop/start (`sandbox stop`, `sandbox start`)
- Destroy (`sandbox destroy --force`)

### Reproducible Failure
- Lease renew failed in two live attempts:
  - `agentlab sandbox lease renew --ttl 90m 1065`
  - `agentlab sandbox lease renew --ttl 120m 1066`
- Returned error each time:
  - `cannot renew lease in RUNNING state. Valid states: RUNNING`
- This appears to be a validation/message bug in lease-renew path.

### Environment End State
- Test sandboxes `1065` and `1066` destroyed.
- No temporary repo server left running on port `18080`.
- Pre-existing workload unaffected: only original long-running job remains (`job_f5b9ff873fddc66f` on sandbox `1056`).

## Lease Renew Bug Fix Validation
- UTC: 2026-02-14T13:39:08Z

### Post-Fix Versions
- UTC: 2026-02-14T13:39:08Z
```bash
agentlab --version && /usr/local/bin/agentlabd --version && systemctl is-active agentlabd.service
```
```
version=v0.1.4-dirty commit=5843248 date=2026-02-14T13:38:52Z
version=v0.1.4-dirty commit=5843248 date=2026-02-14T13:38:53Z
active
```
- exit_code: 0

### Create Lease-Fix Sandbox
- UTC: 2026-02-14T13:39:22Z
```bash
agentlab --json sandbox new --profile yolo-ephemeral --name lease-fix-133908 --ttl 25m
```
```
{
  "vmid": 1067,
  "name": "lease-fix-133908",
  "profile": "yolo-ephemeral",
  "state": "RUNNING",
  "ip": "10.77.0.177",
  "network": {
    "mode": "nat"
  },
  "keepalive": false,
  "lease_expires_at": "2026-02-14T14:04:08.454640181Z",
  "created_at": "2026-02-14T13:39:08.454640181Z",
  "updated_at": "2026-02-14T13:39:21.205749624Z"
}
```
- exit_code: 0
- vmid: 1067

### Show Before Renew (RUNNING)
- UTC: 2026-02-14T13:39:22Z
```bash
agentlab sandbox show 1067
```
```
VMID: 1067
Name: lease-fix-133908
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:04:08.454640181Z
Last Used At: 2026-02-14T13:39:22.078439649Z
Created At: 2026-02-14T13:39:08.454640181Z
Updated At: 2026-02-14T13:39:22.078442165Z
```
- exit_code: 0

### Lease Renew In RUNNING (Expected Success)
- UTC: 2026-02-14T13:39:22Z
```bash
agentlab sandbox lease renew --ttl 120m 1067
```
```
sandbox 1067 lease renewed until 2026-02-14T15:39:22.090805144Z
```
- exit_code: 0

### Show After Renew
- UTC: 2026-02-14T13:39:22Z
```bash
agentlab sandbox show 1067
```
```
VMID: 1067
Name: lease-fix-133908
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T15:39:22.090805144Z
Last Used At: 2026-02-14T13:39:22.102957252Z
Created At: 2026-02-14T13:39:08.454640181Z
Updated At: 2026-02-14T13:39:22.102959256Z
```
- exit_code: 0

### Stop Sandbox For Negative Test
- UTC: 2026-02-14T13:39:22Z
```bash
agentlab sandbox stop 1067
```
```
sandbox 1067 stopped (state=STOPPED)
```
- exit_code: 0

### Lease Renew In STOPPED (Expected Conflict)
- UTC: 2026-02-14T13:39:22Z
```bash
agentlab sandbox lease renew --ttl 30m 1067
```
```
error: cannot renew lease in STOPPED state. Valid states: RUNNING
next: agentlab sandbox --help
```
- exit_code: 1

### Destroy Lease-Fix Sandbox
- UTC: 2026-02-14T13:39:23Z
```bash
agentlab sandbox destroy --force 1067
```
```
sandbox 1067 destroyed (state=DESTROYED)
```
- exit_code: 0

### Show After Destroy (Lease-Fix)
- UTC: 2026-02-14T13:39:23Z
```bash
agentlab sandbox show 1067
```
```
VMID: 1067
Name: lease-fix-133908
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T15:39:22.090805144Z
Last Used At: 2026-02-14T13:39:23.622970192Z
Created At: 2026-02-14T13:39:08.454640181Z
Updated At: 2026-02-14T13:39:23.622971611Z
```
- exit_code: 0

## Post-Fix Runtime Check (Sandbox 1053)
- UTC: 2026-02-14T13:44:00Z

### Sandbox 1053 Health Check
- UTC: 2026-02-14T13:44:00Z
```bash
agentlab sandbox show 1053
```
```
VMID: 1053
Name: openclaw
Profile: openclaw
State: RUNNING
IP: 10.77.0.195
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: true
Lease Expires: 2026-03-10T16:36:17.738975361Z
Last Used At: 2026-02-14T13:39:54.911412154Z
Created At: 2026-02-08T16:36:17.738975361Z
Updated At: 2026-02-14T13:39:54.911413897Z
```
- exit_code: 0

### Overall Agentlab Status Snapshot
- UTC: 2026-02-14T13:44:00Z
```bash
agentlab status
```
```
Sandboxes:
- RUNNING: 3
- DESTROYED: 64

Jobs:
- RUNNING: 1
- COMPLETED: 2
- FAILED: 7
```
- exit_code: 0

## Extended Live Tests Session
- UTC Start: 2026-02-14T13:52:00Z
- Goal: cover race handling, network policies, snapshot correctness, SSH reliability, daemon restart resilience, scale burst, and isolation.
- Guardrails: only use dedicated test VMIDs >= 1070; do not modify existing running sandboxes 1053/1055/1056.

### Baseline Timestamp
- UTC: 2026-02-14T13:50:39Z


- exit_code: 

### Agentlab Version
- UTC: 2026-02-14T13:50:39Z


- exit_code: 

### Agentlab Status
- UTC: 2026-02-14T13:50:39Z


- exit_code: 

### Profile List
- UTC: 2026-02-14T13:50:39Z


- exit_code: 

### Sandbox List (Head)
- UTC: 2026-02-14T13:50:39Z


- exit_code: 

### Baseline Timestamp
- UTC: 2026-02-14T13:50:51Z
```bash
date -u +%Y-%m-%dT%H:%M:%SZ
```
```

### Agentlab Version
- UTC: 2026-02-14T13:50:51Z
```bash
agentlab --version
```
```

### Agentlab Status
- UTC: 2026-02-14T13:50:51Z
```bash
agentlab status
```
```
2026-02-14T13:50:51Z
```
- exit_code: 0

### Profile List
- UTC: 2026-02-14T13:50:51Z
```bash
agentlab profile list
```
```
version=v0.1.4-dirty commit=5843248 date=2026-02-14T13:38:52Z
```
- exit_code: 0
Sandboxes:
STATE         COUNT
REQUESTED     0
PROVISIONING  0
BOOTING       0
READY         0
RUNNING       3
SUSPENDED     0
COMPLETED     0
FAILED        0
TIMEOUT       0
STOPPED       0
DESTROYED     64
Network Modes:
MODE       COUNT
off        0
nat        67
allowlist  0
Jobs:
STATUS     COUNT
QUEUED     0
RUNNING    1
COMPLETED  2
FAILED     7NAME
        TIMEOUT         TEMPLATE0  
UPDATEDArtifacts:

interactive-dev  Root: /var/lib/agentlab/artifacts
9001      Total Bytes: 944350560256
2026-02-14T13:07:59.217301177ZFree Bytes: 933755092992

Used Bytes: 10595467264
minimalError: -
        Metrics:
  9000Enabled: false
      Recent Failures:
2026-01-30T19:50:28.666225086Z
TIMEopenclaw                                 9999          KIND2026-02-07T21:01:22.600832991Z        
JOBubuntu-24-04-ai                  9999         SANDBOX2026-02-06T18:26:55.84754418Z  
MESSAGEyolo-ephemeral
   2026-01-31T20:05:02.528359152Z9001        job.failed2026-02-14T13:07:59.217301177Z  
job_e7deaa1bed9b96d4yolo-workspace     -9001              template validation failed: failed to query template VM 9000: command qm config 9000 failed: exit status 255: ipcc_send_rec[1] failed: Unknown error -12026-02-14T13:07:59.217301177Z

ipcc_send_rec[2] failed: Unknown error -1
ipcc_send_rec[3] failed: Unknown error -1
Unable to load access control list: Unknown error -1
2026-02-14T12:27:19.51578206Z   job.failed  job_51d87ecf0f92ecfb  -     template validation failed: request failed: Get "https://localhost:8006/api2/json/nodes": tls: failed to verify certificate: x509: certificate signed by unknown authority
2026-02-14T12:27:43.687921825Z  job.failed  job_1806a6c5b9473c43  1057  API error (status 400): invalid format - format error
net0.fwgroup: property is not defined in schema and the schema does not allow additional properties
2026-02-14T12:31:06.186773307Z  job.failed  job_56344d760f7842a8  -  template validation failed: failed to query template VM 9000: command qm config 9000 failed: exit status 255: ipcc_send_rec[1] failed: Unknown error -1
ipcc_send_rec[2] failed: Unknown error -1
ipcc_send_rec[3] failed: Unknown error -1
Unable to load access control list: Unknown error -1
2026-02-14T13:05:33.989Z        job.failed              job_8b5ed80a94e47385  1058  job cleanup: sandbox already DESTROYED while job stayed RUNNING
2026-02-14T13:05:33.989Z        job.failed              job_f80051b2f4ed82b6  1060  job cleanup: sandbox already DESTROYED while job stayed RUNNING
2026-02-14T13:19:00.892Z        job.failed              job_9ee731bf095808d8  1061  job cleanup: sandbox already DESTROYED while job stayed RUNNING
2026-02-14T13:19:00.892Z        job.failed              job_943ad77e343b497e  1062  job cleanup: sandbox already DESTROYED while job stayed RUNNING
2026-02-14T13:30:10.891007531Z  sandbox.slo.ssh_failed  -                     1066  ```
ssh not ready after 3m12.224151736s: context deadline exceeded
2026-02-14T13:42:21.202062918Z  - exit_code: 0
sandbox.slo.ssh_failed  -                     1067  ssh not ready after 3m12.747379318s: context deadline exceeded
```
- exit_code: 0

### Sandbox List (Head)
- UTC: 2026-02-14T13:50:53Z
```bash
agentlab sandbox list | sed -n '1,25p'
```
```
VMID  NAME                             PROFILE          STATE      IP           MODE  FWGROUP  LEASE                           LAST USED
1067  lease-fix-133908                 yolo-ephemeral   DESTROYED  10.77.0.177  nat   -        2026-02-14T15:39:22.090805144Z  2026-02-14T13:39:27.736964914Z
1066  lease-renew-132658               yolo-ephemeral   DESTROYED  10.77.0.177  nat   -        2026-02-14T13:56:58.666838186Z  2026-02-14T13:27:13.249164409Z
1065  live-lifecycle-132617            yolo-ephemeral   DESTROYED  10.77.0.177  nat   -        2026-02-14T14:11:17.898002011Z  2026-02-14T13:26:41.929849514Z
1064  sandbox-1064                     yolo-ephemeral   DESTROYED  10.77.0.177  nat   -        2026-02-14T16:18:25.248764329Z  -
1063  sandbox-1063                     interactive-dev  DESTROYED  10.77.0.177  nat   -        2026-02-15T01:14:05.496179652Z  2026-02-14T13:18:19.241138379Z
1062  sandbox-1062                     interactive-dev  DESTROYED  10.77.0.150  nat   -        2026-02-15T01:11:26.070816194Z  2026-02-14T13:13:17.071814776Z
1061  sandbox-1061                     interactive-dev  DESTROYED  10.77.0.150  nat   -        2026-02-15T01:08:07.03400955Z   2026-02-14T13:11:21.426911463Z
1060  sandbox-1060                     interactive-dev  DESTROYED  -            nat   -        2026-02-15T00:31:59.900758468Z  2026-02-14T12:34:54.816006864Z
1059  tmp-check                        interactive-dev  DESTROYED  -            nat   -        2026-02-14T13:01:30.96025769Z   -
1058  sandbox-1058                     openclaw         DESTROYED  10.77.0.149  nat   -        2026-03-16T12:28:01.133599326Z  2026-02-14T12:34:54.785136432Z
1057  sandbox-1057                     interactive-dev  DESTROYED  -            nat   -        2026-02-15T00:27:42.142925675Z  2026-02-14T12:27:47.248962793Z
1056  sandbox-1056                     openclaw         RUNNING    10.77.0.145  nat   -        2026-03-12T02:47:28.125600381Z  -
1055  claude-code-vm                   ubuntu-24-04-ai  RUNNING    10.77.0.152  nat   -        2026-03-11T06:11:58.662121406Z  -
1053  openclaw                         openclaw         RUNNING    10.77.0.195  nat   -        2026-03-10T16:36:17.738975361Z  2026-02-14T13:39:54.911412154Z
1052  test-ttl-default                 openclaw         DESTROYED  10.77.0.119  nat   -        2026-03-10T13:50:13.510366851Z  -
1051  test-ttl-verify                  openclaw         DESTROYED  10.77.0.135  nat   -        2026-02-08T13:59:35.064981328Z  -
1050  openclaw                         openclaw         DESTROYED  10.77.0.115  nat   -        2026-03-10T13:51:44.831425564Z  -
1049  openclaw                         openclaw         DESTROYED  10.77.0.149  nat   -        2026-02-08T11:30:27.8294788Z    -
1048  openclaw                         openclaw         DESTROYED  10.77.0.180  nat   -        2026-02-08T01:01:40.175650731Z  -
1047  openclaw-gateway                 ubuntu-24-04-ai  DESTROYED  10.77.0.133  nat   -        2026-02-14T18:52:00.257712704Z  -
1046  openclaw-install                 ubuntu-24-04-ai  DESTROYED  10.77.0.174  nat   -        2026-02-07T17:57:59.850591774Z  -
1045  codex-openclaw-smoke-1770405611  openclaw         DESTROYED  10.77.0.190  nat   -        2026-02-06T23:20:11.606853582Z  -
1044  codex-smoke-1770405570           ubuntu-24-04-ai  DESTROYED  10.77.0.155  nat   -        2026-02-06T23:19:30.194506029Z  -
1043  codex-e2e3-1770405403            ubuntu-24-04-ai  DESTROYED  10.77.0.141  nat   -        2026-02-06T23:16:43.137249179Z  -
```
- exit_code: 0

## Stage 1: Job/Sandbox Race Handling
- UTC: 2026-02-14T13:51:31Z

### Start Job
- UTC: 2026-02-14T13:51:31Z
```bash
agentlab --json job run --repo https://github.com/octocat/Hello-World.git --task "run diagnostics slowly and keep working" --profile yolo-ephemeral --ttl 90m
```
```
{
  "id": "job_e3bedcaa758dbb57",
  "repo_url": "https://github.com/octocat/Hello-World.git",
  "ref": "main",
  "profile": "yolo-ephemeral",
  "task": "run diagnostics slowly and keep working",
  "mode": "dangerous",
  "ttl_minutes": 90,
  "keepalive": false,
  "status": "QUEUED",
  "created_at": "2026-02-14T13:51:31.247897719Z",
  "updated_at": "2026-02-14T13:51:31.247897719Z"
}
```
- exit_code: 0

### Poll Job For Sandbox Assignment
- UTC: 2026-02-14T13:51:33Z
- job_id: job_e3bedcaa758dbb57
- sandbox_vmid: 1068
- last_status: QUEUED

### Force Destroy Assigned Sandbox During Job
- UTC: 2026-02-14T13:51:33Z
```bash
agentlab sandbox destroy --force 1068
```
```
sandbox 1068 destroyed (state=DESTROYED)
```
- exit_code: 0

### Final Job State After Forced Sandbox Destroy
- UTC: 2026-02-14T13:51:36Z
```bash
agentlab --json job show job_e3bedcaa758dbb57
```
```
{
  "id": "job_e3bedcaa758dbb57",
  "repo_url": "https://github.com/octocat/Hello-World.git",
  "ref": "main",
  "profile": "yolo-ephemeral",
  "task": "run diagnostics slowly and keep working",
  "mode": "dangerous",
  "ttl_minutes": 90,
  "keepalive": false,
  "status": "FAILED",
  "sandbox_vmid": 1068,
  "result": {
    "status": "FAILED",
    "message": "command qm start 1068 failed: exit status 255: Configuration file 'nodes/pve/qemu-server/1068.conf' does not exist",
    "reported_at": "2026-02-14T13:51:34.229142542Z"
  },
  "events": [
    {
      "id": 387,
      "ts": "2026-02-14T13:51:34.230855672Z",
      "kind": "job.failed",
      "sandbox_vmid": 1068,
      "job_id": "job_e3bedcaa758dbb57",
      "msg": "command qm start 1068 failed: exit status 255: Configuration file 'nodes/pve/qemu-server/1068.conf' does not exist"
    }
  ],
  "created_at": "2026-02-14T13:51:31.247897719Z",
  "updated_at": "2026-02-14T13:51:34.229182587Z"
}
```
- terminal_status: FAILED

## Stage 2: Network Mode Coverage (off/nat/allowlist)
- UTC: 2026-02-14T13:51:56Z

### Write Temporary off Profile
- UTC: 2026-02-14T13:51:56Z
```bash
cat > /etc/agentlab/profiles/live-net-off.yaml <<'YAML'\nname: live-net-off\ntemplate_vmid: 9001\nnetwork:\n  bridge: vmbr1\n  model: virtio\n  mode: off\n  firewall_group: agent_nat_off\nresources:\n  cores: 2\n  memory_mb: 4096\nstorage:\n  root_size_gb: 20\nbehavior:\n  mode: dangerous\n  keepalive_default: false\n  ttl_minutes_default: 120\nYAML
```
```
bash: line 1: warning: here-document at line 1 delimited by end-of-file (wanted `YAMLnname:')
cat: 'live-net-offntemplate_vmid:': No such file or directory
cat: '9001nnetwork:n': No such file or directory
cat: 'bridge:': No such file or directory
cat: vmbr1n: No such file or directory
cat: 'model:': No such file or directory
cat: virtion: No such file or directory
cat: 'mode:': No such file or directory
cat: offn: No such file or directory
cat: 'firewall_group:': No such file or directory
cat: 'agent_nat_offnresources:n': No such file or directory
cat: 'cores:': No such file or directory
cat: 2n: No such file or directory
cat: 'memory_mb:': No such file or directory
cat: '4096nstorage:n': No such file or directory
cat: 'root_size_gb:': No such file or directory
cat: '20nbehavior:n': No such file or directory
cat: 'mode:': No such file or directory
cat: dangerousn: No such file or directory
cat: 'keepalive_default:': No such file or directory
cat: falsen: No such file or directory
cat: 'ttl_minutes_default:': No such file or directory
cat: 120nYAML: No such file or directory
```
- exit_code: 1

### Write Temporary allowlist Profile
- UTC: 2026-02-14T13:51:56Z
```bash
cat > /etc/agentlab/profiles/live-net-allowlist.yaml <<'YAML'\nname: live-net-allowlist\ntemplate_vmid: 9001\nnetwork:\n  bridge: vmbr1\n  model: virtio\n  mode: allowlist\n  firewall_group: agent_nat_allowlist\nresources:\n  cores: 2\n  memory_mb: 4096\nstorage:\n  root_size_gb: 20\nbehavior:\n  mode: dangerous\n  keepalive_default: false\n  ttl_minutes_default: 120\nYAML
```
```
bash: line 1: warning: here-document at line 1 delimited by end-of-file (wanted `YAMLnname:')
cat: 'live-net-allowlistntemplate_vmid:': No such file or directory
cat: '9001nnetwork:n': No such file or directory
cat: 'bridge:': No such file or directory
cat: vmbr1n: No such file or directory
cat: 'model:': No such file or directory
cat: virtion: No such file or directory
cat: 'mode:': No such file or directory
cat: allowlistn: No such file or directory
cat: 'firewall_group:': No such file or directory
cat: 'agent_nat_allowlistnresources:n': No such file or directory
cat: 'cores:': No such file or directory
cat: 2n: No such file or directory
cat: 'memory_mb:': No such file or directory
cat: '4096nstorage:n': No such file or directory
cat: 'root_size_gb:': No such file or directory
cat: '20nbehavior:n': No such file or directory
cat: 'mode:': No such file or directory
cat: dangerousn: No such file or directory
cat: 'keepalive_default:': No such file or directory
cat: falsen: No such file or directory
cat: 'ttl_minutes_default:': No such file or directory
cat: 120nYAML: No such file or directory
```
- exit_code: 1

### Restart Daemon To Reload Profiles
- UTC: 2026-02-14T13:51:56Z
```bash
systemctl restart agentlabd.service && systemctl is-active agentlabd.service
```
```
active
```
- exit_code: 0

### Profile List After Adding Net Profiles
- UTC: 2026-02-14T13:51:57Z
```bash
agentlab profile list
```
```
NAME             TEMPLATE  UPDATED
interactive-dev  9001      2026-02-14T13:07:59.217301177Z
minimal          9000      2026-01-30T19:50:28.666225086Z
openclaw         9999      2026-02-07T21:01:22.600832991Z
ubuntu-24-04-ai  9999      2026-02-06T18:26:55.84754418Z
yolo-ephemeral   9001      2026-02-14T13:07:59.217301177Z
yolo-workspace   9001      2026-02-14T13:07:59.217301177Z
```
- exit_code: 0

### Create NAT Sandbox
- UTC: 2026-02-14T13:51:58Z
```bash
agentlab sandbox new --profile yolo-ephemeral --name net-nat-145158 --vmid 1071 --ttl 60m
```
```
VMID: 1071
Name: net-nat-145158
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:51:58.105328143Z
Last Used At: -
Created At: 2026-02-14T13:51:58.105328143Z
Updated At: 2026-02-14T13:52:10.840802996Z
```
- exit_code: 0

### Create OFF Sandbox
- UTC: 2026-02-14T13:52:11Z
```bash
agentlab sandbox new --profile live-net-off --name net-off-145211 --vmid 1072 --ttl 60m
```
```
error: unknown profile "live-net-off". Available profiles: interactive-dev, minimal, openclaw, ubuntu-24-04-ai, yolo-ephemeral, yolo-workspace
next: agentlab profile list
```
- exit_code: 1

### Create ALLOWLIST Sandbox
- UTC: 2026-02-14T13:52:11Z
```bash
agentlab sandbox new --profile live-net-allowlist --name net-allow-145211 --vmid 1073 --ttl 60m
```
```
error: unknown profile "live-net-allowlist". Available profiles: interactive-dev, minimal, openclaw, ubuntu-24-04-ai, yolo-ephemeral, yolo-workspace
next: agentlab profile list
```
- exit_code: 1

### Show NAT Sandbox
- UTC: 2026-02-14T13:52:11Z
```bash
agentlab sandbox show 1071
```
```
VMID: 1071
Name: net-nat-145158
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:51:58.105328143Z
Last Used At: 2026-02-14T13:52:11.865229159Z
Created At: 2026-02-14T13:51:58.105328143Z
Updated At: 2026-02-14T13:52:11.865231206Z
```
- exit_code: 0

### Show OFF Sandbox
- UTC: 2026-02-14T13:52:11Z
```bash
agentlab sandbox show 1072
```
```
error: sandbox 1072 not found
next: agentlab sandbox list
hint: closest VMIDs: 1071, 1068, 1067
```
- exit_code: 1

### Show ALLOWLIST Sandbox
- UTC: 2026-02-14T13:52:11Z
```bash
agentlab sandbox show 1073
```
```
error: sandbox 1073 not found
next: agentlab sandbox list
hint: closest VMIDs: 1071, 1068, 1067
```
- exit_code: 1

### Destroy NAT/OFF/ALLOWLIST Sandboxes
- UTC: 2026-02-14T13:52:11Z
```bash
agentlab sandbox destroy --force 1071; agentlab sandbox destroy --force 1072; agentlab sandbox destroy --force 1073
```
```
sandbox 1071 destroyed (state=DESTROYED)
error: sandbox 1072 not found
next: agentlab sandbox list
hint: closest VMIDs: 1071, 1068, 1067
error: sandbox 1073 not found
next: agentlab sandbox list
hint: closest VMIDs: 1071, 1068, 1067
```
- exit_code: 1

### Remove Temporary Net Profiles
- UTC: 2026-02-14T13:52:12Z
```bash
rm -f /etc/agentlab/profiles/live-net-off.yaml /etc/agentlab/profiles/live-net-allowlist.yaml
```
```
```
- exit_code: 0

### Restart Daemon After Profile Cleanup
- UTC: 2026-02-14T13:52:12Z
```bash
systemctl restart agentlabd.service && systemctl is-active agentlabd.service
```
```
active
```
- exit_code: 0

### Profile List After Cleanup
- UTC: 2026-02-14T13:52:12Z
```bash
agentlab profile list
```
```
NAME             TEMPLATE  UPDATED
interactive-dev  9001      2026-02-14T13:07:59.217301177Z
minimal          9000      2026-01-30T19:50:28.666225086Z
openclaw         9999      2026-02-07T21:01:22.600832991Z
ubuntu-24-04-ai  9999      2026-02-06T18:26:55.84754418Z
yolo-ephemeral   9001      2026-02-14T13:07:59.217301177Z
yolo-workspace   9001      2026-02-14T13:07:59.217301177Z
```
- exit_code: 0

## Stage 2B: Network Mode Coverage Retry
- UTC: 2026-02-14T13:52:37Z

### Create live-net-off Profile
- UTC: 2026-02-14T13:52:37Z
```bash
cat > /etc/agentlab/profiles/live-net-off.yaml <<'YAML'
name: live-net-off
template_vmid: 9001
network:
  bridge: vmbr1
  model: virtio
  mode: off
  firewall_group: agent_nat_off
resources:
  cores: 2
  memory_mb: 4096
storage:
  root_size_gb: 20
behavior:
  mode: dangerous
  keepalive_default: false
  ttl_minutes_default: 120
YAML
```
```
name: live-net-off
template_vmid: 9001
network:
  bridge: vmbr1
  model: virtio
  mode: off
  firewall_group: agent_nat_off
resources:
  cores: 2
  memory_mb: 4096
storage:
  root_size_gb: 20
behavior:
  mode: dangerous
  keepalive_default: false
  ttl_minutes_default: 120
```
- exit_code: 0

### Create live-net-allowlist Profile
- UTC: 2026-02-14T13:52:37Z
```bash
cat > /etc/agentlab/profiles/live-net-allowlist.yaml <<'YAML'
name: live-net-allowlist
template_vmid: 9001
network:
  bridge: vmbr1
  model: virtio
  mode: allowlist
  firewall_group: agent_nat_allowlist
resources:
  cores: 2
  memory_mb: 4096
storage:
  root_size_gb: 20
behavior:
  mode: dangerous
  keepalive_default: false
  ttl_minutes_default: 120
YAML
```
```
name: live-net-allowlist
template_vmid: 9001
network:
  bridge: vmbr1
  model: virtio
  mode: allowlist
  firewall_group: agent_nat_allowlist
resources:
  cores: 2
  memory_mb: 4096
storage:
  root_size_gb: 20
behavior:
  mode: dangerous
  keepalive_default: false
  ttl_minutes_default: 120
```
- exit_code: 0

### Restart Daemon To Reload Net Profiles
- UTC: 2026-02-14T13:52:37Z
```bash
systemctl restart agentlabd.service && systemctl is-active agentlabd.service
```
```
active
```
- exit_code: 0

### Profile List With Net Profiles
- UTC: 2026-02-14T13:52:37Z
```bash
agentlab profile list
```
```
NAME             TEMPLATE  UPDATED
interactive-dev  9001      2026-02-14T13:07:59.217301177Z
minimal          9000      2026-01-30T19:50:28.666225086Z
openclaw         9999      2026-02-07T21:01:22.600832991Z
ubuntu-24-04-ai  9999      2026-02-06T18:26:55.84754418Z
yolo-ephemeral   9001      2026-02-14T13:07:59.217301177Z
yolo-workspace   9001      2026-02-14T13:07:59.217301177Z
```
- exit_code: 0

### Create OFF Sandbox (1072)
- UTC: 2026-02-14T13:52:38Z
```bash
agentlab sandbox new --profile live-net-off --name net-off-145238 --vmid 1072 --ttl 45m
```
```
error: unknown profile "live-net-off". Available profiles: interactive-dev, minimal, openclaw, ubuntu-24-04-ai, yolo-ephemeral, yolo-workspace
next: agentlab profile list
```
- exit_code: 1

### Create ALLOWLIST Sandbox (1073)
- UTC: 2026-02-14T13:52:38Z
```bash
agentlab sandbox new --profile live-net-allowlist --name net-allow-145238 --vmid 1073 --ttl 45m
```
```
error: unknown profile "live-net-allowlist". Available profiles: interactive-dev, minimal, openclaw, ubuntu-24-04-ai, yolo-ephemeral, yolo-workspace
next: agentlab profile list
```
- exit_code: 1

### Show OFF Sandbox (1072)
- UTC: 2026-02-14T13:52:38Z
```bash
agentlab sandbox show 1072
```
```
error: sandbox 1072 not found
next: agentlab sandbox list
hint: closest VMIDs: 1071, 1068, 1067
```
- exit_code: 1

### Show ALLOWLIST Sandbox (1073)
- UTC: 2026-02-14T13:52:38Z
```bash
agentlab sandbox show 1073
```
```
error: sandbox 1073 not found
next: agentlab sandbox list
hint: closest VMIDs: 1071, 1068, 1067
```
- exit_code: 1

### Destroy OFF/ALLOWLIST Sandboxes
- UTC: 2026-02-14T13:52:38Z
```bash
agentlab sandbox destroy --force 1072; agentlab sandbox destroy --force 1073
```
```
error: sandbox 1072 not found
next: agentlab sandbox list
hint: closest VMIDs: 1071, 1068, 1067
error: sandbox 1073 not found
next: agentlab sandbox list
hint: closest VMIDs: 1071, 1068, 1067
```
- exit_code: 1

### Remove Net Test Profiles
- UTC: 2026-02-14T13:52:38Z
```bash
rm -f /etc/agentlab/profiles/live-net-off.yaml /etc/agentlab/profiles/live-net-allowlist.yaml
```
```
```
- exit_code: 0

### Restart Daemon After Net Test Cleanup
- UTC: 2026-02-14T13:52:38Z
```bash
systemctl restart agentlabd.service && systemctl is-active agentlabd.service
```
```
active
```
- exit_code: 0

### Profile List After Net Test Cleanup
- UTC: 2026-02-14T13:52:39Z
```bash
agentlab profile list
```
```
NAME             TEMPLATE  UPDATED
interactive-dev  9001      2026-02-14T13:07:59.217301177Z
minimal          9000      2026-01-30T19:50:28.666225086Z
openclaw         9999      2026-02-07T21:01:22.600832991Z
ubuntu-24-04-ai  9999      2026-02-06T18:26:55.84754418Z
yolo-ephemeral   9001      2026-02-14T13:07:59.217301177Z
yolo-workspace   9001      2026-02-14T13:07:59.217301177Z
```
- exit_code: 0

## Stage 2C: Network Mode Coverage (Final Corrected)
- UTC: 2026-02-14T13:53:36Z

### Create live-net-off Profile File
- UTC: 2026-02-14T13:53:36Z
```bash
cat > /etc/agentlab/profiles/live-net-off.yaml <<'YAML' ... YAML
```
```
name: live-net-off
template_vmid: 9001
network:
  bridge: vmbr1
  model: virtio
  mode: off
  firewall_group: agent_nat_off
resources:
  cores: 2
  memory_mb: 4096
storage:
  root_size_gb: 20
behavior:
  mode: dangerous
  keepalive_default: false
  ttl_minutes_default: 120
```
- exit_code: 0

### Create live-net-allowlist Profile File
- UTC: 2026-02-14T13:53:36Z
```bash
cat > /etc/agentlab/profiles/live-net-allowlist.yaml <<'YAML' ... YAML
```
```
name: live-net-allowlist
template_vmid: 9001
network:
  bridge: vmbr1
  model: virtio
  mode: allowlist
  firewall_group: agent_nat_allowlist
resources:
  cores: 2
  memory_mb: 4096
storage:
  root_size_gb: 20
behavior:
  mode: dangerous
  keepalive_default: false
  ttl_minutes_default: 120
```
- exit_code: 0

### Verify Test Profiles Exist On Disk
- UTC: 2026-02-14T13:53:36Z
```bash
ls -la /etc/agentlab/profiles/live-net-*.yaml
```
```
-rw-r--r-- 1 root root 292 Feb 14 14:53 /etc/agentlab/profiles/live-net-allowlist.yaml
-rw-r--r-- 1 root root 274 Feb 14 14:53 /etc/agentlab/profiles/live-net-off.yaml
```
- exit_code: 0

### Restart Daemon To Load 8 Profiles
- UTC: 2026-02-14T13:53:36Z
```bash
systemctl restart agentlabd.service && systemctl is-active agentlabd.service
```
```
active
```
- exit_code: 0

### Daemon Log Loaded Profile Count
- UTC: 2026-02-14T13:53:37Z
```bash
tail -n 20 /var/log/agentlab/agentlabd.log | grep 'loaded .* profiles' | tail -n 1
```
```
2026/02/14 14:53:37 agentlabd: loaded 8 profiles from /etc/agentlab/profiles
```
- exit_code: 0

### Profile List With Net Profiles (Expect 8)
- UTC: 2026-02-14T13:53:37Z
```bash
agentlab profile list
```
```
NAME                TEMPLATE  UPDATED
interactive-dev     9001      2026-02-14T13:07:59.217301177Z
live-net-allowlist  9001      2026-02-14T13:53:36.989618131Z
live-net-off        9001      2026-02-14T13:53:36.986617918Z
minimal             9000      2026-01-30T19:50:28.666225086Z
openclaw            9999      2026-02-07T21:01:22.600832991Z
ubuntu-24-04-ai     9999      2026-02-06T18:26:55.84754418Z
yolo-ephemeral      9001      2026-02-14T13:07:59.217301177Z
yolo-workspace      9001      2026-02-14T13:07:59.217301177Z
```
- exit_code: 0

### Create OFF Sandbox (1072)
- UTC: 2026-02-14T13:53:38Z
```bash
agentlab sandbox new --profile live-net-off --name net-off-145338 --vmid 1072 --ttl 40m
```
```
error: failed to provision sandbox
next: agentlab sandbox --help
```
- exit_code: 1

### Create ALLOWLIST Sandbox (1073)
- UTC: 2026-02-14T13:53:41Z
```bash
agentlab sandbox new --profile live-net-allowlist --name net-allow-145341 --vmid 1073 --ttl 40m
```
```
error: failed to provision sandbox
next: agentlab sandbox --help
```
- exit_code: 1

### Show OFF Sandbox (1072)
- UTC: 2026-02-14T13:53:44Z
```bash
agentlab sandbox show 1072
```
```
VMID: 1072
Name: net-off-145338
Profile: live-net-off
State: DESTROYED
IP: -
Workspace: -
Network Mode: off
Firewall: true
Firewall Group: agent_nat_off
Keepalive: false
Lease Expires: 2026-02-14T14:33:38.119249542Z
Last Used At: 2026-02-14T13:53:44.080802772Z
Created At: 2026-02-14T13:53:38.119249542Z
Updated At: 2026-02-14T13:53:44.080803805Z
```
- exit_code: 0

### Show ALLOWLIST Sandbox (1073)
- UTC: 2026-02-14T13:53:44Z
```bash
agentlab sandbox show 1073
```
```
VMID: 1073
Name: net-allow-145341
Profile: live-net-allowlist
State: DESTROYED
IP: -
Workspace: -
Network Mode: allowlist
Firewall: true
Firewall Group: agent_nat_allowlist
Keepalive: false
Lease Expires: 2026-02-14T14:33:41.117977164Z
Last Used At: 2026-02-14T13:53:44.090843531Z
Created At: 2026-02-14T13:53:41.117977164Z
Updated At: 2026-02-14T13:53:44.090844629Z
```
- exit_code: 0

### Destroy OFF/ALLOWLIST Sandboxes
- UTC: 2026-02-14T13:53:44Z
```bash
agentlab sandbox destroy --force 1072; agentlab sandbox destroy --force 1073
```
```
sandbox 1072 destroyed (state=DESTROYED)
sandbox 1073 destroyed (state=DESTROYED)
```
- exit_code: 0

### Remove Net Test Profiles
- UTC: 2026-02-14T13:53:44Z
```bash
rm -f /etc/agentlab/profiles/live-net-off.yaml /etc/agentlab/profiles/live-net-allowlist.yaml
```
```
```
- exit_code: 0

### Restart Daemon After Net Cleanup
- UTC: 2026-02-14T13:53:44Z
```bash
systemctl restart agentlabd.service && systemctl is-active agentlabd.service
```
```
active
```
- exit_code: 0

### Profile List After Net Cleanup
- UTC: 2026-02-14T13:53:44Z
```bash
agentlab profile list
```
```
NAME             TEMPLATE  UPDATED
interactive-dev  9001      2026-02-14T13:07:59.217301177Z
minimal          9000      2026-01-30T19:50:28.666225086Z
openclaw         9999      2026-02-07T21:01:22.600832991Z
ubuntu-24-04-ai  9999      2026-02-06T18:26:55.84754418Z
yolo-ephemeral   9001      2026-02-14T13:07:59.217301177Z
yolo-workspace   9001      2026-02-14T13:07:59.217301177Z
```
- exit_code: 0

## Stage 3: Snapshot Restore Correctness
- UTC: 2026-02-14T13:54:14Z

### Create Snapshot Test Sandbox (1074)
- UTC: 2026-02-14T13:54:14Z
```bash
agentlab sandbox new --profile yolo-ephemeral --name snap-test-145414 --vmid 1074 --ttl 60m
```
```
VMID: 1074
Name: snap-test-145414
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:54:14.061099019Z
Last Used At: -
Created At: 2026-02-14T13:54:14.061099019Z
Updated At: 2026-02-14T13:54:26.780201544Z
```
- exit_code: 0

### Write ORIGINAL Marker Over SSH
- UTC: 2026-02-14T13:54:28Z
```bash
agentlab ssh 1074 --wait -- bash -lc 'echo ORIGINAL > /tmp/al_state.txt; cat /tmp/al_state.txt'
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o BatchMode=yes -o ConnectTimeout=10 -o IdentitiesOnly=yes -i /etc/agentlab/keys/agentlab_id_ed25519 agent@10.77.0.177 bash -lc 'echo ORIGINAL > /tmp/al_state.txt; cat /tmp/al_state.txt'
```
- exit_code: 0

### Save Snapshot live-snap1
- UTC: 2026-02-14T13:54:28Z
```bash
agentlab sandbox snapshot save --force 1074 live-snap1
```
```
Sandbox: 1074
Snapshot: live-snap1
Created At: 2026-02-14T13:54:29.419125246Z
```
- exit_code: 0

### Mutate Marker To CHANGED
- UTC: 2026-02-14T13:54:29Z
```bash
agentlab ssh 1074 --wait -- bash -lc 'echo CHANGED > /tmp/al_state.txt; cat /tmp/al_state.txt'
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o BatchMode=yes -o ConnectTimeout=10 -o IdentitiesOnly=yes -i /etc/agentlab/keys/agentlab_id_ed25519 agent@10.77.0.177 bash -lc 'echo CHANGED > /tmp/al_state.txt; cat /tmp/al_state.txt'
```
- exit_code: 0

### Verify Marker Is CHANGED
- UTC: 2026-02-14T13:54:29Z
```bash
agentlab ssh 1074 --wait -- bash -lc 'cat /tmp/al_state.txt'
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o BatchMode=yes -o ConnectTimeout=10 -o IdentitiesOnly=yes -i /etc/agentlab/keys/agentlab_id_ed25519 agent@10.77.0.177 bash -lc 'cat /tmp/al_state.txt'
```
- exit_code: 0

### Restore Snapshot live-snap1
- UTC: 2026-02-14T13:54:29Z
```bash
agentlab sandbox snapshot restore --force 1074 live-snap1
```
```
Sandbox: 1074
Snapshot: live-snap1
```
- exit_code: 0

### Verify Marker After Restore (Expect ORIGINAL)
- UTC: 2026-02-14T13:54:30Z
```bash
agentlab ssh 1074 --wait -- bash -lc 'cat /tmp/al_state.txt'
```
```
error: cannot reach sandbox 1074 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
```
- exit_code: 1

### Destroy Snapshot Test Sandbox
- UTC: 2026-02-14T13:54:31Z
```bash
agentlab sandbox destroy --force 1074
```
```
sandbox 1074 destroyed (state=DESTROYED)
```
- exit_code: 0

### Show Snapshot Test Sandbox After Destroy
- UTC: 2026-02-14T13:54:32Z
```bash
agentlab sandbox show 1074
```
```
VMID: 1074
Name: snap-test-145414
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:54:14.061099019Z
Last Used At: 2026-02-14T13:54:32.092877958Z
Created At: 2026-02-14T13:54:14.061099019Z
Updated At: 2026-02-14T13:54:32.092879097Z
```
- exit_code: 0

## Stage 3B: Snapshot Restore Correctness Retry (Real SSH Exec)
- UTC: 2026-02-14T13:55:31Z

### Create Snapshot Retry Sandbox (1075)
- UTC: 2026-02-14T13:55:31Z
```bash
agentlab sandbox new --profile yolo-ephemeral --name snap-retry-145531 --vmid 1075 --ttl 60m
```
```
VMID: 1075
Name: snap-retry-145531
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:55:31.992918757Z
Last Used At: -
Created At: 2026-02-14T13:55:31.992918757Z
Updated At: 2026-02-14T13:55:44.24133473Z
```
- exit_code: 0

### Write ORIGINAL Marker (SSH exec)
- UTC: 2026-02-14T13:55:45Z
```bash
agentlab ssh 1075 --wait --exec -- sh -c "'echo ORIGINAL >/tmp/al_state.txt; cat /tmp/al_state.txt'"
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
ORIGINAL
```
- exit_code: 0

### Save Snapshot live-snap2
- UTC: 2026-02-14T13:55:46Z
```bash
agentlab sandbox snapshot save --force 1075 live-snap2
```
```
Sandbox: 1075
Snapshot: live-snap2
Created At: 2026-02-14T13:55:47.403359628Z
```
- exit_code: 0

### Mutate Marker To CHANGED (SSH exec)
- UTC: 2026-02-14T13:55:47Z
```bash
agentlab ssh 1075 --wait --exec -- sh -c "'echo CHANGED >/tmp/al_state.txt; cat /tmp/al_state.txt'"
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
CHANGED
```
- exit_code: 0

### Verify Marker Is CHANGED
- UTC: 2026-02-14T13:55:47Z
```bash
agentlab ssh 1075 --wait --exec -- sh -c "'cat /tmp/al_state.txt'"
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
CHANGED
```
- exit_code: 0

### Restore Snapshot live-snap2
- UTC: 2026-02-14T13:55:47Z
```bash
agentlab sandbox snapshot restore --force 1075 live-snap2
```
```
Sandbox: 1075
Snapshot: live-snap2
```
- exit_code: 0

### Verify Marker After Restore (Expect ORIGINAL)
- UTC: 2026-02-14T13:55:49Z
```bash
agentlab ssh 1075 --wait --exec -- sh -c "'cat /tmp/al_state.txt'"
```
```
error: cannot reach sandbox 1075 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
```
- exit_code: 1

### Destroy Snapshot Retry Sandbox
- UTC: 2026-02-14T13:55:49Z
```bash
agentlab sandbox destroy --force 1075
```
```
sandbox 1075 destroyed (state=DESTROYED)
```
- exit_code: 0

### Show Snapshot Retry Sandbox After Destroy
- UTC: 2026-02-14T13:55:50Z
```bash
agentlab sandbox show 1075
```
```
VMID: 1075
Name: snap-retry-145531
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:55:31.992918757Z
Last Used At: 2026-02-14T13:55:50.605885245Z
Created At: 2026-02-14T13:55:31.992918757Z
Updated At: 2026-02-14T13:55:50.605886521Z
```
- exit_code: 0

## Stage 3C: Snapshot Restore With Explicit SSH Retry Loop
- UTC: 2026-02-14T13:56:11Z

### Create Snapshot Loop Sandbox (1076)
- UTC: 2026-02-14T13:56:11Z
```bash
agentlab sandbox new --profile yolo-ephemeral --name snap-loop-145611 --vmid 1076 --ttl 60m
```
```
VMID: 1076
Name: snap-loop-145611
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:56:11.987945625Z
Last Used At: -
Created At: 2026-02-14T13:56:11.987945625Z
Updated At: 2026-02-14T13:56:24.227964848Z
```
- exit_code: 0

### Write ORIGINAL Marker
- UTC: 2026-02-14T13:56:25Z
```bash
agentlab ssh 1076 --exec -- sh -c "'echo ORIGINAL >/tmp/al_state.txt; cat /tmp/al_state.txt'"
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
ORIGINAL
```
- exit_code: 0

### Save Snapshot live-snap3
- UTC: 2026-02-14T13:56:26Z
```bash
agentlab sandbox snapshot save --force 1076 live-snap3
```
```
Sandbox: 1076
Snapshot: live-snap3
Created At: 2026-02-14T13:56:27.35915656Z
```
- exit_code: 0

### Mutate Marker To CHANGED
- UTC: 2026-02-14T13:56:27Z
```bash
agentlab ssh 1076 --exec -- sh -c "'echo CHANGED >/tmp/al_state.txt; cat /tmp/al_state.txt'"
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
CHANGED
```
- exit_code: 0

### Restore Snapshot live-snap3
- UTC: 2026-02-14T13:56:27Z
```bash
agentlab sandbox snapshot restore --force 1076 live-snap3
```
```
Sandbox: 1076
Snapshot: live-snap3
```
- exit_code: 0

### Verify Marker After Restore Using Retry Loop
- UTC: 2026-02-14T13:56:28Z
```bash
for i in \1
2
3
4
5
6
7
8
9
10
11
12
13
14
15
16
17
18; do agentlab ssh 1076 --exec -- sh -c "'cat /tmp/al_state.txt'" && break; sleep 5; done
```
```
attempt 1:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 2:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 3:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 4:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 5:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 6:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 7:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 8:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 9:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 10:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 11:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 12:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 13:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 14:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 15:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 16:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 17:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
attempt 18:
error: cannot reach sandbox 1076 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
attempt_exit_code: 1
```
- exit_code: 1

### Destroy Snapshot Loop Sandbox
- UTC: 2026-02-14T13:58:10Z
```bash
agentlab sandbox destroy --force 1076
```
```
sandbox 1076 destroyed (state=DESTROYED)
```
- exit_code: 0

### Show Snapshot Loop Sandbox After Destroy
- UTC: 2026-02-14T13:58:11Z
```bash
agentlab sandbox show 1076
```
```
VMID: 1076
Name: snap-loop-145611
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:56:11.987945625Z
Last Used At: 2026-02-14T13:58:11.098843725Z
Created At: 2026-02-14T13:56:11.987945625Z
Updated At: 2026-02-14T13:58:11.098844927Z
```
- exit_code: 0

## Stage 3D: Snapshot Restore + Explicit Start Validation
- UTC: 2026-02-14T13:58:43Z

### Create Sandbox (1077)
- UTC: 2026-02-14T13:58:43Z
```bash
agentlab sandbox new --profile yolo-ephemeral --name snap-start-145843 --vmid 1077 --ttl 60m
```
```
VMID: 1077
Name: snap-start-145843
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:58:43.744326543Z
Last Used At: -
Created At: 2026-02-14T13:58:43.744326543Z
Updated At: 2026-02-14T13:58:55.98064336Z
```
- exit_code: 0

### Write ORIGINAL Marker
- UTC: 2026-02-14T13:58:57Z
```bash
agentlab ssh 1077 --exec -- sh -c "'echo ORIGINAL >/tmp/al_state.txt; cat /tmp/al_state.txt'"
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
ORIGINAL
```
- exit_code: 0

### Save Snapshot live-snap4
- UTC: 2026-02-14T13:58:57Z
```bash
agentlab sandbox snapshot save --force 1077 live-snap4
```
```
Sandbox: 1077
Snapshot: live-snap4
Created At: 2026-02-14T13:58:59.216634904Z
```
- exit_code: 0

### Mutate Marker To CHANGED
- UTC: 2026-02-14T13:58:59Z
```bash
agentlab ssh 1077 --exec -- sh -c "'echo CHANGED >/tmp/al_state.txt; cat /tmp/al_state.txt'"
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
CHANGED
```
- exit_code: 0

### Restore Snapshot live-snap4
- UTC: 2026-02-14T13:58:59Z
```bash
agentlab sandbox snapshot restore --force 1077 live-snap4
```
```
Sandbox: 1077
Snapshot: live-snap4
```
- exit_code: 0

### Show Sandbox State After Restore
- UTC: 2026-02-14T13:59:00Z
```bash
agentlab sandbox show 1077
```
```
VMID: 1077
Name: snap-start-145843
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:58:43.744326543Z
Last Used At: 2026-02-14T13:59:00.551905945Z
Created At: 2026-02-14T13:58:43.744326543Z
Updated At: 2026-02-14T13:59:00.551907642Z
```
- exit_code: 0

### Start Sandbox After Restore
- UTC: 2026-02-14T13:59:00Z
```bash
agentlab sandbox start 1077
```
```
sandbox 1077 started (state=RUNNING)
```
- exit_code: 0

### Read Marker After Start (Expect ORIGINAL)
- UTC: 2026-02-14T13:59:00Z
```bash
agentlab ssh 1077 --exec -- sh -c "'cat /tmp/al_state.txt'"
```
```
error: cannot reach sandbox 1077 at 10.77.0.177:22
next: agentlab ssh --help
hint: enable the Tailscale subnet route (accept-routes) for 10.77.0.0/16
hint: or configure a jump host: agentlab connect --jump-user <user> --jump-host <host>
```
- exit_code: 1

### Destroy Sandbox 1077
- UTC: 2026-02-14T13:59:01Z
```bash
agentlab sandbox destroy --force 1077
```
```
sandbox 1077 destroyed (state=DESTROYED)
```
- exit_code: 0

### Show Sandbox 1077 After Destroy
- UTC: 2026-02-14T13:59:02Z
```bash
agentlab sandbox show 1077
```
```
VMID: 1077
Name: snap-start-145843
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:58:43.744326543Z
Last Used At: 2026-02-14T13:59:02.205832661Z
Created At: 2026-02-14T13:58:43.744326543Z
Updated At: 2026-02-14T13:59:02.205833955Z
```
- exit_code: 0

## Stage 4: SSH Readiness Reliability (3-cycle)
- UTC: 2026-02-14T13:59:18Z

### Create Sandbox 1080
- UTC: 2026-02-14T13:59:18Z
```bash
agentlab sandbox new --profile yolo-ephemeral --name ssh-rel-1080-145918 --vmid 1080 --ttl 45m
```
```
VMID: 1080
Name: ssh-rel-1080-145918
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:44:18.429170686Z
Last Used At: -
Created At: 2026-02-14T13:59:18.429170686Z
Updated At: 2026-02-14T13:59:30.599922416Z
```
- exit_code: 0

### SSH Probe 1080
- UTC: 2026-02-14T13:59:32Z
```bash
agentlab ssh 1080 --wait --exec -- sh -c "'echo SSH_OK_1080'"
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
SSH_OK_1080
```
- exit_code: 0

### Destroy Sandbox 1080
- UTC: 2026-02-14T13:59:33Z
```bash
agentlab sandbox destroy --force 1080
```
```
sandbox 1080 destroyed (state=DESTROYED)
```
- exit_code: 0

### Create Sandbox 1081
- UTC: 2026-02-14T13:59:34Z
```bash
agentlab sandbox new --profile yolo-ephemeral --name ssh-rel-1081-145934 --vmid 1081 --ttl 45m
```
```
VMID: 1081
Name: ssh-rel-1081-145934
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:44:34.305333104Z
Last Used At: -
Created At: 2026-02-14T13:59:34.305333104Z
Updated At: 2026-02-14T13:59:46.591186646Z
```
- exit_code: 0

### SSH Probe 1081
- UTC: 2026-02-14T13:59:48Z
```bash
agentlab ssh 1081 --wait --exec -- sh -c "'echo SSH_OK_1081'"
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
SSH_OK_1081
```
- exit_code: 0

### Destroy Sandbox 1081
- UTC: 2026-02-14T13:59:49Z
```bash
agentlab sandbox destroy --force 1081
```
```
sandbox 1081 destroyed (state=DESTROYED)
```
- exit_code: 0

### Create Sandbox 1082
- UTC: 2026-02-14T13:59:50Z
```bash
agentlab sandbox new --profile yolo-ephemeral --name ssh-rel-1082-145950 --vmid 1082 --ttl 45m
```
```
VMID: 1082
Name: ssh-rel-1082-145950
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:44:50.190088455Z
Last Used At: -
Created At: 2026-02-14T13:59:50.190088455Z
Updated At: 2026-02-14T14:00:02.403780503Z
```
- exit_code: 0

### SSH Probe 1082
- UTC: 2026-02-14T14:00:03Z
```bash
agentlab ssh 1082 --wait --exec -- sh -c "'echo SSH_OK_1082'"
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
SSH_OK_1082
```
- exit_code: 0

### Destroy Sandbox 1082
- UTC: 2026-02-14T14:00:04Z
```bash
agentlab sandbox destroy --force 1082
```
```
sandbox 1082 destroyed (state=DESTROYED)
```
- exit_code: 0

## Stage 5: Daemon Restart Resilience
- UTC: 2026-02-14T14:00:23Z

### Create Restart-Test Sandbox (1083)
- UTC: 2026-02-14T14:00:23Z
```bash
agentlab sandbox new --profile yolo-ephemeral --name restart-test-150023 --vmid 1083 --ttl 60m
```
```
VMID: 1083
Name: restart-test-150023
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T15:00:23.139374386Z
Last Used At: -
Created At: 2026-02-14T14:00:23.139374386Z
Updated At: 2026-02-14T14:00:35.411733572Z
```
- exit_code: 0

### Pre-Restart SSH Probe (1083)
- UTC: 2026-02-14T14:00:37Z
```bash
agentlab ssh 1083 --wait --exec -- sh -c "'echo PRE_RESTART_OK'"
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
PRE_RESTART_OK
```
- exit_code: 0

### Show Running Job Before Restart
- UTC: 2026-02-14T14:00:38Z
```bash
agentlab --json job show job_f5b9ff873fddc66f
```
```
{
  "id": "job_f5b9ff873fddc66f",
  "repo_url": "https://github.com/openclaw/openclaw",
  "ref": "main",
  "profile": "openclaw",
  "task": "diagnostic",
  "mode": "local",
  "ttl_minutes": 43200,
  "keepalive": true,
  "status": "RUNNING",
  "sandbox_vmid": 1056,
  "events": [
    {
      "id": 250,
      "ts": "2026-02-10T02:47:57.727885626Z",
      "kind": "job.running",
      "sandbox_vmid": 1056,
      "job_id": "job_f5b9ff873fddc66f",
      "msg": "sandbox running"
    }
  ],
  "created_at": "2026-02-10T02:47:28.073432886Z",
  "updated_at": "2026-02-10T02:47:57.726751404Z"
}

```
- exit_code: 0

### Restart agentlabd
- UTC: 2026-02-14T14:00:38Z
```bash
systemctl restart agentlabd.service && systemctl is-active agentlabd.service
```
```
active
```
- exit_code: 0

### Show Restart-Test Sandbox After Restart
- UTC: 2026-02-14T14:00:38Z
```bash
agentlab sandbox show 1083
```
```
VMID: 1083
Name: restart-test-150023
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T15:00:23.139374386Z
Last Used At: 2026-02-14T14:00:40.021421498Z
Created At: 2026-02-14T14:00:23.139374386Z
Updated At: 2026-02-14T14:00:40.021422815Z
```
- exit_code: 0

### Post-Restart SSH Probe (1083)
- UTC: 2026-02-14T14:00:40Z
```bash
agentlab ssh 1083 --wait --exec -- sh -c "'echo POST_RESTART_OK'"
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
POST_RESTART_OK
```
- exit_code: 0

### Show Running Job After Restart
- UTC: 2026-02-14T14:00:40Z
```bash
agentlab --json job show job_f5b9ff873fddc66f
```
```
{
  "id": "job_f5b9ff873fddc66f",
  "repo_url": "https://github.com/openclaw/openclaw",
  "ref": "main",
  "profile": "openclaw",
  "task": "diagnostic",
  "mode": "local",
  "ttl_minutes": 43200,
  "keepalive": true,
  "status": "RUNNING",
  "sandbox_vmid": 1056,
  "events": [
    {
      "id": 250,
      "ts": "2026-02-10T02:47:57.727885626Z",
      "kind": "job.running",
      "sandbox_vmid": 1056,
      "job_id": "job_f5b9ff873fddc66f",
      "msg": "sandbox running"
    }
  ],
  "created_at": "2026-02-10T02:47:28.073432886Z",
  "updated_at": "2026-02-10T02:47:57.726751404Z"
}

```
- exit_code: 0

### Destroy Restart-Test Sandbox
- UTC: 2026-02-14T14:00:40Z
```bash
agentlab sandbox destroy --force 1083
```
```
sandbox 1083 destroyed (state=DESTROYED)
```
- exit_code: 0

### Show Restart-Test Sandbox After Destroy
- UTC: 2026-02-14T14:00:41Z
```bash
agentlab sandbox show 1083
```
```
VMID: 1083
Name: restart-test-150023
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T15:00:23.139374386Z
Last Used At: 2026-02-14T14:00:41.958269168Z
Created At: 2026-02-14T14:00:23.139374386Z
Updated At: 2026-02-14T14:00:41.958270191Z
```
- exit_code: 0

## Stage 6: Parallel Soak (5 Sandboxes)
- UTC: 2026-02-14T14:00:57Z

## Stage 6B: Parallel Soak Retry (5 Sandboxes)
- UTC: 2026-02-14T14:01:07Z

## Stage 6C: Parallel Soak Retry 2 (5 Sandboxes)
- UTC: 2026-02-14T14:01:20Z

### Parallel Create 1084-1088
- UTC: 2026-02-14T14:01:20Z
```bash
printf '%s\n' 1084 1085 1086 1087 1088 | xargs -I{} -P5 /tmp/soak_create_one.sh {}
```
```
VMID=1084 EXIT=0
VMID: 1084
Name: soak-1084-150120
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:20.975673464Z
Last Used At: -
Created At: 2026-02-14T14:01:20.975673464Z
Updated At: 2026-02-14T14:01:39.333136841Z
VMID=1087 EXIT=0
VMID: 1087
Name: soak-1087-150120
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:20.977316161Z
Last Used At: -
Created At: 2026-02-14T14:01:20.977316161Z
Updated At: 2026-02-14T14:01:41.218845887Z
VMID=1085 EXIT=0
VMID: 1085
Name: soak-1085-150120
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:20.976924552Z
Last Used At: -
Created At: 2026-02-14T14:01:20.976924552Z
Updated At: 2026-02-14T14:01:41.177854113Z
VMID=1088 EXIT=0
VMID: 1088
Name: soak-1088-150120
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:20.977614015Z
Last Used At: -
Created At: 2026-02-14T14:01:20.977614015Z
Updated At: 2026-02-14T14:01:42.540699981Z
VMID=1086 EXIT=0
VMID: 1086
Name: soak-1086-150120
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:20.976243379Z
Last Used At: -
Created At: 2026-02-14T14:01:20.976243379Z
Updated At: 2026-02-14T14:01:42.474946416Z
```
- exit_code: 0

## Stage 6D: Parallel Soak Final (5 Sandboxes)
- UTC: 2026-02-14T14:01:56Z

### Parallel Create 1084-1088
- UTC: 2026-02-14T14:01:56Z
```bash
printf '%s\n' 1084 1085 1086 1087 1088 | xargs -I{} -P5 /tmp/soak_create_one.sh {}
```
```
VMID=1085 EXIT=1
error: failed to create sandbox
next: agentlab sandbox --help
VMID=1086 EXIT=1
VMID=1084 EXIT=1
error: failed to create sandbox
next: agentlab sandbox --help
error: failed to create sandbox
next: agentlab sandbox --help
VMID=1087 EXIT=0
VMID: 1089
Name: sandbox-1088
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:56.670245629Z
Last Used At: -
Created At: 2026-02-14T14:01:56.670245629Z
Updated At: 2026-02-14T14:02:13.603883741Z
VMID=1088 EXIT=0
VMID: 1090
Name: sandbox-1089
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:56.670308234Z
Last Used At: -
Created At: 2026-02-14T14:01:56.670308234Z
Updated At: 2026-02-14T14:02:15.424668404Z
```
- exit_code: 0

### Show Soak Sandboxes
- UTC: 2026-02-14T14:02:16Z
```bash
for v in 1084 1085 1086 1087 1088; do echo '---'; agentlab sandbox show $v; done
```
```
---
VMID: 1084
Name: soak-1084-150120
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:20.975673464Z
Last Used At: 2026-02-14T14:02:16.599181365Z
Created At: 2026-02-14T14:01:20.975673464Z
Updated At: 2026-02-14T14:02:16.599183122Z
---
VMID: 1085
Name: soak-1085-150120
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:20.976924552Z
Last Used At: 2026-02-14T14:02:16.605406064Z
Created At: 2026-02-14T14:01:20.976924552Z
Updated At: 2026-02-14T14:02:16.605407322Z
---
VMID: 1086
Name: soak-1086-150120
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:20.976243379Z
Last Used At: 2026-02-14T14:02:16.610877919Z
Created At: 2026-02-14T14:01:20.976243379Z
Updated At: 2026-02-14T14:02:16.610879754Z
---
VMID: 1087
Name: soak-1087-150120
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:20.977316161Z
Last Used At: 2026-02-14T14:02:16.616856151Z
Created At: 2026-02-14T14:01:20.977316161Z
Updated At: 2026-02-14T14:02:16.616857596Z
---
VMID: 1088
Name: soak-1088-150120
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:20.977614015Z
Last Used At: 2026-02-14T14:02:16.622227113Z
Created At: 2026-02-14T14:01:20.977614015Z
Updated At: 2026-02-14T14:02:16.622228462Z
```
- exit_code: 0

### SSH Probe Soak Sandboxes
- UTC: 2026-02-14T14:02:16Z
```bash
for v in 1084 1085 1086 1087 1088; do echo '--- vmid='$v; agentlab ssh $v --wait --exec -- sh -c "'echo SOAK_OK_'$v"; done
```
```
--- vmid=1084
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
SOAK_OK_1084
--- vmid=1085
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
SOAK_OK_1085
--- vmid=1086
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
SOAK_OK_1086
--- vmid=1087
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
SOAK_OK_1087
--- vmid=1088
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
SOAK_OK_1088
```
- exit_code: 0

### Destroy Soak Sandboxes
- UTC: 2026-02-14T14:02:18Z
```bash
for v in 1084 1085 1086 1087 1088; do agentlab sandbox destroy --force $v; done
```
```
sandbox 1084 destroyed (state=DESTROYED)
sandbox 1085 destroyed (state=DESTROYED)
sandbox 1086 destroyed (state=DESTROYED)
sandbox 1087 destroyed (state=DESTROYED)
sandbox 1088 destroyed (state=DESTROYED)
```
- exit_code: 0

### Show Soak Sandboxes After Destroy
- UTC: 2026-02-14T14:02:24Z
```bash
for v in 1084 1085 1086 1087 1088; do echo '---'; agentlab sandbox show $v; done
```
```
---
VMID: 1084
Name: soak-1084-150120
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:20.975673464Z
Last Used At: 2026-02-14T14:02:24.39749029Z
Created At: 2026-02-14T14:01:20.975673464Z
Updated At: 2026-02-14T14:02:24.397492143Z
---
VMID: 1085
Name: soak-1085-150120
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:20.976924552Z
Last Used At: 2026-02-14T14:02:24.404434136Z
Created At: 2026-02-14T14:01:20.976924552Z
Updated At: 2026-02-14T14:02:24.404435827Z
---
VMID: 1086
Name: soak-1086-150120
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:20.976243379Z
Last Used At: 2026-02-14T14:02:24.410712869Z
Created At: 2026-02-14T14:01:20.976243379Z
Updated At: 2026-02-14T14:02:24.410714916Z
---
VMID: 1087
Name: soak-1087-150120
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:20.977316161Z
Last Used At: 2026-02-14T14:02:24.417175523Z
Created At: 2026-02-14T14:01:20.977316161Z
Updated At: 2026-02-14T14:02:24.417177476Z
---
VMID: 1088
Name: soak-1088-150120
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:20.977614015Z
Last Used At: 2026-02-14T14:02:24.423694579Z
Created At: 2026-02-14T14:01:20.977614015Z
Updated At: 2026-02-14T14:02:24.423696301Z
```
- exit_code: 0

## Stage 7: Cross-Sandbox Isolation Check
- UTC: 2026-02-14T14:02:40Z

### Create Sandbox 1091
- UTC: 2026-02-14T14:02:40Z
```bash
agentlab sandbox new --profile yolo-ephemeral --name iso-a-150240 --vmid 1091 --ttl 45m
```
```
VMID: 1091
Name: iso-a-150240
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:47:40.287270617Z
Last Used At: -
Created At: 2026-02-14T14:02:40.287270617Z
Updated At: 2026-02-14T14:02:53.174084136Z
```
- exit_code: 0

### Create Sandbox 1092
- UTC: 2026-02-14T14:02:54Z
```bash
agentlab sandbox new --profile yolo-ephemeral --name iso-b-150254 --vmid 1092 --ttl 45m
```
```
VMID: 1092
Name: iso-b-150254
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:47:54.718012363Z
Last Used At: -
Created At: 2026-02-14T14:02:54.718012363Z
Updated At: 2026-02-14T14:03:07.570928729Z
```
- exit_code: 0

### Show Sandboxes 1091/1092
- UTC: 2026-02-14T14:03:08Z
```bash
agentlab sandbox show 1091; agentlab sandbox show 1092
```
```
VMID: 1091
Name: iso-a-150240
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:47:40.287270617Z
Last Used At: 2026-02-14T14:03:08.800338872Z
Created At: 2026-02-14T14:02:40.287270617Z
Updated At: 2026-02-14T14:03:08.800340501Z
VMID: 1092
Name: iso-b-150254
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:47:54.718012363Z
Last Used At: 2026-02-14T14:03:08.806730734Z
Created At: 2026-02-14T14:02:54.718012363Z
Updated At: 2026-02-14T14:03:08.806732665Z
```
- exit_code: 0

### From 1091: Ping 1092
- UTC: 2026-02-14T14:03:08Z
```bash
agentlab ssh 1091 --exec -- sh -c "'ping -c 2 -W 1 10.77.0.177'"
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
client_loop: send disconnect: Broken pipe
```
- exit_code: 255

### From 1091: TCP 22 to 1092
- UTC: 2026-02-14T14:03:09Z
```bash
agentlab ssh 1091 --exec -- sh -c "'timeout 3 bash -lc ""cat < /dev/null > /dev/tcp/10.77.0.177/22"" && echo TCP22_OPEN || echo TCP22_BLOCKED'"
```
```
Warning: route to 10.77.0.177 goes via vmbr1, not Tailscale; enable the agent subnet route (10.77.0.0/16)
TCP22_BLOCKED
sh: 1: cannot create /dev/tcp/10.77.0.177/22: Directory nonexistent
```
- exit_code: 0

### Destroy Sandbox 1091
- UTC: 2026-02-14T14:03:10Z
```bash
agentlab sandbox destroy --force 1091
```
```
sandbox 1091 destroyed (state=DESTROYED)
```
- exit_code: 0

### Destroy Sandbox 1092
- UTC: 2026-02-14T14:03:11Z
```bash
agentlab sandbox destroy --force 1092
```
```
sandbox 1092 destroyed (state=DESTROYED)
```
- exit_code: 0

### Show 1091/1092 After Destroy
- UTC: 2026-02-14T14:03:12Z
```bash
agentlab sandbox show 1091; agentlab sandbox show 1092
```
```
VMID: 1091
Name: iso-a-150240
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:47:40.287270617Z
Last Used At: 2026-02-14T14:03:12.505425906Z
Created At: 2026-02-14T14:02:40.287270617Z
Updated At: 2026-02-14T14:03:12.505427472Z
VMID: 1092
Name: iso-b-150254
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:47:54.718012363Z
Last Used At: 2026-02-14T14:03:12.511043259Z
Created At: 2026-02-14T14:02:54.718012363Z
Updated At: 2026-02-14T14:03:12.511044358Z
```
- exit_code: 0

## Stage 8: Cleanup Of Leftover Test Sandboxes
- UTC: 2026-02-14T14:03:33Z

### Show Leftover 1089/1090
- UTC: 2026-02-14T14:03:33Z
```bash
agentlab sandbox show 1089; agentlab sandbox show 1090
```
```
VMID: 1089
Name: sandbox-1088
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:56.670245629Z
Last Used At: 2026-02-14T14:03:33.453100657Z
Created At: 2026-02-14T14:01:56.670245629Z
Updated At: 2026-02-14T14:03:33.453102141Z
VMID: 1090
Name: sandbox-1089
Profile: yolo-ephemeral
State: RUNNING
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:56.670308234Z
Last Used At: 2026-02-14T14:03:33.458969916Z
Created At: 2026-02-14T14:01:56.670308234Z
Updated At: 2026-02-14T14:03:33.458971055Z
```
- exit_code: 0

### Destroy Leftover 1089/1090
- UTC: 2026-02-14T14:03:33Z
```bash
agentlab sandbox destroy --force 1089; agentlab sandbox destroy --force 1090
```
```
sandbox 1089 destroyed (state=DESTROYED)
sandbox 1090 destroyed (state=DESTROYED)
```
- exit_code: 0

### Show 1089/1090 After Destroy
- UTC: 2026-02-14T14:03:35Z
```bash
agentlab sandbox show 1089; agentlab sandbox show 1090
```
```
VMID: 1089
Name: sandbox-1088
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:56.670245629Z
Last Used At: 2026-02-14T14:03:35.509513086Z
Created At: 2026-02-14T14:01:56.670245629Z
Updated At: 2026-02-14T14:03:35.509514374Z
VMID: 1090
Name: sandbox-1089
Profile: yolo-ephemeral
State: DESTROYED
IP: 10.77.0.177
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:46:56.670308234Z
Last Used At: 2026-02-14T14:03:35.515136361Z
Created At: 2026-02-14T14:01:56.670308234Z
Updated At: 2026-02-14T14:03:35.515137157Z
```
- exit_code: 0

### Final Status Snapshot
- UTC: 2026-02-14T14:03:35Z
```bash
agentlab status
```
```
Sandboxes:
STATE         COUNT
REQUESTED     0
PROVISIONING  0
BOOTING       0
READY         0
RUNNING       3
SUSPENDED     0
COMPLETED     0
FAILED        0
TIMEOUT       0
STOPPED       0
DESTROYED     85
Network Modes:
MODE       COUNT
off        0
nat        88
allowlist  0
Jobs:
STATUS     COUNT
QUEUED     0
RUNNING    1
COMPLETED  2
FAILED     8
TIMEOUT    0
Artifacts:
Root: /var/lib/agentlab/artifacts
Total Bytes: 944368648192
Free Bytes: 933772001280
Used Bytes: 10596646912
Error: -
Metrics:
Enabled: false
Recent Failures:
TIME                            KIND        JOB                   SANDBOX  MESSAGE
2026-02-14T12:27:19.51578206Z   job.failed  job_51d87ecf0f92ecfb  -        template validation failed: request failed: Get "https://localhost:8006/api2/json/nodes": tls: failed to verify certificate: x509: certificate signed by unknown authority
2026-02-14T12:27:43.687921825Z  job.failed  job_1806a6c5b9473c43  1057     API error (status 400): invalid format - format error
net0.fwgroup: property is not defined in schema and the schema does not allow additional properties
2026-02-14T12:31:06.186773307Z  job.failed  job_56344d760f7842a8  -  template validation failed: failed to query template VM 9000: command qm config 9000 failed: exit status 255: ipcc_send_rec[1] failed: Unknown error -1
ipcc_send_rec[2] failed: Unknown error -1
ipcc_send_rec[3] failed: Unknown error -1
Unable to load access control list: Unknown error -1
2026-02-14T13:05:33.989Z        job.failed              job_8b5ed80a94e47385  1058  job cleanup: sandbox already DESTROYED while job stayed RUNNING
2026-02-14T13:05:33.989Z        job.failed              job_f80051b2f4ed82b6  1060  job cleanup: sandbox already DESTROYED while job stayed RUNNING
2026-02-14T13:19:00.892Z        job.failed              job_9ee731bf095808d8  1061  job cleanup: sandbox already DESTROYED while job stayed RUNNING
2026-02-14T13:19:00.892Z        job.failed              job_943ad77e343b497e  1062  job cleanup: sandbox already DESTROYED while job stayed RUNNING
2026-02-14T13:30:10.891007531Z  sandbox.slo.ssh_failed  -                     1066  ssh not ready after 3m12.224151736s: context deadline exceeded
2026-02-14T13:42:21.202062918Z  sandbox.slo.ssh_failed  -                     1067  ssh not ready after 3m12.747379318s: context deadline exceeded
2026-02-14T13:51:34.230855672Z  job.failed              job_e3bedcaa758dbb57  1068  command qm start 1068 failed: exit status 255: Configuration file 'nodes/pve/qemu-server/1068.conf' does not exist
```
- exit_code: 0

## Session Summary (2026-02-14)
- Scope executed live: race handling, network-mode profiles, snapshot restore behavior, SSH readiness cycles, daemon restart resilience, parallel soak, isolation smoke, and cleanup.

### Passed
- Forced job/sandbox race: destroying assigned sandbox during queued/start phase moved job `job_e3bedcaa758dbb57` to terminal `FAILED` (not stuck RUNNING).
- Fresh sandbox SSH readiness: 3/3 sequential probes passed (VMIDs 1080-1082).
- Daemon restart resilience: sandbox 1083 remained reachable across `agentlabd` restart; existing running job `job_f5b9ff873fddc66f` remained `RUNNING` and queryable.
- Parallel soak baseline run: 5 concurrent creates (1084-1088), SSH probes, and teardown completed.

### Failed / Reproducible Issues
- Proxmox fwgroup incompatibility in this environment:
  - Any profile that sets `net0.fwgroup` fails provisioning with:
  - `net0.fwgroup: property is not defined in schema and the schema does not allow additional properties`
  - Reproduced on VMIDs 1072/1073 and in daemon logs.
- Snapshot restore operational gap:
  - `sandbox snapshot restore` (`qmrollback`) succeeds, but sandbox becomes unreachable over SSH afterward in repeated attempts (VMIDs 1074-1077).
  - In one run, daemon reconciliation logged: `VM 1076 stopped unexpectedly, marking as failed` after rollback.
- CLI/behavior edge during contention:
  - Parallel retry attempted VMIDs 1084-1088 while those VMIDs were occupied; two additional sandboxes were created as VMIDs 1089/1090 with auto names (`sandbox-1088`, `sandbox-1089`) and required explicit cleanup.

### Environment End State
- All test-created sandboxes destroyed (including 1089/1090 cleanup).
- Remaining RUNNING sandboxes are pre-existing non-test workloads (count=3).

## Stage 9-11 (Post-fix Live Validation)
- UTC: 2026-02-14T14:21:31Z
- Binary: version=v0.1.4-dirty commit=5843248 date=2026-02-14T14:19:52Z
- Daemon: version=v0.1.4-dirty commit=5843248 date=2026-02-14T14:19:53Z

### Stage 9: Explicit VMID Conflict
- UTC: 2026-02-14T14:21:31Z

## Stage 9-11 (Post-fix Live Validation)
- UTC: 2026-02-14T14:22:05Z
- Binary: version=v0.1.4-dirty commit=5843248 date=2026-02-14T14:19:52Z
- Daemon: version=v0.1.4-dirty commit=5843248 date=2026-02-14T14:19:53Z

### Stage 9: Explicit VMID Conflict
- UTC: 2026-02-14T14:22:05Z
```bash
agentlab sandbox new --profile minimal --vmid 1053 --name vmid-conflict-152205 --ttl 15 --json
```
```
error: sandbox vmid already exists
next: agentlab sandbox --help
```
- exit_code: 1
- verdict: PASS

## Stage 9-11 (Post-fix Live Validation)
- UTC: 2026-02-14T14:22:26Z
- Binary: version=v0.1.4-dirty commit=5843248 date=2026-02-14T14:19:52Z
- Daemon: version=v0.1.4-dirty commit=5843248 date=2026-02-14T14:19:53Z

### Stage 9: Explicit VMID Conflict
- UTC: 2026-02-14T14:22:26Z
```bash
agentlab sandbox new --profile minimal --vmid 1053 --name vmid-conflict-152226 --ttl 15 --json
```
```
+++ agentlab sandbox new --profile minimal --vmid 1053 --name vmid-conflict-152226 --ttl 15 --json
error: sandbox vmid already exists
next: agentlab sandbox --help
```
- exit_code: 1
- verdict: PASS

## Stage 9-11 (Post-fix Live Validation, Retry)
- UTC: 2026-02-14T14:23:15Z
- Binary: version=v0.1.4-dirty commit=5843248 date=2026-02-14T14:19:52Z
- Daemon: version=v0.1.4-dirty commit=5843248 date=2026-02-14T14:19:53Z

### Stage 9: Explicit VMID Conflict
- UTC: 2026-02-14T14:23:15Z
```bash
agentlab sandbox new --profile minimal --vmid 1053 --name vmid-conflict-152315 --ttl 15 --json
```
```
error: sandbox vmid already exists
next: agentlab sandbox --help
```
- exit_code: 1
- verdict: PASS

### Stage 10: FWGroup Fallback Provisioning
- UTC: 2026-02-14T14:23:32Z
- profile_file: /etc/agentlab/profiles/fwgroup-live.yaml
```bash
agentlab sandbox new --profile fwgroup-live --name fwgroup-live-152317 --ttl 20 --json
```
```json
{
  "profiles": [
    {
      "name": "fwgroup-live",
      "template_vmid": 9001,
      "updated_at": "2026-02-14T14:23:15.848975013Z"
    },
    {
      "name": "interactive-dev",
      "template_vmid": 9001,
      "updated_at": "2026-02-14T13:07:59.217301177Z"
    },
    {
      "name": "minimal",
      "template_vmid": 9000,
      "updated_at": "2026-01-30T19:50:28.666225086Z"
    },
    {
      "name": "openclaw",
      "template_vmid": 9999,
      "updated_at": "2026-02-07T21:01:22.600832991Z"
    },
    {
      "name": "ubuntu-24-04-ai",
      "template_vmid": 9999,
      "updated_at": "2026-02-06T18:26:55.84754418Z"
    },
    {
      "name": "yolo-ephemeral",
      "template_vmid": 9001,
      "updated_at": "2026-02-14T13:07:59.217301177Z"
    },
    {
      "name": "yolo-workspace",
      "template_vmid": 9001,
      "updated_at": "2026-02-14T13:07:59.217301177Z"
    }
  ]
}
```
```
{
  "vmid": 1095,
  "name": "fwgroup-live-152317",
  "profile": "fwgroup-live",
  "state": "RUNNING",
  "network": {
    "mode": "nat",
    "firewall": true,
    "firewall_group": "agent_nat_default"
  },
  "keepalive": false,
  "lease_expires_at": "2026-02-14T14:43:17.717543642Z",
  "created_at": "2026-02-14T14:23:17.717543642Z",
  "updated_at": "2026-02-14T14:23:30.760625252Z"
}
```
```
VMID: 1095
Name: fwgroup-live-152317
Profile: fwgroup-live
State: RUNNING
IP: -
Workspace: -
Network Mode: nat
Firewall: true
Firewall Group: agent_nat_default
Keepalive: false
Lease Expires: 2026-02-14T14:43:17.717543642Z
Last Used At: 2026-02-14T14:23:31.778531897Z
Created At: 2026-02-14T14:23:17.717543642Z
Updated At: 2026-02-14T14:23:31.778533533Z
```
- vmid: 1095
- firewall_group_observed: <none>
- create_exit_code: 0
- show_exit_code: 0
- cleanup_exit_code: 0
- verdict: FAIL

### Stage 11: Snapshot Restore Reconciliation
- UTC: 2026-02-14T14:23:51Z
```bash
agentlab sandbox new --profile yolo-ephemeral --name snapfix-152332 --ttl 20 --json
agentlab sandbox snapshot save --force 1096 livefix
agentlab sandbox snapshot restore --force 1096 livefix
agentlab sandbox show 1096 --json
```
```
{
  "vmid": 1096,
  "name": "snapfix-152332",
  "profile": "yolo-ephemeral",
  "state": "RUNNING",
  "network": {
    "mode": "nat"
  },
  "keepalive": false,
  "lease_expires_at": "2026-02-14T14:43:32.91968146Z",
  "created_at": "2026-02-14T14:23:32.91968146Z",
  "updated_at": "2026-02-14T14:23:45.250186537Z"
}
```
```
Sandbox: 1096
Snapshot: livefix
Created At: 2026-02-14T14:23:48.985957186Z
```
```
Sandbox: 1096
Snapshot: livefix
```
```
VMID: 1096
Name: snapfix-152332
Profile: yolo-ephemeral
State: STOPPED
IP: -
Workspace: -
Network Mode: nat
Firewall: -
Firewall Group: -
Keepalive: false
Lease Expires: 2026-02-14T14:43:32.91968146Z
Last Used At: 2026-02-14T14:23:50.598854017Z
Created At: 2026-02-14T14:23:32.91968146Z
Updated At: 2026-02-14T14:23:50.598860195Z
```
- vmid: 1096
- initial_ip: <none>
- post_restore_state: <none>
- post_restore_ip: <none>
- create_exit_code: 0
- snapshot_exit_code: 0
- restore_exit_code: 0
- show_exit_code: 0
- cleanup_exit_code: 0
- verdict: FAIL

### Post-Stage Status
- UTC: 2026-02-14T14:23:51Z
```json
{
  "sandboxes": {
    "BOOTING": 0,
    "COMPLETED": 0,
    "DESTROYED": 87,
    "FAILED": 0,
    "PROVISIONING": 0,
    "READY": 0,
    "REQUESTED": 0,
    "RUNNING": 5,
    "STOPPED": 0,
    "SUSPENDED": 0,
    "TIMEOUT": 0
  },
  "jobs": {
    "COMPLETED": 2,
    "FAILED": 8,
    "QUEUED": 0,
    "RUNNING": 1,
    "TIMEOUT": 0
  },
  "network_modes": {
    "allowlist": 0,
    "nat": 92,
    "off": 0
  },
  "artifacts": {
    "root": "/var/lib/agentlab/artifacts",
    "total_bytes": 944167845888,
    "free_bytes": 933560451072,
    "used_bytes": 10607394816
  },
  "metrics": {
    "enabled": false
  },
  "recent_failures": [
    {
      "id": 251,
      "ts": "2026-02-14T12:27:19.51578206Z",
      "kind": "job.failed",
      "job_id": "job_51d87ecf0f92ecfb",
      "msg": "template validation failed: request failed: Get \"https://localhost:8006/api2/json/nodes\": tls: failed to verify certificate: x509: certificate signed by unknown authority"
    },
    {
      "id": 253,
      "ts": "2026-02-14T12:27:43.687921825Z",
      "kind": "job.failed",
      "sandbox_vmid": 1057,
      "job_id": "job_1806a6c5b9473c43",
      "msg": "API error (status 400): invalid format - format error\nnet0.fwgroup: property is not defined in schema and the schema does not allow additional properties"
    },
    {
      "id": 265,
      "ts": "2026-02-14T12:31:06.186773307Z",
      "kind": "job.failed",
      "job_id": "job_56344d760f7842a8",
      "msg": "template validation failed: failed to query template VM 9000: command qm config 9000 failed: exit status 255: ipcc_send_rec[1] failed: Unknown error -1\nipcc_send_rec[2] failed: Unknown error -1\nipcc_send_rec[3] failed: Unknown error -1\nUnable to load access control list: Unknown error -1"
    },
    {
      "id": 282,
      "ts": "2026-02-14T13:05:33.989Z",
      "kind": "job.failed",
      "sandbox_vmid": 1058,
      "job_id": "job_8b5ed80a94e47385",
      "msg": "job cleanup: sandbox already DESTROYED while job stayed RUNNING",
      "json": {
        "cleanup": 1
      }
    },
    {
      "id": 283,
      "ts": "2026-02-14T13:05:33.989Z",
      "kind": "job.failed",
      "sandbox_vmid": 1060,
      "job_id": "job_f80051b2f4ed82b6",
      "msg": "job cleanup: sandbox already DESTROYED while job stayed RUNNING",
      "json": {
        "cleanup": 1
      }
    },
    {
      "id": 341,
      "ts": "2026-02-14T13:19:00.892Z",
      "kind": "job.failed",
      "sandbox_vmid": 1061,
      "job_id": "job_9ee731bf095808d8",
      "msg": "job cleanup: sandbox already DESTROYED while job stayed RUNNING",
      "json": {
        "cleanup": 1
      }
    },
    {
      "id": 342,
      "ts": "2026-02-14T13:19:00.892Z",
      "kind": "job.failed",
      "sandbox_vmid": 1062,
      "job_id": "job_943ad77e343b497e",
      "msg": "job cleanup: sandbox already DESTROYED while job stayed RUNNING",
      "json": {
        "cleanup": 1
      }
    },
    {
      "id": 370,
      "ts": "2026-02-14T13:30:10.891007531Z",
      "kind": "sandbox.slo.ssh_failed",
      "sandbox_vmid": 1066,
      "msg": "ssh not ready after 3m12.224151736s: context deadline exceeded",
      "json": {
        "duration_ms": 192224,
        "ip": "10.77.0.177",
        "error": "context deadline exceeded"
      }
    },
    {
      "id": 382,
      "ts": "2026-02-14T13:42:21.202062918Z",
      "kind": "sandbox.slo.ssh_failed",
      "sandbox_vmid": 1067,
      "msg": "ssh not ready after 3m12.747379318s: context deadline exceeded",
      "json": {
        "duration_ms": 192747,
        "ip": "10.77.0.177",
        "error": "context deadline exceeded"
      }
    },
    {
      "id": 387,
      "ts": "2026-02-14T13:51:34.230855672Z",
      "kind": "job.failed",
      "sandbox_vmid": 1068,
      "job_id": "job_e3bedcaa758dbb57",
      "msg": "command qm start 1068 failed: exit status 255: Configuration file 'nodes/pve/qemu-server/1068.conf' does not exist"
    }
  ]
}
```

## Stage 10-11 Follow-up (Correct JSON Parsing)
- UTC: 2026-02-14T14:24:35Z
- cleanup_1093_exit_code: 0
- cleanup_1094_exit_code: 0

### Stage 10b: FWGroup Fallback Provisioning
- UTC: 2026-02-14T14:24:50Z
```bash
agentlab sandbox new --profile fwgroup-live --name fwgroup-live-fu-152435 --ttl 20 --json
agentlab --json sandbox show 1097
```
```
{
  "vmid": 1097,
  "name": "fwgroup-live-fu-152435",
  "profile": "fwgroup-live",
  "state": "RUNNING",
  "ip": "10.77.0.177",
  "network": {
    "mode": "nat",
    "firewall": true,
    "firewall_group": "agent_nat_default"
  },
  "keepalive": false,
  "lease_expires_at": "2026-02-14T14:44:35.086191581Z",
  "created_at": "2026-02-14T14:24:35.086191581Z",
  "updated_at": "2026-02-14T14:24:47.688354234Z"
}
```
```json
{
  "vmid": 1097,
  "name": "fwgroup-live-fu-152435",
  "profile": "fwgroup-live",
  "state": "RUNNING",
  "ip": "10.77.0.177",
  "network": {
    "mode": "nat",
    "firewall": true,
    "firewall_group": "agent_nat_default"
  },
  "keepalive": false,
  "lease_expires_at": "2026-02-14T14:44:35.086191581Z",
  "last_used_at": "2026-02-14T14:24:49.375939311Z",
  "created_at": "2026-02-14T14:24:35.086191581Z",
  "updated_at": "2026-02-14T14:24:49.37594085Z"
}
```
- vmid: 1097
- firewall_group_observed: agent_nat_default
- create_exit_code: 0
- show_exit_code: 0
- cleanup_exit_code: 0
- verdict: PASS

### Stage 11b: Snapshot Restore Reconciliation
- UTC: 2026-02-14T14:25:07Z
```bash
agentlab sandbox new --profile yolo-ephemeral --name snapfix-fu-152450 --ttl 20 --json
agentlab sandbox snapshot save --force 1098 livefix2
agentlab sandbox snapshot restore --force 1098 livefix2
agentlab --json sandbox show 1098
```
```
{
  "vmid": 1098,
  "name": "snapfix-fu-152450",
  "profile": "yolo-ephemeral",
  "state": "RUNNING",
  "ip": "10.77.0.177",
  "network": {
    "mode": "nat"
  },
  "keepalive": false,
  "lease_expires_at": "2026-02-14T14:44:50.530994677Z",
  "created_at": "2026-02-14T14:24:50.530994677Z",
  "updated_at": "2026-02-14T14:25:02.758335953Z"
}
```
```
Sandbox: 1098
Snapshot: livefix2
Created At: 2026-02-14T14:25:05.635481014Z
```
```
Sandbox: 1098
Snapshot: livefix2
```
```json
{
  "vmid": 1098,
  "name": "snapfix-fu-152450",
  "profile": "yolo-ephemeral",
  "state": "STOPPED",
  "network": {
    "mode": "nat"
  },
  "keepalive": false,
  "lease_expires_at": "2026-02-14T14:44:50.530994677Z",
  "last_used_at": "2026-02-14T14:25:07.050722313Z",
  "created_at": "2026-02-14T14:24:50.530994677Z",
  "updated_at": "2026-02-14T14:25:07.050723621Z"
}
```
- vmid: 1098
- initial_ip: 10.77.0.177
- post_restore_state: STOPPED
- post_restore_ip: <none>
- create_exit_code: 0
- snapshot_exit_code: 0
- restore_exit_code: 0
- show_exit_code: 0
- cleanup_exit_code: 0
- verdict: PASS

### Follow-up Cleanup
- UTC: 2026-02-14T14:25:08Z
- removed_profile: /etc/agentlab/profiles/fwgroup-live.yaml
- daemon_restart: ok
- final_running: 3
```json
{
  "sandboxes": {
    "BOOTING": 0,
    "COMPLETED": 0,
    "DESTROYED": 91,
    "FAILED": 0,
    "PROVISIONING": 0,
    "READY": 0,
    "REQUESTED": 0,
    "RUNNING": 3,
    "STOPPED": 0,
    "SUSPENDED": 0,
    "TIMEOUT": 0
  },
  "jobs": {
    "COMPLETED": 2,
    "FAILED": 8,
    "QUEUED": 0,
    "RUNNING": 1,
    "TIMEOUT": 0
  },
  "network_modes": {
    "allowlist": 0,
    "nat": 94,
    "off": 0
  },
  "artifacts": {
    "root": "/var/lib/agentlab/artifacts",
    "total_bytes": 944369827840,
    "free_bytes": 933762301952,
    "used_bytes": 10607525888
  },
  "metrics": {
    "enabled": false
  },
  "recent_failures": [
    {
      "id": 251,
      "ts": "2026-02-14T12:27:19.51578206Z",
      "kind": "job.failed",
      "job_id": "job_51d87ecf0f92ecfb",
      "msg": "template validation failed: request failed: Get \"https://localhost:8006/api2/json/nodes\": tls: failed to verify certificate: x509: certificate signed by unknown authority"
    },
    {
      "id": 253,
      "ts": "2026-02-14T12:27:43.687921825Z",
      "kind": "job.failed",
      "sandbox_vmid": 1057,
      "job_id": "job_1806a6c5b9473c43",
      "msg": "API error (status 400): invalid format - format error\nnet0.fwgroup: property is not defined in schema and the schema does not allow additional properties"
    },
    {
      "id": 265,
      "ts": "2026-02-14T12:31:06.186773307Z",
      "kind": "job.failed",
      "job_id": "job_56344d760f7842a8",
      "msg": "template validation failed: failed to query template VM 9000: command qm config 9000 failed: exit status 255: ipcc_send_rec[1] failed: Unknown error -1\nipcc_send_rec[2] failed: Unknown error -1\nipcc_send_rec[3] failed: Unknown error -1\nUnable to load access control list: Unknown error -1"
    },
    {
      "id": 282,
      "ts": "2026-02-14T13:05:33.989Z",
      "kind": "job.failed",
      "sandbox_vmid": 1058,
      "job_id": "job_8b5ed80a94e47385",
      "msg": "job cleanup: sandbox already DESTROYED while job stayed RUNNING",
      "json": {
        "cleanup": 1
      }
    },
    {
      "id": 283,
      "ts": "2026-02-14T13:05:33.989Z",
      "kind": "job.failed",
      "sandbox_vmid": 1060,
      "job_id": "job_f80051b2f4ed82b6",
      "msg": "job cleanup: sandbox already DESTROYED while job stayed RUNNING",
      "json": {
        "cleanup": 1
      }
    },
    {
      "id": 341,
      "ts": "2026-02-14T13:19:00.892Z",
      "kind": "job.failed",
      "sandbox_vmid": 1061,
      "job_id": "job_9ee731bf095808d8",
      "msg": "job cleanup: sandbox already DESTROYED while job stayed RUNNING",
      "json": {
        "cleanup": 1
      }
    },
    {
      "id": 342,
      "ts": "2026-02-14T13:19:00.892Z",
      "kind": "job.failed",
      "sandbox_vmid": 1062,
      "job_id": "job_943ad77e343b497e",
      "msg": "job cleanup: sandbox already DESTROYED while job stayed RUNNING",
      "json": {
        "cleanup": 1
      }
    },
    {
      "id": 370,
      "ts": "2026-02-14T13:30:10.891007531Z",
      "kind": "sandbox.slo.ssh_failed",
      "sandbox_vmid": 1066,
      "msg": "ssh not ready after 3m12.224151736s: context deadline exceeded",
      "json": {
        "duration_ms": 192224,
        "ip": "10.77.0.177",
        "error": "context deadline exceeded"
      }
    },
    {
      "id": 382,
      "ts": "2026-02-14T13:42:21.202062918Z",
      "kind": "sandbox.slo.ssh_failed",
      "sandbox_vmid": 1067,
      "msg": "ssh not ready after 3m12.747379318s: context deadline exceeded",
      "json": {
        "duration_ms": 192747,
        "ip": "10.77.0.177",
        "error": "context deadline exceeded"
      }
    },
    {
      "id": 387,
      "ts": "2026-02-14T13:51:34.230855672Z",
      "kind": "job.failed",
      "sandbox_vmid": 1068,
      "job_id": "job_e3bedcaa758dbb57",
      "msg": "command qm start 1068 failed: exit status 255: Configuration file 'nodes/pve/qemu-server/1068.conf' does not exist"
    }
  ]
}
```
