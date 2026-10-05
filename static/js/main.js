// State
let subscriptions = [];
let templates = [];
let currentTemplateId = null;
let currentTemplateToken = "";
let pristineTemplateContent = null;
let templateContentEdited = false;

// ── Utilities ────────────────────────────────────────────────────────────────

function api(path, options = {}) {
  const headers = { "Content-Type": "application/json" };
  const token = localStorage.getItem("token");
  if (token) headers["Authorization"] = `Bearer ${token}`;

  return fetch(`/api${path}`, { ...options, headers: { ...headers, ...options.headers } })
    .then(async (res) => {
      const json = await res.json().catch(() => ({}));
      if (!res.ok) {
        const err = new Error(json.error || `HTTP ${res.status}`);
        err.status = res.status;
        if (res.status === 401) showLogin();
        throw err;
      }
      return json;
    });
}

// ── Auth / Login ─────────────────────────────────────────────────────────────

const AUTH_KEY = "token";

function getAuthKey() {
  try {
    return localStorage.getItem(AUTH_KEY) || "";
  } catch {
    return "";
  }
}

function setAuthKey(key) {
  try {
    localStorage.setItem(AUTH_KEY, key);
  } catch {
    /* ignore */
  }
}

function clearAuthKey() {
  try {
    localStorage.removeItem(AUTH_KEY);
  } catch {
    /* ignore */
  }
}

function showLogin() {
  const overlay = document.getElementById("login-overlay");
  if (overlay) overlay.hidden = false;
}

function hideLogin() {
  const overlay = document.getElementById("login-overlay");
  if (overlay) overlay.hidden = true;
}

function showLoginError(message) {
  const errorEl = document.getElementById("login-error");
  if (!errorEl) return;
  errorEl.textContent = message;
  errorEl.hidden = false;
}

function hideLoginError() {
  const errorEl = document.getElementById("login-error");
  if (errorEl) errorEl.hidden = true;
}

async function tryLoadAll() {
  try {
    await loadSubscriptions();
    await loadTemplates();
    return true;
  } catch (err) {
    if (err && err.status === 401) return false;
    showToast("加载失败: " + (err.message || err), true);
    return true;
  }
}

function afterLoginReady() {
  updateDashboardMetrics();
  newTemplate();
  renderTemplateList();
}

function showToast(message, isError = false) {
  const toast = document.getElementById("toast");
  const msg = document.getElementById("toast-message");
  msg.textContent = message;
  toast.classList.toggle("error", isError);
  toast.classList.remove("hidden");
  clearTimeout(toast._timer);
  toast._timer = setTimeout(() => toast.classList.add("hidden"), 3500);
}

function copyToClipboard(text) {
  navigator.clipboard.writeText(text).then(
    () => showToast("已复制到剪贴板"),
    () => showToast("复制失败，请手动复制", true)
  );
}

function subscriptionDownloadUrl(sub) {
  if (!sub) return "";
  const token = encodeURIComponent(sub.access_token || "");
  return `${location.origin}/download/${sub.id}?token=${token}`;
}

const TEMPLATE_RENDER_SUFFIX = {
  providers: "render",
  proxies: "render-proxies",
  nodes: "render-nodes",
};

function templateCopyPath(mode) {
  const suffix = TEMPLATE_RENDER_SUFFIX[mode] || "render";
  return `/api/templates/${currentTemplateId}/${suffix}?token=${encodeURIComponent(currentTemplateToken || "")}`;
}

function copyDefaultTemplateLink(mode) {
  const item = templates.find((t) => t.is_default);
  if (!item?.access_token) {
    showToast("请先保存默认配置", true);
    return;
  }
  if (item.mode === "raw" && mode !== "providers") {
    showToast("原样托管请使用完整配置地址", true);
    return;
  }
  const suffix = TEMPLATE_RENDER_SUFFIX[mode] || "render";
  copyToClipboard(`${location.origin}/api/templates/default/${suffix}?token=${encodeURIComponent(item.access_token)}`);
}

function formatBytes(bytes) {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`;
}

function relativeTime(dateStr) {
  if (!dateStr) return "—";
  const diff = Date.now() - new Date(dateStr).getTime();
  const m = Math.floor(diff / 60000);
  if (m < 1) return "刚刚";
  if (m < 60) return `${m} 分钟前`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h} 小时前`;
  return `${Math.floor(h / 24)} 天前`;
}

// ── Tab Switching ─────────────────────────────────────────────────────────────

function initTabs() {
  const tabBtns = document.querySelectorAll(".workspace-tab");
  tabBtns.forEach((btn) => {
    btn.addEventListener("click", () => {
      tabBtns.forEach((b) => {
        b.classList.remove("is-active");
        b.setAttribute("aria-selected", "false");
      });
      btn.classList.add("is-active");
      btn.setAttribute("aria-selected", "true");

      const target = btn.dataset.tabTarget;
      document.querySelectorAll(".workspace-panel").forEach((p) => {
        p.classList.remove("is-active");
        p.hidden = true;
      });
      const panel = document.getElementById(target);
      if (panel) {
        panel.classList.add("is-active");
        panel.hidden = false;
      }
    });
  });
}

// ── Quick Action Routing ──────────────────────────────────────────────────────

function initQuickActions() {
  document.getElementById("hero-subscribe-btn")?.addEventListener("click", () => {
    switchTab("subscriptions-panel");
    document.getElementById("subscribe-form")?.querySelector("input[name=name]")?.focus();
  });

  document.getElementById("hero-template-btn")?.addEventListener("click", () => {
    switchTab("templates-panel");
    newTemplate();
  });

  document.getElementById("default-template-link-btn")?.addEventListener("click", () => {
    copyDefaultTemplateLink("providers");
  });

  document.getElementById("default-expanded-template-link-btn")?.addEventListener("click", () => {
    copyDefaultTemplateLink("proxies");
  });
}

function switchTab(tabId) {
  const btn = document.querySelector(`[data-tab-target="${tabId}"]`);
  if (btn) btn.click();
}

// ── Dashboard Metrics ─────────────────────────────────────────────────────────

function updateDashboardMetrics() {
  document.getElementById("dashboard-subscription-count").textContent = subscriptions.length;

  const templateCount = document.getElementById("dashboard-template-count");
  if (templateCount) templateCount.textContent = templates.length;
}

// ── Advanced Settings Toggle ────────────────────────────────────────────────

function initAdvancedToggle() {
  document.querySelectorAll("[data-toggle-advanced]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const targetId = btn.dataset.toggleAdvanced;
      const panel = document.getElementById(targetId);
      if (!panel) return;
      const isHidden = panel.hidden;
      panel.hidden = !isHidden;
      btn.setAttribute("aria-expanded", String(isHidden));
    });
  });
}

function getSubscriptionSourceMode() {
  return document.querySelector('input[name="subscription_source"]:checked')?.value || "url";
}

function syncSubscriptionSourceMode() {
  const mode = getSubscriptionSourceMode();
  const urlField = document.getElementById("subscription-url-field");
  const urlInput = document.querySelector('#subscribe-form input[name="url"]');
  const nodesField = document.getElementById("subscription-nodes-field");
  const nodesInput = document.querySelector('#subscribe-form textarea[name="node_links_text"]');
  const headersField = document.getElementById("request-headers-field");

  const usingNodes = mode === "nodes";

  if (urlField) urlField.classList.toggle("hidden", usingNodes);
  if (nodesField) nodesField.classList.toggle("hidden", !usingNodes);
  if (headersField) headersField.classList.toggle("hidden", usingNodes);
  if (urlInput) urlInput.required = !usingNodes;
  if (nodesInput) nodesInput.required = usingNodes;
}

function initSubscriptionSourceToggle() {
  document.querySelectorAll('input[name="subscription_source"]').forEach((input) => {
    input.addEventListener("change", syncSubscriptionSourceMode);
  });
  syncSubscriptionSourceMode();
}

// ── Request Headers Helpers ─────────────────────────────────────────────────

function parseHeadersText(text) {
  const headers = {};
  text.split("\n").forEach((line) => {
    const idx = line.indexOf(":");
    if (idx > 0) {
      const key = line.slice(0, idx).trim();
      const val = line.slice(idx + 1).trim();
      if (key && val) headers[key] = val;
    }
  });
  return headers;
}

function stringifyHeaders(headers) {
  return Object.entries(headers || {})
    .map(([k, v]) => `${k}: ${v}`)
    .join("\n");
}

function formatSubscriptionType(type) {
  const normalized = (type || "unknown").toLowerCase();
  const labels = {
    clash: "Clash",
    mixed: "混合订阅",
    ss: "Shadowsocks",
    vmess: "VMess",
    trojan: "Trojan",
    vless: "VLESS",
    unknown: "待识别",
  };

  return {
    label: labels[normalized] || normalized.toUpperCase(),
    tone: ["mixed", "unknown"].includes(normalized) ? normalized : "known",
  };
}

function formatSubscriptionStatus(sub) {
  const lastRefresh = sub.last_check || sub.last_error_time;
  const relative = relativeTime(lastRefresh);

  if (sub.last_error) {
    return {
      label: `最后更新: ${relative} (失败: ${sub.last_error})`,
      className: "subscription-status subscription-status-error",
    };
  }

  return {
    label: lastRefresh ? `最后更新: ${relative}` : "等待首次刷新",
    className: "subscription-status",
  };
}

// ── Subscription List ─────────────────────────────────────────────────────────

function renderSubscriptionList() {
  const listEl = document.getElementById("subscriptions");
  const loadingEl = document.getElementById("loading");
  const emptyEl = document.getElementById("empty-state");

  if (!listEl) return;

  loadingEl.hidden = true;

  if (subscriptions.length === 0) {
    listEl.innerHTML = "";
    emptyEl.hidden = false;
    return;
  }

  emptyEl.hidden = true;
  listEl.innerHTML = subscriptions
    .map((sub) => {
      const typeMeta = formatSubscriptionType(sub.type);
      const statusMeta = formatSubscriptionStatus(sub);
      return `
    <article class="subscription-card" data-id="${sub.id}">
      <div class="subscription-head">
        <h3 class="subscription-name">${escapeHtml(sub.name)}</h3>
        <span class="subscription-type subscription-type-${escapeHtml(typeMeta.tone)}">${escapeHtml(typeMeta.label)}</span>
      </div>
      <div class="subscription-meta">
        ${sub.url === "nodes://manual"
          ? `<div class="subscription-meta-item">
               <strong>来源</strong>
               <span>手动添加</span>
             </div>`
          : `<div class="subscription-meta-item">
               <strong>地址</strong>
               <span class="subscription-url-text" title="${escapeHtml(sub.url)}">${escapeHtml(sub.url)}</span>
             </div>`
        }
        ${sub.filter ? `<div class="subscription-meta-item"><strong>筛选</strong><span>${escapeHtml(sub.filter)}</span></div>` : ""}
        ${sub.file_size ? `<div class="subscription-meta-item"><strong>大小</strong><span>${formatBytes(sub.file_size)}</span></div>` : ""}
        ${sub.node_count ? `<div class="subscription-meta-item"><strong>节点</strong><span>${sub.node_count}</span></div>` : ""}
        <div class="subscription-meta-item">
          <strong>刷新状态</strong>
          <span class="${escapeHtml(statusMeta.className)}">${escapeHtml(statusMeta.label)}</span>
        </div>
        <div class="subscription-meta-item">
          <strong>状态</strong>
          <span>${sub.status || "active"}</span>
        </div>
        ${Object.keys(sub.request_headers || {}).length > 0 ? `<div class="subscription-meta-item"><strong>请求头</strong><span>${Object.keys(sub.request_headers).length} 条</span></div>` : ""}
      </div>
      <div class="subscription-actions">
        <button type="button" class="btn btn-secondary btn-compact copy-download-btn" data-id="${sub.id}">复制下载地址</button>
        <button type="button" class="btn btn-secondary btn-compact reset-subscription-link-btn" data-id="${sub.id}" title="重新生成下载链接，旧链接立即失效">重置链接</button>
        <button type="button" class="btn btn-secondary btn-compact edit-subscription-btn" data-id="${sub.id}">编辑</button>
        ${sub.url !== "nodes://manual" ? `<button type="button" class="btn btn-secondary btn-compact refresh-subscription-btn" data-id="${sub.id}">刷新</button>` : ""}
        <button type="button" class="btn btn-danger btn-compact delete-subscription-btn" data-id="${sub.id}">删除</button>
      </div>
    </article>
  `
    })
    .join("");

  // Update subscription-last-updated
  const lastUpdatedEl = document.getElementById("subscription-last-updated");
  if (lastUpdatedEl) {
    const latest = subscriptions.reduce((max, s) => {
      if (!s.last_check) return max;
      const t = new Date(s.last_check).getTime();
      return t > max ? t : max;
    }, 0);
    lastUpdatedEl.textContent = latest ? relativeTime(new Date(latest).toISOString()) : "等待数据载入";
  }

  // Update header usage
  const headerUsageEl = document.getElementById("subscription-header-usage");
  if (headerUsageEl) {
    const count = subscriptions.filter((s) => s.request_headers && Object.keys(s.request_headers).length > 0).length;
    headerUsageEl.textContent = `${count} 条订阅已配置`;
  }

  // Bind events
  listEl.querySelectorAll(".copy-download-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      const sub = subscriptions.find((s) => s.id === btn.dataset.id);
      if (sub) copyToClipboard(subscriptionDownloadUrl(sub));
      else copyToClipboard(`${location.origin}/download/${btn.dataset.id}`);
    });
  });
  listEl.querySelectorAll(".reset-subscription-link-btn").forEach((btn) => {
    btn.addEventListener("click", () => resetSubscriptionLink(btn.dataset.id));
  });
  listEl.querySelectorAll(".edit-subscription-btn").forEach((btn) => {
    btn.addEventListener("click", () => openEditModal(btn.dataset.id));
  });
  listEl.querySelectorAll(".refresh-subscription-btn").forEach((btn) => {
    btn.addEventListener("click", () => refreshSubscription(btn.dataset.id));
  });
  listEl.querySelectorAll(".delete-subscription-btn").forEach((btn) => {
    btn.addEventListener("click", () => deleteSubscription(btn.dataset.id));
  });
}

function escapeHtml(str) {
  const div = document.createElement("div");
  div.textContent = str;
  return div.innerHTML;
}

// ── Subscription Form ─────────────────────────────────────────────────────────

function initSubscriptionForm() {
  const form = document.getElementById("subscribe-form");
  if (!form) return;

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const btn = document.getElementById("submit-btn");
    const orig = btn.textContent;
    btn.disabled = true;
    btn.textContent = "添加中...";

    try {
      const fd = new FormData(form);
      const sourceMode = fd.get("subscription_source") || "url";
      const headersText = fd.get("request_headers_text") || "";
      const payload = {
        name: fd.get("name"),
        filter: fd.get("filter") || "",
      };

      let endpoint = "/subscribe";
      if (sourceMode === "nodes") {
        payload.node_links_text = fd.get("node_links_text") || "";
        endpoint = "/subscribe/nodes";
      } else {
        payload.url = fd.get("url");
        payload.request_headers = parseHeadersText(headersText);
      }

      const res = await api(endpoint, { method: "POST", body: JSON.stringify(payload) });
      if (res.success) {
        showToast("订阅添加成功");
        form.reset();
        document.getElementById("subscription-advanced").hidden = true;
        document.querySelector('input[name="subscription_source"][value="url"]').checked = true;
        syncSubscriptionSourceMode();
        await loadSubscriptions();
        updateDashboardMetrics();
      } else {
        showToast(res.error || "添加失败", true);
      }
    } catch (err) {
      showToast(err.message, true);
    } finally {
      btn.disabled = false;
      btn.textContent = orig;
    }
  });
}

// ── Subscription CRUD ─────────────────────────────────────────────────────────

async function resetSubscriptionLink(id) {
  try {
    const res = await api(`/subscribe/${id}/reset`, { method: "POST" });
    if (!res.success) {
      showToast(res.error || "重置失败", true);
      return;
    }
    const updated = res.data;
    const idx = subscriptions.findIndex((s) => s.id === id);
    if (idx >= 0 && updated) subscriptions[idx] = updated;
    renderSubscriptionList();
    showToast("订阅链接已重置，旧链接已失效");
  } catch (err) {
    if (!(err && err.status === 401)) showToast(err.message, true);
  }
}

async function loadSubscriptions() {
  try {
    const res = await api("/subscriptions");
    subscriptions = res.data || [];
    renderSubscriptionList();
  } catch (err) {
    if (err && err.status === 401) throw err;
    showToast("加载订阅失败: " + err.message, true);
  }
}

async function refreshSubscription(id) {
  const sub = subscriptions.find((s) => s.id === id);
  if (!sub) return;
  try {
    const res = await api(`/subscribe/${id}/refresh`, {
      method: "POST",
      body: JSON.stringify({
        name: sub.name,
        url: sub.url,
        filter: sub.filter,
        request_headers: sub.request_headers || {},
      }),
    });
    if (res.success) {
      showToast("刷新成功");
      await loadSubscriptions();
      updateDashboardMetrics();
    } else {
      showToast(res.error || "刷新失败", true);
    }
  } catch (err) {
    showToast(err.message, true);
  }
}

async function deleteSubscription(id) {
  if (!confirm("确定要删除这个订阅吗？")) return;
  try {
    const res = await api(`/subscribe/${id}`, { method: "DELETE" });
    if (res.success) {
      showToast("已删除");
      await loadSubscriptions();
      updateDashboardMetrics();
    } else {
      showToast(res.error || "删除失败", true);
    }
  } catch (err) {
    showToast(err.message, true);
  }
}

// ── Edit Modal ────────────────────────────────────────────────────────────────

function openEditModal(id) {
  const sub = subscriptions.find((s) => s.id === id);
  if (!sub) return;

  const modal = document.getElementById("edit-modal");
  const form = document.getElementById("edit-form");
  const idInput = document.getElementById("edit-subscription-id-input");
  const nameInput = document.getElementById("edit-subscription-name-input");
  const urlInput = document.getElementById("edit-subscription-url-input");
  const filterInput = document.getElementById("edit-subscription-filter-input");
  const headersInput = document.getElementById("edit-request-headers-input");

  if (!modal || !form || !idInput || !nameInput || !urlInput || !filterInput || !headersInput) {
    showToast("编辑表单加载失败，请刷新页面后重试", true);
    return;
  }

  form.reset();

  idInput.value = sub.id;
  nameInput.value = sub.name;
  urlInput.value = sub.url;
  filterInput.value = sub.filter || "";
  headersInput.value = stringifyHeaders(sub.request_headers);

  modal.classList.remove("hidden");

  // Bind modal buttons
  document.getElementById("close-edit-modal").onclick = () => modal.classList.add("hidden");
  document.getElementById("cancel-edit-modal").onclick = () => modal.classList.add("hidden");

  document.getElementById("edit-save-btn").onclick = () => saveEditOnly(id);
  document.getElementById("edit-submit-btn").onclick = () => saveEditAndRefresh(id);

  form.onsubmit = (e) => {
    e.preventDefault();
    saveEditAndRefresh(id);
  };
}

async function saveEditOnly(id) {
  const form = document.getElementById("edit-form");
  const fd = new FormData(form);
  try {
    const res = await api(`/subscribe/${id}`, {
      method: "PUT",
      body: JSON.stringify({
        name: fd.get("name"),
        url: fd.get("url"),
        filter: fd.get("filter"),
        request_headers: parseHeadersText(fd.get("request_headers_text")),
      }),
    });
    if (res.success) {
      showToast("保存成功");
      document.getElementById("edit-modal").classList.add("hidden");
      await loadSubscriptions();
      updateDashboardMetrics();
    } else {
      showToast(res.error || "保存失败", true);
    }
  } catch (err) {
    showToast(err.message, true);
  }
}

async function saveEditAndRefresh(id) {
  const form = document.getElementById("edit-form");
  const fd = new FormData(form);
  const sub = subscriptions.find((s) => s.id === id);
  try {
    const res = await api(`/subscribe/${id}/refresh`, {
      method: "POST",
      body: JSON.stringify({
        name: fd.get("name"),
        url: fd.get("url"),
        filter: fd.get("filter"),
        request_headers: parseHeadersText(fd.get("request_headers_text")),
      }),
    });
    if (res.success) {
      showToast("保存并刷新成功");
      document.getElementById("edit-modal").classList.add("hidden");
      await loadSubscriptions();
      updateDashboardMetrics();
    } else {
      showToast(res.error || "操作失败", true);
    }
  } catch (err) {
    showToast(err.message, true);
  }
}

// ── Template List ─────────────────────────────────────────────────────────────

function renderTemplateList() {
  const listEl = document.getElementById("templates");
  const emptyEl = document.getElementById("template-empty-state");
  if (!listEl) return;

  if (templates.length === 0) {
    listEl.innerHTML = "";
    emptyEl.hidden = false;
    return;
  }

  emptyEl.hidden = true;
  listEl.innerHTML = templates
    .map(
      (t) => `
    <article class="template-card ${t.id === currentTemplateId ? "active" : ""}" data-id="${t.id}">
      <div class="template-card-head">
        <h3 class="template-name">${escapeHtml(t.name)}</h3>
      </div>
      <p class="template-card-meta">
        ${t.mode === "raw" ? "完整配置原样托管" : (t.use_all_subscriptions ? "使用全部订阅" : `${(t.selected_subscription_ids || []).length} 个订阅`)}
        · ${relativeTime(t.updated_at)}
      </p>
      <div class="template-actions">
        <button type="button" class="btn btn-secondary btn-compact select-template-btn" data-id="${t.id}">选择</button>
      </div>
    </article>
  `
    )
    .join("");

  const lastUpdated = document.getElementById("template-last-updated");
  if (lastUpdated) {
    const latest = templates.reduce((max, t) => {
      if (!t.updated_at) return max;
      const d = new Date(t.updated_at).getTime();
      return d > max ? d : max;
    }, 0);
    lastUpdated.textContent = latest ? relativeTime(new Date(latest).toISOString()) : "等待数据载入";
  }

  // Bind events
  listEl.querySelectorAll(".select-template-btn").forEach((btn) => {
    btn.addEventListener("click", () => selectTemplate(btn.dataset.id));
  });
}

async function selectTemplate(id) {
  try {
    const res = await api(`/templates/${id}`);
    if (res.success) {
      currentTemplateId = id;
      currentTemplateToken = res.data?.access_token || "";
      populateTemplateForm(res.data);
      renderTemplateList();
    }
  } catch (err) {
    showToast("加载模板失败: " + err.message, true);
  }
}

function populateTemplateForm(t) {
  const form = document.getElementById("template-form");
  if (!form) return;
  form.reset();
  form.querySelector('[name="id"]').value = t.id;
  form.querySelector('[name="name"]').value = t.name;
  form.querySelector('[name="mode"]').value = t.mode || "generated";
  form.querySelector('[name="content"]').value = t.content;
  pristineTemplateContent = t.mode === "raw" ? t.content : null;
  templateContentEdited = false;
  document.getElementById("template-current-name").textContent = t.name;
  document.getElementById("template-editor-status").textContent = "编辑模式";
  document.getElementById("template-current-subscription-count").textContent = t.use_all_subscriptions
    ? "将使用全部订阅"
    : `${(t.selected_subscription_ids || []).length} 个订阅`;

  // Populate subscription options
  renderTemplateSubscriptionOptions(t);
  updateTemplateModeUI(t.mode || "generated");

  // Show delete button only for existing templates
  const deleteBtn = document.getElementById("delete-template-btn");
  if (deleteBtn) deleteBtn.style.display = t.id ? "" : "none";
}

function renderTemplateSubscriptionOptions(template) {
  const container = document.getElementById("template-subscription-options");
  if (!container) return;

  if (subscriptions.length === 0) {
    container.innerHTML = '<p class="template-subscription-empty">暂无订阅可供选择</p>';
    return;
  }

  const useAll = template?.use_all_subscriptions === true;
  const selected = new Set(template?.selected_subscription_ids || []);
  const prefixes = template?.subscription_prefixes || {};

  container.innerHTML = subscriptions
    .map(
      (sub) => `
    <label class="template-subscription-option" data-id="${sub.id}">
      <input type="checkbox" name="template-subs" value="${sub.id}" ${useAll || selected.has(sub.id) ? "checked" : ""}>
      <div class="template-subscription-info">
        <strong>${escapeHtml(sub.name)}</strong>
        <span>${escapeHtml(sub.url)}</span>
      </div>
      <input type="text" class="template-prefix-input" name="template-prefix-${sub.id}" value="${escapeHtml(prefixes[sub.id] || "")}" placeholder="自定义前缀" title="自定义 additional-prefix，留空使用默认">
    </label>
  `
    )
    .join("");
}

function updateTemplateModeUI(mode) {
  const raw = mode === "raw";
  const field = document.querySelector(".template-subscription-field");
  if (field) field.hidden = raw;
  const content = document.getElementById("template-content-input");
  if (content) content.placeholder = raw ? "输入或导入完整 Clash YAML 配置，保存后按原内容输出" : "输入基础 YAML 配置";
  document.querySelectorAll(".copy-dropdown-item").forEach((item) => {
    item.hidden = raw && item.dataset.copy !== "providers";
    if (item.dataset.copy === "providers") item.textContent = raw ? "复制完整配置地址" : "复制模板地址";
  });
  if (raw) document.getElementById("template-current-subscription-count").textContent = "完整配置原样输出";
}

// ── Template Form ────────────────────────────────────────────────────────────

const NEW_TEMPLATE_DEFAULT_CONTENT = `proxy-providers: {}
proxy-groups:
  - { name: 🚩 PROXY, type: select, proxies: [♻️ 自动选择, 🚀 手动切换] }
  - { name: 🤖 人工智能, type: fallback, proxies: [✨ AISelect] }

  #  代理
  - { name: 🚀 手动切换, type: select, include-all: true }
  - { name: ♻️ 自动选择, type: fallback, interval: 300, proxies: [🇸🇬 Singapore, 🇯🇵 Japan, 🇺🇸 USA, 🚀 手动切换] }
  # 基础节点
  - { name: ✨ AISelect, type: select, interval: 300, proxies: [🇺🇸 USA, 🇸🇬 Singapore, 🇯🇵 Japan] }

  - { name: 🇺🇸 USA, type: fallback, include-all: true, exclude-type: direct, filter: "美国|United States" }
  - { name: 🇯🇵 Japan, type: fallback, include-all: true, exclude-type: direct, filter: "日本|Japan" }
  - { name: 🇸🇬 Singapore, type: fallback, include-all: true, exclude-type: direct, filter: "新加坡|Singapore" }
  - { name: 🇭🇰 HongKong, type: fallback, include-all: true, exclude-type: direct, filter: "香港|Hong Kong" }

rule-providers:
  AWAvenue-Ads:
    type: http
    behavior: domain
    format: yaml
    # path可为空(仅限clash.meta 1.15.0以上版本)
    path: ./ruleset/AWAvenue-Ads.yaml
    url: "https://raw.githubusercontent.com/TG-Twilight/AWAvenue-Ads-Rule/main/Filters/AWAvenue-Ads-Rule-Clash.yaml"
    interval: 600

rules:
  - SRC-PORT,7890,🚩 PROXY
  - DST-PORT,22,DIRECT
  - DOMAIN-SUFFIX,apifox.it.com,REJECT
  - AND,(AND,(DST-PORT,443),(NETWORK,UDP)),(NOT,((GEOSITE,cn))),REJECT
  - AND,(AND,(DST-PORT,443),(NETWORK,UDP)),(NOT,((GEOIP,CN))),REJECT
  - RULE-SET,AWAvenue-Ads,REJECT
  - GEOIP,lan,DIRECT,no-resolve

  - GEOSITE,category-ads-all,REJECT
  - GEOSITE,private,DIRECT
  - GEOSITE,icloud,DIRECT
  - GEOSITE,apple,DIRECT
  - GEOSITE,anthropic,🤖 人工智能

  - GEOSITE,category-ai-chat-!cn,🤖 人工智能

  - GEOSITE,youtube,🚩 PROXY
  - GEOSITE,google,🤖 人工智能
  - GEOSITE,github,🚩 PROXY
  - GEOSITE,onedrive,DIRECT
  - GEOSITE,microsoft,DIRECT

  - GEOSITE,CN,DIRECT
  - GEOSITE,steam@cn,DIRECT
  - GEOSITE,category-games@cn,DIRECT
  - GEOSITE,geolocation-!cn,🚩 PROXY
  - GEOSITE,telegram,🇭🇰 HongKong
  - GEOIP,google,🚩 PROXY
  - GEOIP,telegram,🇭🇰 HongKong
  - GEOIP,CN,DIRECT
  - MATCH,🚩 PROXY
`;

function initTemplateForm() {
  const form = document.getElementById("template-form");
  if (!form) return;

  document.getElementById("template-content-input")?.addEventListener("input", () => {
    templateContentEdited = true;
  });

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const btn = document.getElementById("template-submit-btn");
    const orig = btn.textContent;
    btn.disabled = true;
    btn.textContent = "保存中...";

    try {
      const fd = new FormData(form);
      const useAll = fd.get("use_all") === "on";
      const selectedIds = useAll
        ? []
        : Array.from(form.querySelectorAll('input[name="template-subs"]:checked')).map((c) => c.value);

      const subscriptionPrefixes = {};
      form.querySelectorAll(".template-prefix-input").forEach((input) => {
        const subId = input.name.replace("template-prefix-", "");
        if (input.value.trim()) {
          subscriptionPrefixes[subId] = input.value.trim();
        }
      });

      const payload = {
        name: fd.get("name"),
        mode: fd.get("mode") || "raw",
        content: fd.get("mode") === "raw" && pristineTemplateContent !== null && !templateContentEdited
          ? pristineTemplateContent
          : fd.get("content"),
        use_all_subscriptions: useAll,
        selected_subscription_ids: selectedIds,
        subscription_prefixes: subscriptionPrefixes,
      };

      const id = fd.get("id");
      const isNew = !id;
      const res = isNew
        ? await api("/templates", { method: "POST", body: JSON.stringify(payload) })
        : await api(`/templates/${id}`, { method: "PUT", body: JSON.stringify(payload) });

      if (res.success) {
        showToast(isNew ? "模板创建成功" : "模板更新成功");
        await loadTemplates();
        if (res.data?.id) {
          currentTemplateId = res.data.id;
          await selectTemplate(res.data.id);
        }
        updateDashboardMetrics();
      } else {
        showToast(res.error || "保存失败", true);
      }
    } catch (err) {
      showToast(err.message, true);
    } finally {
      btn.disabled = false;
      btn.textContent = orig;
    }
  });

  document.getElementById("copy-template-url-btn")?.addEventListener("click", () => {
    if (currentTemplateId) copyToClipboard(`${location.origin}${templateCopyPath("providers")}`);
    else showToast("请先选择一个模板", true);
  });

  document.getElementById("copy-expanded-template-url-btn")?.addEventListener("click", () => {
    if (currentTemplateId) copyToClipboard(`${location.origin}${templateCopyPath("proxies")}`);
    else showToast("请先选择一个模板", true);
  });

  document.getElementById("reset-template-link-btn")?.addEventListener("click", async () => {
    if (!currentTemplateId) {
      showToast("请先选择一个模板", true);
      return;
    }
    try {
      const res = await api(`/templates/${currentTemplateId}/reset`, { method: "POST" });
      if (!res.success) {
        showToast(res.error || "重置失败", true);
        return;
      }
      currentTemplateToken = res.data?.access_token || "";
      showToast("模板链接已重置，旧链接已失效");
    } catch (err) {
      if (!(err && err.status === 401)) showToast(err.message, true);
    }
  });

  // Copy dropdown
  const copyToggle = document.getElementById("copy-dropdown-toggle");
  const copyMenu = document.getElementById("copy-dropdown-menu");
  if (copyToggle && copyMenu) {
    copyToggle.addEventListener("click", (e) => {
      e.stopPropagation();
      copyMenu.classList.toggle("hidden");
    });

    copyMenu.querySelectorAll(".copy-dropdown-item").forEach((item) => {
      item.addEventListener("click", () => {
        if (!currentTemplateId) {
          showToast("请先选择一个模板", true);
          copyMenu.classList.add("hidden");
          return;
        }
        const mode = item.dataset.copy;
        copyToClipboard(`${location.origin}${templateCopyPath(mode)}`);
        copyMenu.classList.add("hidden");
      });
    });

    document.addEventListener("click", () => copyMenu.classList.add("hidden"));
  }

  document.getElementById("delete-template-btn")?.addEventListener("click", async () => {
    if (!currentTemplateId) return;
    if (!confirm("确定要删除这个模板吗？")) return;
    try {
      const res = await api(`/templates/${currentTemplateId}`, { method: "DELETE" });
      if (res.success) {
        showToast("已删除");
        currentTemplateId = null;
        newTemplate();
        await loadTemplates();
        updateDashboardMetrics();
      } else {
        showToast(res.error || "删除失败", true);
      }
    } catch (err) {
      showToast(err.message, true);
    }
  });

  document.getElementById("new-template-btn")?.addEventListener("click", newTemplate);

  document.getElementById("template-mode-input")?.addEventListener("change", (e) => {
    const content = document.getElementById("template-content-input");
    if (e.target.value === "generated" && !content.value) content.value = NEW_TEMPLATE_DEFAULT_CONTENT;
    updateTemplateModeUI(e.target.value);
  });
  document.getElementById("template-file-input")?.addEventListener("change", async (e) => {
    const file = e.target.files?.[0];
    if (!file) return;
    if (file.size > 50 * 1024 * 1024) {
      showToast("配置文件过大", true);
      e.target.value = "";
      return;
    }
    try {
      pristineTemplateContent = await file.text();
      templateContentEdited = false;
      document.getElementById("template-content-input").value = pristineTemplateContent;
      document.getElementById("template-mode-input").value = "raw";
      updateTemplateModeUI("raw");
    } catch (err) {
      showToast("读取 YAML 文件失败: " + err.message, true);
    } finally {
      e.target.value = "";
    }
  });

  document.getElementById("select-all-template-subscriptions-btn")?.addEventListener("click", () => {
    document.querySelectorAll('#template-form input[name="template-subs"]').forEach((c) => (c.checked = true));
  });

  document.getElementById("clear-template-subscriptions-btn")?.addEventListener("click", () => {
    document.querySelectorAll('#template-form input[name="template-subs"]').forEach((c) => (c.checked = false));
  });
}

function newTemplate() {
  currentTemplateId = null;
  currentTemplateToken = "";
  pristineTemplateContent = null;
  templateContentEdited = false;
  const form = document.getElementById("template-form");
  if (form) {
    form.reset();
    const idInput = form.querySelector('[name="id"]');
    if (idInput) idInput.value = "";
    const contentInput = form.querySelector('[name="content"]');
    if (contentInput) contentInput.value = "";
    const modeInput = form.querySelector('[name="mode"]');
    if (modeInput) modeInput.value = "raw";
  }
  document.getElementById("template-editor-status").textContent = "新建模式";
  document.getElementById("template-current-name").textContent = "未命名模板";
  document.getElementById("template-current-subscription-count").textContent = "将使用全部订阅";
  const deleteBtn = document.getElementById("delete-template-btn");
  if (deleteBtn) deleteBtn.style.display = "none";
  renderTemplateSubscriptionOptions({});
  updateTemplateModeUI("raw");
  renderTemplateList();
}

// ── Template CRUD ─────────────────────────────────────────────────────────────

async function loadTemplates() {
  try {
    const res = await api("/templates");
    templates = res.data || [];
    renderTemplateList();
  } catch (err) {
    if (err && err.status === 401) throw err;
    showToast("加载模板失败: " + err.message, true);
  }
}

// ── Reload button ────────────────────────────────────────────────────────────

function initReloadButton() {
  document.getElementById("reload-subscriptions-btn")?.addEventListener("click", async () => {
    const btn = document.getElementById("reload-subscriptions-btn");
    const orig = btn.textContent;
    btn.disabled = true;
    btn.textContent = "刷新中...";
    try {
      await loadSubscriptions();
      await loadTemplates();
      updateDashboardMetrics();
      showToast("已刷新");
    } catch (err) {
      if (!(err && err.status === 401)) showToast(err.message, true);
    } finally {
      btn.disabled = false;
      btn.textContent = orig;
    }
  });
}

// ── Auth UI / Init ───────────────────────────────────────────────────────────

function initLogin() {
  const form = document.getElementById("login-form");
  const input = document.getElementById("login-key-input");
  const logoutBtn = document.getElementById("logout-btn");

  form?.addEventListener("submit", async (e) => {
    e.preventDefault();
    const key = (input?.value || "").trim();
    if (!key) return;

    setAuthKey(key);
    hideLoginError();
    const ok = await tryLoadAll();
    if (!ok) {
      clearAuthKey();
      if (input) input.select();
      showLoginError("密钥错误，请重新输入");
      return;
    }
    hideLogin();
    if (input) input.value = "";
    afterLoginReady();
  });

  logoutBtn?.addEventListener("click", () => {
    clearAuthKey();
    subscriptions = [];
    templates = [];
    currentTemplateId = null;
    currentTemplateToken = "";
    hideLoginError();
    const subUpdated = document.getElementById("subscription-last-updated");
    if (subUpdated) subUpdated.textContent = "等待数据载入";
    const tplUpdated = document.getElementById("template-last-updated");
    if (tplUpdated) tplUpdated.textContent = "等待数据载入";
    newTemplate();
    renderSubscriptionList();
    renderTemplateList();
    updateDashboardMetrics();
    showLogin();
  });
}

document.addEventListener("DOMContentLoaded", async () => {
  initTabs();
  initQuickActions();
  initAdvancedToggle();
  initSubscriptionSourceToggle();
  initSubscriptionForm();
  initTemplateForm();
  initReloadButton();
  initLogin();

  const ok = await tryLoadAll();
  if (ok) {
    hideLogin();
    afterLoginReady();
  } else {
    showLogin();
  }
});
