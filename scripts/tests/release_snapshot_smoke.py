#!/usr/bin/env python3
"""Verify snapshot packages and native entry points without a Proxmox host."""

import argparse
import hashlib
import http.cookiejar
import http.server
import io
import json
import os
import pathlib
import platform
import re
import secrets
import socket
import struct
import subprocess
import tarfile
import tempfile
import threading
import time
import urllib.error
import urllib.request


def check(condition, message):
    if not condition:
        raise RuntimeError(message)


def run(command, directory, environment, input_data=None):
    result = subprocess.run(command, cwd=directory, env=environment,
                            input=input_data,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                            timeout=15, check=False)
    check(result.returncode == 0, "packaged command failed: " + command[0])
    return result.stdout


def smoke(distribution):
    metadata = json.loads((distribution / "metadata.json").read_text())
    tag = metadata["tag"]
    expected = {(name, system, arch)
                for name, systems in (("agentlab", ("linux", "darwin")),
                                      ("agentlabd", ("linux",)))
                for system in systems for arch in ("amd64", "arm64")}
    checksums = {}
    for line in (distribution / "checksums.txt").read_text().splitlines():
        digest, name = line.split()
        check(name not in checksums, "duplicate archive checksum")
        checksums[name] = digest
    expected_names = {f"{name}_{tag}_{system}-{arch}.tar.gz"
                      for name, system, arch in expected}
    check(set(checksums) == expected_names, "unexpected release checksum entries")
    check({path.name for path in distribution.glob("*.tar.gz")} == expected_names,
          "unexpected release archive set")
    native_system = platform.system().lower()
    native_arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine().lower())
    with tempfile.TemporaryDirectory(prefix="agentlab-release-smoke-") as temporary:
        root = pathlib.Path(temporary)
        environment = {"PATH": os.environ["PATH"], "XDG_CONFIG_HOME": str(root / "config"),
                       "AGENTLAB_CONFIG_DIR": str(root / "config" / "agentlab"),
                       "AGENTLAB_CREDENTIAL_STORE": "file",
                       "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull,
                       "GIT_CONFIG_SYSTEM": os.devnull, "GIT_TERMINAL_PROMPT": "0",
                       "GIT_ALLOW_PROTOCOL": "file"}
        binaries = {}
        for name, system, arch in sorted(expected):
            filename = f"{name}_{tag}_{system}-{arch}.tar.gz"
            archive_path = distribution / filename
            check(hashlib.sha256(archive_path.read_bytes()).hexdigest() == checksums[filename], "archive checksum mismatch")
            with tarfile.open(archive_path, "r:gz") as archive:
                members = [entry for entry in archive.getmembers() if entry.name == name]
                check(len(members) == 1 and members[0].isfile(), "missing or duplicate packaged binary")
                data = archive.extractfile(members[0]).read()
                if name == "agentlab":
                    for notice in ("third_party/purego/LICENSE", "third_party/purego/NOTICE.txt"):
                        entries = [entry for entry in archive.getmembers() if entry.name == notice]
                        check(len(entries) == 1 and entries[0].isfile(), "missing purego license notice")
                        text = archive.extractfile(entries[0]).read()
                        check(b"Apache License" in text or b"purego v0.11.2" in text, "incorrect purego notice")
            if system == "linux":
                check(data[:4] == b"\x7fELF" and data[4:6] == b"\x02\x01", "unexpected ELF format")
                check(struct.unpack_from("<H", data, 18)[0] == {"amd64": 62, "arm64": 183}[arch], "incorrect Linux architecture")
            else:
                check(data[:4] == b"\xcf\xfa\xed\xfe", "unexpected Mach-O format")
                check(struct.unpack_from("<I", data, 4)[0] == {"amd64": 0x1000007, "arm64": 0x100000C}[arch], "incorrect macOS architecture")
            if system == native_system and arch == native_arch:
                path = root / name
                path.write_bytes(data)
                path.chmod(0o700)
                binaries[name] = str(path)
        check("agentlab" in binaries, "no native client in the snapshot")
        check(metadata["version"].encode() in run([binaries["agentlab"], "--version"], root, environment), "incorrect packaged version")
        verify_skill(binaries["agentlab"], root, environment)
        verify_token_inspection(binaries["agentlab"], root, environment)
        verify_credential_context(binaries["agentlab"], root, environment)
        export = [binaries["agentlab"], "repo", "result"]
        if "agentlabd" in binaries:
            export = [binaries["agentlabd"], "repo-result"]
            run(export + ["--help"], root, environment)
        repo = root / "source"
        repo.mkdir()
        run(["git", "init", "--initial-branch=main"], repo, environment)
        (repo / "code.txt").write_text("before\n")
        run(["git", "add", "code.txt"], repo, environment)
        run(["git", "-c", "user.name=Release fixture", "-c", "user.email=fixture@example.invalid", "-c", "core.hooksPath=" + os.devnull, "commit", "-m", "Initial"], repo, environment)
        revision = run(["git", "rev-parse", "HEAD"], repo, environment).decode().strip()
        (repo / "code.txt").write_text("after\n")
        result = json.loads(run(export + ["--repo", str(repo), "--base-revision", revision, "--out", str(root / "results")], root, environment))
        check(b"+after" in pathlib.Path(result["patch_path"]).read_bytes(), "packaged exporter omitted changes")
        check((repo / "code.txt").read_text() == "after\n", "packaged exporter changed source content")
        if "agentlabd" in binaries:
            verify_execution(binaries["agentlabd"], root, environment, revision)
        credential_help = run([binaries["agentlab"], "token", "--help"], root, environment)
        check(b"token enroll" in credential_help and b"token renew" in credential_help,
              "packaged client omits credential enrollment or renewal discovery")
        worker_protocol = json.loads(run([binaries["agentlab"], "__setup-worker", "probe"], root, environment))
        check(worker_protocol.get("setup_worker_protocol") == 1, "packaged client lacks server setup worker protocol 1")
        verify_dashboard(binaries["agentlab"], root, environment)
        return {"archives": len(expected), "tag": tag, "version": metadata["version"],
                "native_system": native_system, "native_arch": native_arch, "setup_worker_protocol": "verified",
                "dashboard": "verified", "result_export": "verified", "agent_skill": "verified", "token_inspection": "verified", "credential_context": "verified",
                "daemon_helper": "verified" if "agentlabd" in binaries else "requires_linux"}


def verify_token_inspection(client, root, environment):
    key = root / "fixture-signing-key"
    run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(key)], root, environment)
    created = json.loads(run([client, "token", "create", "--key", str(key),
                              "--cmds", "schema.read", "--ttl", "5m", "--json"], root, environment))
    raw = created["token"].encode()
    inspected_bytes = run([client, "token", "inspect", "--token-stdin", "--json"],
                          root, environment, input_data=raw)
    inspected = json.loads(inspected_bytes)
    check(raw not in inspected_bytes, "packaged token inspection exposed the credential")
    check(inspected["verified"] is False, "unverified inspection claimed signature verification")
    check(created["fingerprint"] == inspected["issuer"], "packaged token issuer changed")
    check(len(created["revocation_id"]) == 64 and created["revocation_id"] == inspected["revocation_id"],
          "packaged revocation metadata differs between creation and inspection")


def verify_skill(client, root, environment):
    environment = dict(environment, AGENTLAB_CONTEXT="missing-fixture-context",
                       AGENTLAB_ENDPOINT="https://invalid.example",
                       XDG_CACHE_HOME=str(root / "cache"))
    info = json.loads(run([client, "skill", "show", "--json"], root, environment))
    export_command = [client, "skill", "export", "--out", str(root / "exported-skill"), "--json"]
    exported = json.loads(run(export_command, root, environment))
    directory = pathlib.Path(exported["directory"])
    check(pathlib.Path(exported["entrypoint"]) == directory / "SKILL.md", "packaged skill entrypoint is incorrect")
    check(info["manifest"] == exported["manifest"], "packaged skill metadata changed during export")
    actual = {str(path.relative_to(directory)) for path in directory.rglob("*") if path.is_file()}
    check(actual == set(info["files"]), "packaged skill files are incomplete")
    digest = hashlib.sha256()
    for name in sorted(info["files"]):
        path = directory / name
        check(path.is_file() and not path.is_symlink(), "unsafe packaged skill entry")
        data = path.read_bytes()
        digest.update(f"{len(name.encode())}:{name}:{len(data)}:".encode())
        digest.update(data)
    check(digest.hexdigest() == info["sha256"] == exported["sha256"], "packaged skill digest differs from exported bytes")
    for guide in info["manifest"]["included_skills"]:
        check(guide["file"] in actual, "packaged skill guide is absent")
    entrypoint = pathlib.Path(exported["entrypoint"])
    entrypoint.write_text("User customization\n")
    replay = subprocess.run(export_command, cwd=root, env=environment,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=15, check=False)
    check(replay.returncode != 0 and json.loads(replay.stdout)["code"] == "skill_export_changed",
          "packaged skill export accepted modified instructions")
    check(entrypoint.read_text() == "User customization\n", "packaged skill export overwrote user instructions")


def verify_execution(helper, root, environment, revision):
    report = {"schema_version": 1, "job_id": "package-test", "source_id": "a" * 64,
              "source_revision": revision, "status": "COMPLETED", "exit_code": 0,
              "duration_seconds": 1, "repository_result": {"manifest_sha256": "b" * 64}}
    archive_path = root / "execution.tar.gz"
    with tarfile.open(archive_path, "w:gz") as archive:
        for name, data in (("report.json", json.dumps(report).encode()),
                           ("agent-runner.log", b"agent finished\n")):
            member = tarfile.TarInfo(name)
            member.mode = 0o600
            member.size = len(data)
            archive.addfile(member, io.BytesIO(data))
    evidence = json.loads(run([helper, "execution-check", "--archive", str(archive_path),
                              "--job-id", "package-test", "--source-id", "a" * 64,
                              "--source-revision", revision, "--manifest-sha256", "b" * 64,
                              "--status", "COMPLETED"], root, environment))
    check(evidence["state"] == "guest_reported" and evidence["tests"] == "not_reported",
          "packaged evidence validator invented a test verdict")
    test_log = b"fixture assertion passed\n"
    report["agent_exit_code"] = 0
    report["test_runs"] = [{"command": "go test ./...", "state": "passed", "exit_code": 0,
                            "duration_seconds": 1, "log_path": "tests/001.log",
                            "log_sha256": hashlib.sha256(test_log).hexdigest()}]
    for tampered in (False, True):
        with tarfile.open(archive_path, "w:gz") as archive:
            for name, data in (("report.json", json.dumps(report).encode()),
                               ("agent-runner.log", b"agent finished\n"),
                               ("tests/001.log", b"changed\n" if tampered else test_log)):
                member = tarfile.TarInfo(name)
                member.mode = 0o600
                member.size = len(data)
                archive.addfile(member, io.BytesIO(data))
        command = [helper, "execution-check", "--archive", str(archive_path),
                   "--job-id", "package-test", "--source-id", "a" * 64,
                   "--source-revision", revision, "--manifest-sha256", "b" * 64,
                   "--status", "COMPLETED", "--test-command", "go test ./..."]
        result = subprocess.run(command, cwd=root, env=environment, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, timeout=15, check=False)
        if tampered:
            check(result.returncode != 0, "packaged validator accepted a changed test log")
        else:
            check(result.returncode == 0 and json.loads(result.stdout)["tests"] == "passed",
                  "packaged validator rejected bound test evidence")


def verify_credential_context(client, root, environment):
    token = secrets.token_urlsafe(24)
    config = root / "credential-context"
    environment = dict(environment, AGENTLAB_CONFIG_DIR=str(config), AGENTLAB_CREDENTIAL_STORE="file")

    recovered = []
    recovery_enabled = True

    class Daemon(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_GET(self):
            valid = self.headers.get("Authorization") == "Bearer " + token
            status = 200 if valid else 401
            body = {"server_identity": "credential-fixture", "sandboxes": {}, "jobs": {}} if valid else {"error": "fixture credential required"}
            if valid and self.path == "/v1/capabilities":
                body = {"server_identity": "credential-fixture", "protocol_version": 1, "api_schema_version": 1,
                        "features": {"request_server_identity": True, "workspace_fork_recovery": recovery_enabled}}
            expected = self.headers.get("X-AgentLab-Server-Identity")
            if valid and expected is not None and expected != "credential-fixture":
                status, body = 409, {"error": "server identity changed", "code": "v1/auth/server_identity_mismatch"}
            data = json.dumps(body).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_POST(self):
            valid = (self.headers.get("Authorization") == "Bearer " + token
                     and self.headers.get("X-AgentLab-Server-Identity") == "credential-fixture"
                     and self.path == "/v1/workspaces/source/forks/original/resume")
            if valid:
                recovered.append(self.path)
            data = json.dumps({"id": "original-target"} if valid else {"error": "invalid recovery request"}).encode()
            self.send_response(200 if valid else 403)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

    daemon = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Daemon)
    thread = threading.Thread(target=daemon.serve_forever, daemon=True)
    thread.start()
    endpoint = "http://127.0.0.1:" + str(daemon.server_port)
    try:
        connected = run([client, "connect", "--endpoint", endpoint, "--token-stdin", "--credential-store", "file", "--json"],
                        root, environment, input_data=token.encode())
        check(token.encode() not in connected, "packaged connect exposed a credential")
        metadata = json.loads(connected)
        check(metadata["identity_verified"] and metadata["server_identity"] == "credential-fixture", "packaged connect did not verify request identity support")
        check(metadata["context"] == "default" and metadata["credential_storage"] == "file",
              "packaged connect did not report its credential context")
        check(pathlib.Path(metadata["config_path"]) == config / "contexts.json", "packaged connect reported the wrong metadata path")
        check(not (config / "client.json").exists(), "packaged connect wrote a new legacy credential")
        index = (config / "contexts.json").read_bytes()
        check(token.encode() not in index, "credential appeared in context metadata")
        files = list((config / "credentials").glob("*.json"))
        check(len(files) == 1 and files[0].stat().st_mode & 0o777 == 0o600, "packaged fallback credential is not protected")
        old = files[0]
        status = run([client, "status", "--json"], root, environment)
        check(token.encode() not in status, "packaged status exposed its credential")
        resumed = json.loads(run([client, "workspace", "fork-resume", "source", "original", "--json"], root, environment))
        check(resumed["id"] == "original-target" and len(recovered) == 1, "packaged fork recovery lost its original binding")
        recovery_enabled = False
        unsupported = subprocess.run([client, "workspace", "fork-resume", "source", "original", "--json"],
                                     cwd=root, env=environment, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=15)
        check(unsupported.returncode != 0 and len(recovered) == 1, "packaged client ignored missing fork recovery support")
        check(json.loads(unsupported.stdout)["requirement"] == "workspace_fork_recovery", "missing fork support lacked structured output")
        # A supplied environment credential must bypass an unavailable saved
        # reference. Local help must also work without reading that reference.
        held = old.with_suffix(".held")
        old.rename(held)
        try:
            replacement_environment = dict(environment, AGENTLAB_TOKEN=token)
            replaced = run([client, "status", "--json"], root, replacement_environment)
            check(token.encode() not in replaced, "replacement status exposed its credential")
            run([client, "status", "--help"], root, environment)
            run([client, "--version"], root, environment)
            unavailable = subprocess.run([client, "status", "--json"], cwd=root, env=environment,
                                         stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=15)
            check(unavailable.returncode != 0, "missing credential silently selected another connection")
            check(token.encode() not in unavailable.stdout + unavailable.stderr,
                  "missing-credential error exposed a credential")
        finally:
            held.rename(old)
        moved = run([client, "context", "credentials", "default", "--credential-store", "file", "--json"], root, environment)
        check(token.encode() not in moved and not old.exists(), "packaged credential replacement did not retire the old record")
        cleanup = json.loads(run([client, "context", "credentials-cleanup", "--json"], root, environment))
        check(cleanup["credential_cleanup_complete"], "packaged credential cleanup did not finish")
        run([client, "disconnect", "--json"], root, environment)
        check(not list((config / "credentials").glob("*.json")), "packaged disconnect retained a credential")
        final = json.loads((config / "contexts.json").read_bytes())
        check(not final["contexts"] and not final["active"], "packaged disconnect kept an active context")
        pending = json.loads(run([client, "context", "credentials-pending", "--json"], root, environment))
        check(not pending["credential_transactions"], "packaged lifecycle left a pending credential write")
        check(not list((config / "credential-transactions").iterdir()), "packaged lifecycle left credential journal stages")
    finally:
        daemon.shutdown()
        daemon.server_close()
        thread.join(timeout=5)
        check(not thread.is_alive(), "credential fixture did not stop")


def verify_dashboard(client, root, environment):
    token = secrets.token_urlsafe(24)

    class Daemon(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def serve(self, status, body):
            data = json.dumps(body).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_GET(self):
            if self.headers.get("Authorization") != "Bearer " + token:
                self.serve(401, {"error": "fixture authentication required"})
            elif self.path == "/v1/capabilities":
                self.serve(200, {"server_identity": "release-fixture", "protocol_version": 1, "api_schema_version": 1,
                                 "features": {"request_server_identity": True}})
            else:
                self.serve(200, {"server_identity": "release-fixture", "sandboxes": {}, "jobs": {}})

        def do_POST(self):
            if self.headers.get("Authorization") != "Bearer " + token:
                self.serve(401, {"error": "fixture authentication required"})
                return
            self.serve(403, {"error": "fixture scoped denial"})

    daemon = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Daemon)
    thread = threading.Thread(target=daemon.serve_forever, daemon=True)
    thread.start()
    endpoint = "http://127.0.0.1:" + str(daemon.server_port)
    with socket.socket() as port:
        port.bind(("127.0.0.1", 0))
        address = "127.0.0.1:" + str(port.getsockname()[1])
    origin = "http://" + address
    environment = dict(environment, AGENTLAB_TOKEN=token)
    logs = root / "dashboard.log"
    process = None
    try:
        with logs.open("wb") as output:
            process = subprocess.Popen([client, "--endpoint", endpoint, "dashboard", "--listen", address], cwd=root, env=environment, stdout=output, stderr=output)
            deadline = time.monotonic() + 10
            code = None
            while time.monotonic() < deadline:
                check(process.poll() is None, "packaged dashboard stopped during startup")
                match = re.search(r"expires in five minutes\): ([A-Za-z0-9_-]+)", logs.read_text())
                if match:
                    code = match.group(1)
                    break
                time.sleep(0.02)
            check(code, "packaged dashboard did not offer browser enrollment")
            opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
            with opener.open(origin + "/", timeout=5) as response:
                data = response.read()
                check(b"<html" in data.lower() and token.encode() not in data, "dashboard HTML is absent or contains a credential")
            with opener.open(origin + "/assets/app.js", timeout=5) as response:
                data = response.read()
                check(len(data) > 1000 and token.encode() not in data, "dashboard JavaScript is absent or contains a credential")
            headers = {"Origin": origin, "X-Requested-With": "XMLHttpRequest", "Authorization": "Bearer " + code}
            request = urllib.request.Request(origin + "/auth/enroll", data=b"", headers=headers, method="POST")
            with opener.open(request, timeout=5) as response:
                cookie = response.headers.get("Set-Cookie", "")
                check(response.status == 204 and "HttpOnly" in cookie and "SameSite=Strict" in cookie, "browser enrollment did not create a protected session")
            with opener.open(origin + "/api/v1/status", timeout=5) as response:
                check(json.load(response)["server_identity"] == "release-fixture", "dashboard reached a different server")
            request = urllib.request.Request(origin + "/api/v1/jobs", data=b"{}", headers={"Origin": origin, "X-Requested-With": "XMLHttpRequest", "Content-Type": "application/json"}, method="POST")
            try:
                opener.open(request, timeout=5)
                raise RuntimeError("dashboard bypassed the scoped denial")
            except urllib.error.HTTPError as error:
                check(error.code == 403, "dashboard changed the scoped denial")
            process.terminate()
            check(process.wait(timeout=5) == 0, "dashboard shutdown failed")
            with urllib.request.urlopen(urllib.request.Request(endpoint + "/v1/status", headers={"Authorization": "Bearer " + token}), timeout=5) as response:
                check(response.status == 200, "dashboard shutdown stopped the upstream service")
            check(token not in logs.read_text(), "dashboard logged the upstream credential")
    finally:
        if process is not None and process.poll() is None:
            process.kill()
            process.wait(timeout=5)
        daemon.shutdown()
        daemon.server_close()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dist", type=pathlib.Path, default=pathlib.Path("dist"))
    arguments = parser.parse_args()
    print(json.dumps(smoke(arguments.dist.resolve()), sort_keys=True))
