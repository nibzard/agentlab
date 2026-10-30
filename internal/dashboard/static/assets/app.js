// AgentLab Dashboard - Client-side JavaScript

(function () {
  "use strict";

  let refreshTimer = null;
  let profiles = [];
  const REFRESH_INTERVAL = 5000;
  var connectionState = "loading";
  var capabilities = null;
  var dashboardTarget = "Selected server";
  var refreshing = false;
  var reviewedSandbox = null;
  var formRevision = 0;
  var creatingSandbox = false;
  var modalReturnFocus = null;
  var detailSequence = 0;
  var activeJobID = null;
  var lastJobData = "";

  // --- API helpers ---

  var authPrompt = null;
  var authCancelled = false;

  function applyDashboardHeaders(opts) {
    opts = opts || {};
    opts.headers = Object.assign({}, opts.headers || {});
    opts.headers["X-Requested-With"] = "XMLHttpRequest";
    return opts;
  }

  async function api(path, opts) {
    opts = applyDashboardHeaders(opts);
    var res;
    try { res = await fetch("/api" + path, opts); }
    catch (cause) {
      showConnectionFailure("disconnected");
      throw new Error("Connection lost. Select Refresh to check the selected server.");
    }
    if (res.status === 401 && res.headers.get("X-Agentlab-Dashboard-Auth") === "required" && !authCancelled) {
      if (!authPrompt) {
        authPrompt = Promise.resolve().then(async function () {
          var code = window.prompt("Dashboard enrollment code:");
          if (!code) { authCancelled = true; return false; }
          var enrolled = await fetch("/auth/enroll", {
            method: "POST",
            headers: {"X-Requested-With": "XMLHttpRequest", "Authorization": "Bearer " + code}
          });
          if (!enrolled.ok) { authCancelled = true; return false; }
          return true;
        });
      }
      if (await authPrompt) res = await fetch("/api" + path, opts);
    }
    if (!res.ok && (res.status !== 404 || (opts.method && opts.method !== "GET"))) {
      var body = {};
      try { body = await res.json(); } catch (ignore) { /* Use a safe status message. */ }
      var error = new Error(body.error || body.message || "Request failed (" + res.status + ").");
      error.status = res.status;
      error.code = body.code || "";
      if (error.code === "v1/auth/server_identity_mismatch" || error.code === "v1/auth/server_identity_unavailable") {
        connectionState = "credential_blocked";
        invalidateSandboxReview();
        document.getElementById("connection-state").textContent = error.code === "v1/auth/server_identity_mismatch" ? "Server identity changed" : "Server identity unavailable";
        document.getElementById("connection-action").textContent = body.action || error.message;
        document.getElementById("btn-new-sandbox").disabled = true;
      } else if (error.code.startsWith("dashboard_credential_") || error.code === "dashboard_context_changed") {
        connectionState = "credential_blocked";
        invalidateSandboxReview();
        document.getElementById("connection-state").textContent = "Credential refresh blocked";
        document.getElementById("connection-action").textContent = body.action || error.message;
        document.getElementById("btn-new-sandbox").disabled = true;
      } else {
        if (res.status === 401) showConnectionFailure("authentication");
        if (res.status === 502 || res.status === 503 || res.status === 504) showConnectionFailure("disconnected");
      }
      throw error;
    }
    return res;
  }

  async function apiJSON(path) {
    var res = await api(path);
    if (res.status === 404) return null;
    return res.json();
  }

  // --- DOM helpers ---
  //
  // Rows are built with createElement/textContent and addEventListener so
  // untrusted values (exposure names, job IDs, states) can never break out of
  // an attribute or handler string (review F3).

  function appendTd(tr, text) {
    var td = document.createElement("td");
    if (text !== null && text !== undefined) {
      td.textContent = text;
    }
    tr.appendChild(td);
    return td;
  }

  function codeEl(text) {
    var code = document.createElement("code");
    code.textContent = text;
    return code;
  }

  function stateBadgeEl(state) {
    var span = document.createElement("span");
    span.className = "state state-" + (classSlug(state) || "unknown");
    span.textContent = state || "-";
    return span;
  }

  function addActionButton(parent, label, className, handler) {
    var btn = document.createElement("button");
    btn.type = "button";
    btn.className = className;
    btn.textContent = label;
    btn.addEventListener("click", handler);
    parent.appendChild(btn);
  }

  function errorRow(tbody, colspan, message) {
    tbody.innerHTML = "";
    var tr = document.createElement("tr");
    var td = document.createElement("td");
    td.colSpan = colspan;
    td.className = "error";
    td.textContent = connectionState === "disconnected" || connectionState === "authentication" ? "Data is unavailable. See the connection status above." : "Failed to load: " + message;
    tr.appendChild(td);
    tbody.appendChild(tr);
  }

  // isHttpUrl reports whether a URL is safe to render as a link. It keeps
  // untrusted URLs (for example a javascript: pseudo-URL) out of href.
  function isHttpUrl(u) {
    try {
      var protocol = new URL(String(u), window.location.origin).protocol;
      return protocol === "http:" || protocol === "https:";
    } catch (e) {
      return false;
    }
  }

  // --- Time formatting ---

  function timeAgo(ts) {
    if (!ts) return "-";
    var d = new Date(ts);
    if (isNaN(d.getTime())) return esc(ts);
    var diff = Date.now() - d.getTime();
    var mins = Math.floor(diff / 60000);
    if (mins < 1) return "just now";
    if (mins < 60) return mins + "m ago";
    var hours = Math.floor(mins / 60);
    if (hours < 24) return hours + "h ago";
    var days = Math.floor(hours / 24);
    return days + "d ago";
  }

  function shortTime(ts) {
    if (!ts) return "-";
    var d = new Date(ts);
    if (isNaN(d.getTime())) return esc(ts);
    return (
      String(d.getHours()).padStart(2, "0") +
      ":" +
      String(d.getMinutes()).padStart(2, "0") +
      ":" +
      String(d.getSeconds()).padStart(2, "0")
    );
  }

  // esc encodes the five characters that can break out of an HTML text node,
  // a double-quoted attribute, or a single-quoted attribute. Never interpolate
  // it into JavaScript string contexts (inline handlers); attach listeners
  // instead.
  function esc(s) {
    if (!s) return "";
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  // classSlug reduces an untrusted value to the characters that are safe
  // inside a class attribute: [a-z0-9-].
  function classSlug(s) {
    return String(s || "")
      .toLowerCase()
      .replace(/[^a-z0-9-]/g, "");
  }

  // --- Tabs ---

  function initTabs() {
    document.querySelectorAll(".tab").forEach(function (tab) {
      tab.addEventListener("click", function (e) {
        e.preventDefault();
        switchView(tab.dataset.view);
      });
    });
  }

  function switchView(name) {
    document.querySelectorAll(".tab").forEach(function (t) {
      t.classList.toggle("active", t.dataset.view === name);
      if (t.dataset.view === name) t.setAttribute("aria-current", "page");
      else t.removeAttribute("aria-current");
    });
    document.querySelectorAll(".view").forEach(function (v) {
      v.classList.toggle("active", v.id === "view-" + name);
    });
  }

  // --- Load profiles ---

  async function loadProfiles() {
    var sel = document.getElementById("select-profile");
    sel.disabled = true;
    sel.innerHTML = "";
    profiles = [];
    try {
      var data = await apiJSON("/v1/profiles");
      profiles = (data && data.profiles) || [];
      profiles.forEach(function (p) {
        var opt = document.createElement("option");
        opt.value = p.name;
        opt.textContent = p.name + (p.type ? " (" + p.type + ")" : "");
        sel.appendChild(opt);
      });
      if (!profiles.length) throw new Error("No profiles are available. Ask the server operator to configure a profile.");
      sel.disabled = false;
      document.getElementById("sandbox-plan-state").textContent = "Select a profile, then check the plan.";
    } catch (e) {
      document.getElementById("sandbox-plan-state").textContent = e.message;
    }
    document.getElementById("btn-check-sandbox").disabled = sel.disabled;
  }

  function permissionAllowed(command) {
    if (!capabilities || connectionState !== "ready") return false;
    var access = capabilities.authorization || {};
    if (access.method === "local_trusted" || access.method === "legacy-token") return true;
    return (access.commands || []).some(function (grant) {
      return grant === "*" || grant === command || command.startsWith(grant + ".");
    });
  }

  function invalidateSandboxReview() {
    formRevision++;
    reviewedSandbox = null;
    document.getElementById("sandbox-plan-review").textContent = "";
    document.getElementById("btn-create-sandbox").disabled = true;
  }

  function showConnectionFailure(kind) {
    connectionState = kind;
    invalidateSandboxReview();
    document.getElementById("connection-state").textContent = kind === "authentication" ? "Authentication required" : "Disconnected";
    document.getElementById("connection-action").textContent = kind === "authentication"
      ? "Restore the selected credential or browser enrollment, or finish its pending renewal. Then select Refresh. Explicit credential overrides require a dashboard restart."
      : "Check the server and network, then select Refresh. Server work continues while this dashboard is disconnected.";
    document.getElementById("btn-new-sandbox").disabled = true;
  }

  async function loadConnection() {
    connectionState = "loading";
    try {
      var data = await apiJSON("/v1/capabilities");
      if (!data || !data.server_identity || data.protocol_version !== 1 || data.api_schema_version !== 1) throw new Error("This server does not provide identity and permissions. Update the server before creating a sandbox here.");
      if (capabilities && capabilities.server_identity !== data.server_identity) throw new Error("The server identity changed. Close this dashboard and verify the selected CLI context.");
      var selected = await apiJSON("/connection");
      dashboardTarget = selected && selected.target ? selected.target : "Selected connection";
      capabilities = data;
      connectionState = "ready";
      var access = data.authorization || {};
      document.getElementById("connection-state").textContent = "Connected";
      document.getElementById("connection-identity").textContent = "Server: " + dashboardTarget + ". Identity: " + data.server_identity;
      var grants = access.method === "local_trusted" || access.method === "legacy-token" ? "full access" : ((access.commands || []).join(", ") || "none");
      document.getElementById("connection-permissions").textContent = "Access: " + (access.method || "unknown") + ". Permissions: " + grants + ". Scope: " + ((access.scope || []).join(", ") || "all permitted resources") + (access.expires_at ? ". Expires: " + new Date(access.expires_at * 1000).toLocaleString() : "") + ". Credential source: " + ((selected && selected.credential_source) || "startup credential") + ".";
      var canCreate = permissionAllowed("sandbox.create") && permissionAllowed("sandbox.validate") && permissionAllowed("profile.read") && (access.scope || []).every(function (scope) { return scope === "*"; });
      document.getElementById("btn-new-sandbox").disabled = !canCreate;
      document.getElementById("connection-action").textContent = canCreate ? "Review this server before each change." : "Your credential cannot create new sandboxes here. Ask the operator for the required permissions and scope.";
      return true;
    } catch (e) {
      if (connectionState !== "authentication" && connectionState !== "disconnected" && connectionState !== "credential_blocked") {
        connectionState = "blocked";
        invalidateSandboxReview();
        document.getElementById("connection-state").textContent = "Connection blocked";
        document.getElementById("connection-action").textContent = e.message;
        document.getElementById("btn-new-sandbox").disabled = true;
      }
      return false;
    }
  }

  // --- Load data ---

  async function loadStatus() {
    try {
      var data = await apiJSON("/v1/status");
      if (!data) return;
      document.getElementById("status-daemon").textContent = "Daemon: OK";
      var sb = data.sandboxes || {};
      var running = (sb["RUNNING"] || 0) + (sb["READY"] || 0);
      var total = 0;
      for (var k in sb) {
        if (sb.hasOwnProperty(k) && k !== "DESTROYED") total += sb[k];
      }
      document.getElementById("status-sandboxes").textContent =
        "Sandboxes: " + total + " (" + running + " active)";
      var jb = data.jobs || {};
      var jobActive = (jb["RUNNING"] || 0) + (jb["QUEUED"] || 0);
      document.getElementById("status-jobs").textContent =
        "Jobs: " + jobActive + " active";

      // Pool status badge
      await loadPoolStatus();
    } catch (e) {
      document.getElementById("status-daemon").textContent =
        "Daemon: unavailable";
    }
  }

  async function loadPoolStatus() {
    try {
      var data = await apiJSON("/v1/pool/status");
      if (!data) {
        document.getElementById("pool-badge").style.display = "none";
        return;
      }
      var badge = document.getElementById("pool-badge");
      var cpuPct = 0;
      var memPct = 0;
      if (data.config && data.config.total_cores > 0) {
        cpuPct = Math.round((data.utilization_cpu || 0) * 100);
      }
      if (data.config && data.config.total_memory_mb > 0) {
        memPct = Math.round(
          (data.utilization_memory || 0) * 100
        );
      }
      badge.textContent = "Pool: " + cpuPct + "% CPU / " + memPct + "% RAM";
      badge.className = "pool-badge";
      if (cpuPct > 90 || memPct > 90) {
        badge.classList.add("pool-full");
      } else if (cpuPct > 70 || memPct > 70) {
        badge.classList.add("pool-warn");
      }
      badge.style.display = "inline";
    } catch (e) {
      document.getElementById("pool-badge").style.display = "none";
    }
  }

  async function loadHostInfo() {
    try {
      var data = await apiJSON("/v1/host");
      if (!data) return;
      var el = document.getElementById("status-host");
      var parts = [];
      if (data.version) parts.push("v" + data.version);
      if (data.agent_subnet) parts.push("subnet: " + data.agent_subnet);
      el.textContent = parts.join(" | ");
    } catch (e) {
      // host info is optional, ignore errors
    }
  }

  async function loadSandboxes() {
    var tbody = document.getElementById("sandbox-list");
    var empty = document.getElementById("sandbox-empty");
    try {
      var data = await apiJSON("/v1/sandboxes");
      var list = (data && data.sandboxes) || [];
      if (list.length === 0) {
        tbody.innerHTML = "";
        empty.style.display = "block";
        return;
      }
      empty.style.display = "none";
      tbody.innerHTML = "";
      list.forEach(function (sb) {
        var vmid = sb.vmid;
        var tr = document.createElement("tr");
        appendTd(tr, String(sb.vmid));
        appendTd(tr, sb.name);
        appendTd(tr, sb.profile);
        appendTd(tr, sb.type || "vm");
        appendTd(tr).appendChild(stateBadgeEl(sb.state));
        appendTd(tr).appendChild(codeEl(sb.ip || "-"));
        appendTd(tr, timeAgo(sb.created_at));
        appendActions(tr, sb);
        tbody.appendChild(tr);
      });
    } catch (e) {
      errorRow(tbody, 8, e.message);
    }
  }

  function appendActions(tr, sb) {
    var td = document.createElement("td");
    var group = document.createElement("div");
    group.className = "action-group";
    var vmid = sb.vmid;
    var state = (sb.state || "").toLowerCase();
    addActionButton(group, "Detail", "btn btn-sm", function () {
      showDetail(vmid);
    });
    if (state === "running" || state === "ready") {
      addActionButton(group, "Stop", "btn btn-sm", function () {
        stopSandbox(vmid);
      });
      addActionButton(group, "Expose", "btn btn-sm", function () {
        showExpose(vmid);
      });
      addActionButton(group, "Snapshot", "btn btn-sm", function () {
        showSnapshot(vmid);
      });
    } else if (state === "stopped") {
      addActionButton(group, "Start", "btn btn-sm", function () {
        startSandbox(vmid);
      });
    }
    addActionButton(group, "Destroy", "btn btn-sm btn-danger", function () {
      destroySandbox(vmid);
    });
    td.appendChild(group);
    tr.appendChild(td);
  }

  async function loadJobs() {
    var tbody = document.getElementById("job-list");
    var empty = document.getElementById("job-empty");
    try {
      var data = await apiJSON("/v1/status");
      var failures = (data && data.recent_failure_digest) || [];
      var jobTimelines = (data && data.job_timelines) || {};
      var timelineList = [];
      for (var jid in jobTimelines) {
        if (jobTimelines.hasOwnProperty(jid)) {
          timelineList.push(jobTimelines[jid]);
        }
      }

      // Combine timeline entries and failure digests into a unified view.
      var seen = {};
      var rows = [];

      // Add timeline entries first (non-duplicate).
      timelineList.forEach(function (tl) {
        var key = tl.job_id;
        if (!seen[key]) {
          seen[key] = true;
          rows.push({
            job_id: tl.job_id,
            repo: "-",
            task: "-",
            profile: "-",
            status: tl.status,
            created: tl.started_at || "",
            error: tl.last_failure_message || "",
          });
        }
      });

      // Add failures not already shown.
      failures.forEach(function (f) {
        var key = f.job_id || f.event_id;
        if (!seen[key]) {
          seen[key] = true;
          rows.push({
            job_id: f.job_id || f.event_id,
            repo: f.kind || "-",
            task: f.stage || "-",
            profile: "-",
            status: "failed",
            created: f.ts || "",
            error: f.error || f.message || "",
          });
        }
      });

      if (rows.length === 0) {
        tbody.innerHTML = "";
        empty.style.display = "block";
        return;
      }
      empty.style.display = "none";
      tbody.innerHTML = "";
      rows.forEach(function (r) {
        var jobId = r.job_id;
        var tr = document.createElement("tr");
        appendTd(tr).appendChild(codeEl(String(jobId).substring(0, 8)));
        appendTd(tr, r.repo);
        appendTd(tr, r.task);
        appendTd(tr, r.profile);
        appendTd(tr).appendChild(stateBadgeEl(r.status));
        appendTd(tr, timeAgo(r.created));
        var actions = document.createElement("td");
        {
          addActionButton(actions, "View", "btn btn-sm", function () {
            showJobDetail(jobId);
          });
        }
        tr.appendChild(actions);
        tbody.appendChild(tr);
      });
    } catch (e) {
      errorRow(tbody, 7, e.message);
    }
  }

  async function loadWorkspaces() {
    var tbody = document.getElementById("workspace-list");
    var empty = document.getElementById("workspace-empty");
    try {
      var data = await apiJSON("/v1/workspaces");
      var list = (data && data.workspaces) || [];
      if (list.length === 0) {
        tbody.innerHTML = "";
        empty.style.display = "block";
        return;
      }
      empty.style.display = "none";
      tbody.innerHTML = list
        .map(function (ws) {
          return (
            "<tr>" +
            "<td><code>" +
            esc(ws.id) +
            "</code></td>" +
            "<td>" +
            esc(ws.name) +
            "</td>" +
            "<td>" +
            esc(ws.size_gb) +
            " GB</td>" +
            "<td>" +
            (ws.attached_vmid
              ? '<code>' + esc(ws.attached_vmid) + "</code>"
              : "-") +
            "</td>" +
            "<td>" +
            timeAgo(ws.created_at) +
            "</td>" +
            "</tr>"
          );
        })
        .join("");
    } catch (e) {
      tbody.innerHTML =
        '<tr><td colspan="5" class="error">Failed to load: ' +
        esc(e.message) +
        "</td></tr>";
    }
  }

  async function loadExposures() {
    var tbody = document.getElementById("exposure-list");
    var empty = document.getElementById("exposure-empty");
    try {
      var data = await apiJSON("/v1/exposures");
      var list = (data && data.exposures) || [];
      if (list.length === 0) {
        tbody.innerHTML = "";
        empty.style.display = "block";
        return;
      }
      empty.style.display = "none";
      tbody.innerHTML = "";
      list.forEach(function (ex) {
        var name = ex.name;
        var tr = document.createElement("tr");
        appendTd(tr, name);
        appendTd(tr, String(ex.vmid));
        appendTd(tr, String(ex.port));
        var urlTd = document.createElement("td");
        if (ex.url && isHttpUrl(ex.url)) {
          var a = document.createElement("a");
          a.className = "exposure-url";
          a.href = ex.url;
          a.target = "_blank";
          a.rel = "noopener noreferrer";
          a.textContent = ex.url;
          urlTd.appendChild(a);
        } else {
          urlTd.textContent = (ex.url && String(ex.url)) || "-";
        }
        tr.appendChild(urlTd);
        appendTd(tr).appendChild(stateBadgeEl(ex.state));
        appendTd(tr, timeAgo(ex.created_at));
        var actions = document.createElement("td");
        addActionButton(actions, "Remove", "btn btn-sm btn-danger", function () {
          removeExposure(name);
        });
        tr.appendChild(actions);
        tbody.appendChild(tr);
      });
    } catch (e) {
      errorRow(tbody, 7, e.message);
    }
  }

  async function loadEvents() {
    var container = document.getElementById("events-list");
    var empty = document.getElementById("event-empty");
    try {
      var status = await apiJSON("/v1/status");
      var workspaces = await apiJSON("/v1/workspaces");
      var scopes = Object.keys((status && status.job_timelines) || {}).map(function (id) {
        return "scope_type=job&scope_id=" + encodeURIComponent(id);
      });
      ((workspaces && workspaces.workspaces) || []).forEach(function (ws) {
        scopes.push("scope_type=workspace&scope_id=" + encodeURIComponent(ws.id));
      });
      var batches = await Promise.all(scopes.map(async function (scope) {
        var data = await apiJSON("/v1/messages?" + scope + "&limit=20");
        return (data && data.messages) || [];
      }));
      var messages = [].concat.apply([], batches);
      var failures = (status && status.recent_failure_digest) || [];

      // Combine into unified timeline
      var events = [];

      messages.forEach(function (m) {
        events.push({
          ts: m.ts,
          kind: m.kind || "message",
          scope: m.scope_type + ":" + m.scope_id,
          msg: m.text || "",
        });
      });

      failures.forEach(function (f) {
        events.push({
          ts: f.ts,
          kind: f.error ? "failed" : f.kind || "event",
          scope: f.job_id ? "job:" + f.job_id : f.sandbox_vmid ? "sb:" + f.sandbox_vmid : "",
          msg: f.error || f.message || "",
        });
      });

      // Sort by timestamp descending
      events.sort(function (a, b) {
        return new Date(b.ts || 0) - new Date(a.ts || 0);
      });

      if (events.length === 0) {
        container.innerHTML = "";
        empty.style.display = "block";
        return;
      }
      empty.style.display = "none";
      container.innerHTML = events
        .slice(0, 100)
        .map(function (ev) {
          var kindClass = "event-kind kind-" + (classSlug(ev.kind) || "event");
          return (
            '<div class="event-row">' +
            '<span class="event-time">' +
            shortTime(ev.ts) +
            "</span>" +
            '<span class="' +
            kindClass +
            '">' +
            esc(ev.kind || "-") +
            "</span>" +
            '<span class="event-scope">' +
            esc(ev.scope || "-") +
            "</span>" +
            '<span class="event-msg">' +
            esc(ev.msg || "-") +
            "</span>" +
            "</div>"
          );
        })
        .join("");
    } catch (e) {
      container.innerHTML =
        '<div class="empty-state">Failed to load events: ' +
        esc(e.message) +
        "</div>";
    }
  }

  async function refreshAll() {
    if (refreshing) return;
    refreshing = true;
    try {
      if (!await loadConnection()) return;
      await Promise.all([loadStatus(), loadSandboxes(), loadJobs(), loadWorkspaces(), loadExposures(), loadEvents()]);
      if (activeJobID) await showJobDetail(activeJobID, true);
    } finally { refreshing = false; }
  }

  // --- Actions ---
  //
  // Handlers are plain local functions wired with addEventListener; no
  // function needs to be global, so no inline handler can reach them.

  async function startSandbox(vmid) {
    try {
      await api("/v1/sandboxes/" + vmid + "/start", { method: "POST" });
      await loadSandboxes();
    } catch (e) {
      alert("Failed to start: " + e.message);
    }
  }

  async function stopSandbox(vmid) {
    if (!confirm("Stop sandbox " + vmid + " on server " + capabilities.server_identity + "?")) return;
    try {
      await api("/v1/sandboxes/" + vmid + "/stop", { method: "POST" });
      await loadSandboxes();
    } catch (e) {
      alert("Failed to stop: " + e.message);
    }
  }

  async function destroySandbox(vmid) {
    if (!confirm("Destroy sandbox " + vmid + " on server " + capabilities.server_identity + "? This cannot be undone."))
      return;
    try {
      await api("/v1/sandboxes/" + vmid + "/destroy", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ force: true }),
      });
      await loadSandboxes();
    } catch (e) {
      alert("Failed to destroy: " + e.message);
    }
  }

  async function showDetail(vmid) {
    activeJobID = null;
    var sequence = ++detailSequence;
    document.getElementById("job-results").innerHTML = "";
    document.getElementById("detail-summary").textContent = "Loading sandbox details.";
    document.getElementById("detail-json").textContent = "";
    openModal("modal-sandbox-detail");
    try {
      var data = await apiJSON("/v1/sandboxes/" + vmid);
      if (sequence !== detailSequence) return;
      document.getElementById("detail-title").textContent =
        "Sandbox " + vmid + " \u2014 " + (data.name || "");

      // Build summary fields
      var fields = [
        { label: "VMID", value: data.vmid },
        { label: "Name", value: data.name },
        { label: "Profile", value: data.profile },
        { label: "State", value: data.state },
        { label: "Type", value: data.type || "vm" },
        { label: "IP", value: data.ip || "-" },
        { label: "Keepalive", value: data.keepalive ? "yes" : "no" },
        {
          label: "Lease Expires",
          value: data.lease_expires_at ? timeAgo(data.lease_expires_at) : "-",
        },
        { label: "Created", value: timeAgo(data.created_at) },
      ];
      if (data.resources) {
        fields.push({ label: "CPUs", value: data.resources.cores || "-" });
        fields.push({
          label: "Memory",
          value: data.resources.memory_mb ? data.resources.memory_mb + " MB" : "-",
        });
      }
      if (data.health) {
        fields.push({
          label: "Healthy",
          value: data.health.healthy ? "yes" : "no",
        });
        if (data.health.failure_count > 0) {
          fields.push({
            label: "Failures",
            value: data.health.failure_count,
          });
        }
      }

      var summaryHtml = fields
        .map(function (f) {
          return (
            '<div class="detail-field">' +
            '<div class="detail-label">' +
            esc(f.label) +
            "</div>" +
            '<div class="detail-value">' +
            esc(String(f.value)) +
            "</div>" +
            "</div>"
          );
        })
        .join("");
      document.getElementById("detail-summary").innerHTML = summaryHtml;
      document.getElementById("detail-json").textContent = JSON.stringify(
        data,
        null,
        2
      );
      openModal("modal-sandbox-detail");
    } catch (e) {
      alert("Failed to load details: " + e.message);
    }
  }

  async function showJobDetail(jobId, quiet) {
    var sequence = quiet ? detailSequence : ++detailSequence;
    if (!quiet) {
      activeJobID = jobId;
      lastJobData = "";
      document.getElementById("job-results").innerHTML = "";
      document.getElementById("detail-summary").textContent = "Loading job details.";
      document.getElementById("detail-json").textContent = "";
      openModal("modal-sandbox-detail");
    }
    try {
      var data = await apiJSON("/v1/jobs/" + encodeURIComponent(jobId));
      if (sequence !== detailSequence || activeJobID !== jobId) return;
      var snapshot = JSON.stringify(data);
      // Preserve focus on cancellation and artifact controls during polling.
      if (quiet && (snapshot === lastJobData || document.getElementById("job-results").contains(document.activeElement))) return;
      lastJobData = snapshot;
      if (!data) {
        alert("Job not found");
        return;
      }
      document.getElementById("detail-title").textContent =
        "Job " + jobId.substring(0, 8) + " \u2014 " + (data.repo_url || "");
      var fields = [
        { label: "ID", value: data.id },
        { label: "Repo", value: data.repo_url },
        { label: "Ref", value: data.ref },
        { label: "Profile", value: data.profile },
        { label: "Status", value: data.status },
        { label: "Task", value: data.task || "-" },
        { label: "Mode", value: data.mode || "-" },
        { label: "Sandbox VMID", value: data.sandbox_vmid || "-" },
        { label: "Created", value: timeAgo(data.created_at) },
      ];
      var summaryHtml = fields
        .map(function (f) {
          return (
            '<div class="detail-field">' +
            '<div class="detail-label">' +
            esc(f.label) +
            "</div>" +
            '<div class="detail-value">' +
            esc(String(f.value)) +
            "</div>" +
            "</div>"
          );
        })
        .join("");
      document.getElementById("detail-summary").innerHTML = summaryHtml;
      document.getElementById("detail-json").textContent = JSON.stringify(
        data,
        null,
        2
      );
      await renderJobResults(data, sequence);
    } catch (e) {
      if (!quiet) document.getElementById("detail-summary").textContent = "Cannot load this job. " + e.message;
    }
  }

  async function renderJobResults(job, sequence) {
    var container = document.getElementById("job-results");
    container.innerHTML = "";
    function paragraph(text) {
      var p = document.createElement("p");
      p.textContent = text;
      container.appendChild(p);
      return p;
    }
    function heading(text) {
      var h = document.createElement("h4");
      h.textContent = text;
      container.appendChild(h);
    }
    heading("Progress and results");
    var timeline = job.timeline || {};
    paragraph("Job: " + job.status + (timeline.last_event_kind ? ". Latest event: " + timeline.last_event_kind : "") + ". Updated: " + (job.updated_at || "not reported"));
    var cancellation = job.cancellation || (job.result || {}).cancellation;
    if (cancellation) paragraph("Cancellation: " + cancellation.state + ". Work stopped: " + (cancellation.work_stopped ? "verified" : "not verified") + ". Cleanup: " + cancellation.cleanup_status + ". " + (cancellation.action || ""));
    if (!["COMPLETED", "FAILED", "TIMEOUT", "CANCELLED"].includes(String(job.status).toUpperCase()) && permissionAllowed("job.cancel")) {
      addActionButton(container, "Cancel job", "btn btn-danger", async function () {
        if (!confirm("Cancel job " + job.id + " on server " + capabilities.server_identity + "? Inspect retained resources after cancellation.")) return;
        this.disabled = true;
        try {
          var response = await api("/v1/jobs/" + encodeURIComponent(job.id) + "/cancel", {method: "POST", headers: {"Content-Type": "application/json"}, body: "{}"});
          if (!response.ok) throw new Error("Cancellation is not supported by this server.");
          await showJobDetail(job.id);
        } catch (e) { paragraph("Cancellation requires inspection: " + e.message); }
        finally { this.disabled = false; }
      });
    }
    var result = job.result || {};
    var receipt = result.repository_result;
    var execution = receipt && receipt.execution;
    heading("Test commands");
    var runs = (execution && execution.test_runs) || result.test_runs || [];
    paragraph(execution ? "Execution evidence: " + execution.state + ". Test result: " + execution.tests + ". Command results do not establish test coverage." : "Test evidence is not verified. Job completion alone does not establish that tests passed.");
    if (!runs.length) paragraph("No test command results are reported.");
    runs.forEach(function (run) {
      paragraph(run.command + ": " + run.state + (run.exit_code !== undefined ? " (exit " + run.exit_code + ")" : "") + (run.reason ? ". " + run.reason : ""));
    });
    heading("Remaining resources");
    var resources = (cancellation && cancellation.resources) || (receipt && receipt.resources) || [];
    if (receipt) paragraph("Source result: " + receipt.state + ". Cleanup: " + receipt.cleanup_status + ". " + (receipt.action || ""));
    if (!resources.length) paragraph((cancellation && cancellation.cleanup_status === "verified") || (receipt && receipt.cleanup_status === "verified") ? "The server reports verified cleanup with no retained resources." : "No retained resource list is reported. Inspect the sandbox and workspace before cleanup.");
    resources.forEach(function (resource) {
      paragraph(resource.kind + " " + resource.id + ": " + resource.state + (resource.expires_at ? ". Lease expires: " + resource.expires_at : ". Lease expiry is not reported."));
    });
    heading("Artifacts and logs");
    try {
      var data = await apiJSON("/v1/jobs/" + encodeURIComponent(job.id) + "/artifacts");
      if (sequence !== undefined && sequence !== detailSequence) return;
      var artifacts = (data && data.artifacts) || [];
      if (!artifacts.length) paragraph("No artifacts are available yet.");
      artifacts.forEach(function (artifact) {
        var line = paragraph(artifact.name + " (" + artifact.size_bytes + " bytes). ");
        var link = document.createElement("a");
        link.textContent = artifact.path && artifact.path.endsWith("/execution.tar.gz") ? "Download execution logs and report" : "Download artifact";
        link.href = "/api/v1/jobs/" + encodeURIComponent(job.id) + "/artifacts/download?path=" + encodeURIComponent(artifact.path);
        link.download = artifact.name || "artifact";
        line.appendChild(link);
      });
    } catch (e) { if (sequence === undefined || sequence === detailSequence) paragraph("Artifacts are unavailable: " + e.message); }
    if (sequence === undefined || sequence === detailSequence) renderExecutionLogs(job, container, sequence);
  }

  function renderExecutionLogs(job, container, sequence) {
    var section = document.createElement("section");
    section.className = "execution-logs";
    section.setAttribute("aria-label", "Execution logs");
    container.appendChild(section);
    function message(text) {
      var p = document.createElement("p"); p.textContent = text; section.appendChild(p); return p;
    }
    var features = (capabilities && capabilities.features) || {};
    if (!features.execution_inspection) {
      message("Inline logs require a server with execution inspection. Download the archive to inspect available logs.");
      return;
    }
    if (!permissionAllowed("job.artifacts")) { message("Your credential cannot read execution logs."); return; }
    var receipt = (job.result || {}).repository_result;
    if (!receipt || receipt.state !== "verified" || !["COMPLETED", "FAILED", "TIMEOUT"].includes(String(job.status).toUpperCase())) { message("Inline logs become available after the job ends and the server verifies the execution archive."); return; }
    var status = message("Inspect the runner log, test logs, or execution report.");
    status.setAttribute("role", "status");
    var button = document.createElement("button");
    button.type = "button"; button.className = "btn btn-sm"; button.textContent = "View execution logs";
    section.appendChild(button);
    var requestSequence = 0;
    function current(request) { return (sequence === undefined || sequence === detailSequence) && request === requestSequence; }
    button.addEventListener("click", async function () {
      var request = ++requestSequence;
      button.disabled = true; status.textContent = "Checking execution archive.";
      try {
        var index = await apiJSON("/v1/jobs/" + encodeURIComponent(job.id) + "/execution");
        if (!current(request)) return;
        if (!index || index.state !== "verified" || index.job_id !== job.id || index.server_identity !== capabilities.server_identity || !/^[0-9a-f]{64}$/.test(index.archive_sha256 || "") || index.manifest_sha256 !== receipt.manifest_sha256) throw new Error("Execution log identity changed. Reopen this job to inspect its current results.");
        var entries = index.entries || [];
        var readable = entries.filter(function (entry) { return entry.text && Number.isSafeInteger(entry.size_bytes) && entry.size_bytes >= 0; });
        if (!readable.length) throw new Error("No UTF-8 execution logs are available. Download the archive to inspect it.");
        var label = document.createElement("label"); label.textContent = "Execution log";
        var select = document.createElement("select"); select.setAttribute("aria-label", "Execution log"); label.appendChild(select); section.appendChild(label);
        readable.forEach(function (entry) { var option = document.createElement("option"); option.value = entry.path; option.textContent = entry.path + " (" + entry.size_bytes + " bytes)"; select.appendChild(option); });
        select.value = readable.some(function (entry) { return entry.path === "agent-runner.log"; }) ? "agent-runner.log" : readable[0].path;
        var output = document.createElement("pre"); output.className = "execution-log-text"; output.tabIndex = 0; output.setAttribute("aria-label", "Execution log text"); section.appendChild(output);
        var controls = document.createElement("div"); controls.className = "execution-log-controls"; section.appendChild(controls);
        var previous = document.createElement("button"); previous.type = "button"; previous.textContent = "Previous chunk"; previous.className = "btn btn-sm"; controls.appendChild(previous);
        var next = document.createElement("button"); next.type = "button"; next.textContent = "Next chunk"; next.className = "btn btn-sm"; controls.appendChild(next);
        var reload = document.createElement("button"); reload.type = "button"; reload.textContent = "Reload chunk"; reload.className = "btn btn-sm"; controls.appendChild(reload);
        var offsets = [0], position = 0, nextOffset = 0;
        async function read(offset) {
          var pageRequest = ++requestSequence;
          previous.disabled = true; next.disabled = true;
          output.textContent = ""; status.textContent = "Loading execution log.";
          var entry = readable.find(function (item) { return item.path === select.value; });
          var selectedPath = select.value;
          try {
            var page = await apiJSON("/v1/jobs/" + encodeURIComponent(job.id) + "/execution?path=" + encodeURIComponent(selectedPath) + "&offset=" + offset + "&limit=16384&archive_sha256=" + encodeURIComponent(index.archive_sha256));
            if (!current(pageRequest)) return;
            var chunk = page && page.chunk;
            if (!page || page.job_id !== job.id || page.state !== "verified" || page.server_identity !== index.server_identity || page.manifest_sha256 !== index.manifest_sha256 || page.archive_sha256 !== index.archive_sha256 || !chunk || chunk.path !== selectedPath || chunk.offset !== offset || typeof chunk.text !== "string" || !Number.isSafeInteger(chunk.next_offset) || chunk.next_offset < offset || chunk.next_offset > entry.size_bytes || chunk.next_offset - offset > 16384 || new TextEncoder().encode(chunk.text).length !== chunk.next_offset - offset || typeof chunk.eof !== "boolean" || chunk.eof !== (chunk.next_offset === entry.size_bytes) || (!chunk.eof && chunk.next_offset === offset)) throw new Error("Execution log changed or returned an invalid chunk. Reopen this job before continuing.");
            // Guest output is text. It must never become HTML or a terminal command.
            output.textContent = chunk.text;
            nextOffset = chunk.next_offset;
            status.textContent = selectedPath + ": bytes " + offset + " to " + chunk.next_offset + " of " + entry.size_bytes + (chunk.eof ? ". End of log." : ". More text is available.");
            previous.disabled = position === 0; next.disabled = chunk.eof;
          } catch (error) { if (current(pageRequest)) status.textContent = "Cannot read execution log. " + error.message; }
        }
        reload.addEventListener("click", function () { return read(offsets[position]); });
        select.addEventListener("change", function () { offsets = [0]; position = 0; return read(0); });
        next.addEventListener("click", function () { offsets = offsets.slice(0, position + 1); offsets.push(nextOffset); position++; return read(nextOffset); });
        previous.addEventListener("click", function () { position--; return read(offsets[position]); });
        button.hidden = true;
        await read(0);
      } catch (error) { if (current(request)) status.textContent = "Cannot open execution logs. " + error.message; }
      finally { if (current(request)) button.disabled = false; }
    });
  }

  // --- Expose ---

  var exposeVMID = null;

  function showExpose(vmid) {
    exposeVMID = vmid;
    document.getElementById("expose-vmid").textContent = "#" + vmid + " on server " + capabilities.server_identity;
    document.getElementById("form-expose").reset();
    openModal("modal-expose");
  }

  // --- Snapshot ---

  var snapshotVMID = null;

  function showSnapshot(vmid) {
    snapshotVMID = vmid;
    document.getElementById("snapshot-vmid").textContent = "#" + vmid + " on server " + capabilities.server_identity;
    document.getElementById("form-snapshot").reset();
    openModal("modal-snapshot");
  }

  // --- Remove exposure ---

  async function removeExposure(name) {
    if (!confirm("Remove exposure " + name + " on server " + capabilities.server_identity + "?")) return;
    try {
      await api("/v1/exposures/" + encodeURIComponent(name), {
        method: "DELETE",
      });
      await loadExposures();
    } catch (e) {
      alert("Failed to remove exposure: " + e.message);
    }
  }

  function openModal(id) {
    var modal = document.getElementById(id);
    if (modal.style.display !== "flex") modalReturnFocus = document.activeElement;
    modal.style.display = "flex";
    document.querySelector("main").inert = true;
    document.querySelector("nav").inert = true;
    var first = modal.querySelector("input, select, button, summary, a[href]");
    if (first) first.focus();
  }

  function closeModal(id) {
    if (id === "modal-new-sandbox" && creatingSandbox) return;
    document.getElementById(id).style.display = "none";
    if (id === "modal-sandbox-detail") { detailSequence++; activeJobID = null; }
    document.querySelector("main").inert = false;
    document.querySelector("nav").inert = false;
    if (modalReturnFocus && modalReturnFocus.isConnected) modalReturnFocus.focus();
    else document.getElementById("btn-refresh").focus();
  }

  function sandboxFormBody(form) {
    var fd = new FormData(form);
    var body = {name: String(fd.get("name") || "").trim(), profile: fd.get("profile")};
    if (!profiles.some(function (p) { return p.name === body.profile; })) throw new Error("Select an available profile before checking the plan.");
    var ttl = fd.get("ttl_minutes");
    if (ttl) {
      if (!/^\d+$/.test(ttl) || Number(ttl) < 1 || Number(ttl) > 10080) throw new Error("Use a lease from 1 to 10080 minutes.");
      body.ttl_minutes = Number(ttl);
    }
    if (fd.get("keepalive") !== "") body.keepalive = fd.get("keepalive") === "true";
    var prompt = String(fd.get("prompt") || "").trim();
    if (prompt) body.prompt = prompt;
    return body;
  }

  async function validateSandboxBody(body) {
    var validation = Object.assign({}, body);
    delete validation.prompt;
    var res;
    try { res = await api("/v1/sandboxes/validate-plan", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify(validation)}); }
    catch (e) {
      if (e.status === 404) throw new Error("This server does not support sandbox plan checks. Update the server before creating a sandbox here.");
      throw e;
    }
    if (res.status === 404) throw new Error("This server does not support sandbox plan checks. Update the server before creating a sandbox here.");
    var plan = await res.json();
    if (!plan.ok || !plan.plan) throw new Error((plan.errors || []).map(function (issue) { return issue.message; }).join(" ") || "The sandbox plan is blocked.");
    return plan;
  }

  function initNewSandboxForm() {
    var form = document.getElementById("form-new-sandbox");
    var create = document.getElementById("btn-create-sandbox");
    var check = document.getElementById("btn-check-sandbox");
    var status = document.getElementById("sandbox-plan-state");
    var error = document.getElementById("sandbox-create-error");
    document.getElementById("btn-new-sandbox").addEventListener("click", async function () {
      invalidateSandboxReview();
      form.reset();
      error.textContent = "";
      document.getElementById("sandbox-plan-review").textContent = "";
      document.getElementById("sandbox-server").textContent = "Server: " + dashboardTarget + ". Identity: " + capabilities.server_identity;
      openModal("modal-new-sandbox");
      await loadProfiles();
    });
    form.addEventListener("input", function () {
      invalidateSandboxReview();
      status.textContent = "Settings changed. Check the plan again.";
    });
    form.addEventListener("submit", async function (event) {
      event.preventDefault();
      invalidateSandboxReview();
      var revision = formRevision;
      check.disabled = true;
      error.textContent = "";
      status.textContent = "Checking the template, profile, and configured capacity.";
      try {
        var body = sandboxFormBody(form);
        var validation = await validateSandboxBody(body);
        if (revision !== formRevision || connectionState !== "ready") return;
        reviewedSandbox = {body: body, plan: validation.plan, server: capabilities.server_identity};
        var capacity = validation.plan.capacity;
        var summary = "Profile: " + body.profile + ". Lease: " + (validation.plan.ttl_minutes || "server default") + " minutes. Keep alive: " + (validation.plan.keepalive ? "yes" : "no") + ".";
        if (!capacity) summary += " Capacity is not reported by this server.";
        if (capacity) summary += " Requested: " + capacity.cores + " CPU cores, " + capacity.memory_mb + " MB memory. Capacity: " + (capacity.checked ? "checked against the configured pool" : "not checked") + ".";
        summary += " " + (validation.warnings || []).map(function (issue) { return issue.message; }).join(" ");
        document.getElementById("sandbox-plan-review").textContent = summary;
        status.textContent = "Plan checked. Review the server and settings, then create. Capacity can change before creation.";
        create.disabled = false;
      } catch (e) { error.textContent = e.message; status.textContent = "Plan blocked. Correct the settings and check again."; }
      finally { check.disabled = document.getElementById("select-profile").disabled; }
    });
    create.addEventListener("click", async function () {
      if (!reviewedSandbox || creatingSandbox || connectionState !== "ready") return;
      var reviewed = reviewedSandbox;
      creatingSandbox = true;
      create.disabled = true;
      check.disabled = true;
      Array.from(form.elements).forEach(function (el) { el.disabled = true; });
      error.textContent = "";
      status.textContent = "Checking the selected server and plan again.";
      var submitted = false;
      try {
        if (!await loadConnection() || capabilities.server_identity !== reviewed.server || !permissionAllowed("sandbox.create")) throw new Error("Verify the selected server and creation permission before creation.");
        var latest = await validateSandboxBody(reviewed.body);
        if (JSON.stringify(latest.plan) !== JSON.stringify(reviewed.plan)) throw new Error("The server plan changed. Check and review the plan again.");
        submitted = true;
        status.textContent = "Creating the sandbox. Keep this view open for the result.";
        var res = await api("/v1/sandboxes", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify(reviewed.body)});
        if (!res.ok) throw new Error("Creation did not return a sandbox.");
        var sandbox = await res.json();
        status.textContent = "Sandbox " + (sandbox.vmid || "") + " created. Follow its state in Sandboxes.";
        creatingSandbox = false;
        closeModal("modal-new-sandbox");
        await loadSandboxes();
      } catch (e) {
        error.textContent = submitted ? "Creation did not return a verified result. Check the sandbox list before creating again. " + e.message : e.message;
        status.textContent = "Creation requires inspection.";
      } finally {
        creatingSandbox = false;
        Array.from(form.elements).forEach(function (el) { el.disabled = false; });
        invalidateSandboxReview();
      }
    });
    document.querySelectorAll(".modal-backdrop").forEach(function (el) {
      el.addEventListener("click", function () { closeModal(el.parentElement.id); });
    });
    document.addEventListener("keydown", function (event) {
      var modal = Array.from(document.querySelectorAll(".modal")).find(function (el) { return el.style.display === "flex"; });
      if (!modal) return;
      if (event.key === "Escape") { event.preventDefault(); closeModal(modal.id); }
      if (event.key === "Tab") {
        var controls = Array.from(modal.querySelectorAll("input, select, button, summary, a[href]")).filter(function (el) { return !el.disabled && el.getClientRects().length; });
        if (!controls.length) { event.preventDefault(); return; }
        var first = controls[0], last = controls[controls.length - 1];
        if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
        else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
      }
    });
  }

  // --- Expose Form ---

  function initExposeForm() {
    document
      .getElementById("form-expose")
      .addEventListener("submit", async function (e) {
        e.preventDefault();
        if (!exposeVMID) return;
        var fd = new FormData(e.target);
        var body = {
          vmid: exposeVMID,
          port: parseInt(fd.get("port")) || 80,
        };
        var name = fd.get("name");
        if (name) body.name = name;

        try {
          await api("/v1/exposures", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
          });
          closeModal("modal-expose");
          await loadExposures();
        } catch (e) {
          alert("Failed to expose sandbox: " + e.message);
        }
      });
  }

  // --- Snapshot Form ---

  function initSnapshotForm() {
    document
      .getElementById("form-snapshot")
      .addEventListener("submit", async function (e) {
        e.preventDefault();
        if (!snapshotVMID) return;
        var fd = new FormData(e.target);
        var body = {
          name: fd.get("name") || "snap",
        };

        try {
          await api("/v1/sandboxes/" + snapshotVMID + "/snapshot", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
          });
          closeModal("modal-snapshot");
        } catch (e) {
          alert("Failed to create snapshot: " + e.message);
        }
      });
  }

  // --- Bulk actions ---

  function initBulkActions() {
    async function bulk(button, states, action, label) {
      button.disabled = true;
      try {
        var data = await apiJSON("/v1/sandboxes");
        var selected = ((data && data.sandboxes) || []).filter(function (sb) { return states.includes(String(sb.state).toUpperCase()); });
        if (!selected.length) { alert("No matching sandboxes are available."); return; }
        var listing = selected.map(function (sb) { return sb.vmid + " (" + sb.name + ")"; }).join(", ");
        if (!confirm(label + " on server " + capabilities.server_identity + ": " + listing + "?")) return;
        for (var sb of selected) {
          await api("/v1/sandboxes/" + encodeURIComponent(sb.vmid) + "/" + action, {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({force: false})});
        }
        await loadSandboxes();
      } catch (e) { alert("The operation stopped. Inspect the sandbox list before retrying. " + e.message); }
      finally { button.disabled = false; }
    }
    document.getElementById("btn-stop-all").addEventListener("click", function () {
      bulk(this, ["RUNNING", "READY"], "stop", "Stop these sandboxes");
    });
    document.getElementById("btn-prune").addEventListener("click", function () {
      bulk(this, ["STOPPED"], "destroy", "Permanently destroy these stopped sandboxes");
    });
  }

  // --- Init ---

  function init() {
    initTabs();
    initNewSandboxForm();
    initExposeForm();
    initSnapshotForm();
    initBulkActions();

    // Modal cancel/close buttons declare data-close-modal instead of inline
    // onclick handlers (the CSP forbids inline handlers).
    document.querySelectorAll("[data-close-modal]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        closeModal(btn.dataset.closeModal);
      });
    });

    document.getElementById("btn-refresh").addEventListener("click", function () {
      authPrompt = null;
      authCancelled = false;
      refreshAll();
    });

    // Initial load.
    refreshAll().then(function () { if (connectionState === "ready") loadHostInfo(); });

    // Auto-refresh.
    refreshTimer = setInterval(refreshAll, REFRESH_INTERVAL);
  }

  document.addEventListener("DOMContentLoaded", init);
})();
