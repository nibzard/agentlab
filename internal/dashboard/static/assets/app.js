// AgentLab Dashboard - Client-side JavaScript

(function () {
  "use strict";

  let refreshTimer = null;
  let profiles = [];
  const REFRESH_INTERVAL = 5000;

  // --- API helpers ---

  // The inbound dashboard token lives only in this browser session; it is never
  // embedded in the shipped JavaScript. The user enters it when the server
  // demands it. Every bind requires a token: the operator passes
  // --browser-token, or the server generates one and logs it at startup.
  function dashboardToken() {
    try {
      return sessionStorage.getItem("agentlab_dashboard_token") || "";
    } catch (e) {
      return "";
    }
  }

  function setDashboardToken(tok) {
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
    // If the server requires an inbound token we do not yet hold, ask the user
    // once, persist it to this session, and retry the original request.
    if (res.status === 401 && !opts.__agentlabRetried) {
      var tok = window.prompt("Dashboard access token:");
      if (tok) {
        setDashboardToken(tok);
        opts.__agentlabRetried = true;
        opts.headers["Authorization"] = "Bearer " + tok;
        res = await fetch("/api" + path, opts);
      }
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
      var running = sb["running"] || sb["ready"] || 0;
      var total = 0;
      for (var k in sb) {
        if (sb.hasOwnProperty(k)) total += sb[k];
      }
      document.getElementById("status-sandboxes").textContent =
        "Sandboxes: " + total + " (" + running + " active)";
      var jb = data.jobs || {};
      var jobActive = (jb["running"] || 0) + (jb["queued"] || 0);
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
        if (r.status === "failed") {
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
      // Load messages as event stream
      var data = await apiJSON("/v1/messages");
      var messages = (data && data.messages) || [];

      // Also get status for failure digest
      var status = await apiJSON("/v1/status");
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
    await Promise.all([
      loadStatus(),
      loadSandboxes(),
      loadJobs(),
      loadWorkspaces(),
      loadExposures(),
      loadEvents(),
    ]);
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
    if (!confirm("Destroy sandbox " + vmid + "? This cannot be undone."))
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
      document.getElementById("modal-sandbox-detail").style.display = "flex";
    } catch (e) {
      alert("Failed to load details: " + e.message);
    }
  }

  async function showJobDetail(jobId) {
    try {
      var data = await apiJSON("/v1/jobs/" + jobId);
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
      document.getElementById("modal-sandbox-detail").style.display = "flex";
    } catch (e) {
      alert("Failed to load job: " + e.message);
    }
  }

  // --- Expose ---

  var exposeVMID = null;

  function showExpose(vmid) {
    exposeVMID = vmid;
    document.getElementById("expose-vmid").textContent = "#" + vmid;
    document.getElementById("form-expose").reset();
    document.getElementById("modal-expose").style.display = "flex";
  }

  // --- Snapshot ---

  var snapshotVMID = null;

  function showSnapshot(vmid) {
    snapshotVMID = vmid;
    document.getElementById("snapshot-vmid").textContent = "#" + vmid;
    document.getElementById("form-snapshot").reset();
    document.getElementById("modal-snapshot").style.display = "flex";
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

  function closeModal(id) {
    document.getElementById(id).style.display = "none";
  }

  // --- New Sandbox Form ---

  function initNewSandboxForm() {
    document
      .getElementById("btn-new-sandbox")
      .addEventListener("click", function () {
        loadProfiles();
        document.getElementById("modal-new-sandbox").style.display = "flex";
      });

    document
      .getElementById("form-new-sandbox")
      .addEventListener("submit", async function (e) {
        e.preventDefault();
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

        try {
          await api("/v1/sandboxes", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
          });
          closeModal("modal-new-sandbox");
          e.target.reset();
          await loadSandboxes();
        } catch (e) {
          alert("Failed to create sandbox: " + e.message);
        }
      });

    // Close modal on backdrop click.
    document.querySelectorAll(".modal-backdrop").forEach(function (el) {
      el.addEventListener("click", function () {
        el.parentElement.style.display = "none";
      });
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
        if (!confirm("Prune all stopped sandboxes? This cannot be undone."))
          return;
        try {
          await api("/v1/sandboxes/prune", { method: "POST" });
          await loadSandboxes();
        } catch (e) {
          alert("Failed to prune: " + e.message);
        }
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

    document.getElementById("btn-refresh").addEventListener("click", refreshAll);

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
