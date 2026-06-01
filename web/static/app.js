const $ = (q, el = document) => el.querySelector(q);
const $$ = (q, el = document) => [...el.querySelectorAll(q)];
const state = { view: "projects", editorProjectId: "", detailProjectId: "", detailTab: "status", currentUser: null, projects: [], secrets: [], nodes: [], workers: [], notifications: [], users: [], records: [], gitRefs: [] };
let editing = null;
let detailTimer = null;
let searchText = "";
let groupFilter = "";
let logStream = null;
const LOG_LIMIT = 1200;
const LOG_TAIL = 800;
const STALE_RUNNING_MS = 35 * 60 * 1000;
const CUSTOM_TEMPLATE_KEY = "qfb_command_templates";
let logLines = [];
let logPending = [];
let logFlushTimer = null;
let activeLogRecord = null;
let applyingRoute = false;
let syncTimer = null;
let syncingState = false;
let logReconnectTimer = null;
let logReconnectAttempt = 0;
let currentStreamId = "";
let consoleTerm = null;
let consoleSocket = null;
let consoleFitAddon = null;
let unreadNotifs = [];
let prevRecordStatuses = {};
let prevRecordStatuses_records = [];

async function api(path, options = {}) {
  const res = await fetch(path, { headers: { "Content-Type": "application/json" }, ...options });
  if (!res.ok) {
    let msg = res.statusText;
    try { msg = (await res.json()).error || msg; } catch {}
    const err = new Error(msg);
    err.status = res.status;
    throw err;
  }
  return res.json();
}

async function load() {
  let data;
  try {
    data = await api("/api/bootstrap");
  } catch (e) {
    if (e.status === 401) {
      renderLogin();
      return;
    }
    throw e;
  }
  applyBootstrap(data, false);
  prevRecordStatuses_records = (data.records || []).map(r => ({ id: r.id, status: r.status }));
  applyRouteFromHash(false);
  render();
}

function applyBootstrap(data, shouldRender = true) {
  Object.assign(state, {
    currentUser: data.currentUser || null,
    projects: data.projects || [],
    secrets: data.secrets || [],
    nodes: data.nodes || [],
    workers: data.workers || [],
    notifications: data.notifications || [],
    users: data.users || [],
    records: data.records || []
  });
  if (activeLogRecord) {
    const latest = state.records.find(r => r.id === activeLogRecord.id);
    if (latest) {
      activeLogRecord = { ...activeLogRecord, ...latest };
      if ((latest.log || []).length >= logLines.length) resetLiveLog((latest.log || []).slice(-LOG_LIMIT));
      updateLogProgress();
    }
  }
  checkRecordChanges(prevRecordStatuses_records, data.records || []);
  prevRecordStatuses_records = (data.records || []).map(r => ({ id: r.id, status: r.status }));
  ensureLiveSync();
  if (shouldRender) render();
}

function fmt(t) {
  return t ? new Date(t).toLocaleString("zh-CN", { hour12: false }) : "-";
}

function esc(s = "") {
  return String(s).replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

function statusBadge(s = "") {
  const labels = { success: "成功", failed: "失败", stopped: "已终止", running: "发布中", valid: "有效", invalid: "无效", unknown: "未知" };
  const icons = { success: "check_circle", failed: "error", stopped: "stop_circle", running: "sync", valid: "check_circle", invalid: "cancel", unknown: "warning" };
  const key = s || "unknown";
  return `<span class="badge ${esc(key)}"><span class="material-symbols-outlined">${icons[key] || "radio_button_unchecked"}</span>${esc(labels[key] || s || "未发布")}</span>`;
}

function nodeName(id) {
  return state.nodes.find(n => n.id === id)?.code || "-";
}

function workerName(id) {
  return state.workers.find(w => w.id === id)?.name || "-";
}

function projectWorkerNames(project) {
  const ids = project.build?.workerIds || [];
  return ids.length ? ids.map(workerName).filter(x => x !== "-").join("、") || "指定 worker" : "全部";
}

function secretOptions(selected = "") {
  return `<option value="">不选择</option>${state.secrets.map(s => `<option value="${s.id}" ${s.id === selected ? "selected" : ""}>${esc(s.code)} · ${esc(s.type)}</option>`).join("")}`;
}

function nodeOptions(selected = "") {
  const valid = state.nodes.filter(n => n.status === "valid" || n.id === selected);
  return `<option value="">请选择</option>${valid.map(n => `<option value="${n.id}" ${n.id === selected ? "selected" : ""}>${esc(n.code)} · ${esc(n.type)}${n.status !== "valid" ? " · 无效" : ""}</option>`).join("")}`;
}

function notificationOptions(selected = "") {
  return `<option value="">不选择</option>${state.notifications.map(n => `<option value="${n.id}" ${n.id === selected ? "selected" : ""}>${esc(n.code)} · ${esc(notifyTypeLabel(n.type))}</option>`).join("")}`;
}

function refPicker({ id, optionsId, name = "", value = "", refs = [], disabled = false, refresh = "" }) {
  const current = value || "";
  const list = refs.length ? refs : [current].filter(Boolean);
  return `<div class="ref-picker ${disabled ? "disabled" : ""}">
    <input id="${esc(id)}" ${name ? `name="${esc(name)}"` : ""} type="hidden" value="${esc(current)}">
    <div class="ref-selected">${refSelectedHtml(current)}</div>
    <div class="ref-actions">
      <button type="button" class="ref-trigger" onclick="toggleRefDropdown(this, event)" ${disabled ? "disabled" : ""}><span class="material-symbols-outlined">arrow_drop_down</span>选择</button>
      ${refresh ? `<button type="button" class="action-icon ref-refresh" onclick="${refresh}" title="刷新分支/Tag" ${disabled ? "disabled" : ""}><span class="material-symbols-outlined">sync</span></button>` : ""}
    </div>
    <div class="ref-dropdown">
      <input class="ref-filter" placeholder="过滤分支 / Tag" autocomplete="off" oninput="filterRefOptions(this)">
      <div id="${esc(optionsId)}" class="ref-options">${refOptionHtml(list, current)}</div>
    </div>
  </div>`;
}

function refSelectedHtml(current = "") {
  return current ? `<span class="ref-tag" title="${esc(current)}">${esc(current)}</span>` : `<span class="ref-empty">未选择分支 / Tag</span>`;
}

function refOptionHtml(refs = [], current = "") {
  const uniq = [...new Set(refs.filter(Boolean))];
  if (!uniq.length) return `<div class="ref-empty">暂无分支 / Tag，请先测试连接</div>`;
  return uniq.map(ref => `<button type="button" class="${ref === current ? "selected" : ""}" data-ref="${esc(ref)}" onclick="selectRefOption(this, event)"><span>${esc(ref)}</span>${ref === current ? `<span class="material-symbols-outlined">check</span>` : ""}</button>`).join("");
}

function renderRefOptions(id, refs = []) {
  const options = document.getElementById(id);
  if (!options) return;
  const picker = options.closest(".ref-picker");
  const value = $(".ref-picker > input", picker)?.value || "";
  options.innerHTML = refOptionHtml(value ? [value, ...refs] : refs, value);
  if (picker.classList.contains("open")) filterRefOptions($(".ref-filter", picker));
}

function toggleRefDropdown(btn, ev) {
  ev?.preventDefault();
  ev?.stopPropagation();
  const picker = btn.closest(".ref-picker");
  const open = !picker.classList.contains("open");
  closeRefDropdowns(picker);
  picker.classList.toggle("open", open);
  if (open) {
    const filter = $(".ref-filter", picker);
    if (filter) {
      filter.value = "";
      filter.focus();
      filterRefOptions(filter);
    }
  }
}

function closeRefDropdowns(except = null) {
  $$(".ref-picker.open").forEach(p => {
    if (p !== except) p.classList.remove("open");
  });
}

function filterRefOptions(input) {
  const picker = input.closest(".ref-picker");
  if (!picker) return;
  const q = input.value.trim().toLowerCase();
  $$(".ref-options button", picker).forEach(btn => {
    const text = btn.dataset.ref.toLowerCase();
    btn.hidden = q && !text.includes(q);
  });
}

function selectRefOption(btn, ev) {
  ev?.preventDefault();
  const picker = btn.closest(".ref-picker");
  const input = $(".ref-picker > input", picker);
  if (input) {
    input.value = btn.dataset.ref || "";
    input.dispatchEvent(new Event("change", { bubbles: true }));
  }
  $(".ref-selected", picker).innerHTML = refSelectedHtml(input?.value || "");
  $$(".ref-options button", picker).forEach(item => {
    const selected = item.dataset.ref === input?.value;
    item.classList.toggle("selected", selected);
    item.querySelector(".material-symbols-outlined")?.remove();
    if (selected) item.insertAdjacentHTML("beforeend", `<span class="material-symbols-outlined">check</span>`);
  });
  picker.classList.remove("open");
}

function notifyTypeLabel(t) {
  return { wecom: "企业微信", feishu: "飞书" }[t] || t || "-";
}

function projectDisplayStatus(project) {
  const records = state.records.filter(r => r.projectId === project.id);
  if (!records.length) return "";
  const latest = records.sort((a, b) => new Date(b.startedAt) - new Date(a.startedAt))[0];
  return latest.status;
}

function projectDisplayTime(project) {
  const records = state.records.filter(r => r.projectId === project.id);
  if (!records.length) return null;
  return records.sort((a, b) => new Date(b.startedAt) - new Date(a.startedAt))[0].startedAt;
}

function isAdmin() {
  return state.currentUser?.role === "admin";
}

function roleLabel(role) {
  return { admin: "管理员", user: "普通用户" }[role] || role || "-";
}

function userPermSummary(u) {
  if (u.role === "admin") return "全部项目";
  return (u.projectPerms || []).map(p => {
    const proj = state.projects.find(x => x.id === p.projectId);
    const flags = [p.canRun ? "运行" : "", p.canEdit ? "修改" : ""].filter(Boolean).join("/");
    return proj ? `${proj.name}(${flags})` : "";
  }).filter(Boolean).join("、") || "无授权";
}

function projectPerm(projectId) {
  if (isAdmin()) return { canRun: true, canEdit: true };
  return (state.currentUser?.projectPerms || []).find(p => p.projectId === projectId) || {};
}

function canRunProject(projectId) {
  return !!projectPerm(projectId).canRun;
}

function canEditProject(projectId) {
  return !!projectPerm(projectId).canEdit;
}

function renderLogin() {
  state.currentUser = null;
  closeLogStream();
  document.body.classList.add("auth-mode");
  $("#loginPassword").value = "";
  $("#loginError").textContent = "";
}

function renderAppShell() {
  document.body.classList.remove("auth-mode");
  $("#accountName").textContent = state.currentUser?.name || state.currentUser?.code || "-";
  $$("[data-admin-only]").forEach(el => el.style.display = isAdmin() ? "" : "none");
  $("#settingsBtn").style.display = isAdmin() ? "" : "none";
  if (!isAdmin() && state.view === "global") state.view = "projects";
}

function goProjects() {
  state.view = "projects";
  state.detailProjectId = "";
  state.editorProjectId = "";
  render();
}

function goGlobalConfig() {
  if (!isAdmin()) return alert("需要管理员权限");
  state.view = "global";
  render();
}

function applyRouteFromHash(shouldRender = true) {
  const raw = location.hash.replace(/^#\/?/, "");
  const parts = raw.split("/").filter(Boolean);
  applyingRoute = true;
  if (!parts.length || parts[0] === "projects") {
    if (parts[1] === "new") {
      state.view = "project-editor";
      state.editorProjectId = "";
    } else if (parts[1]) {
      state.view = "project-detail";
      state.detailProjectId = decodeURIComponent(parts[1]);
      state.detailTab = parts[2] === "edit" ? "edit" : "status";
      state.editorProjectId = state.detailTab === "edit" ? state.detailProjectId : "";
    } else {
      state.view = "projects";
      state.detailProjectId = "";
      state.editorProjectId = "";
      state.detailTab = "status";
    }
  } else if (["global", "records"].includes(parts[0])) {
    state.view = parts[0];
    state.detailProjectId = "";
    state.editorProjectId = "";
  }
  applyingRoute = false;
  if (shouldRender) render();
}

function routeForState() {
  if (state.view === "project-detail" && state.detailProjectId) {
    return `#/projects/${encodeURIComponent(state.detailProjectId)}${state.detailTab === "edit" ? "/edit" : ""}`;
  }
  if (state.view === "project-editor") {
    return state.editorProjectId ? `#/projects/${encodeURIComponent(state.editorProjectId)}/edit` : "#/projects/new";
  }
  if (state.view === "global") return "#/global";
  if (state.view === "records") return "#/records";
  return "#/projects";
}

function syncRoute() {
  if (applyingRoute || !state.currentUser) return;
  const next = routeForState();
  if (location.hash !== next) history.replaceState(null, "", next);
}

async function login(ev) {
  ev.preventDefault();
  $("#loginError").textContent = "";
  try {
    await api("/api/auth/login", {
      method: "POST",
      body: JSON.stringify({ code: $("#loginCode").value.trim(), password: $("#loginPassword").value })
    });
    await load();
  } catch (e) {
    $("#loginError").textContent = e.message;
  }
}

async function logout() {
  await api("/api/auth/logout", { method: "POST" }).catch(() => {});
  renderLogin();
}

function openAccountMenu() {
  openModal("账号", `
    <div class="account-panel">
      <div><strong>${esc(state.currentUser?.name || "-")}</strong><span>${esc(roleLabel(state.currentUser?.role))} · ${esc(state.currentUser?.code || "")}</span></div>
      <button type="button" onclick="openChangePassword()">修改密码</button>
      <button type="button" class="danger" onclick="logout()">退出登录</button>
    </div>`, null);
  $("#modalSave").style.display = "none";
}

function openChangePassword() {
  if ($("#modal").open) $("#modal").close();
  openModal("修改密码", `
    <div class="form-grid">
      <div class="field full"><label>原密码</label><input name="oldPassword" type="password" autocomplete="current-password"></div>
      <div class="field full"><label>新密码</label><input name="newPassword" type="password" autocomplete="new-password"></div>
      <div class="field full"><label>确认新密码</label><input name="confirmPassword" type="password" autocomplete="new-password"></div>
    </div>`, async () => {
      const data = formData($("#modalBody"));
      if (data.newPassword !== data.confirmPassword) throw new Error("两次输入的新密码不一致");
      await api("/api/auth/change-password", { method: "POST", body: JSON.stringify(data) });
      alert("密码已修改，请使用新密码登录");
      $("#modal").close();
      await logout();
    });
}

function render() {
  clearDetailTimer();
  renderAppShell();
  document.body.classList.toggle("detail-mode", state.view === "project-detail");
  const titles = {
    projects: ["项目", "管理项目配置、发布环境、版本回退。", "新建项目"],
    "project-detail": ["项目详情", "查看状态、发布和编辑项目。", ""],
    "project-editor": [state.editorProjectId ? "编辑项目" : "新建项目", "配置源码、workspace、发布环境和脚本。", ""],
    global: ["全局配置", "管理系统级秘钥、通知、节点和全局设置。", ""],
    records: ["发布记录", "查看历史发布并重新部署。", ""]
  };
  const [title, desc, btn] = titles[state.view];
  $("#pageTitle").textContent = title;
  $("#pageDesc").textContent = desc;
  $("#primaryBtn").textContent = btn;
  $("#primaryBtn").style.display = btn ? "" : "none";
  if (state.view === "projects" && !isAdmin()) $("#primaryBtn").style.display = "none";
  ({ projects: renderProjects, "project-detail": renderProjectDetail, "project-editor": renderProjectEditor, global: renderGlobalConfig, records: renderRecords }[state.view])();
  initShellEditors();
  syncRoute();
}

function renderProjects() {
  const groups = projectGroups();
  const list = state.projects.filter(p => {
    const s = `${p.code || ""} ${p.name || ""} ${p.group || ""} ${projectWorkerNames(p)} ${projectDeployNodes(p)}`.toLowerCase();
    return (!searchText || s.includes(searchText.toLowerCase())) && (!groupFilter || (p.group || "未分组") === groupFilter);
  });
  $("#content").innerHTML = state.projects.length ? `
    <div class="tabs">
      <button class="tab ${groupFilter ? "" : "active"}" onclick="setGroupFilter('')">全部项目</button>
      ${groups.map(g => `<button class="tab ${groupFilter === g ? "active" : ""}" onclick="setGroupFilter('${escAttr(g)}')">${esc(g)}</button>`).join("")}
    </div>
    <div class="table-wrap"><table class="table"><thead><tr><th>编号</th><th>名称</th><th>分组</th><th>worker</th><th>发布节点</th><th>最后状态</th><th>最后发布</th></tr></thead><tbody>
      ${list.map(p => `<tr>
        <td class="mono">#${esc(p.code)}</td>
        <td><button class="link-btn" onclick="openProjectDetail('${p.id}')">${esc(p.name)}</button></td>
        <td><span class="badge">${esc(p.group || "未分组")}</span></td>
        <td class="mono">${esc(projectWorkerNames(p))}</td>
        <td class="mono">${esc(projectDeployNodes(p))}</td>
        <td>${statusBadge(projectDisplayStatus(p))}</td>
        <td>${fmt(projectDisplayTime(p))}</td>
      </tr>`).join("")}
    </tbody></table></div>
    <div class="hint">显示 ${list.length} / ${state.projects.length} 个项目</div>` : `<div class="empty">暂无项目，先新建一个发布项目。</div>`;
}

function projectGroups() {
  return [...new Set(state.projects.map(p => p.group || "未分组"))].sort((a, b) => a.localeCompare(b, "zh-CN"));
}

function setGroupFilter(group) {
  groupFilter = group;
  renderProjects();
}

function escAttr(s = "") {
  return esc(s).replace(/`/g, "&#96;");
}

function projectDeployNodes(project) {
  const ids = new Set();
  (project.environments || []).forEach(env => (env.artifacts || []).forEach(a => (a.nodeIds || []).forEach(id => ids.add(id))));
  return [...ids].map(nodeName).filter(x => x !== "-").join("、") || "-";
}

function maskUrl(url = "") {
  if (!url) return "-";
  return url.length > 34 ? `${url.slice(0, 18)}...${url.slice(-8)}` : url;
}

function renderSecrets() {
  $("#content").innerHTML = state.secrets.length ? `
    <section class="card">
      <div class="section-head"><div class="title-icon"><span class="material-symbols-outlined">key</span><h2>秘钥管理</h2></div><button class="primary" onclick="editSecret()">新增秘钥</button></div>
      <div class="table-wrap"><table class="table"><thead><tr><th>编码</th><th>类型</th><th>账号</th><th>备注</th><th>更新时间</th><th class="right">操作</th></tr></thead><tbody>
    ${state.secrets.map(s => `<tr>
      <td class="mono"><strong>${esc(s.code)}</strong></td><td><span class="badge">${esc(s.type)}</span></td><td>${esc(s.username || (s.token ? "Token" : "-"))}</td>
      <td>${esc(s.remark || "-")}</td><td>${fmt(s.updatedAt)}</td>
      <td class="actions"><button class="action-icon" title="编辑" onclick="editSecret('${s.id}')"><span class="material-symbols-outlined">edit</span></button><button class="action-icon danger" title="删除" onclick="removeItem('secrets','${s.id}')"><span class="material-symbols-outlined">delete</span></button></td>
    </tr>`).join("")}</tbody></table></div></section>` : `<div class="empty">暂无秘钥。</div>`;
}

function renderNodes() {
  $("#content").innerHTML = state.nodes.length ? `
    <section class="card">
      <div class="section-head"><div class="title-icon"><span class="material-symbols-outlined">dns</span><h2>节点管理</h2></div><button class="primary" onclick="editNode()">新增节点</button></div>
      <div class="table-wrap"><table class="table"><thead><tr><th>编码</th><th>类型</th><th>地址</th><th>目录</th><th>秘钥</th><th class="right">操作</th></tr></thead><tbody>
    ${state.nodes.map(n => `<tr>
      <td class="mono"><strong>${esc(n.code)}</strong><br>${statusBadge(n.status || "unknown")}</td><td><span class="badge">${esc(n.type)}</span></td><td class="mono">${esc(n.type === "ssh" ? `${n.user || ""}@${n.host}:${n.port || 22}` : "-")}</td>
      <td class="mono">${esc(n.baseDir || "-")}${n.lastError ? `<br><small class="error-text">${esc(n.lastError)}</small>` : ""}</td><td>${esc(state.secrets.find(s => s.id === n.secretId)?.code || "-")}</td>
      <td class="actions"><button class="action-icon" title="编辑" onclick="editNode('${n.id}')"><span class="material-symbols-outlined">edit</span></button><button class="action-icon danger" title="删除" onclick="removeItem('nodes','${n.id}')"><span class="material-symbols-outlined">delete</span></button></td>
    </tr>`).join("")}</tbody></table></div></section>` : `<div class="empty">暂无节点。</div>`;
}

function renderGlobalConfig() {
  const keepValues = state.projects.map(p => p.retention?.keepReleases).filter(Boolean);
  const avgKeep = keepValues.length ? Math.round(keepValues.reduce((a, b) => a + b, 0) / keepValues.length) : 5;
  $("#content").innerHTML = `
    <div class="global-layout">
      <section>
        <div class="settings-grid">
          <div class="setting-card">
            <span class="material-symbols-outlined">notifications_active</span>
            <h3>通知配置</h3>
            <p>支持企业微信和飞书 Hook，项目发布完成后可选择全局通知推送结果。</p>
            <strong>${state.notifications.length} 个通知</strong>
          </div>
          <div class="setting-card">
            <span class="material-symbols-outlined">verified</span>
            <h3>有效节点</h3>
            <p>无效节点不会出现在项目发布目标选择中。</p>
            <strong>${state.nodes.filter(n => n.status === "valid").length}/${state.nodes.length}</strong>
          </div>
          <div class="setting-card">
            <span class="material-symbols-outlined">precision_manufacturing</span>
            <h3>编译器</h3>
            <p>发布任务按权重和繁忙状态选择编译器执行编译。</p>
            <strong>${state.workers.length} 个编译器</strong>
          </div>
        </div>
      </section>

      <section class="card">
        <div class="section-head">
          <div class="title-icon"><span class="material-symbols-outlined">manage_accounts</span><h2>用户管理</h2></div>
          <button class="primary" onclick="editUser()"><span class="material-symbols-outlined">add</span>新增用户</button>
        </div>
        <div class="table-wrap"><table class="table compact-table"><thead><tr><th>编码</th><th>名称</th><th>角色</th><th>项目授权</th><th>备注</th><th class="right">操作</th></tr></thead><tbody>
          ${state.users.map(u => `<tr>
            <td class="mono">${esc(u.code)}</td>
            <td>${esc(u.name)}</td>
            <td><span class="badge ${u.role === "admin" ? "valid" : ""}">${esc(roleLabel(u.role))}</span></td>
            <td class="perm-cell">${esc(userPermSummary(u))}</td>
            <td>${esc(u.remark || "-")}</td>
            <td class="actions"><button class="action-icon" onclick="editUser('${u.id}')" title="编辑"><span class="material-symbols-outlined">edit</span></button><button class="action-icon danger" onclick="removeItem('users','${u.id}')" title="删除"><span class="material-symbols-outlined">delete</span></button></td>
          </tr>`).join("") || `<tr><td colspan="6" class="empty-cell">暂无用户</td></tr>`}
        </tbody></table></div>
      </section>

      <section class="card">
        <div class="section-head">
          <div class="title-icon"><span class="material-symbols-outlined">precision_manufacturing</span><h2>编译器管理</h2></div>
          <button class="primary" onclick="editWorker()"><span class="material-symbols-outlined">add</span>新增编译器</button>
        </div>
        <div class="table-wrap"><table class="table compact-table"><thead><tr><th>名称</th><th>节点</th><th>工作目录</th><th>权重</th><th class="right">操作</th></tr></thead><tbody>
          ${state.workers.map(w => `<tr>
            <td class="mono">${esc(w.name)}</td>
            <td>${esc(nodeName(w.nodeId))}</td>
            <td class="mono">${esc(w.workDir || ".code-dep/workspaces")}</td>
            <td><span class="badge">${esc(String(w.weight ?? 5))}</span></td>
            <td class="actions"><button class="action-icon" onclick="editWorker('${w.id}')" title="编辑"><span class="material-symbols-outlined">edit</span></button><button class="action-icon danger" onclick="removeItem('workers','${w.id}')" title="删除"><span class="material-symbols-outlined">delete</span></button></td>
          </tr>`).join("") || `<tr><td colspan="5" class="empty-cell">暂无编译器</td></tr>`}
        </tbody></table></div>
      </section>

      <section class="card">
        <div class="section-head">
          <div class="title-icon"><span class="material-symbols-outlined">key</span><h2>秘钥管理</h2></div>
          <button class="primary" onclick="editSecret()"><span class="material-symbols-outlined">add</span>新增秘钥</button>
        </div>
        <div class="table-wrap"><table class="table compact-table"><thead><tr><th>编码</th><th>类型</th><th>用户</th><th>值</th><th class="right">操作</th></tr></thead><tbody>
          ${state.secrets.map(s => `<tr>
            <td class="mono">${esc(s.code)}</td>
            <td><span class="badge">${esc(s.type)}</span></td>
            <td>${esc(s.username || "-")}</td>
            <td class="mono muted-value">${s.password || s.token || s.privateKey ? "••••••••••••" : "-"}</td>
            <td class="actions"><button class="action-icon" onclick="editSecret('${s.id}')" title="编辑"><span class="material-symbols-outlined">edit</span></button><button class="action-icon danger" onclick="removeItem('secrets','${s.id}')" title="删除"><span class="material-symbols-outlined">delete</span></button></td>
          </tr>`).join("") || `<tr><td colspan="5" class="empty-cell">暂无秘钥</td></tr>`}
        </tbody></table></div>
      </section>

      <section class="card">
        <div class="section-head">
          <div class="title-icon"><span class="material-symbols-outlined">notifications_active</span><h2>通知管理</h2></div>
          <button class="primary" onclick="editNotification()"><span class="material-symbols-outlined">add</span>新增通知</button>
        </div>
        <div class="table-wrap"><table class="table compact-table"><thead><tr><th>编码</th><th>通知类型</th><th>Hook 地址</th><th>邮件</th><th class="right">操作</th></tr></thead><tbody>
          ${state.notifications.map(n => `<tr>
            <td class="mono">${esc(n.code)}</td>
            <td><span class="badge">${esc(notifyTypeLabel(n.type))}</span></td>
            <td class="mono">${esc(maskUrl(n.hookUrl))}</td>
            <td>${n.emailEnabled ? `<span class="badge valid">启用</span><br><small>${esc(n.emailTo)}</small>` : `<span class="badge">否</span>`}</td>
            <td class="actions"><button class="action-icon" onclick="testNotification('${n.id}')" title="测试"><span class="material-symbols-outlined">science</span></button><button class="action-icon" onclick="editNotification('${n.id}')" title="编辑"><span class="material-symbols-outlined">edit</span></button><button class="action-icon danger" onclick="removeItem('notifications','${n.id}')" title="删除"><span class="material-symbols-outlined">delete</span></button></td>
          </tr>`).join("") || `<tr><td colspan="5" class="empty-cell">暂无通知</td></tr>`}
        </tbody></table></div>
      </section>

      <section class="card">
        <div class="section-head">
          <div class="title-icon"><span class="material-symbols-outlined">dns</span><h2>节点管理</h2></div>
          <button class="primary" onclick="editNode()"><span class="material-symbols-outlined">add</span>新增节点</button>
        </div>
        <div class="table-wrap"><table class="table compact-table"><thead><tr><th>编码</th><th>类型</th><th>地址</th><th>状态</th><th>Console</th><th class="right">操作</th></tr></thead><tbody>
          ${state.nodes.map(n => `<tr>
            <td class="mono">${esc(n.code)}</td>
            <td><span class="badge">${esc(n.type)}</span></td>
            <td class="mono">${esc(n.type === "ssh" ? `${n.user || ""}@${n.host}:${n.port || 22}` : n.baseDir || "-")}</td>
            <td>${statusBadge(n.status || "unknown")}${n.lastError ? `<br><small class="error-text">${esc(n.lastError)}</small>` : ""}</td>
            <td><button class="console-btn" onclick="openConsole('${n.id}')"><span class="material-symbols-outlined">terminal</span>Console</button></td>
            <td class="actions"><button class="action-icon" onclick="editNode('${n.id}')" title="编辑"><span class="material-symbols-outlined">edit</span></button><button class="action-icon danger" onclick="removeItem('nodes','${n.id}')" title="删除"><span class="material-symbols-outlined">delete</span></button></td>
          </tr>`).join("") || `<tr><td colspan="6" class="empty-cell">暂无节点</td></tr>`}
        </tbody></table></div>
      </section>
    </div>`;
}
function renderRecords() {
  const records = [...state.records].sort((a, b) => new Date(b.startedAt) - new Date(a.startedAt));
  $("#content").innerHTML = records.length ? `
    <div class="table-wrap"><table class="table"><thead><tr><th>项目</th><th>环境</th><th>版本</th><th>Ref</th><th>模式</th><th>状态</th><th>时间</th><th class="right">操作</th></tr></thead><tbody>
    ${records.map(r => `<tr>
      <td>${esc(r.projectName)}</td><td><span class="badge">${esc(r.env)}</span></td><td class="mono">${esc(r.version)}</td><td class="mono">${esc(r.ref || "-")}</td><td>${esc(r.mode)}</td>
      <td>${statusBadge(r.status)}</td><td>${fmt(r.startedAt)}</td>
      <td class="actions"><button onclick="showRecordLog('${r.id}')">日志</button>${canRunProject(r.projectId) ? `<button onclick="redeploy('${r.id}')">重新部署</button>` : ""}</td>
    </tr>`).join("")}</tbody></table></div>` : `<div class="empty">暂无发布记录。</div>`;
}

function openProjectDetail(id, tab = "status") {
  state.detailProjectId = id;
  state.detailTab = tab;
  state.view = "project-detail";
  render();
}

function backToList() {
  state.view = "projects";
  state.detailProjectId = "";
  state.detailTab = "status";
  render();
}

function renderProjectDetail() {
  const p = state.projects.find(x => x.id === state.detailProjectId);
  if (!p) {
    $("#content").innerHTML = `<div class="empty">项目不存在</div>`;
    return;
  }
  $("#pageTitle").textContent = p.name;
  $("#pageDesc").textContent = `状态：${projectDisplayStatus(p) || "未发布"}，上次编译时间：${fmt(projectDisplayTime(p))}`;
  $("#primaryBtn").style.display = "none";
  $("#content").innerHTML = `
    <section class="detail-main compact" id="detailMain"></section>`;
  if (state.detailTab === "edit") {
    state.editorProjectId = p.id;
    renderProjectEditor($("#detailMain"), true);
    return;
  }
  renderProjectStatus(p);
}

function renderProjectStatus(project) {
  const records = projectRecords(project.id);
  const active = records[0];
  const activeStages = active ? stageSummary(active) : null;
  const canRun = canRunProject(project.id);
  const canEdit = canEditProject(project.id);
  $("#detailMain").innerHTML = `
    <div class="detail-grid">
      <div class="detail-left">
        <section class="project-header-card">
          <div>
            <div class="project-title-row">
              <h1>${esc(project.name)}</h1>
              ${statusBadge(projectDisplayStatus(project))}
            </div>
            <p>最后发布：${fmt(projectDisplayTime(project))}（分支/Tag：${esc(project.git?.ref || "-")}，分组：${esc(project.group || "未分组")}）</p>
          </div>
          <div class="detail-actions">
            ${canRun ? `<button class="primary publish-big" onclick="openPublish('${project.id}')">发布</button>` : ""}
            ${canEdit ? `<button class="small-btn" title="编辑" onclick="openProjectDetail('${project.id}', 'edit')"><span class="material-symbols-outlined">edit</span></button>` : ""}
            <button class="small-btn" title="返回" onclick="backToList()"><span class="material-symbols-outlined">arrow_back</span></button>
          </div>
        </section>

        <section class="pipeline-stepper-card">
          <h3>发布流程</h3>
          <div class="pipeline-stepper">
            ${["git", "build", "deploy", "message"].map(k => pipelineStep(k, activeStages?.[k])).join("")}
          </div>
        </section>

        <section class="log-card">
          <div class="log-head">
            <span><span class="material-symbols-outlined">terminal</span>发布日志</span>
            <div>
              ${active ? `<button class="icon" onclick="showRecordLog('${active.id}')" title="查看日志"><span class="material-symbols-outlined">fullscreen</span></button>` : ""}
            </div>
          </div>
          <div class="embedded-log">${renderLogLines(active?.log || ["暂无发布日志"])}</div>
        </section>

      </div>

      <aside class="history-panel">
        <div class="history-head"><h3>发布历史</h3></div>
        <div class="history-list">
          ${historyListByDate(records)}
        </div>
      </aside>
    </div>
  `;
}

function pipelineStep(key, stage) {
  const names = { git: "初始化", build: "编译", deploy: "发布", message: "通知" };
  const status = stage?.status || "pending";
  const icon = { success: "check", failed: "close", stopped: "stop", skipped: "skip_next", running: "autorenew", pending: "flag" }[status] || "flag";
  return `<button class="pipeline-step ${status}" onclick="${stage ? `showRecordLog('${projectRecords(state.detailProjectId)[0]?.id || ""}')` : ""}">
    <span><span class="material-symbols-outlined">${icon}</span></span>
    <strong>${names[key]}</strong>
  </button>`;
}

function historyListByDate(records) {
  const groups = {};
  records.forEach(r => {
    const d = r.startedAt ? new Date(r.startedAt) : new Date();
    const key = d.toLocaleDateString("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit" });
    if (!groups[key]) groups[key] = [];
    groups[key].push(r);
  });
  return Object.entries(groups).map(([date, items]) =>
    `<div class="history-date-group">
      <div class="history-date-label">${esc(date)}</div>
      ${items.map(r => historyItem(r)).join("")}
    </div>`
  ).join("") || `<div class="empty-cell">暂无记录</div>`;
}

function historyItem(record) {
  const emoji = record.status === "success" ? "✅" : record.status === "failed" ? "❌" : record.status === "stopped" ? "⏹" : "⏳";
  const t = record.startedAt ? new Date(record.startedAt) : null;
  const time = t ? t.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", hour12: false }) : "--:--";
  const canDelete = canEditProject(record.projectId) && record.status !== "running";
  const canRedeploy = canRunProject(record.projectId) && record.status !== "running";
  const menuId = `hm_${record.id}`;
  return `<div class="history-row ${record.status}" data-menu="${menuId}">
    <button class="history-item" onclick="showRecordLog('${record.id}')">
      <span class="hi-emoji">${emoji}</span>
      <span class="hi-ver">${esc(shortVersion(record.version))}</span>
      ${record.workerName ? `<span class="hi-worker" title="${esc(record.workerName)}">${esc(record.workerName)}</span>` : ""}
      ${historyDuration(record)}
      <span class="hi-time">${time}</span>
    </button>
    <button class="hi-menu-btn" onclick="toggleHistoryMenu('${menuId}', event)" title="操作"><span class="material-symbols-outlined">more_vert</span></button>
    <div class="hi-menu" id="${menuId}">
      <button onclick="showRecordLog('${record.id}')"><span class="material-symbols-outlined">terminal</span>日志</button>
      ${canRedeploy ? `<button onclick="redeploy('${record.id}')"><span class="material-symbols-outlined">replay</span>重新部署</button>` : ""}
      ${canDelete ? `<button class="danger" onclick="deleteRecord('${record.id}')"><span class="material-symbols-outlined">delete</span>删除</button>` : ""}
    </div>
  </div>`;
}

function historyDuration(record) {
  if (record.endedAt) {
    return `<span class="hi-dur">耗时 ${esc(fmtDuration(new Date(record.endedAt) - new Date(record.startedAt)))}</span>`;
  }
  if (record.status !== "running") return `<span class="hi-dur">--</span>`;
  const stages = stageSummary(record);
  const done = ["git", "build", "deploy", "message"].filter(k => ["success", "skipped"].includes(stages[k]?.status)).length;
  const percent = Math.max(12, done * 25);
  return `<span class="hi-dur hi-progress" title="发布中" aria-label="发布中"><i style="width:${percent}%"></i></span>`;
}

function toggleHistoryMenu(id, ev) {
  ev.stopPropagation();
  closeHistoryMenus();
  const menu = document.getElementById(id);
  if (menu) menu.classList.toggle("open");
}

function closeHistoryMenus() {
  $$(".hi-menu.open").forEach(el => el.classList.remove("open"));
}

document.addEventListener("click", ev => {
  if (!ev.target.closest(".hi-menu") && !ev.target.closest(".hi-menu-btn")) closeHistoryMenus();
  if (!ev.target.closest(".target-node-picker")) closeTargetNodeDropdowns();
  if (!ev.target.closest(".ref-picker")) closeRefDropdowns();
});

function renderLogLines(logs) {
  return logs.slice(-80).map(line => {
    const raw = esc(line);
    const time = raw.match(/^\[(\d\d:\d\d:\d\d)\]\s*(.*)$/);
    const body = time ? time[2] : raw;
    const level = /失败|fatal|error|denied/i.test(body) ? "error" : /warn|warning/i.test(body) ? "warn" : "info";
    return `<div class="log-line ${level}">
      <span>${time ? time[1] : "--:--:--"}</span>
      <code>${body.replace(/(git|mvn|npm|docker|kubectl|mkdir|rsync|scp)/g, `<b>$1</b>`)}</code>
    </div>`;
  }).join("");
}

function projectRecords(projectId) {
  return state.records.filter(r => r.projectId === projectId).sort((a, b) => {
    if (a.status === "running" && b.status !== "running") return -1;
    if (a.status !== "running" && b.status === "running") return 1;
    return new Date(b.startedAt) - new Date(a.startedAt);
  });
}

function shortVersion(version = "") {
  const raw = version.split("-").pop() || version;
  return raw.length > 8 ? raw.slice(-8) : raw;
}

async function deleteRecord(id) {
  const r = state.records.find(x => x.id === id);
  const label = r ? `${r.env || ""} ${r.version || ""} ${r.startedAt ? fmt(r.startedAt) : ""}`.trim() : id;
  if (!confirm(`确认删除发布记录「${label}」及对应构建产物？`)) return;
  const input = prompt("请输入 DELETE 确认删除");
  if (input !== "DELETE") return;
  await api(`/api/records/${id}`, { method: "DELETE" });
  if (activeLogRecord?.id === id && $("#publishModal").open) $("#publishModal").close();
  await load();
  if (state.view === "project-detail") renderProjectDetail();
}

function stageSummary(record) {
  const logs = record.log || [];
  const timeOf = text => {
    const line = logs.find(l => l.includes(text));
    const raw = line?.match(/\[(\d\d):(\d\d):(\d\d)\]/);
    if (!raw) return null;
    return (+raw[1] * 3600 + +raw[2] * 60 + +raw[3]) * 1000;
  };
  const findTime = text => {
    const line = logs.find(l => l.includes(text));
    return line?.match(/\[(\d\d:\d\d:\d\d)\]/)?.[1] || "";
  };
  const recordEndMs = record.endedAt ? new Date(record.endedAt).getTime() : (record.startedAt ? new Date(record.startedAt).getTime() : Date.now());
  const recordStartMs = record.startedAt ? new Date(record.startedAt).getTime() : Date.now();

  const failed = record.status === "failed";
  const stopped = record.status === "stopped";
  const running = record.status === "running";
  const success = record.status === "success";
  const finalDone = success || failed || stopped;
  const canceled = stopped || logs.some(l => l.includes("用户终止发布任务"));

  const gitStarted = logs.some(l => l.includes("克隆源码") || l.includes("更新源码") || l.includes("执行预处理命令") || l.includes("预处理命令") || l.includes("git clone") || l.includes("git fetch") || l.includes("远程执行") && l.includes("git "));
  const buildStarted = logs.some(l => l.includes("执行目标编译命令") || l.includes("BUILD SUCCESS"));
  const buildDone = logs.some(l => l.includes("同步远程构建产物") || l.includes("保存构建产物") || l.includes("BUILD SUCCESS"));
  const deployStarted = logs.some(l => l.includes("发布到"));
  const deployDone = logs.some(l => l.includes("部署文件同步完成") || l.includes("部署成功") || (success && deployStarted));
  const messageSkipped = logs.some(l => l.includes("未配置消息通知"));
  const gitDone = buildStarted || buildDone || deployStarted || deployDone || success || logs.some(l => l.includes("远程切换版本") || l.includes("切换版本") || l.includes("git checkout") || l.includes("预处理完成"));
  const messageStarted = messageSkipped || logs.some(l => l.includes("通知") || l.includes("消息")) || finalDone;

  // Collect stage boundary timestamps (all time-of-day ms from midnight)
  const logStart = timeOf("mkdir") || timeOf("克隆源码") || timeOf("git fetch") || timeOf("远程执行");
  const buildStart = timeOf("执行目标编译命令") || timeOf("mvn ");
  const deployStart = timeOf("发布到");
  const messageStart = timeOf("发送") || timeOf("通知") || timeOf("消息");
  // Last log line timestamp as time-of-day ms
  const lastLogTime = (() => {
    for (let i = logs.length - 1; i >= 0; i--) {
      const m = logs[i].match(/\[(\d\d):(\d\d):(\d\d)\]/);
      if (m) return (+m[1] * 3600 + +m[2] * 60 + +m[3]) * 1000;
    }
    return null;
  })();
  // Record total duration as fallback
  const recordTotalMs = (record.endedAt && record.startedAt)
    ? new Date(record.endedAt).getTime() - new Date(record.startedAt).getTime()
    : 0;

  const dur = (start, end) => {
    if (start == null) return 0;
    const e = end ?? lastLogTime;
    if (e != null && e >= start) return e - start;
    return recordTotalMs > 0 ? recordTotalMs : 0;
  };

  const gitDur = dur(logStart, buildStart);
  const buildDur = dur(buildStart, deployStart);
  const deployDur = dur(deployStart, messageStart);
  const messageDur = dur(messageStart, lastLogTime);

  const statusOf = {
    git: gitDone ? "success" : canceled && gitStarted ? "stopped" : failed && gitStarted ? "failed" : running && gitStarted ? "running" : "pending",
    build: buildDone || deployStarted || deployDone || success ? "success" : canceled && buildStarted ? "stopped" : failed && buildStarted ? "failed" : running && buildStarted ? "running" : "pending",
    deploy: deployDone || success ? "success" : canceled && deployStarted ? "stopped" : failed && (deployStarted || buildDone) ? "failed" : running && deployStarted ? "running" : "pending",
    message: messageSkipped ? "skipped" : success ? "success" : canceled && messageStarted ? "stopped" : failed && messageStarted ? "failed" : running && deployDone && messageStarted ? "running" : "pending"
  };
  if (running) {
    const order = ["git", "build", "deploy", "message"];
    const doneStatus = s => s === "success" || s === "skipped";
    const current = order.find(k => !doneStatus(statusOf[k])) || "message";
    let beforeCurrent = true;
    for (const key of order) {
      if (key === current) { beforeCurrent = false; statusOf[key] = "running"; continue; }
      if (beforeCurrent && statusOf[key] === "pending") statusOf[key] = "success";
      if (!beforeCurrent && statusOf[key] === "running") statusOf[key] = "pending";
    }
  }
  if (finalDone && failed) {
    const order = ["git", "build", "deploy", "message"];
    const failedStage = order.find(k => statusOf[k] === "running" || statusOf[k] === "pending");
    if (failedStage) statusOf[failedStage] = "failed";
  }
  return {
    git: { label: "初始化", status: statusOf.git, time: findTime("git fetch") || findTime("切换版本") || findTime("预处理命令"), duration: gitDur },
    build: { label: "编译", status: statusOf.build, time: findTime("执行目标编译命令") || findTime("mvn "), duration: buildDur },
    deploy: { label: "部署", status: statusOf.deploy, time: findTime("发布到"), duration: deployDur },
    message: { label: "消息", status: statusOf.message, time: record.endedAt ? fmt(record.endedAt) : "", duration: messageDur }
  };
}

function duration(start, end) {
  return start != null && end != null && end >= start ? end - start : 0;
}

function fmtDuration(ms) {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const sec = Math.round(ms / 1000);
  if (sec < 60) return `${sec}s`;
  return `${Math.floor(sec / 60)}min ${sec % 60}s`;
}

function pipelineIcon(status) {
  if (status === "success") return "✓";
  if (status === "failed") return "!";
  if (status === "running") return "…";
  return "-";
}

function renderDetailPublish(project) {
  $("#detailMain").innerHTML = `<div class="editor-section"><h2>发布</h2><p>选择环境和分支后开始发布。</p><button class="primary" onclick="openPublish('${project.id}')">发布项目</button></div>`;
}

function startDetailPolling() {
  clearDetailTimer();
  detailTimer = setInterval(async () => {
    let data;
    try {
      data = await api("/api/bootstrap");
    } catch (e) {
      if (e.status === 401) renderLogin();
      return;
    }
    Object.assign(state, { currentUser: data.currentUser || null, projects: data.projects || [], records: data.records || [], nodes: data.nodes || [], workers: data.workers || [], secrets: data.secrets || [], notifications: data.notifications || [], users: data.users || [] });
    if (state.view === "project-detail" && state.detailTab === "status") renderProjectDetail();
  }, 3000);
}

function clearDetailTimer() {
  if (detailTimer) clearInterval(detailTimer);
  detailTimer = null;
}

function ensureLiveSync() {
  const hasRunning = state.records.some(r => r.status === "running");
  if (hasRunning && !syncTimer) {
    syncTimer = setInterval(refreshRuntimeState, 3000);
  }
  if (!hasRunning && syncTimer) {
    clearInterval(syncTimer);
    syncTimer = null;
  }
}

async function refreshRuntimeState() {
  if (syncingState || !state.currentUser) return;
  syncingState = true;
  try {
    const data = await api("/api/bootstrap");
    const view = state.view;
    applyBootstrap(data, false);
    const isEditing = view === "project-editor" || (view === "project-detail" && state.detailTab === "edit");
    if (!isEditing && ["projects", "project-detail", "records"].includes(view)) render();
    if ($("#publishModal").open && activeLogRecord) updateLogProgress();
  } catch (e) {
    if (e.status === 401) renderLogin();
  } finally {
    syncingState = false;
  }
}

function openModal(title, body, onSave) {
  $("#modalTitle").textContent = title;
  $("#modalBody").innerHTML = body;
  editing = onSave;
  $("#modalSave").style.display = onSave ? "" : "none";
  $("#modal").showModal();
}

function formData(form) {
  const data = {};
  $$("input, select, textarea", form).forEach(el => {
    if (!el.name || el.disabled) return;
    if (el.type === "checkbox") {
      if (el.checked) data[el.name] = el.value;
      return;
    }
    data[el.name] = el.value;
  });
  return data;
}

function editSecret(id = "") {
  const s = state.secrets.find(x => x.id === id) || { type: "git" };
  openModal(id ? "编辑秘钥" : "新建秘钥", `
    <div class="form-grid">
      <div class="field"><label>编码</label><input name="code" value="${esc(s.code)}"></div>
      <div class="field"><label>类型</label><select name="type"><option>git</option><option>ssh</option><option>api</option><option>token</option></select></div>
      <div class="field"><label>用户</label><input name="username" value="${esc(s.username)}"></div>
      <div class="field"><label>密码</label><input name="password" type="password" value="${esc(s.password)}"></div>
      <div class="field full"><label>Token</label><input name="token" value="${esc(s.token)}"></div>
      <div class="field full"><label>私钥</label><textarea name="privateKey">${esc(s.privateKey)}</textarea></div>
      <div class="field full"><label>备注</label><input name="remark" value="${esc(s.remark)}"></div>
    </div>`, async () => {
      const data = formData($("#modalBody"));
      await save("secrets", id, data);
    });
  $('[name="type"]').value = s.type || "git";
}

function editNode(id = "") {
  const n = state.nodes.find(x => x.id === id) || { type: "local", port: 22 };
  openModal(id ? "编辑节点" : "新建节点", `
    <div class="form-grid">
      <div class="field"><label>编码</label><input name="code" value="${esc(n.code)}"></div>
      <div class="field"><label>类型</label><select name="type"><option value="local">本地目录</option><option value="ssh">SSH 远程</option></select></div>
      <div class="field"><label>Host</label><input name="host" value="${esc(n.host)}"></div>
      <div class="field"><label>端口</label><input name="port" type="number" value="${esc(n.port || 22)}"></div>
      <div class="field"><label>用户</label><input name="user" value="${esc(n.user)}"></div>
      <div class="field"><label>秘钥</label><select name="secretId">${secretOptions(n.secretId)}</select></div>
      <div class="field full"><label>基础目录</label><input name="baseDir" value="${esc(n.baseDir)}"></div>
      <div class="field full"><label>备注</label><input name="remark" value="${esc(n.remark)}"></div>
    </div>`, async () => {
      const data = formData($("#modalBody"));
      data.port = Number(data.port || 22);
      await save("nodes", id, data);
    });
  $('[name="type"]').value = n.type || "local";
}

function editWorker(id = "") {
  const w = state.workers.find(x => x.id === id) || { weight: 5 };
  openModal(id ? "编辑编译器" : "新增编译器", `
    <div class="form-grid">
      <div class="field"><label>名称</label><input name="name" value="${esc(w.name)}" placeholder="worker-1"></div>
      <div class="field"><label>节点</label><select name="nodeId">${nodeOptions(w.nodeId)}</select></div>
      <div class="field full"><label>工作目录</label><input name="workDir" value="${esc(w.workDir)}" placeholder=".code-dep/workspaces 或 /data/workspaces"></div>
      <div class="field"><label>权重</label><input name="weight" type="number" min="0" max="9" value="${esc(w.weight ?? 5)}"></div>
      <div class="field full"><small class="hint">权重 0-9，数值越小越优先。编译器会在工作目录下进入项目目录执行初始化和编译。</small></div>
    </div>`, async () => {
      const data = formData($("#modalBody"));
      data.weight = Math.max(0, Math.min(9, Number(data.weight || 5)));
      await save("workers", id, data);
    });
}

function editNotification(id = "") {
  const n = state.notifications.find(x => x.id === id) || { type: "wecom", emailEnabled: false };
  openModal(id ? "编辑通知" : "新增通知", `
    <div class="form-grid">
      <div class="field"><label>编码</label><input name="code" value="${esc(n.code)}" placeholder="prod-wecom"></div>
      <div class="field"><label>通知类型</label><select name="type"><option value="wecom">企业微信</option><option value="feishu">飞书</option></select></div>
      <div class="field full"><label>Hook 地址</label><input name="hookUrl" value="${esc(n.hookUrl)}" placeholder="https://..."></div>
      <div class="field"><label>是否邮件</label><select name="emailEnabled"><option value="false">否</option><option value="true">是</option></select></div>
      <div class="field"><label>收件地址</label><input name="emailTo" value="${esc(n.emailTo)}" placeholder="a@company.com,b@company.com"></div>
      <div class="field full"><label>备注</label><input name="remark" value="${esc(n.remark)}"></div>
    </div>`, async () => {
      const data = formData($("#modalBody"));
      data.emailEnabled = data.emailEnabled === "true";
      await save("notifications", id, data);
    });
  $('[name="type"]').value = n.type || "wecom";
  $('[name="emailEnabled"]').value = n.emailEnabled ? "true" : "false";
}

function editUser(id = "") {
  const u = state.users.find(x => x.id === id) || { role: "user", projectPerms: [] };
  const permByProject = new Map((u.projectPerms || []).map(p => [p.projectId, p]));
  openModal(id ? "编辑用户" : "新增用户", `
    <div class="form-grid user-form">
      <div class="field"><label>用户编码</label><input name="code" value="${esc(u.code)}" placeholder="zhangsan"></div>
      <div class="field"><label>用户名称</label><input name="name" value="${esc(u.name)}" placeholder="张三"></div>
      <div class="field"><label>角色</label><select name="role"><option value="admin">管理员</option><option value="user">普通用户</option></select></div>
      <div class="field"><label>密码</label><input name="password" type="password" value="" placeholder="${id ? "不修改请留空" : "新用户必填"}"></div>
      <div class="field full"><label>备注</label><input name="remark" value="${esc(u.remark)}"></div>
      <div class="field full">
        <label>项目授权</label>
        <div class="perm-editor">
          ${state.projects.map(p => {
            const perm = permByProject.get(p.id) || {};
            return `<div class="perm-row" data-project-id="${p.id}">
              <div><strong>${esc(p.name)}</strong><small>${esc(p.code || "")} · ${esc(p.group || "未分组")}</small></div>
              <label><input type="checkbox" class="perm-run" ${perm.canRun ? "checked" : ""}> 可运行</label>
              <label><input type="checkbox" class="perm-edit" ${perm.canEdit ? "checked" : ""}> 可修改</label>
            </div>`;
          }).join("") || `<div class="empty-cell">暂无项目可授权</div>`}
        </div>
        <small id="roleHint" class="hint">管理员默认拥有全部项目权限；普通用户按下方授权控制。</small>
      </div>
    </div>`, async () => {
      const data = formData($("#modalBody"));
      data.projectPerms = $$(".perm-row", $("#modalBody")).map(row => ({
        projectId: row.dataset.projectId,
        canRun: $(".perm-run", row).checked,
        canEdit: $(".perm-edit", row).checked
      })).filter(p => p.canRun || p.canEdit);
      await save("users", id, data);
    });
  $('[name="role"]').value = u.role || "user";
  bindUserRoleToggle();
}

function bindUserRoleToggle() {
  const role = $('[name="role"]');
  const sync = () => {
    const admin = role.value === "admin";
    $$(".perm-row input", $("#modalBody")).forEach(input => input.disabled = admin);
    $("#roleHint").textContent = admin ? "管理员拥有全部项目权限，无需单独授权。" : "普通用户仅能操作已授权项目。";
  };
  role.addEventListener("change", sync);
  sync();
}

async function testNotification(id) {
  try {
    const res = await api(`/api/notifications/${id}/test`, { method: "POST" });
    alert(res.message || "通知测试成功");
  } catch (e) {
    alert(`通知测试失败：${e.message}`);
  }
}

function testSelectedNotification() {
  const id = $('[name="notify.notificationId"]')?.value;
  if (!id) return alert("请先选择通知");
  testNotification(id);
}

function editProject(id = "") {
  if ((id && !canEditProject(id)) || (!id && !isAdmin())) return alert("没有项目修改权限");
  state.editorProjectId = id;
  state.view = "project-editor";
  render();
}

function renderProjectEditor(container = $("#content"), embedded = false) {
  const id = state.editorProjectId;
  if ((id && !canEditProject(id)) || (!id && !isAdmin())) {
    container.innerHTML = `<div class="empty">没有项目修改权限。</div>`;
    return;
  }
  const p = state.projects.find(x => x.id === id) || {
    git: {}, build: {}, notify: {}, retention: { keepReleases: 5 },
    environments: [{ name: "sit", compileDeploy: false, buildCommand: "", deployCommand: "", artifacts: [{ source: ".", targetDir: "", nodeIds: [] }] }]
  };
  const preEnabled = !!(p.build?.preprocessEnabled || p.build?.preprocessCommand);
  container.innerHTML = `
    <div id="projectEditor" class="project-editor pipeline-config-page">
      <div class="pipeline-page-head">
        <div>
          <div class="breadcrumbs">
            <button type="button" onclick="backToProjects()">项目</button>
            <span class="material-symbols-outlined">chevron_right</span>
            <button type="button">${esc(p.name || "新项目")}</button>
            <span class="material-symbols-outlined">chevron_right</span>
            <strong>发布配置</strong>
          </div>
          <h1>发布配置</h1>
        </div>
        <div class="pipeline-actions">
          ${embedded ? `<button onclick="openProjectDetail('${id}')">取消</button>` : `<button onclick="backToProjects()">取消</button>`}
          <button class="primary" onclick="saveProject('${id}')"><span class="material-symbols-outlined">save</span>保存配置</button>
        </div>
      </div>

      <section class="pipeline-panel">
        <div class="pipeline-panel-head"><span class="material-symbols-outlined">source</span><h2>代码仓库</h2></div>
        <div class="pipeline-repo-grid">
          <div class="repo-row repo-row-3">
            <div class="field"><label>项目编号</label><input name="code" value="${esc(p.code)}" placeholder="留空自动生成"></div>
            <div class="field"><label>项目名称</label><input name="name" value="${esc(p.name)}" placeholder="例如：订单服务"></div>
            <div class="field"><label>项目分组</label><input name="group" list="projectGroupList" value="${esc(p.group)}" placeholder="例如：后端 / 前端 / ERP"><datalist id="projectGroupList">${projectGroups().map(g => `<option value="${esc(g)}"></option>`).join("")}</datalist></div>
          </div>
        <div class="repo-row repo-row-3">
            <div class="field"><label>Git 秘钥</label><select name="git.secretId">${secretOptions(p.git?.secretId)}</select></div>
            
            <div class="field"><label>输出路径</label><input name="build.artifactSource" value="${esc(p.build?.artifactSource || (p.environments || [])[0]?.artifacts?.[0]?.source || ".")}" placeholder="dist/ 或 target/*.jar"></div>
          </div>
          <div class="repo-row repo-row-2">
            <div class="field"><label>Git 地址</label><div class="repo-url-row"><span class="material-symbols-outlined">link</span><input name="git.url" value="${esc(p.git?.url)}" placeholder="git@github.com:org/repo.git"><button type="button" class="action-icon" onclick="validateGitFromEditor(true)" title="测试连接"><span class="material-symbols-outlined">sync</span></button></div><small id="gitStatus" class="hint">支持 SSH 和 HTTPS 地址。</small></div>
          </div>
          <div class="repo-row repo-row-3">
            <div class="field"><label>分支 / Tag</label>${refPicker({ id: "gitRefInput", optionsId: "gitRefOptions", name: "git.ref", value: p.git?.ref || "", refs: state.gitRefs })}</div>
          </div>
        </div>
        <div class="repo-command">
          ${commandBlock({
            title: "预处理命令",
            hint: "Shell 脚本 · 在源码准备后运行，属于初始化阶段",
            inputClass: "preprocessCmd",
            wrapperClass: "preprocess-command-field",
            checked: preEnabled,
            value: p.build?.preprocessCommand || "",
            checkboxName: "build.preprocessEnabled",
            scriptName: "preprocess.sh"
          })}
        </div>
      </section>

      <section class="pipeline-panel">
        <div class="pipeline-panel-head with-action">
          <div><span class="material-symbols-outlined">dns</span><h2>发布目标</h2></div>
          <button type="button" onclick="addEnv()"><span class="material-symbols-outlined">add</span>新增目标</button>
        </div>
        <div id="envRows" class="target-list">${(p.environments || []).map(envRow).join("")}</div>
      </section>

      <div class="pipeline-bottom-grid">
        <section class="pipeline-panel">
          <div class="pipeline-panel-head"><span class="material-symbols-outlined">notifications</span><h2>通知</h2></div>
          <div class="notify-list">
            <div class="notify-card active">
              <div><strong><span class="material-symbols-outlined">chat</span>发布通知</strong><small>从全局通知管理选择企业微信或飞书 Hook</small><select name="notify.notificationId">${notificationOptions(p.notify?.notificationId)}</select></div>
              <button type="button" onclick="testSelectedNotification()">测试</button>
            </div>
            <small class="hint">请先在全局配置的通知管理中维护通知；旧项目里的直接 Hook 配置仍兼容发送。</small>
          </div>
        </section>

        <section class="pipeline-panel">
          <div class="pipeline-panel-head"><span class="material-symbols-outlined">tune</span><h2>高级设置</h2></div>
          <div class="advanced-grid">
            <div class="field"><label>版本保留数量</label><div class="inline-number"><input name="retention.keepReleases" type="number" min="1" value="${esc(p.retention?.keepReleases || 5)}"><span>个版本</span></div></div>
            <div class="field"><label>发布模式</label><select name="build.publishMode"><option value="overwrite">覆盖发布</option><option value="clean">清理发布</option></select></div>
            <div class="field"><label>编译超时时间</label><div class="inline-number"><input type="number" value="30" disabled><span>分钟</span></div><small class="hint">超过该时间后，可由操作人员终止发布流程。</small></div>
            <div class="field full"><label>编译器</label>${workerPicker(p.build?.workerIds || [])}<small class="hint">指定编译器编译，默认根据繁忙状态分配；不选就是随机。</small></div>
          </div>
        </section>
      </div>
      ${id ? `<section class="editor-section danger-zone">
        <h3>删除项目</h3>
        <p>删除会移除项目和关联发布记录，请连续确认。</p>
        <button type="button" class="danger" onclick="deleteProjectDeep('${id}', '${esc(p.name)}')">删除项目</button>
      </section>` : ""}
    </div>`;
  $('[name="build.publishMode"]', container).value = p.build?.publishMode || "overwrite";
  bindProjectEditorEvents();
}

function envRow(e = { artifacts: [{}] }) {
  const selected = e.artifacts?.[0]?.nodeIds || [];
  const compileDeploy = !!(e.compileDeploy || e.buildCommand);
  return `<div class="target-card env">
    <button type="button" class="target-delete" onclick="this.closest('.env').remove()" title="删除目标"><span class="material-symbols-outlined">delete</span></button>
    <div class="target-title"><span class="material-symbols-outlined">hard_drive</span><strong>发布目标 ${esc(e.name || "sit")}</strong></div>
    <div class="target-grid">
      <div class="field"><label>环境</label><input class="envName" value="${esc(e.name || "")}" placeholder="sit / uat / prod"></div>
      <div class="field"><label>目标节点</label>${targetNodePicker(selected)}</div>
      <div class="field"><label>目标目录</label><input class="targetDir" value="${esc(e.artifacts?.[0]?.targetDir || "")}" placeholder="/var/www/app"></div>
      <div class="field"><label>编译目标</label><div class="cross-build-row"><select class="envGoos"><option value="">OS</option><option value="linux" ${e.goos==="linux"?"selected":""}>Linux</option><option value="darwin" ${e.goos==="darwin"?"selected":""}>macOS</option><option value="windows" ${e.goos==="windows"?"selected":""}>Windows</option></select><select class="envGoarch"><option value="">架构</option><option value="amd64" ${e.goarch==="amd64"?"selected":""}>amd64</option><option value="arm64" ${e.goarch==="arm64"?"selected":""}>arm64</option><option value="386" ${e.goarch==="386"?"selected":""}>386</option></select></div><small class="hint">设置 GOOS/GOARCH，留空使用默认。</small></div>
    </div>
    ${commandBlock({
      title: "编译命令",
      hint: "Shell 脚本 · 在worker节点运行",
      inputClass: "buildCmd",
      wrapperClass: "target-build-command",
      checked: compileDeploy,
      value: e.buildCommand || "",
      checkboxClass: "compileDeploy",
      scriptName: "build.sh"
    })}
    ${commandBlock({
      title: "发布命令",
      hint: "发布到目标服务器后运行",
      inputClass: "deployCmd",
      wrapperClass: "target-deploy-command",
      checked: !!e.deployCommand,
      value: e.deployCommand || "",
      scriptName: "deploy.sh"
    })}
  </div>`;
}

function targetNodePicker(selected = []) {
  const nodes = state.nodes.filter(n => n.status === "valid" || selected.includes(n.id));
  if (!nodes.length) return `<div class="target-node-picker empty"><span class="hint">请先创建有效节点</span></div>`;
  return `<div class="target-node-picker" data-selected="${esc(selected.join(","))}">
    <div class="target-node-tags">${targetNodeTags(selected)}</div>
    <button type="button" class="target-node-trigger" onclick="toggleTargetNodeDropdown(this, event)"><span class="material-symbols-outlined">add</span>选择节点</button>
    <div class="target-node-dropdown">
      <input class="target-node-filter" placeholder="过滤节点" autocomplete="off" oninput="filterTargetNodes(this)">
      <div class="target-node-options">${targetNodeOptions(selected)}</div>
    </div>
  </div>`;
}

function targetNodeTags(selected = []) {
  if (!selected.length) return `<span class="target-node-empty">未选择节点</span>`;
  return selected.map(id => {
    const node = state.nodes.find(n => n.id === id);
    const label = node ? `${node.code}${node.status !== "valid" ? " · 无效" : ""}` : id;
    return `<span class="node-tag" title="${esc(label)}">${esc(label)}<button type="button" onclick="removeTargetNode(this, '${esc(id)}', event)" title="移除"><span class="material-symbols-outlined">close</span></button></span>`;
  }).join("");
}

function targetNodeOptions(selected = []) {
  const nodes = state.nodes.filter(n => n.status === "valid" || selected.includes(n.id));
  return nodes.map(n => `<button type="button" class="target-node-option ${selected.includes(n.id) ? "selected" : ""}" data-id="${esc(n.id)}" data-label="${esc(n.code)} ${esc(n.type)}" onclick="toggleTargetNode(this, event)" ${n.status !== "valid" ? "disabled" : ""}>
    <span>${esc(n.code)}<small>${esc(n.type)}${n.status !== "valid" ? " · 无效" : ""}</small></span>
    <span class="material-symbols-outlined">${selected.includes(n.id) ? "check" : "add"}</span>
  </button>`).join("");
}

function selectedTargetNodeIds(picker) {
  return (picker?.dataset.selected || "").split(",").filter(Boolean);
}

function setSelectedTargetNodeIds(picker, ids) {
  picker.dataset.selected = [...new Set(ids)].join(",");
  $(".target-node-tags", picker).innerHTML = targetNodeTags(selectedTargetNodeIds(picker));
  $(".target-node-options", picker).innerHTML = targetNodeOptions(selectedTargetNodeIds(picker));
}

function toggleTargetNodeDropdown(btn, ev) {
  ev?.preventDefault();
  ev?.stopPropagation();
  const picker = btn.closest(".target-node-picker");
  const open = !picker.classList.contains("open");
  closeTargetNodeDropdowns(picker);
  picker.classList.toggle("open", open);
  if (open) $(".target-node-filter", picker)?.focus();
}

function closeTargetNodeDropdowns(except = null) {
  $$(".target-node-picker.open").forEach(p => {
    if (p !== except) p.classList.remove("open");
  });
}

function toggleTargetNode(btn, ev) {
  ev?.preventDefault();
  const picker = btn.closest(".target-node-picker");
  const id = btn.dataset.id;
  const ids = selectedTargetNodeIds(picker);
  setSelectedTargetNodeIds(picker, ids.includes(id) ? ids.filter(x => x !== id) : [...ids, id]);
  filterTargetNodes($(".target-node-filter", picker));
}

function removeTargetNode(btn, id, ev) {
  ev?.preventDefault();
  ev?.stopPropagation();
  const picker = btn.closest(".target-node-picker");
  setSelectedTargetNodeIds(picker, selectedTargetNodeIds(picker).filter(x => x !== id));
}

function filterTargetNodes(input) {
  const q = (input?.value || "").trim().toLowerCase();
  const picker = input?.closest(".target-node-picker");
  $$(".target-node-option", picker).forEach(btn => {
    btn.hidden = q && !btn.dataset.label.toLowerCase().includes(q);
  });
}

function workerPicker(selected = []) {
  if (!state.workers.length) return `<div class="target-node-picker empty"><span class="hint">请先在全局配置创建 worker</span></div>`;
  return `<div class="target-node-picker worker-picker" data-selected="${esc(selected.join(","))}">
    <div class="target-node-tags">${workerTags(selected)}</div>
    <button type="button" class="target-node-trigger" onclick="toggleTargetNodeDropdown(this, event)"><span class="material-symbols-outlined">add</span>选择</button>
    <div class="target-node-dropdown">
      <input class="target-node-filter" placeholder="过滤" autocomplete="off" oninput="filterTargetNodes(this)">
      <div class="target-node-options">${workerOptions(selected)}</div>
    </div>
  </div>`;
}

function workerTags(selected = []) {
  if (!selected.length) return `<span class="target-node-empty">随机</span>`;
  return selected.map(id => {
    const worker = state.workers.find(w => w.id === id);
    const label = worker ? `${worker.name} · 权重${worker.weight ?? 5}` : id;
    return `<span class="node-tag" title="${esc(label)}">${esc(label)}<button type="button" onclick="removeWorkerPick(this, '${esc(id)}', event)" title="移除"><span class="material-symbols-outlined">close</span></button></span>`;
  }).join("");
}

function workerOptions(selected = []) {
  return state.workers.map(w => `<button type="button" class="target-node-option ${selected.includes(w.id) ? "selected" : ""}" data-id="${esc(w.id)}" data-label="${esc(w.name)} ${esc(nodeName(w.nodeId))}" onclick="toggleWorkerPick(this, event)">
    <span>${esc(w.name)}<small>${esc(nodeName(w.nodeId))} · 权重${esc(String(w.weight ?? 5))}</small></span>
    <span class="material-symbols-outlined">${selected.includes(w.id) ? "check" : "add"}</span>
  </button>`).join("");
}

function setSelectedWorkerIds(picker, ids) {
  picker.dataset.selected = [...new Set(ids)].join(",");
  $(".target-node-tags", picker).innerHTML = workerTags(selectedTargetNodeIds(picker));
  $(".target-node-options", picker).innerHTML = workerOptions(selectedTargetNodeIds(picker));
}

function toggleWorkerPick(btn, ev) {
  ev?.preventDefault();
  const picker = btn.closest(".worker-picker");
  const id = btn.dataset.id;
  const ids = selectedTargetNodeIds(picker);
  setSelectedWorkerIds(picker, ids.includes(id) ? ids.filter(x => x !== id) : [...ids, id]);
  filterTargetNodes($(".target-node-filter", picker));
}

function removeWorkerPick(btn, id, ev) {
  ev?.preventDefault();
  ev?.stopPropagation();
  const picker = btn.closest(".worker-picker");
  setSelectedWorkerIds(picker, selectedTargetNodeIds(picker).filter(x => x !== id));
}

function commandBlock({ title, hint, inputClass, wrapperClass, checked, value, checkboxName = "", checkboxClass = "", scriptName = "script.sh" }) {
  const isOpen = checked || !!String(value || "").trim();
  return `<div class="command-block ${wrapperClass}" data-script-name="${esc(scriptName)}">
    <div class="command-head">
      <label class="toggle-line">
        <input type="checkbox" ${checkboxName ? `name="${esc(checkboxName)}"` : ""} class="command-toggle ${esc(checkboxClass)}" ${isOpen ? "checked" : ""} onchange="toggleCommandBlock(this)">
        <span>${esc(title)}</span>
      </label>
      <small>${esc(hint)}</small>
      <button type="button" class="action-icon template-icon" onclick="openTemplateMenu(this, event)" title="命令模板"><span class="material-symbols-outlined">content_paste</span></button>
    </div>
    <div class="command-body ${isOpen ? "" : "hidden"}">${shellEditor(inputClass, value || "", scriptName)}</div>
  </div>`;
}

function shellEditor(cls, value = "", scriptName = "script.sh") {
  return `<div class="shell-editor" data-script-name="${esc(scriptName)}">
    <pre aria-hidden="true"><code>${highlightShell(value)}</code></pre>
    <textarea class="${cls} shell-input" spellcheck="false" autocomplete="off" autocapitalize="off">${esc(value)}</textarea>
  </div>`;
}

function highlightShell(value = "") {
  const paint = fragment => fragment.replace(/(&quot;.*?&quot;|&#39;.*?&#39;|\$\{[^}]+}|\$[A-Za-z_][\w]*|--?[\w-]+)/g, token => {
    if (token.startsWith("&quot;") || token.startsWith("&#39;")) return `<span class="sh-string">${token}</span>`;
    if (token.startsWith("$")) return `<span class="sh-var">${token}</span>`;
    return `<span class="sh-flag">${token}</span>`;
  });
  return (value || "\n").split("\n").map(line => {
    let html = esc(line);
    const hash = html.indexOf("#");
    const comment = hash >= 0 ? html.slice(hash) : "";
    html = hash >= 0 ? html.slice(0, hash) : html;
    const match = html.match(/^(\s*)([a-zA-Z_][\w.-]*)(.*)$/);
    if (match) {
      html = `${match[1]}<span class="sh-command">${match[2]}</span>${paint(match[3])}`;
    } else {
      html = paint(html);
    }
    return html + (comment ? `<span class="sh-comment">${comment}</span>` : "");
  }).join("\n");
}

function initShellEditors() {
  $$(".shell-editor").forEach(editor => {
    const input = $(".shell-input", editor);
    const code = $("code", editor);
    const sync = () => {
      code.innerHTML = highlightShell(input.value);
      editor.style.setProperty("--scroll-top", `${input.scrollTop}px`);
      editor.style.setProperty("--scroll-left", `${input.scrollLeft}px`);
    };
    input.oninput = sync;
    input.onscroll = () => {
      $("pre", editor).scrollTop = input.scrollTop;
      $("pre", editor).scrollLeft = input.scrollLeft;
    };
    sync();
  });
}

function addEnv() {
  $("#envRows").insertAdjacentHTML("beforeend", envRow({ name: "uat", compileDeploy: false, artifacts: [{}] }));
  initShellEditors();
}

function toggleCommandBlock(input) {
  const block = input.closest(".command-block");
  $(".command-body", block)?.classList.toggle("hidden", !input.checked);
}

function bindProjectEditorEvents() {
  const root = $("#projectEditor");
  const gitUrl = $('[name="git.url"]', root);
  const gitSecret = $('[name="git.secretId"]', root);
  const trigger = debounce(() => validateGitFromEditor(false), 700);
  gitUrl?.addEventListener("input", trigger);
  gitSecret?.addEventListener("change", trigger);
  if (gitUrl?.value) validateGitFromEditor(false);
}

function debounce(fn, wait) {
  let timer;
  return (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => fn(...args), wait);
  };
}

function editorProjectPayload() {
  const root = $("#projectEditor");
  const f = formData(root);
  return {
    name: f.name, code: f.code, group: f.group,
    git: { url: f["git.url"], ref: f["git.ref"], secretId: f["git.secretId"] },
    build: {
      artifactSource: f["build.artifactSource"] || ".",
      preprocessEnabled: !!$('[name="build.preprocessEnabled"]', root)?.checked,
      preprocessCommand: $('[name="build.preprocessEnabled"]', root)?.checked ? ($(".preprocessCmd", root)?.value || "") : "",
      workerIds: selectedTargetNodeIds($(".worker-picker", root)),
      publishMode: f["build.publishMode"] || "overwrite"
    },
    notify: { notificationId: f["notify.notificationId"], weComHook: f["notify.weComHook"], feishuHook: f["notify.feishuHook"] },
    retention: { keepReleases: Number(f["retention.keepReleases"] || 5) },
    environments: editorEnvs(root)
  };
}

function editorEnvs(root = $("#projectEditor")) {
  return $$(".env", root).map(row => ({
    name: $(".envName", row).value.trim(),
    goos: $(".envGoos", row)?.value || "",
    goarch: $(".envGoarch", row)?.value || "",
    compileDeploy: $(".compileDeploy", row)?.checked || false,
    buildCommand: $(".compileDeploy", row)?.checked ? ($(".buildCmd", row)?.value || "") : "",
    deployCommand: $(".target-deploy-command .command-toggle", row)?.checked ? ($(".deployCmd", row)?.value || "") : "",
    artifacts: [{
      source: $('[name="build.artifactSource"]', root)?.value || ".",
      targetDir: $(".targetDir", row).value,
      nodeIds: selectedTargetNodeIds($(".target-node-picker", row))
    }]
  })).filter(e => e.name);
}

async function validateGitFromEditor(manual) {
  const status = $("#gitStatus");
  const input = $('[name="git.url"]');
  if (!input?.value) {
    status.textContent = "";
    return;
  }
  status.className = "hint";
  status.textContent = "正在验证代码仓库并拉取分支/Tag...";
  try {
    const id = state.editorProjectId || "_draft";
    const refs = await api(`/api/projects/${id}/refs`, { method: "POST", body: JSON.stringify(editorProjectPayload()) });
    state.gitRefs = refs.refs || [];
    renderRefOptions("gitRefOptions", state.gitRefs);
    status.className = "hint ok-text";
    status.textContent = `代码仓库可用，分支 ${refs.branches?.length || 0} 个，Tag ${refs.tags?.length || 0} 个`;
  } catch (e) {
    status.className = "hint error-text";
    status.textContent = `代码仓库验证失败：${e.message}`;
    if (manual) alert(status.textContent);
  }
}

function defaultCommandTemplates(kind) {
  const templates = {
    mvn: {
      name: "Java Maven",
      build: "mvn clean package -DskipTests",
      deploy: "systemctl restart your-service"
    },
    node: {
      name: "Node.js",
      build: "npm ci --registry=https://registry.npmmirror.com\nnpm run build",
      deploy: "pm2 reload ecosystem.config.js --env production"
    },
    docker: {
      name: "Docker",
      build: "docker build -t your-app:${BUILD_VERSION:-latest} .",
      deploy: "docker compose pull\ndocker compose up -d"
    }
  };
  const field = kind === "deploy" ? "deploy" : "build";
  return Object.values(templates).map(t => ({ name: t.name, script: t[field], builtin: true }));
}

function customCommandTemplates() {
  try {
    return JSON.parse(localStorage.getItem(CUSTOM_TEMPLATE_KEY) || "[]");
  } catch {
    return [];
  }
}

function saveCustomCommandTemplates(items) {
  localStorage.setItem(CUSTOM_TEMPLATE_KEY, JSON.stringify(items));
}

function commandKind(block) {
  if ($(".deployCmd", block)) return "deploy";
  if ($(".preprocessCmd", block)) return "preprocess";
  return "build";
}

function commandInput(block) {
  return $(".shell-input", block);
}

function openTemplateMenu(button, ev) {
  ev?.stopPropagation();
  closeTemplateMenu();
  const block = button.closest(".command-block");
  const kind = commandKind(block);
  const templates = [
    ...defaultCommandTemplates(kind),
    ...customCommandTemplates().filter(t => t.kind === kind)
  ];
  const menu = document.createElement("div");
  menu.id = "templateMenu";
  menu.className = "template-menu";
  const title = document.createElement("strong");
  title.textContent = "命令模板";
  menu.appendChild(title);
  const list = document.createElement("div");
  list.className = "template-menu-list";
  templates.forEach(t => {
    const item = document.createElement("button");
    item.type = "button";
    item.innerHTML = `<span>${esc(t.name)}</span>${t.builtin ? "<small>内置</small>" : "<small>自定义</small>"}`;
    item.addEventListener("click", () => {
      applyCommandTemplate(block, t.script);
      closeTemplateMenu();
    });
    list.appendChild(item);
  });
  menu.appendChild(list);
  const saveBtn = document.createElement("button");
  saveBtn.type = "button";
  saveBtn.className = "template-save";
  saveBtn.innerHTML = `<span class="material-symbols-outlined">add</span>保存当前为模板`;
  saveBtn.addEventListener("click", () => saveCommandTemplate(block, kind, button));
  menu.appendChild(saveBtn);
  document.body.appendChild(menu);
  const rect = button.getBoundingClientRect();
  menu.style.left = `${Math.min(window.innerWidth - 260, rect.right - 240)}px`;
  menu.style.top = `${rect.bottom + 8}px`;
}

function closeTemplateMenu() {
  $("#templateMenu")?.remove();
}

function applyCommandTemplate(block, script) {
  const input = commandInput(block);
  if (!input) return;
  const toggle = $(".command-toggle", block);
  toggle.checked = true;
  toggleCommandBlock(toggle);
  input.value = script;
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

function saveCommandTemplate(block, kind, reopenButton) {
  const input = commandInput(block);
  const script = input?.value.trim();
  if (!script) return alert("当前命令为空，不能保存模板");
  const name = prompt("模板名称");
  if (!name?.trim()) return;
  const items = customCommandTemplates().filter(t => !(t.kind === kind && t.name === name.trim()));
  items.push({ kind, name: name.trim(), script });
  saveCustomCommandTemplates(items);
  closeTemplateMenu();
  openTemplateMenu(reopenButton);
}

async function deleteProjectDeep(id, name) {
  const a = prompt(`输入项目名称确认删除：${name}`);
  if (a !== name) return alert("项目名称不匹配，已取消");
  const b = prompt("再次输入 DELETE 确认删除项目和发布记录");
  if (b !== "DELETE") return alert("未确认，已取消");
  await removeItem("projects", id, false);
  backToProjects();
}

async function saveProject(id) {
  if ((id && !canEditProject(id)) || (!id && !isAdmin())) return alert("没有项目修改权限");
  await save("projects", id, editorProjectPayload());
}

function backToProjects() {
  state.view = "projects";
  state.editorProjectId = "";
  render();
}

async function save(type, id, data) {
  const saved = await api(`/api/${type}${id ? "/" + id : ""}`, { method: id ? "PUT" : "POST", body: JSON.stringify(data) });
  if ($("#modal").open) $("#modal").close();
  if (type === "projects") {
    if (state.view === "project-detail") {
      state.detailTab = "status";
    } else {
      state.view = "projects";
      state.editorProjectId = "";
    }
  }
  await load();
  if (type === "nodes") {
    await autoTestNode(saved.id || id);
  }
}

async function autoTestNode(id) {
  try {
    await api(`/api/nodes/${id}/test`, { method: "POST" });
  } catch {}
  await load();
}

async function removeItem(type, id, ask = true) {
  if (ask && !confirm("确认删除？")) return;
  await api(`/api/${type}/${id}`, { method: "DELETE" });
  await load();
}

function openPublish(id, baseRecord = null) {
  if (!canRunProject(id)) return alert("没有项目运行权限");
  const p = state.projects.find(x => x.id === id);
  const envOptions = (p.environments || []).map(e => `<option>${esc(e.name)}</option>`).join("");
  const refs = state.gitRefs.length ? state.gitRefs : [p.git?.ref].filter(Boolean);
  $("#publishModeTag").textContent = baseRecord ? "重新部署历史版本" : "编译并发布";
  $("#publishBody").innerHTML = `
    <div class="publish-form-line">
      <div class="field"><label>项目</label><input value="${esc(p.name)}" disabled></div>
      <div class="field"><label>编译器</label>${workerPicker(p.build?.workerIds || [])}</div>
      <div class="field"><label>环境</label><select id="pubEnv">${envOptions}</select></div>
      <div class="field ref-field"><label>分支/Tag</label>${refPicker({ id: "pubRef", optionsId: "pubRefOptions", value: baseRecord?.ref || p.git?.ref || "", refs, disabled: !!baseRecord, refresh: "refreshPublishRefs()" })}</div>
    </div>`;
  closeLogStream();
  activeLogRecord = null;
  resetLiveLog([]);
  $("#publishModal").dataset.projectId = id;
  $("#publishModal").dataset.recordId = baseRecord?.id || "";
  $("#publishModal").dataset.activeRecordId = "";
  $("#stopPublish").style.display = "none";
  $("#startPublish").disabled = false;
  $("#startPublish").textContent = "开始发布";
  $("#publishModal").showModal();
  if (!baseRecord) loadPublishRefs(p);
}

async function loadPublishRefs(project) {
  try {
    const refs = await api(`/api/projects/${project.id}/refs`, { method: "POST", body: JSON.stringify(project) });
    renderRefOptions("pubRefOptions", refs.refs || []);
  } catch {}
}

async function refreshPublishRefs() {
  const project = state.projects.find(x => x.id === $("#publishModal").dataset.projectId);
  if (!project) return;
  const btn = $("#publishBody .ref-field .ref-refresh");
  if (!btn) return;
  btn.disabled = true;
  try {
    await loadPublishRefs(project);
  } finally {
    btn.disabled = false;
  }
}

async function startPublish(ev) {
  ev.preventDefault();
  if ($("#startPublish").disabled) return;
  $("#startPublish").disabled = true;
  $("#startPublish").textContent = "发布中...";
  const projectId = $("#publishModal").dataset.projectId;
  const recordId = $("#publishModal").dataset.recordId;
  try {
    const rec = await api("/api/publish", {
      method: "POST",
      body: JSON.stringify({ projectId, recordId, env: $("#pubEnv").value, ref: $("#pubRef").value, mode: recordId ? "redeploy" : "build", workerIds: selectedTargetNodeIds($(".worker-picker")) })
    });
    activeLogRecord = rec;
    state.records = [rec, ...state.records.filter(r => r.id !== rec.id)];
    $("#publishModal").dataset.activeRecordId = rec.id;
    $("#publishBody").innerHTML = publishLogHeader(rec, []);
    $("#stopPublish").style.display = "";
    $("#stopPublish").disabled = false;
    resetLiveLog([]);
    streamLog(rec.id, { clear: false });
  } catch (e) {
    $("#startPublish").disabled = false;
    $("#startPublish").textContent = "开始发布";
    throw e;
  }
}

async function stopCurrentPublish() {
  const id = $("#publishModal").dataset.activeRecordId;
  if (!id || !confirm("确认终止当前发布任务？")) return;
  $("#stopPublish").disabled = true;
  await api(`/api/publish/${id}/stop`, { method: "POST" });
  appendLiveLog(`[${new Date().toLocaleTimeString("zh-CN", { hour12: false })}] 已发送终止发布请求`);
}

function publishLogHeader(record, lines) {
  return `<div class="toolbar log-toolbar">
    <span class="badge">${esc(record.projectName)} · ${esc(record.env)} · ${esc(record.version)}${record.workerName ? ` · <span class="worker-badge"><span class="material-symbols-outlined">precision_manufacturing</span>${esc(record.workerName)}</span>` : ""}</span>
    <span class="hint">仅显示最近日志，运行中可强制终止。</span>
  </div>
  <div id="logProgress">${renderLogProgress(record, lines)}</div>`;
}

function updateLogProgress() {
  if (!activeLogRecord || !$("#logProgress")) return;
  $("#logProgress").innerHTML = renderLogProgress(activeLogRecord, logLines);
}

function renderLogProgress(record, lines = []) {
  const snapshot = { ...record, log: lines };
  const stages = stageSummary(snapshot);
  const keys = ["git", "build", "deploy", "message"];
  const done = keys.filter(k => ["success", "skipped"].includes(stages[k]?.status)).length;
  const percent = record.status === "success" ? 100 : record.status === "failed" || record.status === "stopped" ? Math.max(8, done * 25) : Math.max(8, done * 25);
  const elapsed = fmtDuration((record.endedAt ? new Date(record.endedAt) : new Date()) - new Date(record.startedAt || Date.now()));
  return `<div class="log-progress">
    <div class="progress-head"><strong>${statusBadge(record.status)}</strong><span>耗时 ${esc(elapsed)}</span></div>
    <div class="progress-track"><i style="width:${percent}%"></i></div>
    <div class="progress-stages">${keys.map(k => progressStage(k, stages[k])).join("")}</div>
  </div>`;
}

function progressStage(key, stage = {}) {
  const names = { git: "初始化", build: "编译", deploy: "部署", message: "消息" };
  const status = stage.status || "pending";
  const icon = { success: "✅", failed: "❌", stopped: "⏹", running: "⏳", skipped: "⏭", pending: "○" }[status] || "○";
  return `<div class="progress-stage ${status}"><b>${icon}</b><strong>${names[key]}</strong><span>${stageDurationText(stage)}</span></div>`;
}

function stageDurationText(stage = {}) {
  if (stage.duration && stage.duration > 0) return fmtDuration(stage.duration);
  if (stage.status === "running" && activeLogRecord?.startedAt) {
    return fmtDuration(new Date() - new Date(activeLogRecord.startedAt));
  }
  return "--";
}

function resetLiveLog(lines = []) {
  logLines = lines.slice(-LOG_LIMIT);
  logPending = [];
  if (logFlushTimer) clearTimeout(logFlushTimer);
  logFlushTimer = null;
  $("#liveLog").textContent = logLines.join("\n");
  $("#liveLog").scrollTop = $("#liveLog").scrollHeight;
  updateLogProgress();
}

function appendLiveLog(line) {
  logPending.push(line);
  if (!logFlushTimer) {
    logFlushTimer = setTimeout(flushLiveLog, 80);
  }
}

function flushLiveLog() {
  logFlushTimer = null;
  if (!logPending.length) return;
  logLines.push(...logPending);
  logPending = [];
  if (logLines.length > LOG_LIMIT) {
    logLines = logLines.slice(-LOG_LIMIT);
  }
  $("#liveLog").textContent = logLines.join("\n");
  $("#liveLog").scrollTop = $("#liveLog").scrollHeight;
  updateLogProgress();
}

function closeLogStream() {
  if (logReconnectTimer) clearTimeout(logReconnectTimer);
  logReconnectTimer = null;
  currentStreamId = "";
  if (logStream) {
    logStream.close();
    logStream = null;
  }
}

function streamLog(id, options = {}) {
  closeLogStream();
  if (options.clear !== false) resetLiveLog([]);
  const tail = options.tail ?? LOG_TAIL;
  currentStreamId = id;
  connectLogStream(id, tail);
}

function connectLogStream(id, tail = 0) {
  if (logStream) logStream.close();
  logStream = new EventSource(`/api/logs/${id}?tail=${tail}`);
  logStream.onmessage = ev => {
    logReconnectAttempt = 0;
    if (ev.data.startsWith("__DONE__:")) {
      if (activeLogRecord) {
        activeLogRecord.status = ev.data.replace("__DONE__:", "");
        activeLogRecord.endedAt = new Date().toISOString();
        updateLogProgress();
      }
      $("#stopPublish").style.display = "none";
      closeLogStream();
      load();
      return;
    }
    appendLiveLog(ev.data);
  };
  logStream.onerror = () => scheduleLogReconnect(id);
}

function scheduleLogReconnect(id) {
  if (currentStreamId !== id || !activeLogRecord || activeLogRecord.status !== "running") return;
  if (logStream) {
    logStream.close();
    logStream = null;
  }
  const delay = Math.min(15000, 800 * Math.pow(2, logReconnectAttempt++));
  if (logReconnectTimer) clearTimeout(logReconnectTimer);
  logReconnectTimer = setTimeout(async () => {
    await refreshRuntimeState();
    if (currentStreamId === id && activeLogRecord?.status === "running") connectLogStream(id, 0);
  }, delay);
}

function redeploy(recordId) {
  const r = state.records.find(x => x.id === recordId);
  if (!canRunProject(r.projectId)) return alert("没有项目运行权限");
  openPublish(r.projectId, r);
  $("#pubEnv").value = r.env;
}

function showRecordLog(id) {
  closeLogStream();
  const r = state.records.find(x => x.id === id);
  activeLogRecord = { ...r };
  const lines = r.log || [];
  const tail = lines.slice(-LOG_TAIL);
  const stale = isStaleRunningRecord(r);
  const modeTag = r.mode === "redeploy" ? "重新部署历史版本" : "编译并发布";
  $("#publishModeTag").textContent = modeTag;
  const extraHint = `仅显示最后 ${Math.min(lines.length, LOG_TAIL)} / ${lines.length} 行，最多保留 ${LOG_LIMIT} 行。${stale ? "检测到可能僵死的运行任务，已停止自动实时连接。" : ""}`;
  const canRedeploy = canRunProject(r.projectId) && r.status !== "running";
  const headerHtml = publishLogHeader(r, tail)
    .replace("仅显示最近日志，运行中可强制终止。", extraHint)
    + `<div class="toolbar log-detail-actions">
        ${canRedeploy ? `<button type="button" class="primary" onclick="redeploy('${r.id}')"><span class="material-symbols-outlined">replay</span>重新部署此版本</button>` : ""}
        ${canEditProject(r.projectId) && r.status !== "running" ? `<button type="button" class="danger" onclick="deleteRecord('${r.id}')"><span class="material-symbols-outlined">delete</span>删除此记录</button>` : ""}
      </div>`;
  $("#publishBody").innerHTML = headerHtml;
  resetLiveLog(tail);
  $("#startPublish").style.display = "none";
  $("#stopPublish").style.display = r.status === "running" ? "" : "none";
  $("#stopPublish").disabled = false;
  $("#publishModal").dataset.activeRecordId = r.id;
  $("#publishModal").showModal();
  if (r.status === "running" && !stale) streamLog(id, { clear: false, tail: 0 });
}

function isStaleRunningRecord(record) {
  if (record.status !== "running") return false;
  const started = new Date(record.startedAt).getTime();
  return Number.isFinite(started) && Date.now() - started > STALE_RUNNING_MS;
}

function showProjectLogs(projectId) {
  const p = state.projects.find(x => x.id === projectId);
  const records = state.records.filter(r => r.projectId === projectId).sort((a, b) => new Date(b.startedAt) - new Date(a.startedAt));
  $("#publishBody").innerHTML = `<div class="toolbar"><strong>${esc(p?.name || "")} 发布日志</strong>${records.map(r => `<button type="button" onclick="showRecordLog('${r.id}')">${esc(r.env)} · ${esc(r.version)} · ${esc(r.status)}</button>`).join("")}</div>`;
  resetLiveLog(records.length ? records.flatMap(r => [`# ${r.env} ${r.version} ${r.status} ${fmt(r.startedAt)}`, ...(r.log || []).slice(-200)]).slice(-LOG_LIMIT) : ["暂无发布日志"]);
  $("#startPublish").style.display = "none";
  $("#publishModal").showModal();
}

function openConsole(nodeId) {
  const node = state.nodes.find(n => n.id === nodeId);
  if (!node) return;
  if (node.status !== "valid") return alert("节点状态无效，请先测试连接");
  const modal = $("#consoleModal");
  $("#consoleNodeName").textContent = `控制台: ${node.code} (${node.type === "ssh" ? node.host : node.baseDir || "本地"})`;
  $("#consoleStatus").textContent = "连接中";
  $("#consoleStatus").className = "badge running";
  modal.showModal();
  initConsoleTerminal(nodeId);
}

function closeConsole() {
  cleanupConsole();
  $("#consoleModal").close();
}

function cleanupConsole() {
  if (consoleSocket) {
    consoleSocket.onclose = null;
    consoleSocket.close();
    consoleSocket = null;
  }
  if (consoleTerm) {
    consoleTerm.dispose();
    consoleTerm = null;
  }
  consoleFitAddon = null;
}

function initConsoleTerminal(nodeId) {
  cleanupConsole();
  const container = $("#consoleTerminal");
  container.innerHTML = "";

  const term = new Terminal({
    cursorBlink: true,
    fontSize: 14,
    fontFamily: '"JetBrains Mono", "SFMono-Regular", Consolas, monospace',
    theme: { background: "#1e1e1e", foreground: "#d4d4d4", cursor: "#d4d4d4", selectionBackground: "rgba(255,255,255,0.15)" },
    allowProposedApi: true
  });
  const fitAddon = new FitAddon.FitAddon();
  term.loadAddon(fitAddon);
  term.open(container);
  fitAddon.fit();
  consoleTerm = term;
  consoleFitAddon = fitAddon;

  const protocol = location.protocol === "https:" ? "wss:" : "ws:";
  const ws = new WebSocket(`${protocol}//${location.host}/api/nodes/${nodeId}/console`);
  consoleSocket = ws;

  ws.onopen = () => {
    $("#consoleStatus").textContent = "已连接";
    $("#consoleStatus").className = "badge success";
    sendConsoleResize();
  };

  ws.onmessage = (ev) => {
    if (ev.data instanceof Blob) {
      ev.data.arrayBuffer().then(buf => term.write(new Uint8Array(buf)));
    } else {
      term.write(ev.data);
    }
  };

  ws.onclose = () => {
    $("#consoleStatus").textContent = "已断开";
    $("#consoleStatus").className = "badge";
    term.write("\r\n\x1b[33m[连接已断开]\x1b[0m\r\n");
    consoleSocket = null;
  };

  ws.onerror = () => {
    $("#consoleStatus").textContent = "连接错误";
    $("#consoleStatus").className = "badge failed";
  };

  term.onData(data => {
    if (ws.readyState === WebSocket.OPEN) {
      const encoder = new TextEncoder();
      const encoded = encoder.encode(data);
      const msg = new Uint8Array(1 + encoded.length);
      msg[0] = 0;
      msg.set(encoded, 1);
      ws.send(msg);
    }
  });

  term.onResize(() => sendConsoleResize());

  const resizeObserver = new ResizeObserver(() => {
    if (consoleFitAddon && consoleTerm) {
      consoleFitAddon.fit();
    }
  });
  resizeObserver.observe(container);

  const dialog = $("#consoleModal");
  dialog._resizeObserver = resizeObserver;
  const origClose = dialog.close.bind(dialog);
  dialog.addEventListener("close", function handler() {
    cleanupConsole();
    resizeObserver.disconnect();
    dialog.removeEventListener("close", handler);
  }, { once: true });
}

function sendConsoleResize() {
  if (!consoleSocket || consoleSocket.readyState !== WebSocket.OPEN || !consoleTerm) return;
  const cols = consoleTerm.cols;
  const rows = consoleTerm.rows;
  const msg = new Uint8Array([1, (cols >> 8) & 0xff, cols & 0xff, (rows >> 8) & 0xff, rows & 0xff]);
  consoleSocket.send(msg);
}

function checkRecordChanges(oldRecords, newRecords) {
  const oldMap = {};
  (oldRecords || []).forEach(r => { oldMap[r.id] = r.status; });
  newRecords.forEach(r => {
    const prev = oldMap[r.id];
    if (prev === "running" && r.status !== "running") {
      const emoji = r.status === "success" ? "✅" : r.status === "failed" ? "❌" : "⏹";
      const label = r.status === "success" ? "发布成功" : r.status === "failed" ? "发布失败" : "已终止";
      const msg = `${emoji} ${r.projectName || ""} → ${r.env || ""} ${label} (${r.version || ""})`;
      unreadNotifs.push({ id: r.id + "_" + Date.now(), recordId: r.id, projectId: r.projectId, text: msg, time: new Date().toISOString(), status: r.status });
      pushBrowserNotification("轻发布", msg, r);
    }
  });
  updateNotifBadge();
}

function pushBrowserNotification(title, body, record) {
  if (!("Notification" in window)) return;
  if (Notification.permission === "granted") {
    doNotify(title, body, record);
  } else if (Notification.permission !== "denied") {
    Notification.requestPermission().then(p => { if (p === "granted") doNotify(title, body, record); });
  }
}

function doNotify(title, body, record) {
  try {
    const n = new Notification(title, { body, tag: record?.id, icon: "data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text y='.9em' font-size='90'>🚀</text></svg>" });
    n.onclick = () => { window.focus(); if (record?.projectId) openProjectDetail(record.projectId); n.close(); };
    setTimeout(() => n.close(), 8000);
  } catch {}
}

function updateNotifBadge() {
  const badge = $("#notifBadge");
  if (!badge) return;
  const count = unreadNotifs.length;
  badge.textContent = count > 99 ? "99+" : count;
  badge.style.display = count > 0 ? "" : "none";
}

function toggleNotifDropdown(ev) {
  ev.stopPropagation();
  const dd = $("#notifDropdown");
  if (!dd) return;
  dd.classList.toggle("open");
  if (dd.classList.contains("open")) renderNotifDropdown();
}

function closeNotifDropdown() {
  $("#notifDropdown")?.classList.remove("open");
}

function renderNotifDropdown() {
  const dd = $("#notifDropdown");
  if (!dd) return;
  if (!unreadNotifs.length) {
    dd.innerHTML = `<div class="notif-empty">暂无新消息</div>`;
    return;
  }
  dd.innerHTML = `<div class="notif-head"><strong>消息通知</strong><button onclick="clearAllNotifs()">全部已读</button></div>` +
    unreadNotifs.map(n => `<div class="notif-item ${n.status}" onclick="gotoNotifRecord('${n.recordId}', '${n.projectId}')">
      <span>${esc(n.text)}</span>
      <small>${new Date(n.time).toLocaleTimeString("zh-CN", { hour12: false })}</small>
    </div>`).join("");
}

function gotoNotifRecord(recordId, projectId) {
  unreadNotifs = unreadNotifs.filter(n => n.recordId !== recordId);
  updateNotifBadge();
  renderNotifDropdown();
  closeNotifDropdown();
  if (projectId) openProjectDetail(projectId);
}

function clearAllNotifs() {
  unreadNotifs = [];
  updateNotifBadge();
  renderNotifDropdown();
}

document.addEventListener("click", ev => {
  if (!ev.target.closest("#notifDropdown") && !ev.target.closest("#notifBtn")) closeNotifDropdown();
});

$$(".nav[data-view]").forEach(btn => btn.addEventListener("click", () => { state.view = btn.dataset.view; render(); }));
window.addEventListener("hashchange", () => applyRouteFromHash(true));
document.addEventListener("click", ev => {
  if (!ev.target.closest("#templateMenu") && !ev.target.closest(".template-icon")) closeTemplateMenu();
});
$("#loginForm").addEventListener("submit", login);
$("#homeLogo").addEventListener("click", goProjects);
$("#settingsBtn").addEventListener("click", goGlobalConfig);
$("#notifBtn").addEventListener("click", toggleNotifDropdown);
$("#accountBtn").addEventListener("click", openAccountMenu);
$("#globalSearch")?.addEventListener("input", ev => {
  searchText = ev.target.value || "";
  if (state.view !== "projects") state.view = "projects";
  render();
});
$("#primaryBtn").addEventListener("click", () => ({ projects: editProject, secrets: editSecret, nodes: editNode }[state.view])());
$("#modalSave").addEventListener("click", async ev => { ev.preventDefault(); try { await editing?.(); } catch (e) { alert(e.message); } });
$("#startPublish").addEventListener("click", async ev => { try { $("#startPublish").style.display = ""; await startPublish(ev); } catch (e) { alert(e.message); } });
$("#stopPublish").addEventListener("click", async () => { try { await stopCurrentPublish(); } catch (e) { alert(e.message); $("#stopPublish").disabled = false; } });
$("#publishModal").addEventListener("close", () => {
  $("#startPublish").style.display = "";
  $("#startPublish").disabled = false;
  $("#startPublish").textContent = "开始发布";
  $("#stopPublish").style.display = "none";
  activeLogRecord = null;
  closeLogStream();
});
load().catch(e => alert(e.message));
