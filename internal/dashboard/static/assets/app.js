// AgentLab Dashboard - Client-side JavaScript

(function () {
  "use strict";

  let refreshTimer = null;
  const REFRESH_INTERVAL = 5000;

  // --- API helpers ---

  async function api(path, opts = {}) {
    const res = await fetch("/api" + path, opts);
    if (!res.ok && res.status !== 404) {
      const body = await res.text();
      try {
        const err = JSON.parse(body);
        throw new Error(err.error || err.message || res.statusText);
      } catch {
        throw new Error(res.statusText || "request failed");
      }
    }
    return res;
  }

  async function apiJSON(path) {
    const res = await api(path);
    if (res.status === 404) return null;
    return res.json();
  }

  // --- State badge ---

  function stateBadge(state) {
    const cls = "state state-" + (state || "unknown").toLowerCase();
    return '<span class="' + cls + '">' + esc(state || "-") + "</span>";
  }

  function jobBadge(status) {
    return stateBadge(status);
  }

  // --- Time formatting ---

  function timeAgo(ts) {
    if (!ts) return "-";
    const d = new Date(ts);
    if (isNaN(d.getTime())) return esc(ts);
    const diff = Date.now() - d.getTime();
    const mins = Math.floor(diff / 60000);
    if (mins < 1) return "just now";
    if (mins < 60) return mins + "m ago";
    const hours = Math.floor(mins / 60);
    if (hours < 24) return hours + "h ago";
    const days = Math.floor(hours / 24);
    return days + "d ago";
  }

  function esc(s) {
    if (!s) return "";
    const d = document.createElement("div");
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

  // --- Load data ---

  async function loadStatus() {
    try {
      const data = await apiJSON("/v1/status");
      if (!data) return;
      document.getElementById("status-daemon").textContent = "Daemon: OK";
      const sb = data.sandboxes || {};
      const running = sb["running"] || sb["ready"] || 0;
      const total = Object.values(sb).reduce((a, b) => a + b, 0);
      document.getElementById("status-sandboxes").textContent =
        "Sandboxes: " + total + " (" + running + " active)";
      const jb = data.jobs || {};
      const jobActive = (jb["running"] || 0) + (jb["queued"] || 0);
      document.getElementById("status-jobs").textContent =
        "Jobs: " + jobActive + " active";
    } catch (e) {
      document.getElementById("status-daemon").textContent =
        "Daemon: " + e.message;
    }
  }

  async function loadSandboxes() {
    const tbody = document.getElementById("sandbox-list");
    const empty = document.getElementById("sandbox-empty");
    try {
      const data = await apiJSON("/v1/sandboxes");
      const list = (data && data.sandboxes) || [];
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
    const vmid = sb.vmid;
    const state = (sb.state || "").toLowerCase();
    let html = '<div class="action-group">';
    html +=
      '<button class="btn btn-sm" onclick="showDetail(' +
      vmid +
      ')">Detail</button>';
    if (state === "running" || state === "ready") {
      html +=
        '<button class="btn btn-sm" onclick="stopSandbox(' +
        vmid +
        ')">Stop</button>';
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
    const tbody = document.getElementById("job-list");
    const empty = document.getElementById("job-empty");
    try {
      // Jobs are accessed individually; list recent via status.
      // For now, show recent failure digest from status.
      const data = await apiJSON("/v1/status");
      const failures = (data && data.recent_failure_digest) || [];
      if (failures.length === 0) {
        tbody.innerHTML = "";
        empty.style.display = "block";
        return;
      }
      empty.style.display = "none";
      tbody.innerHTML = failures
        .map(function (f) {
          return (
            "<tr>" +
            "<td><code>" +
            esc(f.job_id || f.event_id) +
            "</code></td>" +
            "<td>" +
            esc(f.kind) +
            "</td>" +
            "<td>" +
            esc(f.stage || "-") +
            "</td>" +
            "<td>-</td>" +
            "<td>" +
            stateBadge("failed") +
            "</td>" +
            "<td>" +
            timeAgo(f.ts) +
            "</td>" +
            "<td>" +
            esc(f.error || f.message || "-") +
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
    const tbody = document.getElementById("workspace-list");
    const empty = document.getElementById("workspace-empty");
    try {
      const data = await apiJSON("/v1/workspaces");
      const list = (data && data.workspaces) || [];
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
    const tbody = document.getElementById("exposure-list");
    const empty = document.getElementById("exposure-empty");
    try {
      const data = await apiJSON("/v1/exposures");
      const list = (data && data.exposures) || [];
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
            stateBadge(ex.state) +
            "</td>" +
            "<td>" +
            timeAgo(ex.created_at) +
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

  async function refreshAll() {
    await Promise.all([
      loadStatus(),
      loadSandboxes(),
      loadJobs(),
      loadWorkspaces(),
      loadExposures(),
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
    if (!confirm("Destroy sandbox " + vmid + "? This cannot be undone.")) return;
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
      const data = await apiJSON("/v1/sandboxes/" + vmid);
      document.getElementById("detail-title").textContent =
        "Sandbox " + vmid + " — " + (data.name || "");
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

  window.closeModal = function (id) {
    document.getElementById(id).style.display = "none";
  };

  // --- New Sandbox Form ---

  function initNewSandboxForm() {
    document
      .getElementById("btn-new-sandbox")
      .addEventListener("click", function () {
        document.getElementById("modal-new-sandbox").style.display = "flex";
      });

    document
      .getElementById("form-new-sandbox")
      .addEventListener("submit", async function (e) {
        e.preventDefault();
        const fd = new FormData(e.target);
        const body = {
          name: fd.get("name") || "",
          profile: fd.get("profile") || "default",
          type: fd.get("type") || "",
          image: fd.get("image") || "",
          keepalive: fd.has("keepalive"),
        };
        const ttl = parseInt(fd.get("ttl_minutes"));
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

  // --- Init ---

  function init() {
    initTabs();
    initNewSandboxForm();

    document.getElementById("btn-refresh").addEventListener("click", refreshAll);

    // Initial load.
    refreshAll();

    // Auto-refresh.
    refreshTimer = setInterval(refreshAll, REFRESH_INTERVAL);
  }

  document.addEventListener("DOMContentLoaded", init);
})();
