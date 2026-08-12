// AgentLab Dashboard - Client-side JavaScript

(function () {
  "use strict";

  let refreshTimer = null;
  let profiles = [];
  const REFRESH_INTERVAL = 5000;

  // --- API helpers ---

  // The inbound dashboard token lives only in this browser session; it is never
  // embedded in the shipped JavaScript. The user enters it when the server
  // demands it (non-loopback deployments). Loopback deployments do not require
  // it and never trigger the prompt.
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

  // --- State badge ---

  function stateBadge(state) {
    var cls = "state state-" + (state || "unknown").toLowerCase();
    return '<span class="' + cls + '">' + esc(state || "-") + "</span>";
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

  function esc(s) {
    if (!s) return "";
    var d = document.createElement("div");
    d.textContent = String(s);
    return d.innerHTML;
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
      tbody.innerHTML = list
        .map(function (sb) {
          return (
            "<tr>" +
            "<td>" +
            esc(sb.vmid) +
            "</td>" +
            "<td>" +
            esc(sb.name) +
            "</td>" +
            "<td>" +
            esc(sb.profile) +
            "</td>" +
            "<td>" +
            esc(sb.type || "vm") +
            "</td>" +
            "<td>" +
            stateBadge(sb.state) +
            "</td>" +
            "<td><code>" +
            esc(sb.ip || "-") +
            "</code></td>" +
            "<td>" +
            timeAgo(sb.created_at) +
            "</td>" +
            "<td>" +
            actionButtons(sb) +
            "</td>" +
            "</tr>"
          );
        })
        .join("");
    } catch (e) {
      tbody.innerHTML =
        '<tr><td colspan="8" class="error">Failed to load: ' +
        esc(e.message) +
        "</td></tr>";
    }
  }

  function actionButtons(sb) {
    var vmid = sb.vmid;
    var state = (sb.state || "").toLowerCase();
    var html = '<div class="action-group">';
    html +=
      '<button class="btn btn-sm" onclick="showDetail(' +
      vmid +
      ')">Detail</button>';
    if (state === "running" || state === "ready") {
      html +=
        '<button class="btn btn-sm" onclick="stopSandbox(' +
        vmid +
        ')">Stop</button>';
      html +=
        '<button class="btn btn-sm" onclick="showExpose(' +
        vmid +
        ')">Expose</button>';
      html +=
        '<button class="btn btn-sm" onclick="showSnapshot(' +
        vmid +
        ')">Snapshot</button>';
    } else if (state === "stopped") {
      html +=
        '<button class="btn btn-sm" onclick="startSandbox(' +
        vmid +
        ')">Start</button>';
    }
    html +=
      '<button class="btn btn-sm btn-danger" onclick="destroySandbox(' +
      vmid +
      ')">Destroy</button>';
    html += "</div>";
    return html;
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
      tbody.innerHTML = rows
        .map(function (r) {
          return (
            "<tr>" +
            "<td><code>" +
            esc(r.job_id).substring(0, 8) +
            "</code></td>" +
            "<td>" +
            esc(r.repo) +
            "</td>" +
            "<td>" +
            esc(r.task) +
            "</td>" +
            "<td>" +
            esc(r.profile) +
            "</td>" +
            "<td>" +
            stateBadge(r.status) +
            "</td>" +
            "<td>" +
            timeAgo(r.created) +
            "</td>" +
            "<td>" +
            (r.status === "failed"
              ? '<button class="btn btn-sm" onclick="showJobDetail(\'' +
                esc(r.job_id) +
                "')'>View</button>"
              : "") +
            "</td>" +
            "</tr>"
          );
        })
        .join("");
    } catch (e) {
      tbody.innerHTML =
        '<tr><td colspan="7" class="error">Failed to load: ' +
        esc(e.message) +
        "</td></tr>";
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
      tbody.innerHTML = list
        .map(function (ex) {
          return (
            "<tr>" +
            "<td>" +
            esc(ex.name) +
            "</td>" +
            "<td>" +
            esc(ex.vmid) +
            "</td>" +
            "<td>" +
            esc(ex.port) +
            "</td>" +
            "<td>" +
            (ex.url
              ? '<a class="exposure-url" href="' +
                esc(ex.url) +
                '" target="_blank">' +
                esc(ex.url) +
                "</a>"
              : "-") +
            "</td>" +
            "<td>" +
            stateBadge(ex.state) +
            "</td>" +
            "<td>" +
            timeAgo(ex.created_at) +
            "</td>" +
            "<td>" +
            '<button class="btn btn-sm btn-danger" onclick="removeExposure(\'' +
            esc(ex.name) +
            "')\">Remove</button>" +
            "</td>" +
            "</tr>"
          );
        })
        .join("");
    } catch (e) {
      tbody.innerHTML =
        '<tr><td colspan="7" class="error">Failed to load: ' +
        esc(e.message) +
        "</td></tr>";
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
          var kindClass = "event-kind kind-" + (ev.kind || "").toLowerCase();
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

  window.startSandbox = async function (vmid) {
    try {
      await api("/v1/sandboxes/" + vmid + "/start", { method: "POST" });
      await loadSandboxes();
    } catch (e) {
      alert("Failed to start: " + e.message);
    }
  };

  window.stopSandbox = async function (vmid) {
    if (!confirm("Stop sandbox " + vmid + "?")) return;
    try {
      await api("/v1/sandboxes/" + vmid + "/stop", { method: "POST" });
      await loadSandboxes();
    } catch (e) {
      alert("Failed to stop: " + e.message);
    }
  };

  window.destroySandbox = async function (vmid) {
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
  };

  window.showDetail = async function (vmid) {
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
  };

  window.showJobDetail = async function (jobId) {
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
  };

  // --- Expose ---

  var exposeVMID = null;

  window.showExpose = function (vmid) {
    exposeVMID = vmid;
    document.getElementById("expose-vmid").textContent = "#" + vmid;
    document.getElementById("form-expose").reset();
    document.getElementById("modal-expose").style.display = "flex";
  };

  // --- Snapshot ---

  var snapshotVMID = null;

  window.showSnapshot = function (vmid) {
    snapshotVMID = vmid;
    document.getElementById("snapshot-vmid").textContent = "#" + vmid;
    document.getElementById("form-snapshot").reset();
    document.getElementById("modal-snapshot").style.display = "flex";
  };

  // --- Remove exposure ---

  window.removeExposure = async function (name) {
    if (!confirm("Remove exposure " + name + "?")) return;
    try {
      await api("/v1/exposures/" + encodeURIComponent(name), {
        method: "DELETE",
      });
      await loadExposures();
    } catch (e) {
      alert("Failed to remove exposure: " + e.message);
    }
  };

  window.closeModal = function (id) {
    document.getElementById(id).style.display = "none";
  };

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
