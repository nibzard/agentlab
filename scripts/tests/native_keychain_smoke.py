#!/usr/bin/env python3
"""Verify a packaged macOS client against the user's unlocked default Keychain.

Only random records scoped to a disposable configuration directory are created.
Use --previous-client to check an upgrade from a separately supplied client.
No Keychain is created, unlocked, or selected by this script.
"""
import argparse
import base64
import contextlib
import hashlib
import http.server
import json
import os
import pathlib
import secrets
import shutil
import subprocess
import sys
import tempfile
import threading


def check(condition, message):
    if not condition:
        raise RuntimeError(message)


@contextlib.contextmanager
def retained_fixture_directory():
    directory = pathlib.Path(tempfile.mkdtemp(prefix="agentlab-native-keychain-"))
    try:
        yield directory
    except BaseException as error:
        raise RuntimeError("Native verification stopped. Retained fixture records: " + str(directory)) from error
    else:
        shutil.rmtree(directory)


def verify(client, previous_client=None):
    check(sys.platform == "darwin", "Native Keychain verification requires macOS")
    client = str(pathlib.Path(client).resolve(strict=True))
    previous = str(pathlib.Path(previous_client).resolve(strict=True)) if previous_client else client
    token = secrets.token_urlsafe(32)

    class API(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_GET(self):
            valid = self.headers.get("Authorization") == "Bearer " + token
            bound = self.headers.get("X-AgentLab-Server-Identity")
            valid = valid and bound in (None, "native-keychain-fixture")
            body = {"server_identity": "native-keychain-fixture", "sandboxes": {}, "jobs": {}}
            if self.path == "/v1/capabilities":
                body.update(protocol_version=1, api_schema_version=1,
                            features={"request_server_identity": True})
            if not valid:
                body = {"error": "fixture authentication failed"}
            data = json.dumps(body).encode()
            self.send_response(200 if valid else 401)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), API)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        with retained_fixture_directory() as temporary:
            root = pathlib.Path(temporary)
            config = root / "config"
            environment = {key: value for key, value in os.environ.items()
                           if not key.startswith(("AGENTLAB_", "DYLD_", "LD_"))}
            environment.update(AGENTLAB_CONFIG_DIR=str(config), AGENTLAB_CREDENTIAL_STORE="os")
            scope = hashlib.sha256(str(config).encode()).hexdigest()
            references = set()

            def command(binary, *args, input_data=None):
                result = subprocess.run([binary, *args], cwd=root, env=environment,
                                        input=input_data, stdout=subprocess.PIPE,
                                        stderr=subprocess.PIPE, timeout=15, check=False)
                check(token.encode() not in result.stdout + result.stderr,
                      "The packaged client exposed a credential")
                check(result.returncode == 0, "The packaged client could not complete " + args[0])
                return json.loads(result.stdout)

            def worker(operation, reference=""):
                request = {"version": 1, "operation": operation, "scope": scope}
                if reference:
                    request["reference"] = reference
                result = subprocess.run([client, "__agentlab-keychain-worker-v1"], cwd=root,
                                        env=dict(environment, AGENTLAB_KEYCHAIN_WORKER_V1="1"),
                                        input=json.dumps(request).encode(), stdout=subprocess.PIPE,
                                        stderr=subprocess.PIPE, timeout=8, check=False)
                check(result.returncode == 0, "The native Keychain worker did not finish")
                response = json.loads(result.stdout)
                check(response["version"] == 1 and response["scope"] == scope
                      and response["operation"] == operation
                      and response.get("reference", "") == reference,
                      "The native Keychain response lost its binding")
                return response

            def active_reference():
                index = json.loads((config / "contexts.json").read_bytes())
                item = index["contexts"]["default"]
                check(item["credential_storage"] == "macos-keychain", "The client did not select Keychain storage")
                references.add(item["credential_ref"])
                return item["credential_ref"]

            check(worker("probe")["status"] == "ok", "Provide an accessible, unlocked default Keychain")
            try:
                endpoint = "http://127.0.0.1:" + str(server.server_port)
                first = command(previous, "connect", "--endpoint", endpoint, "--token-stdin",
                                "--credential-store", "os", "--json", input_data=token.encode())
                check(first["credential_storage"] == "macos-keychain", "The initial client did not use Keychain")
                original = active_reference()
                command(client, "status", "--json")
                loaded = worker("load", original)
                check(loaded["status"] == "ok", "The packaged client cannot read its saved native record")
                data = json.loads(base64.b64decode(loaded["data"]))
                check(data["token"] == token, "The native record does not match the connected credential")
                command(client, "connect", "--endpoint", endpoint, "--token-stdin",
                        "--credential-store", "os", "--json", input_data=token.encode())
                replacement = active_reference()
                check(replacement != original, "Replacement reused a native reference")
                check(worker("load", original)["status"] == "not_found", "Replacement retained the old native record")
                command(client, "context", "credentials", "default", "--credential-store", "file", "--json")
                check(worker("load", replacement)["status"] == "not_found", "Migration retained the old native record")
                command(client, "context", "credentials", "default", "--credential-store", "os", "--json")
                final = active_reference()
                command(client, "status", "--json")
                command(client, "disconnect", "--json")
                check(worker("load", final)["status"] == "not_found", "Disconnect retained the native record")
                pending = command(client, "context", "credentials-pending", "--json")
                check(not pending["credential_transactions"], "Native lifecycle left a pending transaction")
                check(not list((config / "credentials").glob("*.json")), "Native lifecycle retained a fallback file")
            finally:
                # Inspect only this script's disposable index and intents. This also
                # preserves cleanup after an unknown connect or upgrade response.
                index_path = config / "contexts.json"
                if index_path.exists():
                    index = json.loads(index_path.read_bytes())
                    entries = list(index.get("contexts", {}).values()) + index.get("retired_credentials", [])
                    references.update(item["credential_ref"] for item in entries
                                      if item.get("credential_storage") == "macos-keychain")
                for path in (config / "credential-transactions").glob("*.json"):
                    intent = json.loads(path.read_bytes())
                    if intent.get("credential_storage") == "macos-keychain":
                        references.add(intent["credential_ref"])
                for reference in references:
                    check(worker("delete", reference)["status"] in ("ok", "not_found"),
                          "Restore Keychain access and remove the disposable fixture record: " + reference)
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
    print(json.dumps({"native_keychain": "verified", "upgrade": bool(previous_client)}))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--client", required=True, help="Path to the packaged macOS client")
    parser.add_argument("--previous-client", help="Earlier client that already supports macos-keychain")
    arguments = parser.parse_args()
    verify(arguments.client, arguments.previous_client)
