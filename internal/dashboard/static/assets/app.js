// AgentLab Dashboard - Client-side JavaScript

(function () {
  "use strict";

  let refreshTimer = null;
  let profiles = [];
  let loginPending = null;
  let authPaused = false;
  let refreshPending = false;
  let memoryToken = "";
  let lastRefresh = null;
  let createPending = false;
  let createKey = "";
  let createPayload = "";
  let createOperation = "";
  const observingCreations = new Set();
  const CREATE_RECOVERY_KEY = "agentlab_create_recovery";
  let sandboxPage = 0;
  let jobCursor = "";
  let jobCursorHistory = [];
  let jobNextCursor = "";
  let cachedEventMessages = null;
  let cachedFailureDigest = null;

  function requestLogin() {
    if (loginPending) return loginPending;
    authPaused = true;
    loginPending = new Promise(function (resolve) {
      var dialog = document.createElement("dialog");
      dialog.ariaLabel = "Dashboard sign in";
      var form = document.createElement("form");
      form.method = "dialog";
      var label = document.createElement("label");
      label.textContent = "Dashboard access token ";
      var input = document.createElement("input");
      input.type = "password";
      input.autocomplete = "off";
      input.required = true;
      label.appendChild(input);
      form.appendChild(label);
      var submit = document.createElement("button");
      submit.textContent = "Sign in";
      submit.type = "submit";
      form.appendChild(submit);
      var cancel = document.createElement("button");
      cancel.type = "button";
      cancel.textContent = "Cancel";
      form.appendChild(cancel);
      dialog.appendChild(form);
      document.body.appendChild(dialog);
      function finish(token) {
        if (token) { setDashboardToken(token); authPaused = false; }
        dialog.close();
        dialog.remove();
        resolve(token);
      }
      form.addEventListener("submit", function (e) { e.preventDefault(); finish(input.value.trim()); });
      cancel.addEventListener("click", function () { finish(""); });
      dialog.addEventListener("cancel", function (e) { e.preventDefault(); finish(""); });
      dialog.showModal();
      input.focus();
    }).finally(function () { loginPending = null; });
    return loginPending;
  }
  const REFRESH_INTERVAL = 5000;

  // --- API helpers ---

  // The inbound dashboard token lives only in this browser session; it is never
  // embedded in the shipped JavaScript. The user enters it when the server
  // demands it. Every bind requires a token: the operator passes
  // --browser-token, or the server generates one and logs it at startup.
  function dashboardToken() {
    try {
      return memoryToken || sessionStorage.getItem("agentlab_dashboard_token") || "";
    } catch (e) {
      return memoryToken;
    }
  }

  function setDashboardToken(tok) {
    memoryToken = tok;
    try {
      sessionStorage.setItem("agentlab_dashboard_token", tok);
    } catch (e) {
      /* sessionStorage unavailable; token will be requested again */
    }
  }

  function applyDashboardHeaders(opts) {
    opts = opts || {};
    opts.headers = Object.assign({}, opts.headers || {});
    // Custom header a plain cross-site form cannot set (CSRF defense).
    opts.headers["X-Requested-With"] = "XMLHttpRequest";
    var tok = dashboardToken();
    if (tok) {
      opts.headers["Authorization"] = "Bearer " + tok;
    }
    return opts;
  }

  async function api(path, opts) {
    opts = applyDashboardHeaders(opts);
    var res = await fetch("/api" + path, opts);
    // Only dashboard authentication failures request browser credentials.
    // Daemon authorization errors must be reported without asking for another token.
    if (res.status === 401 && res.headers.get("X-Agentlab-Auth") === "dashboard") {
      var sentToken = (opts.headers.Authorization || "").replace(/^Bearer /, "");
      var tok = dashboardToken();
      if (!tok || tok === sentToken) {
        if (authPaused && !loginPending) throw new Error("Sign-in paused. Use Refresh to retry.");
        tok = await requestLogin();
      }
      if (tok) {
        opts = applyDashboardHeaders(opts);
        res = await fetch("/api" + path, opts);
      }
      if (res.status === 401) authPaused = true;
    }
    if (!res.ok && res.status !== 404) {
      var body = await res.text();
      try {
        var err = JSON.parse(body);
        throw new Error(err.error || err.message || res.statusText);
      } catch (e) {
        if (e instanceof SyntaxError) {
          throw new Error(res.statusText || "request failed");
        }
        throw e;
      }
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
    if (tbody._lastSuccess) {
      document.getElementById("data-status").textContent = "Data unavailable: " + message + ". Showing stale data from " + tbody._lastSuccess.toLocaleTimeString();
      return;
    }
    tbody.innerHTML = "";
    var tr = document.createElement("tr");
    var td = document.createElement("td");
    td.colSpan = colspan;
    td.className = "error";
    td.textContent = "Failed to load: " + message;
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
    if (diff < 0) {
      var futureMins = Math.ceil(-diff / 60000);
      if (futureMins < 60) return "in " + futureMins + "m";
      var futureHours = Math.ceil(futureMins / 60);
      if (futureHours < 24) return "in " + futureHours + "h";
      return "in " + Math.ceil(futureHours / 24) + "d";
    }
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
    });
    document.querySelectorAll(".view").forEach(function (v) {
      v.classList.toggle("active", v.id === "view-" + name);
    });
  }

  // --- Load profiles ---

  async function loadProfiles() {
    try {
      var data = await apiJSON("/v1/profiles");
      profiles = (data && data.profiles) || [];
      var sel = document.getElementById("select-profile");
      sel.innerHTML = "";
      if (profiles.length === 0) {
        sel.innerHTML = '<option value="default">default</option>';
        return;
      }
      profiles.forEach(function (p) {
        var opt = document.createElement("option");
        opt.value = p.name;
        opt.textContent = p.name + (p.type ? " (" + p.type + ")" : "");
        sel.appendChild(opt);
      });
    } catch (e) {
      var sel = document.getElementById("select-profile");
      sel.innerHTML = '<option value="default">default</option>';
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
        "Sandboxes: " + total + " current (" + running + " active), " + (sb["DESTROYED"] || 0) + " destroyed";
      var jb = data.jobs || {};
      var jobActive = (jb["RUNNING"] || 0) + (jb["QUEUED"] || 0);
      document.getElementById("status-jobs").textContent =
        "Jobs: " + jobActive + " active";

      // Pool status badge
      await loadPoolStatus();
    } catch (e) {
      document.getElementById("status-daemon").textContent =
        "Daemon: " + e.message;
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
      if (data.cpu_total > 0) {
        cpuPct = Math.round(((data.cpu_allocated || 0) / data.cpu_total) * 100);
      }
      if (data.memory_total_mb > 0) {
        memPct = Math.round(
          ((data.memory_allocated_mb || 0) / data.memory_total_mb) * 100
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
      if (!data) throw new Error("Sandbox inventory endpoint unavailable");
      tbody._lastSuccess = new Date();
      var list = (data && data.sandboxes) || [];
      var search = document.getElementById("sandbox-search").value.trim().toLowerCase();
      var state = document.getElementById("sandbox-state-filter").value.trim().toUpperCase();
      var profile = document.getElementById("sandbox-profile-filter").value.trim();
      var history = document.getElementById("sandbox-history").checked;
      list = list.filter(function (sb) {
        return (history || sb.state !== "DESTROYED") && (!state || sb.state === state) && (!profile || sb.profile === profile) && (!search || [sb.vmid,sb.name,sb.profile,sb.state].join(" ").toLowerCase().includes(search));
      });
      sandboxPage = Math.min(sandboxPage, Math.max(0, Math.ceil(list.length/25)-1));
      document.getElementById("sandbox-page-status").textContent = "Database records: " + list.length + " matching. Page " + (sandboxPage+1) + ". Updated " + new Date().toLocaleTimeString();
      document.getElementById("sandbox-previous").disabled = sandboxPage === 0;
      document.getElementById("sandbox-next").disabled = (sandboxPage+1)*25 >= list.length;
      empty.textContent = "No sandboxes match these filters. Clear filters or create a sandbox.";
      list = list.slice(sandboxPage*25,(sandboxPage+1)*25);
      if (list.length === 0) {
        empty.textContent = search || state || profile ? "No sandboxes match these filters. Clear Search, State or Profile to see other records." : "No current sandboxes. Use New Sandbox to create one or Include destroyed history to inspect past records.";
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
      var data = await apiJSON("/v1/jobs?limit=50&after=" + encodeURIComponent(jobCursor));
      if (!data) throw new Error("Jobs endpoint unavailable");
      jobNextCursor = (data && data.next_cursor) || "";
      document.getElementById("job-next").disabled = !jobNextCursor;
      document.getElementById("job-previous").disabled = jobCursorHistory.length === 0;
      tbody._lastSuccess = new Date();
      var rows = ((data && data.jobs) || []).map(function (job) {
        return {job_id:job.id, repo:job.repo_url, task:job.task, profile:job.profile, status:job.status, created:job.created_at, sandbox:job.sandbox_vmid, workspace:job.workspace_id};
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
        var createdCell = appendTd(tr, timeAgo(r.created));
        createdCell.title = r.created || "";
        var sandboxCell = appendTd(tr);
        if (r.sandbox) addActionButton(sandboxCell, String(r.sandbox), "btn btn-sm", function () {showDetail(r.sandbox);});
        var workspaceCell = appendTd(tr);
        if (r.workspace) addActionButton(workspaceCell, r.workspace, "btn btn-sm", function () {showWorkspaceDetail(r.workspace);});
        else workspaceCell.textContent = "-";
        workspaceCell.title = r.workspace || "";
        var actions = document.createElement("td");
        if (jobId) {
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
      if (!data) throw new Error("Workspaces endpoint unavailable");
      tbody._lastSuccess = new Date();
      var list = (data && data.workspaces) || [];
      if (list.length === 0) {
        tbody.innerHTML = "";
        empty.style.display = "block";
        return;
      }
      empty.style.display = "none";
      tbody.innerHTML = "";
      list.forEach(function (ws) {
        var row = document.createElement("tr");
        addActionButton(appendTd(row), ws.id, "btn btn-sm", function () {showWorkspaceDetail(ws.id);});
        appendTd(row, ws.name);
        appendTd(row, ws.size_gb + " GB");
        var sandboxCell = appendTd(row);
        if (ws.attached_vmid) addActionButton(sandboxCell, String(ws.attached_vmid), "btn btn-sm", function () {showDetail(ws.attached_vmid);});
        else sandboxCell.textContent = "-";
        appendTd(row, timeAgo(ws.created_at));
        tbody.appendChild(row);
      });
    } catch (e) {
      errorRow(tbody, 5, e.message);
    }
  }

  async function loadExposures() {
    var tbody = document.getElementById("exposure-list");
    var empty = document.getElementById("exposure-empty");
    try {
      var data = await apiJSON("/v1/exposures");
      if (!data) throw new Error("Exposures endpoint unavailable");
      tbody._lastSuccess = new Date();
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
      // Load messages as event stream
      var type = document.getElementById("event-scope-type").value;
      var id = document.getElementById("event-scope-id").value.trim();
      var results = await Promise.allSettled([
        type && id ? apiJSON("/v1/messages?scope_type=" + encodeURIComponent(type) + "&scope_id=" + encodeURIComponent(id)) : Promise.resolve(null),
        apiJSON("/v1/status")
      ]);
      var errors = [], stale = [];
      var scopeKey = JSON.stringify([type, id]);
      var loadedAt = new Date();
      var data = null, status = null;
      if (type && id) {
        if (results[0].status === "fulfilled" && results[0].value) {
          data = results[0].value;
          cachedEventMessages = {scope: scopeKey, data: data, at: loadedAt};
        } else {
          errors.push("Messages: " + (results[0].status === "rejected" ? results[0].reason.message : "endpoint unavailable"));
          if (cachedEventMessages && cachedEventMessages.scope === scopeKey) {
            data = cachedEventMessages.data;
            stale.push("messages from " + cachedEventMessages.at.toLocaleTimeString());
          }
        }
      }
      if (results[1].status === "fulfilled" && results[1].value) {
        status = results[1].value;
        cachedFailureDigest = {data: status, at: loadedAt};
      } else {
        errors.push("Failure digest: " + (results[1].status === "rejected" ? results[1].reason.message : "endpoint unavailable"));
        if (cachedFailureDigest) {
          status = cachedFailureDigest.data;
          stale.push("failure digest from " + cachedFailureDigest.at.toLocaleTimeString());
        }
      }
      document.getElementById("events-status").textContent = errors.length ? "Some sources unavailable: " + errors.join("; ") + (stale.length ? ". Showing stale " + stale.join(" and ") : "") : "Failure digest" + (type && id ? " and selected scope messages" : "; select a scope to see messages") + ". Updated " + loadedAt.toLocaleTimeString();
      var messages = (data && data.messages) || [];
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
        empty.textContent = errors.length ? "Event sources unavailable; retry after restoring access or connectivity." : "No failure digest entries or messages for this selection.";
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
    if (refreshPending || authPaused) return;
    refreshPending = true;
    document.getElementById("data-status").textContent = "";
    try { await Promise.all([
      loadStatus(),
      loadSandboxes(),
      loadJobs(),
      loadWorkspaces(),
      loadExposures(),
      loadEvents(),
    ]);
      lastRefresh = new Date();
      document.getElementById("btn-refresh").title = "Last refresh completed " + lastRefresh.toLocaleTimeString();
    } finally { refreshPending = false; }
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
    if (!confirm("Stop sandbox " + vmid + "?")) return;
    try {
      await api("/v1/sandboxes/" + vmid + "/stop", { method: "POST" });
      await loadSandboxes();
    } catch (e) {
      alert("Failed to stop: " + e.message);
    }
  }

  async function destroySandbox(vmid) {
    var sandbox;
    try { sandbox = await apiJSON("/v1/sandboxes/" + vmid); }
    catch (e) { alert("Cannot load sandbox for confirmation: " + e.message); return; }
    if (!confirm("Destroy sandbox " + vmid + " (" + (sandbox.name || "unnamed") +
      ", " + sandbox.state + ", job " + (sandbox.job_id || "none") + ", workspace " + (sandbox.workspace_id || "none") + ", session " + (sandbox.session_id || "none") + ")? This cannot be undone."))
      return;
    try {
      await api("/v1/sandboxes/" + vmid + "/destroy", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ force: false }),
      });
      await loadSandboxes();
    } catch (e) {
      alert("Failed to destroy: " + e.message);
    }
  }

  async function showDetail(vmid) {
    try {
      var data = await apiJSON("/v1/sandboxes/" + vmid);
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
          value: data.lease_expires_at ? data.lease_expires_at + " (" + timeAgo(data.lease_expires_at) + ")" : "-",
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

  async function showJobDetail(jobId) {
    try {
      var data = await apiJSON("/v1/jobs/" + encodeURIComponent(jobId));
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
        { label: "Workspace", value: data.workspace_id || "-" },
        { label: "Exit code", value: data.outcome && data.outcome.exit_code != null ? data.outcome.exit_code : "Not recorded" },
        { label: "Duration (seconds)", value: data.outcome && data.outcome.duration_seconds != null ? data.outcome.duration_seconds : "Not recorded" },
        { label: "Outcome message", value: data.outcome && data.outcome.message || "-" },
        { label: "Commit", value: data.outcome && data.outcome.commit || "-" },
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
      var related = document.getElementById("detail-summary");
      if (data.sandbox_vmid) addActionButton(related, "Sandbox " + data.sandbox_vmid, "btn btn-sm", function () {showDetail(data.sandbox_vmid);});
      if (data.workspace_id) addActionButton(related, "Workspace " + data.workspace_id, "btn btn-sm", function () {showWorkspaceDetail(data.workspace_id);});
      document.getElementById("detail-json").textContent = JSON.stringify(
        data,
        null,
        2
      );
      openModal("modal-sandbox-detail");
    } catch (e) {
      alert("Failed to load job: " + e.message);
    }
  }

  async function showWorkspaceDetail(workspaceId) {
    try {
      var data = await apiJSON("/v1/workspaces/" + encodeURIComponent(workspaceId));
      document.getElementById("detail-title").textContent = "Workspace " + (data.name || data.id || workspaceId);
      var summary = document.getElementById("detail-summary");
      summary.innerHTML = "";
      [["ID", data.id], ["Name", data.name], ["Size", data.size_gb + " GB"], ["Storage", data.storage], ["Attached sandbox", data.attached_vmid || "Detached"], ["Lease owner", data.lease_owner || "None"], ["Lease expires", data.lease_expires_at || "-"]].forEach(function (field) {
        var item = document.createElement("div");
        item.className = "detail-field";
        item.textContent = field[0] + ": " + (field[1] == null ? "-" : field[1]);
        summary.appendChild(item);
      });
      if (data.attached_vmid) addActionButton(summary, "Sandbox " + data.attached_vmid, "btn btn-sm", function () {showDetail(data.attached_vmid);});
      document.getElementById("detail-json").textContent = JSON.stringify(data, null, 2);
      openModal("modal-sandbox-detail");
    } catch (e) { alert("Failed to load workspace: " + e.message); }
  }

  // --- Expose ---

  var exposeVMID = null;

  function showExpose(vmid) {
    exposeVMID = vmid;
    document.getElementById("expose-vmid").textContent = "#" + vmid;
    document.getElementById("form-expose").reset();
    openModal("modal-expose");
  }

  // --- Snapshot ---

  var snapshotVMID = null;

  function showSnapshot(vmid) {
    snapshotVMID = vmid;
    document.getElementById("snapshot-vmid").textContent = "#" + vmid;
    document.getElementById("form-snapshot").reset();
    openModal("modal-snapshot");
  }

  // --- Remove exposure ---

  async function removeExposure(name) {
    if (!confirm("Remove exposure " + name + "?")) return;
    try {
      await api("/v1/exposures/" + encodeURIComponent(name), {
        method: "DELETE",
      });
      await loadExposures();
    } catch (e) {
      alert("Failed to remove exposure: " + e.message);
    }
  }

  function modalFocusables(modal) {
    return Array.from(modal.querySelectorAll("button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex='0']")).filter(function (item) {return item.getClientRects().length > 0;});
  }
  function openModal(id) {
    var modal = document.getElementById(id);
    if (modal.style.display !== "flex") modal._opener = document.activeElement;
    modal.style.display = "flex";
    // DOM fixture runners lack selector APIs; real browsers receive focus and
    // semantics for every modal, including asynchronous detail views.
    if (modal.setAttribute) {
      modal.setAttribute("role", "dialog");
      modal.setAttribute("aria-modal", "true");
      var heading = modal.querySelector("h3");
      if (heading) modal.setAttribute("aria-label", heading.textContent);
      modal.tabIndex = -1;
      (modalFocusables(modal)[0] || modal).focus();
    }
  }
  function closeModal(id) {
    var modal = document.getElementById(id);
    modal.style.display = "none";
    if (modal._opener && modal._opener.isConnected) modal._opener.focus();
  }

  // --- New Sandbox Form ---

  function readCreateRecovery() {
    try {
      var saved = JSON.parse(sessionStorage.getItem(CREATE_RECOVERY_KEY) || "null");
      if (!saved || typeof saved.key !== "string" || typeof saved.payload !== "string") return null;
      JSON.parse(saved.payload);
      return saved;
    } catch (e) { return null; }
  }

  function saveCreateRecovery(operation) {
    createOperation = operation || "";
    try { sessionStorage.setItem(CREATE_RECOVERY_KEY, JSON.stringify({key:createKey,payload:createPayload,operation:createOperation})); } catch (e) { /* In-memory retry remains available. */ }
    document.getElementById("create-resume").hidden = false;
  }

  async function submitCreation(payload, form) {
    if (createPending) return;
    if (payload !== createPayload || !createKey) { createKey = crypto.randomUUID(); createPayload = payload; createOperation = ""; }
    // Persist before sending: a dropped response or page reload retains the
    // exact payload and key instead of allocating an untraceable duplicate.
    saveCreateRecovery(createOperation);
    createPending = true;
    var submit = form && form.querySelector('[type="submit"]');
    var resume = document.getElementById("create-resume");
    if (submit) submit.disabled = true;
    resume.disabled = true;
    try {
      var response = await api("/v1/sandboxes?async=true", {
        method:"POST",headers:{"Content-Type":"application/json","Idempotency-Key":createKey},body:payload
      });
      var operation = await response.json();
      saveCreateRecovery(operation.id);
      document.getElementById("create-progress").textContent = "Creation " + operation.state + ". Operation " + operation.id + ". Resume with the saved request or agentlab operation show " + operation.id;
      observeCreation(operation.id);
      closeModal("modal-new-sandbox");
      if (form) form.reset();
      await loadSandboxes();
    } catch (e) {
      document.getElementById("create-progress").textContent = "Create request outcome unknown: " + e.message + ". Resume the saved request to reuse its idempotency key.";
    } finally { createPending = false; if (submit) submit.disabled = false; resume.disabled = false; }
  }

  function initNewSandboxForm() {
    var recovery = readCreateRecovery();
    var resume = document.getElementById("create-resume");
    if (recovery) {
      createKey = recovery.key; createPayload = recovery.payload; createOperation = recovery.operation || "";
      resume.hidden = false;
      document.getElementById("create-progress").textContent = "A previous creation request can be resumed" + (createOperation ? ": operation " + createOperation : " using its saved request key") + ".";
    }
    resume.addEventListener("click",function () { if (createPayload) return submitCreation(createPayload,null); });
    document
      .getElementById("btn-new-sandbox")
      .addEventListener("click", function () {
        loadProfiles();
        openModal("modal-new-sandbox");
      });

    document
      .getElementById("form-new-sandbox")
      .addEventListener("submit", async function (e) {
        e.preventDefault();
        if (createPending) return;
        var fd = new FormData(e.target);
        var body = {
          name: fd.get("name") || "",
          profile: fd.get("profile") || "default",
          type: fd.get("type") || "",
          image: fd.get("image") || "",
          prompt: fd.get("prompt") || "",
          keepalive: fd.has("keepalive"),
        };
        var ttl = parseInt(fd.get("ttl_minutes"));
        if (ttl > 0) body.ttl_minutes = ttl;

        await submitCreation(JSON.stringify(body), e.target);
      });

    // Close modal on backdrop click.
    document.querySelectorAll(".modal-backdrop").forEach(function (el) {
      el.addEventListener("click", function () {
        closeModal(el.parentElement.id);
      });
    });
  }

  async function observeCreation(id) {
    if (observingCreations.has(id)) return;
    observingCreations.add(id);
    try {
    var started = Date.now();
    for (var attempt=0; attempt<120; attempt++) {
      try {
        var op = await apiJSON("/v1/operations/" + encodeURIComponent(id));
        var message = "Operation " + id + ": " + op.state + ". Elapsed " + Math.floor((Date.now()-started)/1000) + "s.";
        if (op.vmid) message += " Sandbox " + op.vmid + ".";
        if (op.result && op.result.state) message += " Stage " + op.result.state + ".";
        document.getElementById("create-progress").textContent = message + " Resume: agentlab operation show " + id;
        if (op.state !== "accepted") {
          if (op.state === "succeeded" || op.state === "failed" || op.state === "rejected") {
            if (createOperation === id) {
              createKey = ""; createPayload = ""; createOperation = "";
              try { sessionStorage.removeItem(CREATE_RECOVERY_KEY); } catch (e) { /* storage unavailable */ }
              document.getElementById("create-resume").hidden = true;
            }
          }
          await loadSandboxes(); return;
        }
      } catch (e) {
        document.getElementById("create-progress").textContent = "Observation unavailable: " + e.message + ". Resume: agentlab operation show " + id;
        return;
      }
      await new Promise(function (resolve) { setTimeout(resolve,5000); });
    }
    } finally { observingCreations.delete(id); }
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
          await api("/v1/sandboxes/" + snapshotVMID + "/snapshots", {
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
    document
      .getElementById("btn-stop-all")
      .addEventListener("click", async function () {
        if (!confirm("Stop all running sandboxes?")) return;
        try {
          await api("/v1/sandboxes/stop_all", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ force: false }),
          });
          await loadSandboxes();
        } catch (e) {
          alert("Failed to stop all: " + e.message);
        }
      });

    document
      .getElementById("btn-prune")
      .addEventListener("click", async function () {
        try {
          var previewRes = await api("/v1/sandboxes/prune?dry_run=true", {method:"POST"});
          var preview = await previewRes.json();
          if (!preview.outcomes.length) { alert("No timed-out sandboxes to prune."); return; }
          if (!confirm("Destroy these timed-out sandboxes? " + preview.outcomes.map(function (item) { return item.vmid + " (" + item.name + ")"; }).join(", "))) return;
          var query = preview.outcomes.map(function (item) {return "vmid=" + item.vmid;}).join("&");
          var resultRes = await api("/v1/sandboxes/prune?" + query, {method:"POST"});
          var result = await resultRes.json();
          var failed = result.outcomes.filter(function (item) {return item.outcome !== "destroyed";});
          alert("Pruned " + result.count + " sandboxes." + (failed.length ? " Targets not destroyed: " + failed.map(function (item) {return item.vmid + ": " + item.outcome + " " + item.error + (item.retryable ? " (retryable)" : "");}).join("; ") : ""));
          await loadSandboxes();
        } catch (e) {
          alert("Failed to prune: " + e.message);
        }
      });
  }

  // --- Init ---

  function init() {
    document.addEventListener("keydown", function (event) {
      var modal = Array.from(document.querySelectorAll(".modal")).find(function (item) {return item.style.display === "flex";});
      if (!modal) return;
      if (event.key === "Escape") {event.preventDefault();closeModal(modal.id);}
      if (event.key === "Tab") {
        var items = modalFocusables(modal);
        var first = items[0] || modal, last = items[items.length - 1] || modal;
        if (event.shiftKey && (document.activeElement === first || document.activeElement === modal)) {event.preventDefault();last.focus();}
        else if (!event.shiftKey && (document.activeElement === last || document.activeElement === modal)) {event.preventDefault();first.focus();}
      }
    });
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

    document.getElementById("btn-refresh").addEventListener("click", function () { authPaused = false; refreshAll(); });

    document.getElementById("event-scope-load").addEventListener("click", loadEvents);
    document.getElementById("job-next").addEventListener("click",function () {if(jobNextCursor){jobCursorHistory.push(jobCursor);jobCursor=jobNextCursor;loadJobs();}});
    document.getElementById("job-previous").addEventListener("click",function () {jobCursor=jobCursorHistory.pop()||"";loadJobs();});

    ["sandbox-search","sandbox-state-filter","sandbox-profile-filter","sandbox-history"].forEach(function (id) {
      document.getElementById(id).addEventListener("change",function () {sandboxPage=0;loadSandboxes();});
    });
    document.getElementById("sandbox-previous").addEventListener("click",function () {sandboxPage=Math.max(0,sandboxPage-1);loadSandboxes();});
    document.getElementById("sandbox-next").addEventListener("click",function () {sandboxPage++;loadSandboxes();});
    // Load profiles for the form.
    loadProfiles();

    // Load host info once.
    loadHostInfo();

    // Initial load.
    refreshAll();

    // Auto-refresh.
    refreshTimer = setInterval(refreshAll, REFRESH_INTERVAL);
  }

  document.addEventListener("DOMContentLoaded", init);
})();
