const list = document.querySelector("#task-list");
const statusText = document.querySelector("#status");
const toast = document.querySelector("#toast");
const dialog = document.querySelector("#link-dialog");
const createDialog = document.querySelector("#create-dialog");
const authDialog = document.querySelector("#auth-dialog");
const joinDialog = document.querySelector("#join-dialog");
const settingsDialog = document.querySelector("#settings-dialog");
const logsDialog = document.querySelector("#logs-dialog");
const devicesDialog = document.querySelector("#devices-dialog");
const deviceManagerDialog = document.querySelector("#device-manager-dialog");
const connectionManagerDialog = document.querySelector("#connection-manager-dialog");
const appSettingsDialog = document.querySelector("#app-settings-dialog");
const aboutDialog = document.querySelector("#about-dialog");
const settingsForm = document.querySelector("#settings-form");
const authForm = document.querySelector("#auth-form");
const authTitle = document.querySelector("#auth-title");
const authHint = document.querySelector("#auth-hint");
const authConfirmRow = document.querySelector("#auth-confirm-row");
const authSubmit = document.querySelector("#auth-submit");
const linkForm = document.querySelector("#link-form");
const generatedLink = document.querySelector("#generated-link");
const endpointSuggestions = document.querySelector("#endpoint-suggestions");
const connectionResult = document.querySelector("#connection-result");
const testLinkButton = document.querySelector("#test-link");
const linkReadinessHint = document.querySelector("#link-readiness-hint");
const startStopButton = document.querySelector("#toolbar-start-stop");
const rescanButton = document.querySelector("#toolbar-rescan");
const settingsButton = document.querySelector("#toolbar-settings");
const deleteButton = document.querySelector("#toolbar-delete");
const settingsSummary = document.querySelector("#settings-summary");
const logsList = document.querySelector("#logs-list");
const logLevelFilter = document.querySelector("#log-level-filter");
const logSearch = document.querySelector("#log-search");
const deviceManagerList = document.querySelector("#device-manager-list");
const connectionManagerList = document.querySelector("#connection-manager-list");
const appSettingsBody = document.querySelector("#app-settings-body");
const taskStatsSummary = document.querySelector("#task-stats-summary");
const ignoreTemplate = document.querySelector("#ignore-template");
const ignoreSamplePath = document.querySelector("#ignore-sample-path");
const ignorePreviewResult = document.querySelector("#ignore-preview-result");
const ignoredList = document.querySelector("#ignored-list");
const devicesTitle = document.querySelector("#devices-title");
const devicesBody = document.querySelector("#devices-body");
const aboutVersion = document.querySelector("#about-version");
const defaultConfig = {
  sync_port: 7443,
  management_bind: "127.0.0.1",
  management_port: 8765,
  data_dir: "",
  sync_interval: "30s",
  direct_tls_configured: false,
  direct_tls_hosts: [],
  direct_tls_endpoints: [],
  version: "dev",
};
const sourceLinksStorageKey = "onesync.sourceLinks.v1";
let appConfig = { ...defaultConfig };
let tasksCache = [];
let selectedTaskId = "";
let trafficSnapshot = { time: Date.now(), tasks: {} };
let authState = { enabled: false, configured: false, authenticated: true };
let ignoreTemplates = [];
let visibleLogs = [];

async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
  });
  const body = await response.json();
  if (!response.ok) {
    if (response.status === 401) await openAuthDialog();
    throw new Error(body.error || "请求失败");
  }
  return body;
}

function notify(message) {
  toast.textContent = message;
  toast.classList.add("show");
  setTimeout(() => toast.classList.remove("show"), 2600);
}

async function loadTasks() {
  if (authState.enabled && !authState.authenticated) {
    statusText.textContent = "等待登录";
    return;
  }
  statusText.textContent = "正在加载";
  try {
    const { tasks } = await api("/api/tasks", { headers: {} });
    const rates = trafficRates(tasks || []);
    tasksCache = tasks || [];
    if (selectedTaskId && !tasksCache.some((task) => task.id === selectedTaskId)) selectedTaskId = "";
    list.replaceChildren(...(tasksCache.length ? tasksCache.map((task) => renderTaskRow(task, rates[task.id] || {})) : [emptyTaskRow()]));
    renderTaskStatsSummary(tasksCache, rates);
    statusText.textContent = taskStatusSummary(tasksCache);
    updateToolbar();
  } catch (error) {
    statusText.textContent = "加载失败";
    notify(error.message);
  }
}

async function loadAuthStatus() {
  authState = await api("/api/auth/status", { headers: {} });
  if (authState.enabled && !authState.authenticated) await openAuthDialog();
}

async function openAuthDialog() {
  try {
    authState = await fetch("/api/auth/status").then((response) => response.json());
  } catch {
    authState = { enabled: true, configured: true, authenticated: false };
  }
  if (!authState.enabled || authState.authenticated) return;
  const setup = !authState.configured;
  authTitle.textContent = setup ? "初始化管理账号" : "管理页登录";
  authHint.textContent = setup
    ? "首次远程访问 OneSync，请先设置管理账号和密码。"
    : "请输入 OneSync 管理账号密码。";
  authConfirmRow.hidden = !setup;
  authForm.elements.confirm_password.required = setup;
  authSubmit.textContent = setup ? "设置并进入" : "登录";
  if (!authDialog.open) authDialog.showModal();
}

authForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const data = new FormData(authForm);
  const username = String(data.get("username") || "").trim();
  const password = String(data.get("password") || "");
  const confirmPassword = String(data.get("confirm_password") || "");
  const setup = !authState.configured;
  if (setup && password !== confirmPassword) {
    notify("两次输入的密码不一致");
    return;
  }
  try {
    await api(setup ? "/api/auth/setup" : "/api/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    });
    authState = await api("/api/auth/status", { headers: {} });
    authDialog.close();
    notify(setup ? "管理账号已设置" : "已登录");
    loadConfig().finally(loadTasks);
  } catch (error) { notify(error.message); }
});

function renderTaskRow(task, rate = {}) {
  const row = document.createElement("tr");
  row.className = `${taskClass(task)} ${task.id === selectedTaskId ? "selected" : ""}`;
  row.title = [taskStateDetail(task), sourceReadinessWarning(task), task.last_error].filter(Boolean).join("\n");
  row.addEventListener("click", () => selectTask(task.id));

  const selector = document.createElement("input");
  selector.type = "checkbox";
  selector.checked = task.id === selectedTaskId;
  selector.addEventListener("click", (event) => {
    event.stopPropagation();
    selectTask(task.id);
  });

  appendCell(row, selector, "select-col");
  appendCell(row, typeLabel(task));
  appendCell(row, taskNameCell(task));
  appendCell(row, statusCell(task));
  appendCell(row, syncDeviceCell(task));
  appendCell(row, deviceDetailCell(task));
  appendCell(row, localSizeLabel(task));
  appendCell(row, standardSizeLabel(task));
  appendCell(row, formatRate(rate.received || 0), "muted");
  appendCell(row, formatRate(rate.sent || 0), "muted");
  return row;
}

function emptyTaskRow() {
  const row = document.createElement("tr");
  const cell = document.createElement("td");
  cell.colSpan = 10;
  cell.className = "empty-row";
  cell.textContent = "还没有同步任务。点击右上角“创建同步”或“加入同步”开始。";
  row.append(cell);
  return row;
}

function taskStatusSummary(tasks) {
  const total = tasks.length;
  const running = tasks.filter(isRunningTask).length;
  const failed = tasks.filter((task) => task.state === "failed").length;
  if (!total) return "0 个任务";
  if (failed) return `${total} 个任务 · ${running} 运行 · ${failed} 失败`;
  return `${total} 个任务 · ${running} 运行`;
}

function renderTaskStatsSummary(tasks, rates = {}) {
  if (!taskStatsSummary) return;
  if (!tasks.length) {
    taskStatsSummary.textContent = "暂无任务统计。";
    return;
  }
  const totals = taskTotals(tasks, rates);
  taskStatsSummary.textContent = [
    `本地 ${formatBytes(totals.localBytes)}`,
    `标准 ${formatBytes(totals.standardBytes)}`,
    `累计接收 ${formatBytes(totals.receivedBytes)}`,
    `累计发送 ${formatBytes(totals.sentBytes)}`,
    `当前接收 ${formatRate(totals.receivedRate)}`,
    `当前发送 ${formatRate(totals.sentRate)}`,
  ].join(" · ");
}

function taskTotals(tasks, rates = {}) {
  return tasks.reduce((total, task) => {
    const size = task.size || {};
    const traffic = task.traffic || {};
    const rate = rates[task.id] || {};
    total.localBytes += Number(size.local_bytes || 0);
    total.standardBytes += Number(size.standard_bytes || 0);
    total.receivedBytes += Number(traffic.received_bytes || 0);
    total.sentBytes += Number(traffic.sent_bytes || 0);
    total.receivedRate += Number(rate.received || 0);
    total.sentRate += Number(rate.sent || 0);
    return total;
  }, { localBytes: 0, standardBytes: 0, receivedBytes: 0, sentBytes: 0, receivedRate: 0, sentRate: 0 });
}

function appendCell(row, content, className = "") {
  const cell = document.createElement("td");
  if (className) cell.className = className;
  if (content instanceof Node) cell.append(content);
  else cell.textContent = content;
  row.append(cell);
  return cell;
}

function typeLabel(task) {
  return task.role === "source" ? "发送" : "接收";
}

function roleLabel(task) {
  return task.role === "source" ? "源端" : "目标端";
}

function taskNameCell(task) {
  const box = document.createElement("div");
  const name = document.createElement("strong");
  const path = document.createElement("span");
  name.textContent = task.id;
  path.textContent = task.role === "source" ? task.source_path : task.target_path;
  box.className = "task-name";
  box.append(name, path);
  return box;
}

function statusCell(task) {
  const box = document.createElement("div");
  box.className = "status-cell";
  const badge = document.createElement("span");
  badge.className = `badge ${task.state || "unknown"}`;
  badge.textContent = stateLabel(task);
  box.append(badge);
  const progress = task.progress || null;
  if (progress && isRunningTask(task)) {
    const detail = document.createElement("span");
    detail.className = "progress-inline";
    detail.textContent = progressLabel(progress);
    box.append(detail);
    const percent = progressPercent(progress);
    if (percent !== null) {
      const bar = document.createElement("span");
      bar.className = "progress-bar";
      const fill = document.createElement("span");
      fill.style.width = `${Math.max(0, Math.min(100, percent))}%`;
      bar.append(fill);
      box.append(bar);
    }
  }
  if (task.last_error) {
    const error = document.createElement("span");
    error.className = "row-error";
    error.textContent = task.last_error;
    box.append(error);
  }
  return box;
}

function syncDeviceCell(task) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "link-button";
  button.textContent = deviceCountLabel(task);
  button.title = "查看设备详情";
  button.addEventListener("click", (event) => {
    event.stopPropagation();
    openDevicesDialog(task);
  });
  return button;
}

function deviceDetailCell(task) {
  const box = document.createElement("div");
  box.className = "device-cell";
  const details = actionButton("详情", (event) => {
    event.stopPropagation();
    openDevicesDialog(task);
  }, { compact: true, secondary: true });
  box.append(details);
  if (task.role === "source") {
    const savedLink = savedSourceLink(task.id);
    const button = actionButton(savedLink ? "复制链接" : "生成链接", async (event) => {
      event.stopPropagation();
      if (savedLink) {
        await navigator.clipboard.writeText(savedLink.link);
        notify("源端链接已复制");
      } else {
        issueLink(task.id);
      }
    }, { compact: true });
    box.append(button);
    if (savedLink) {
      const show = actionButton("显示", (event) => {
        event.stopPropagation();
        showSavedLink(task.id, savedLink);
      }, { compact: true, secondary: true });
      box.append(show);
    }
  } else {
    const address = document.createElement("span");
    address.className = "muted mini-detail";
    address.textContent = task.peer_address || task.relay_url || "已加入";
    box.append(address);
  }
  return box;
}

function deviceDisplayName(task) {
  const devices = task.devices || {};
  return devices.alias || (devices.peer_id ? shortPeerID(devices.peer_id) : `${task.id} 的设备`);
}

function localSizeLabel(task) {
  const size = task.size || {};
  if (!size.local_bytes && !size.local_files) return "-";
  return `${formatBytes(size.local_bytes || 0)}${size.local_files ? ` · ${size.local_files} 个文件` : ""}`;
}

function standardSizeLabel(task) {
  const size = task.size || {};
  if (!size.standard_bytes && !size.standard_files) return "-";
  return `${formatBytes(size.standard_bytes || 0)}${size.standard_files ? ` · ${size.standard_files} 个文件` : ""}`;
}

function selectTask(taskId) {
  selectedTaskId = taskId;
  list.replaceChildren(...(tasksCache.length ? tasksCache.map((task) => renderTaskRow(task)) : [emptyTaskRow()]));
  updateToolbar();
}

function selectedTask() {
  return tasksCache.find((task) => task.id === selectedTaskId) || null;
}

function updateToolbar() {
  const task = selectedTask();
  const disabled = !task;
  startStopButton.disabled = disabled;
  rescanButton.disabled = disabled;
  settingsButton.disabled = disabled;
  deleteButton.disabled = disabled;
  startStopButton.textContent = !task ? "暂停/开始" : (isRunningTask(task) ? "暂停" : "开始");
}

function taskClass(task) {
  if (task.state === "failed") return "task-failed";
  if (isRunningTask(task)) return "task-running";
  if (task.state === "stopped") return "task-stopped";
  return "";
}

function isRunningTask(task) {
  return ["connecting", "syncing", "idle"].includes(task.state);
}

function taskStateDetail(task) {
  if (task.role === "source") {
    if (isRunningTask(task)) return `源端运行中：${progressStageSentence(task.progress, "可接受目标端连接并发送同步内容。")}`;
    if (task.state === "failed") return "源端失败：请查看下方错误信息和任务日志。";
    return "源端已停止：请先生成链接并启动。";
  }
  if (isRunningTask(task) && deviceConnected(task)) return `目标端运行中：${progressStageSentence(task.progress, "已连接源端。")}`;
  if (isRunningTask(task)) return `目标端运行中：${progressStageSentence(task.progress, "正在连接源端。")}`;
  if (task.state === "failed") return "目标端失败：请查看下方错误信息和任务日志。";
  return "目标端已停止：请先加入链接并启动。";
}

function renderSavedSourceLink(taskId, savedLink) {
  const box = document.createElement("div");
  box.className = "source-link";
  const summary = document.createElement("p");
  summary.textContent = `最近生成的源端链接：${savedLink.endpoint || "未记录地址"} · ${savedLink.relay_endpoint ? `Relay ${savedLink.relay_endpoint}${savedLink.relay_token_configured ? "（带令牌）" : ""} · ` : ""}${linkExpiryText(savedLink.expires_at)} · 重启不会改变这个链接`;
  const actions = document.createElement("div");
  actions.className = "inline-actions";
  actions.append(actionButton("复制链接", async () => {
    await navigator.clipboard.writeText(savedLink.link);
    notify("源端链接已复制");
  }, { secondary: true }));
  actions.append(actionButton("显示链接", () => {
    showSavedLink(taskId, savedLink);
  }, { secondary: true }));
  actions.append(actionButton("清除记录", () => {
    forgetSourceLink(taskId);
    loadTasks();
  }, { secondary: true }));
  box.append(summary, actions);
  return box;
}

function showSavedLink(taskId, savedLink) {
  generatedLink.value = savedLink.link;
  linkForm.elements.task_id.value = taskId;
  dialog.showModal();
}

function progressLabel(progress) {
  if (!progress) return "";
  const stage = progressStageLabel(progress.stage);
  if (progress.total_files === 0) return stage;
  const base = `本轮进度：${progress.completed_files}/${progress.total_files}`;
  if (!progress.current_path) return base;
  const currentBytes = Number(progress.current_bytes || 0);
  const totalBytes = Number(progress.current_total_bytes || 0);
  const percent = progressPercent(progress);
  const byteProgress = totalBytes > 0 ? ` · ${formatBytes(currentBytes)} / ${formatBytes(totalBytes)}${percent !== null ? ` · ${percent.toFixed(1)}%` : ""}` : "";
  return `${stage ? `${stage} · ` : ""}${base} · 正在处理 ${progress.current_path}${byteProgress}`;
}

function progressPercent(progress) {
  const currentBytes = Number(progress?.current_bytes || 0);
  const totalBytes = Number(progress?.current_total_bytes || 0);
  if (totalBytes > 0) return (currentBytes / totalBytes) * 100;
  const totalFiles = Number(progress?.total_files || 0);
  const completedFiles = Number(progress?.completed_files || 0);
  if (totalFiles > 0) return (completedFiles / totalFiles) * 100;
  return null;
}

function progressStageLabel(stage) {
  switch (stage) {
    case "connecting": return "连接中";
    case "scanning": return "扫描中";
    case "planning": return "生成计划";
    case "transfer": return "传输中";
    case "complete": return "本轮完成";
    case "waiting": return "等待变化";
    default: return "";
  }
}

function progressStageSentence(progress, fallback) {
  const label = progressStageLabel(progress?.stage);
  if (!label) return fallback;
  if (progress?.current_path) return `${label}，当前文件 ${progress.current_path}。`;
  return `${label}。`;
}

function stateLabel(task) {
  const stage = progressStageLabel(task.progress?.stage);
  if (task.role === "source") {
    if (task.state === "failed") return "失败";
    if (isRunningTask(task)) return stage ? `运行-${stage}` : "运行中";
    return "停止";
  }
  if (task.state === "failed") return "失败";
  if (isRunningTask(task) && deviceConnected(task)) return stage ? `运行-${stage}` : "运行-已连接源端";
  if (isRunningTask(task)) return stage ? `运行-${stage}` : "运行-连接中";
  return "停止";
}

function actionButton(label, action, options = {}) {
  const button = document.createElement("button");
  button.textContent = label;
  const classes = [];
  if (options.secondary) classes.push("secondary");
  if (options.danger) classes.push("danger");
  if (options.compact) classes.push("compact");
  button.className = classes.join(" ");
  if (options.disabled) button.disabled = true;
  button.addEventListener("click", action);
  return button;
}

async function taskAction(id, action) {
  try {
    await runTaskAction(id, action);
    notify(action === "start" ? "任务正在启动" : "任务已停止");
    setTimeout(loadTasks, 300);
  } catch (error) { notify(error.message); }
}

async function toggleSelectedTask() {
  const task = selectedTask();
  if (!task) return;
  if (isRunningTask(task)) {
    await taskAction(task.id, "stop");
    return;
  }
  if (task.role === "source" && !savedSourceLink(task.id)) {
    issueLink(task.id);
    return;
  }
  await taskAction(task.id, "start");
}

async function rescanSelectedTask() {
  const task = selectedTask();
  if (!task) return;
  if (task.role === "source" && !savedSourceLink(task.id)) {
    issueLink(task.id);
    return;
  }
  try {
    await runTaskAction(task.id, "rescan");
    notify("已重新扫描并启动同步");
    setTimeout(loadTasks, 300);
  } catch (error) { notify(error.message); }
}

function openSettingsForSelectedTask() {
  const task = selectedTask();
  if (!task) return;
  settingsSummary.textContent = `当前任务：${task.id} · ${roleLabel(task)} · ${task.role === "source" ? task.source_path : task.target_path}`;
  settingsForm.elements.ignore_rules.value = (task.ignore_rules || []).join("\n");
  ignoreSamplePath.value = "";
  ignorePreviewResult.textContent = "保存前可以先测试规则，查看哪些文件会被忽略。";
  ignoredList.textContent = "暂无预览。";
  loadIgnoreTemplates();
  settingsDialog.showModal();
}

async function saveSettings(event) {
  event.preventDefault();
  const task = selectedTask();
  if (!task) return;
  const rules = currentIgnoreRules();
  try {
    await api(`/api/tasks/${encodeURIComponent(task.id)}`, {
      method: "PATCH",
      body: JSON.stringify({ ignore_rules: rules }),
    });
    settingsDialog.close();
    notify("参数已保存");
    loadTasks();
  } catch (error) { notify(error.message); }
}

function currentIgnoreRules() {
  return settingsForm.elements.ignore_rules.value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line && !line.startsWith("#"));
}

async function loadIgnoreTemplates() {
  if (ignoreTemplates.length) return;
  try {
    const result = await api("/api/ignore/templates", { headers: {} });
    ignoreTemplates = result.templates || [];
    ignoreTemplate.replaceChildren(
      optionNode("", "选择模板后追加到规则"),
      ...ignoreTemplates.map((template) => optionNode(template.id, template.name)),
    );
  } catch (error) {
    notify(error.message);
  }
}

function optionNode(value, label) {
  const option = document.createElement("option");
  option.value = value;
  option.textContent = label;
  return option;
}

function applyIgnoreTemplate() {
  const template = ignoreTemplates.find((item) => item.id === ignoreTemplate.value);
  if (!template) {
    notify("请先选择一个模板");
    return;
  }
  const existing = currentIgnoreRules();
  const merged = [...new Set([...existing, ...(template.rules || [])])];
  settingsForm.elements.ignore_rules.value = merged.join("\n");
  notify(`已追加模板：${template.name}`);
}

async function previewIgnoreRules() {
  const task = selectedTask();
  if (!task) return;
  ignorePreviewResult.className = "hint";
  ignorePreviewResult.textContent = "正在测试忽略规则";
  ignoredList.textContent = "正在扫描...";
  try {
    const result = await api(`/api/tasks/${encodeURIComponent(task.id)}/ignore-preview`, {
      method: "POST",
      body: JSON.stringify({
        ignore_rules: currentIgnoreRules(),
        sample_path: ignoreSamplePath.value.trim(),
        sample_is_dir: ignoreSamplePath.value.trim().endsWith("/"),
        limit: 200,
      }),
    });
    const sample = ignoreSamplePath.value.trim()
      ? (result.sample_match ? `测试路径会被忽略，命中规则：${result.sample_match}。` : "测试路径不会被当前规则忽略。")
      : "";
    ignorePreviewResult.textContent = `${sample}${sample ? " " : ""}当前目录共有 ${result.total || 0} 个路径会被忽略${result.truncated ? "，列表只显示前 200 个" : ""}。`;
    const entries = result.entries || [];
    ignoredList.replaceChildren(...(entries.length ? entries.map(renderIgnoredEntry) : [plainHint("没有发现会被忽略的文件。")]));
  } catch (error) {
    ignorePreviewResult.textContent = error.message;
    ignorePreviewResult.className = "hint error";
    ignoredList.textContent = "预览失败。";
  }
}

function renderIgnoredEntry(entry) {
  const row = document.createElement("p");
  row.className = "log-entry";
  row.textContent = `${entry.is_dir ? "目录" : "文件"} · ${entry.path} · 规则：${entry.rule}`;
  return row;
}

function plainHint(text) {
  const row = document.createElement("p");
  row.className = "hint";
  row.textContent = text;
  return row;
}

function openLogsDialog() {
  renderLogs();
  logsDialog.showModal();
}

function allLogRows() {
  const task = selectedTask();
  return task
    ? (task.logs || []).map((entry) => ({ ...entry, task_id: task.id }))
    : tasksCache.flatMap((item) => (item.logs || []).map((entry) => ({ ...entry, task_id: item.id })));
}

function renderLogs() {
  const level = logLevelFilter.value;
  const query = logSearch.value.trim().toLowerCase();
  visibleLogs = allLogRows()
    .filter((entry) => !level || (entry.level || "info") === level)
    .filter((entry) => !query || logLine(entry).toLowerCase().includes(query))
    .slice(-300)
    .reverse();
  logsList.replaceChildren(...(visibleLogs.length ? visibleLogs.map(renderLogEntry) : [emptyLogEntry()]));
}

async function fetchDiagnosticsText() {
  const task = selectedTask();
  const path = task ? `/api/tasks/${encodeURIComponent(task.id)}/diagnostics` : "/api/diagnostics";
  const response = await fetch(path);
  if (!response.ok) {
    let message = "诊断日志导出失败";
    try {
      const body = await response.json();
      message = body.error || message;
    } catch {}
    throw new Error(message);
  }
  return response.text();
}

async function copyDiagnostics() {
  try {
    const text = await fetchDiagnosticsText();
    await navigator.clipboard.writeText(text);
    notify("诊断日志已复制");
  } catch (error) { notify(error.message); }
}

async function downloadDiagnostics() {
  try {
    const text = await fetchDiagnosticsText();
    const task = selectedTask();
    const name = `onesync-diagnostics-${task ? task.id : "all"}-${new Date().toISOString().replace(/[:.]/g, "-")}.txt`;
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = name;
    link.click();
    URL.revokeObjectURL(url);
    notify("诊断日志已下载");
  } catch (error) { notify(error.message); }
}

async function downloadDiagnosticsPackage() {
  try {
    const response = await fetch("/api/diagnostics.zip");
    if (!response.ok) {
      let message = "诊断包下载失败";
      try {
        const body = await response.json();
        message = body.error || message;
      } catch {}
      throw new Error(message);
    }
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `onesync-diagnostics-${new Date().toISOString().replace(/[:.]/g, "-")}.zip`;
    link.click();
    URL.revokeObjectURL(url);
    notify("诊断包已下载");
  } catch (error) { notify(error.message); }
}

async function copyVisibleLogs() {
  const text = visibleLogs.length ? visibleLogs.map(logLine).join("\n") : "暂无日志。";
  await navigator.clipboard.writeText(text);
  notify("当前日志已复制");
}

function downloadVisibleLogs() {
  const task = selectedTask();
  const name = `onesync-logs-${task ? task.id : "all"}-${new Date().toISOString().replace(/[:.]/g, "-")}.txt`;
  const text = visibleLogs.length ? visibleLogs.map(logLine).join("\n") : "暂无日志。";
  const url = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }));
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  link.click();
  URL.revokeObjectURL(url);
  notify("当前日志已下载");
}

function openDevicesDialog(task) {
  devicesTitle.textContent = `设备详情 - ${task.id}`;
  const devices = task.devices || {};
  const rows = [
    ["状态", stateLabel(task)],
    ["设备名称", deviceDisplayName(task)],
    ["是否信任", task.device_trusted ? "已信任" : "未信任"],
    ["是否禁用", task.device_disabled ? "已禁用" : "未禁用"],
    ["同步设备", deviceCountLabel(task)],
    ["连接", devices.connection || "等待连接"],
    ["源端地址", devices.endpoint || task.peer_address || "-"],
    ["Relay 地址", devices.relay_endpoint || task.relay_url || "-"],
    ["加密", devices.tls || "TLS 1.3"],
    ["客户端", devices.client_version || `OneSync ${appConfig.version || "dev"}`],
    ["最近连接", devices.last_seen ? new Date(devices.last_seen).toLocaleString() : "-"],
    ["累计接收", formatBytes((task.traffic || {}).received_bytes || 0)],
    ["累计发送", formatBytes((task.traffic || {}).sent_bytes || 0)],
  ];
  const table = document.createElement("table");
  table.className = "detail-table";
  for (const [label, value] of rows) {
    const row = document.createElement("tr");
    appendCell(row, label, "muted");
    appendCell(row, value || "-");
    table.append(row);
  }
  const children = [table];
  const history = renderDeviceHistory(task);
  if (history) children.push(history);
  if (task.role === "source") {
    const savedLink = savedSourceLink(task.id);
    const actions = document.createElement("div");
    actions.className = "inline-actions device-actions";
    actions.append(actionButton(savedLink ? "复制源端链接" : "生成源端链接", async () => {
      if (savedLink) {
        await navigator.clipboard.writeText(savedLink.link);
        notify("源端链接已复制");
      } else {
        devicesDialog.close();
        issueLink(task.id);
      }
    }, { secondary: true }));
    if (savedLink) {
      actions.append(actionButton("显示源端链接", () => {
        devicesDialog.close();
        showSavedLink(task.id, savedLink);
      }, { secondary: true }));
    }
    children.push(actions);
  }
  devicesBody.replaceChildren(...children);
  devicesDialog.showModal();
}

function openDeviceManager() {
  const children = [];
  const summaries = globalDeviceSummaries();
  if (summaries.length) {
    children.push(sectionTitle("全局设备"));
    children.push(...summaries.map(renderGlobalDeviceCard));
    children.push(sectionTitle("任务设备"));
  }
  children.push(...tasksCache.map(renderDeviceManagerCard));
  deviceManagerList.replaceChildren(...(children.length ? children : [plainHint("暂无设备。")]));
  if (!deviceManagerDialog.open) deviceManagerDialog.showModal();
}

function renderDeviceManagerCard(task) {
  const devices = task.devices || {};
  const card = document.createElement("section");
  card.className = `manager-card ${task.device_disabled ? "disabled" : ""} ${task.device_trusted ? "trusted" : ""}`;
  const title = document.createElement("h3");
  title.textContent = `${deviceDisplayName(task)} · ${task.id}`;
  const detail = document.createElement("p");
  detail.textContent = `${roleLabel(task)} · ${stateLabel(task)} · ${deviceCountLabel(task)} · ${task.device_trusted ? "已信任" : "未信任"} · ${task.device_disabled ? "已拉黑" : "未拉黑"} · ${devices.connection || "等待连接"} · ${devices.relay_endpoint || task.relay_url || "无 Relay"}`;
  const actions = document.createElement("div");
  actions.className = "inline-actions";
  actions.append(actionButton("历史", () => openDevicesDialog(task), { secondary: true, compact: true }));
  actions.append(actionButton("重命名", () => renameDevice(task), { secondary: true, compact: true }));
  actions.append(actionButton(task.device_trusted ? "取消信任" : "信任", () => setDeviceTrusted(task, !task.device_trusted), { secondary: true, compact: true }));
  actions.append(actionButton(task.device_disabled ? "启用" : "禁用", () => setDeviceDisabled(task, !task.device_disabled), { secondary: true, compact: true }));
  actions.append(actionButton("踢出", () => kickDevice(task), { danger: true, compact: true }));
  card.append(title, detail, actions);
  return card;
}

function sectionTitle(text) {
  const row = document.createElement("p");
  row.className = "manager-section-title";
  row.textContent = text;
  return row;
}

function globalDeviceSummaries() {
  const groups = new Map();
  for (const task of tasksCache) {
    const devices = task.devices || {};
    const key = devices.peer_id || devices.alias || `${task.role}:${task.id}`;
    const current = groups.get(key) || {
      name: deviceDisplayName(task),
      tasks: [],
      connected: 0,
      trusted: 0,
      disabled: 0,
      lastSeen: "",
      connection: new Set(),
    };
    current.tasks.push(task);
    if (deviceConnected(task)) current.connected++;
    if (task.device_trusted) current.trusted++;
    if (task.device_disabled) current.disabled++;
    if (devices.last_seen && (!current.lastSeen || new Date(devices.last_seen) > new Date(current.lastSeen))) current.lastSeen = devices.last_seen;
    if (devices.connection) current.connection.add(devices.connection);
    groups.set(key, current);
  }
  return [...groups.values()];
}

function renderGlobalDeviceCard(summary) {
  const card = document.createElement("section");
  card.className = `manager-card ${summary.disabled ? "disabled" : ""} ${summary.trusted ? "trusted" : ""}`;
  const title = document.createElement("h3");
  title.textContent = summary.name;
  const detail = document.createElement("p");
  detail.textContent = `关联任务 ${summary.tasks.length} 个 · 在线 ${summary.connected}/${summary.tasks.length} · 信任 ${summary.trusted} · 拉黑 ${summary.disabled} · ${summary.connection.size ? [...summary.connection].join("/") : "等待连接"} · 最近 ${summary.lastSeen ? new Date(summary.lastSeen).toLocaleString() : "-"}`;
  const taskList = document.createElement("p");
  taskList.className = "muted";
  taskList.textContent = summary.tasks.map((task) => `${task.id}(${roleLabel(task)})`).join("，");
  card.append(title, detail, taskList);
  return card;
}

function renderDeviceHistory(task) {
  const events = task.device_history || [];
  if (!events.length) return null;
  const box = document.createElement("div");
  box.className = "logs-list compact-list";
  box.replaceChildren(...events.slice(-30).reverse().map((event) => {
    const row = document.createElement("p");
    row.className = "log-entry";
    const time = event.time ? new Date(event.time).toLocaleString() : "";
    row.textContent = `${time} · ${event.type || "event"} · ${event.message || ""} · ${event.connection || "-"} · ${shortPeerID(event.peer_id)}`;
    return row;
  }));
  return box;
}

async function renameDevice(task) {
  const name = prompt("请输入设备名称", deviceDisplayName(task));
  if (name === null) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(task.id)}/device`, {
      method: "PATCH",
      body: JSON.stringify({ alias: name.trim() }),
    });
    notify("设备名称已更新");
    await loadTasks();
    openDeviceManager();
  } catch (error) { notify(error.message); }
}

async function setDeviceDisabled(task, disabled) {
  try {
    await api(`/api/tasks/${encodeURIComponent(task.id)}/device`, {
      method: "PATCH",
      body: JSON.stringify({ disabled }),
    });
    notify(disabled ? "设备已禁用" : "设备已启用");
    await loadTasks();
    openDeviceManager();
  } catch (error) { notify(error.message); }
}

async function setDeviceTrusted(task, trusted) {
  try {
    await api(`/api/tasks/${encodeURIComponent(task.id)}/device`, {
      method: "PATCH",
      body: JSON.stringify({ trusted }),
    });
    notify(trusted ? "设备已信任" : "设备已取消信任");
    await loadTasks();
    openDeviceManager();
  } catch (error) { notify(error.message); }
}

async function kickDevice(task) {
  if (!confirm(`踢出「${deviceDisplayName(task)}」？\n\n这会停止任务并清除当前设备绑定，不会删除同步目录文件。`)) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(task.id)}/device/kick`, { method: "POST", body: "{}" });
    notify("设备已踢出");
    await loadTasks();
    openDeviceManager();
  } catch (error) { notify(error.message); }
}

function openConnectionManager() {
  const cards = tasksCache.map(renderConnectionCard);
  connectionManagerList.replaceChildren(...(cards.length ? cards : [plainHint("暂无连接。")]));
  if (!connectionManagerDialog.open) connectionManagerDialog.showModal();
}

function renderConnectionCard(task) {
  const devices = task.devices || {};
  const card = document.createElement("section");
  card.className = "manager-card";
  const title = document.createElement("h3");
  title.textContent = `${task.id} · ${stateLabel(task)} · ${devices.connection || "等待连接"}`;
  const detail = document.createElement("p");
  const direct = devices.endpoint || task.peer_address || "-";
  const relay = devices.relay_endpoint || task.relay_url || "-";
  detail.textContent = `直连：${direct} · Relay：${relay} · 最近连接：${devices.last_seen ? new Date(devices.last_seen).toLocaleString() : "-"} · 错误分类：${errorCategory(task.last_error)}`;
  const stats = document.createElement("p");
  stats.textContent = `本地 ${localSizeLabel(task)} · 标准 ${standardSizeLabel(task)} · 累计接收 ${formatBytes((task.traffic || {}).received_bytes || 0)} · 累计发送 ${formatBytes((task.traffic || {}).sent_bytes || 0)}`;
  const error = document.createElement("p");
  error.className = task.last_error ? "error" : "muted";
  error.textContent = task.last_error || "暂无错误";
  const actions = document.createElement("div");
  actions.className = "inline-actions";
  actions.append(actionButton("复制诊断", async () => {
    selectedTaskId = task.id;
    await copyDiagnostics();
  }, { secondary: true, compact: true }));
  actions.append(actionButton("重新连接", async () => {
    try {
      await restartTask(task.id);
      notify("正在重新连接");
      setTimeout(loadTasks, 300);
    } catch (error) { notify(error.message); }
  }, { secondary: true, compact: true }));
  card.append(title, detail, stats, error, actions);
  return card;
}

function openAppSettings() {
  const rows = [
    ["版本号", appConfig.version || "dev"],
    ["管理页地址", `${appConfig.management_bind || "127.0.0.1"}:${appConfig.management_port || 8765}`],
    ["同步 TLS 端口", appConfig.sync_port || 7443],
    ["同步间隔", appConfig.sync_interval || "30s"],
    ["数据目录", appConfig.data_dir || "-"],
    ["服务日志", appConfig.log_file || "-"],
    ["源端直连 TLS", appConfig.direct_tls_configured ? "已自动加载" : "未加载"],
    ["证书覆盖地址", (appConfig.direct_tls_hosts || []).length ? (appConfig.direct_tls_hosts || []).join("，") : "-"],
    ["推荐直连地址", (appConfig.direct_tls_endpoints || []).length ? (appConfig.direct_tls_endpoints || []).join("，") : "-"],
    ["管理页登录", authState.enabled ? (authState.configured ? "已启用" : "待初始化") : "仅本机访问时未启用"],
  ];
  const table = document.createElement("table");
  table.className = "detail-table";
  for (const [label, value] of rows) {
    const row = document.createElement("tr");
    appendCell(row, label, "muted");
    appendCell(row, String(value || "-"));
    table.append(row);
  }
  appSettingsBody.replaceChildren(table);
  if (!appSettingsDialog.open) appSettingsDialog.showModal();
}

function errorCategory(message) {
  const text = String(message || "").toLowerCase();
  if (!text) return "-";
  if (text.includes("credential")) return "同步链接/凭据";
  if (text.includes("certificate") || text.includes("tls") || text.includes("x509")) return "TLS 证书";
  if (text.includes("relay")) return "Relay 连接";
  if (text.includes("connect") || text.includes("connection") || text.includes("timeout")) return "网络连接";
  if (text.includes("scan") || text.includes("stat") || text.includes("permission") || text.includes("path")) return "本地文件/权限";
  if (text.includes("disk") || text.includes("space")) return "磁盘空间";
  if (text.includes("authentication")) return "同步认证";
  return "其他";
}

function deviceCountLabel(task) {
  const devices = task.devices || {};
  const total = Math.max(Number(devices.total || 0), 1);
  const connected = Math.min(deviceConnected(task) ? Math.max(Number(devices.connected || 0), 1) : 0, total);
  return `${connected} / ${total}`;
}

function deviceConnected(task) {
  const devices = task.devices || {};
  if (Number(devices.connected || 0) > 0) return true;
  return Boolean(devices.last_seen && isRunningTask(task) && task.role === "target");
}

function shortPeerID(peerID) {
  const value = String(peerID || "");
  if (!value) return "-";
  return value.length <= 12 ? value : `${value.slice(0, 6)}...${value.slice(-6)}`;
}

function renderLogEntry(entry) {
  const row = document.createElement("p");
  row.className = `log-entry ${entry.level || "info"}`;
  row.textContent = logLine(entry);
  return row;
}

function logLine(entry) {
  const time = entry.time ? new Date(entry.time).toLocaleString() : "";
  const level = entry.level || "info";
  return `${time} · ${level}${entry.task_id ? ` · ${entry.task_id}` : ""} · ${entry.message || ""}`;
}

function emptyLogEntry() {
  const row = document.createElement("p");
  row.className = "hint";
  row.textContent = "暂无日志。";
  return row;
}

function trafficRates(tasks) {
  const now = Date.now();
  const seconds = Math.max((now - trafficSnapshot.time) / 1000, 1);
  const rates = {};
  const next = {};
  for (const task of tasks) {
    const traffic = task.traffic || {};
    const previous = trafficSnapshot.tasks[task.id] || { received: traffic.received_bytes || 0, sent: traffic.sent_bytes || 0 };
    const received = traffic.received_bytes || 0;
    const sent = traffic.sent_bytes || 0;
    rates[task.id] = {
      received: Math.max(0, (received - previous.received) / seconds),
      sent: Math.max(0, (sent - previous.sent) / seconds),
    };
    next[task.id] = { received, sent };
  }
  trafficSnapshot = { time: now, tasks: next };
  return rates;
}

function formatRate(bytesPerSecond) {
  const units = ["B/s", "KB/s", "MB/s", "GB/s"];
  let value = bytesPerSecond;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value < 10 && unit > 0 ? value.toFixed(2) : Math.round(value)} ${units[unit]}`;
}

function formatBytes(bytes) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = Number(bytes || 0);
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  if (unit === 0) return `${Math.round(value)} ${units[unit]}`;
  return `${value < 10 ? value.toFixed(2) : value.toFixed(1)} ${units[unit]}`;
}

async function runTaskAction(id, action) {
  return api(`/api/tasks/${encodeURIComponent(id)}/${action}`, { method: "POST", body: "{}" });
}

async function deleteTask(task) {
  const role = task.role === "source" ? "源端" : "目标端";
  const confirmed = confirm(`删除同步任务「${task.id}」？\n\n这会移除这个${role}任务和它保存的连接信息，不会删除你的同步目录文件。`);
  if (!confirmed) return;
  try {
    await api(`/api/tasks/${encodeURIComponent(task.id)}`, { method: "DELETE" });
    if (task.role === "source") forgetSourceLink(task.id);
    notify("任务已删除");
    loadTasks();
  } catch (error) { notify(error.message); }
}

function issueLink(taskId) {
  linkForm.reset();
  linkForm.elements.task_id.value = taskId;
  linkForm.elements.endpoint.placeholder = `例如：192.168.1.10:${appConfig.sync_port}`;
  updateLinkReadinessHint();
  generatedLink.value = "";
  renderEndpointSuggestions([]);
  dialog.showModal();
  loadEndpointSuggestions();
}

function sourceReadinessWarning(task) {
  if (task.role !== "source" || appConfig.direct_tls_configured) return "";
  return "源端 TLS 证书没有自动加载成功：直连暂不可用。请重启 OneSync；临时只用 Relay 时请填写 Relay 地址。";
}

function updateLinkReadinessHint() {
  linkReadinessHint.textContent = sourceReadinessWarning({ role: "source" }) || certificateHostWarning();
}

function certificateHostWarning() {
  if (!appConfig.direct_tls_configured) return "";
  const endpoint = linkForm.elements.endpoint.value;
  const host = endpointHost(endpoint);
  if (!host) return "";
  const certificateHosts = (appConfig.direct_tls_hosts || []).map((value) => String(value).toLowerCase());
  if (certificateHosts.length === 0) {
    return "当前源端证书没有 IP/DNS 地址，目标端可能无法验证这个直连地址。";
  }
  if (certificateHosts.some((name) => hostMatchesCertificate(host, name))) return "";
  return `当前源端证书不包含 ${host}，目标端测试直连时会证书验证失败。请使用证书里的地址，或重新生成包含该地址的证书。`;
}

function endpointHost(endpoint) {
  const value = endpoint.trim();
  if (!value) return "";
  try {
    return new URL(`tls://${value}`).hostname.replace(/^\[|\]$/g, "").toLowerCase();
  } catch {
    return "";
  }
}

function hostMatchesCertificate(host, certificateName) {
  if (host === certificateName) return true;
  if (!certificateName.startsWith("*.")) return false;
  const suffix = certificateName.slice(1);
  return host.endsWith(suffix) && host.slice(0, -suffix.length).indexOf(".") === -1;
}

linkForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  const data = new FormData(linkForm);
  const endpoint = String(data.get("endpoint") || "").trim();
  const relayEndpoint = String(data.get("relay_endpoint") || "").trim();
  const relayToken = String(data.get("relay_token") || "").trim();
  if (!appConfig.direct_tls_configured && !relayEndpoint) {
    notify("源端 TLS 证书没有自动加载成功，请重启 OneSync；临时只用 Relay 时请填写 Relay TLS 地址。");
    return;
  }
  if (relayToken && !relayEndpoint) {
    notify("填写 Relay 令牌时，也需要填写 Relay TLS 地址。");
    return;
  }
  try {
    const result = await api("/api/links", {
      method: "POST",
      body: JSON.stringify({
        task_id: data.get("task_id"),
        endpoint,
        relay_endpoint: relayEndpoint,
        relay_token: relayToken,
      }),
    });
    generatedLink.value = result.link;
    saveSourceLink(data.get("task_id"), {
      link: result.link,
      endpoint,
      relay_endpoint: relayEndpoint,
      relay_token_configured: Boolean(relayToken),
      expires_at: result.expires_at,
      created_at: new Date().toISOString(),
    });
    await restartTask(data.get("task_id"));
    notify("同步链接已生成；源端任务已使用这个链接启动");
    setTimeout(loadTasks, 300);
  } catch (error) { notify(error.message); }
});

async function restartTask(taskId) {
  try {
    await runTaskAction(taskId, "stop");
  } catch {
    // A stale or already stopped task should not prevent starting with the new link.
  }
  await runTaskAction(taskId, "start");
}

async function loadEndpointSuggestions() {
  endpointSuggestions.textContent = "正在查找本机地址";
  try {
    const { suggestions } = await api(`/api/endpoint-suggestions?port=${encodeURIComponent(appConfig.sync_port)}`, { headers: {} });
    renderEndpointSuggestions(suggestions || []);
  } catch (error) {
    endpointSuggestions.textContent = "无法读取本机地址，请手动填写";
  }
}

function renderEndpointSuggestions(suggestions) {
  endpointSuggestions.replaceChildren();
  const certificateSuggestions = certificateEndpointSuggestions();
  const localSuggestions = uniqueEndpoints(suggestions)
    .filter((suggestion) => !certificateSuggestions.includes(suggestion));
  if (!certificateSuggestions.length && !localSuggestions.length) {
    endpointSuggestions.textContent = "没有发现局域网 IPv4 地址，请手动填写。";
    return;
  }
  if (certificateSuggestions.length) {
    appendSuggestionGroup("证书地址：", certificateSuggestions);
  }
  if (localSuggestions.length) {
    appendSuggestionGroup("本机地址：", localSuggestions);
  }
}

function certificateEndpointSuggestions() {
  if (!appConfig.direct_tls_configured) return [];
  const configuredEndpoints = uniqueEndpoints(appConfig.direct_tls_endpoints || []);
  if (configuredEndpoints.length) return configuredEndpoints;
  const hosts = appConfig.direct_tls_hosts || [];
  return uniqueEndpoints(hosts
    .map((host) => certificateHostEndpoint(host, appConfig.sync_port))
    .filter(Boolean));
}

function certificateHostEndpoint(host, port) {
  const value = String(host || "").trim();
  if (!value || value.startsWith("*.")) return "";
  if (value.includes(":") && !value.startsWith("[")) return `[${value}]:${port}`;
  return `${value}:${port}`;
}

function uniqueEndpoints(values) {
  return [...new Set((values || []).map((value) => String(value || "").trim()).filter(Boolean))];
}

function appendSuggestionGroup(labelText, suggestions) {
  const label = document.createElement("span");
  label.textContent = labelText;
  endpointSuggestions.append(label);
  for (const suggestion of suggestions) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "suggestion";
    button.textContent = suggestion;
    button.addEventListener("click", () => {
      linkForm.elements.endpoint.value = suggestion;
      updateLinkReadinessHint();
      notify("已填入源端 TLS 地址");
    });
    endpointSuggestions.append(button);
  }
}

document.querySelector("#create-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  const data = new FormData(form);
  const role = data.get("role");
  const path = data.get("path");
  try {
    await api("/api/tasks", {
      method: "POST",
      body: JSON.stringify({
        id: data.get("id"), role,
        source_path: role === "source" ? path : "",
        target_path: role === "target" ? path : "",
      }),
    });
    form.reset();
    createDialog.close();
    notify("任务已创建");
    loadTasks();
  } catch (error) { notify(error.message); }
});

linkForm.elements.endpoint.addEventListener("input", updateLinkReadinessHint);

document.querySelector("#join-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  const data = new FormData(form);
  try {
    const result = await api("/api/links/join", {
      method: "POST",
      body: JSON.stringify(Object.fromEntries(data)),
    });
    await runTaskAction(result.task, "start");
    form.reset();
    joinDialog.close();
    notify("已加入同步，目标端任务正在启动");
    setTimeout(loadTasks, 300);
  } catch (error) { notify(error.message); }
});

testLinkButton.addEventListener("click", async () => {
  const form = document.querySelector("#join-form");
  const link = new FormData(form).get("link");
  if (!link) {
    notify("请先粘贴同步链接");
    return;
  }
  testLinkButton.disabled = true;
  connectionResult.className = "hint";
  connectionResult.textContent = "正在测试连接";
  try {
    const result = await api("/api/links/test", {
      method: "POST",
      body: JSON.stringify({ link }),
    });
    renderConnectionResult(result);
  } catch (error) {
    connectionResult.className = "hint error";
    connectionResult.textContent = error.message;
  } finally {
    testLinkButton.disabled = false;
  }
});

function renderConnectionResult(result) {
  const parts = [endpointStatus("直连", result.direct)];
  if (result.relay) parts.push(endpointStatus("Relay", result.relay));
  connectionResult.className = result.usable ? "hint success" : "hint error";
  connectionResult.textContent = result.usable
    ? `连接可用：${parts.join("；")}`
    : `连接失败：${parts.join("；")}`;
}

function endpointStatus(label, result) {
  if (!result) return `${label} 未配置`;
  if (result.ok) return `${label} 可用（${result.endpoint}）`;
  return `${label} 失败（${result.error || result.endpoint}）`;
}

document.querySelector("#refresh").addEventListener("click", loadTasks);
document.querySelector("#open-create").addEventListener("click", () => createDialog.showModal());
document.querySelector("#open-join").addEventListener("click", () => joinDialog.showModal());
document.querySelector("#close-create-dialog").addEventListener("click", () => createDialog.close());
document.querySelector("#close-join-dialog").addEventListener("click", () => joinDialog.close());
document.querySelector("#close-settings-dialog").addEventListener("click", () => settingsDialog.close());
document.querySelector("#open-logs").addEventListener("click", openLogsDialog);
document.querySelector("#open-device-manager").addEventListener("click", openDeviceManager);
document.querySelector("#open-connection-manager").addEventListener("click", openConnectionManager);
document.querySelector("#open-app-settings").addEventListener("click", openAppSettings);
document.querySelector("#open-about").addEventListener("click", () => aboutDialog.showModal());
document.querySelector("#apply-ignore-template").addEventListener("click", applyIgnoreTemplate);
document.querySelector("#preview-ignore").addEventListener("click", previewIgnoreRules);
document.querySelector("#copy-diagnostics").addEventListener("click", copyDiagnostics);
document.querySelector("#download-diagnostics").addEventListener("click", downloadDiagnostics);
document.querySelector("#download-diagnostics-package").addEventListener("click", downloadDiagnosticsPackage);
document.querySelector("#copy-visible-logs").addEventListener("click", copyVisibleLogs);
document.querySelector("#download-visible-logs").addEventListener("click", downloadVisibleLogs);
logLevelFilter.addEventListener("change", renderLogs);
logSearch.addEventListener("input", renderLogs);
document.querySelector("#copy-all-diagnostics").addEventListener("click", async () => {
  selectedTaskId = "";
  await copyDiagnostics();
});
document.querySelector("#download-all-diagnostics-package").addEventListener("click", downloadDiagnosticsPackage);
document.querySelector("#copy-settings-diagnostics").addEventListener("click", async () => {
  selectedTaskId = "";
  await copyDiagnostics();
});
document.querySelector("#download-settings-diagnostics-package").addEventListener("click", downloadDiagnosticsPackage);
settingsForm.addEventListener("submit", saveSettings);
startStopButton.addEventListener("click", toggleSelectedTask);
rescanButton.addEventListener("click", rescanSelectedTask);
settingsButton.addEventListener("click", openSettingsForSelectedTask);
deleteButton.addEventListener("click", () => {
  const task = selectedTask();
  if (task) deleteTask(task);
});
document.querySelector("#copy-link").addEventListener("click", async () => {
  if (!generatedLink.value) {
    notify("请先生成同步链接");
    return;
  }
  await navigator.clipboard.writeText(generatedLink.value);
  notify("链接已复制");
});
document.querySelector("#close-link-dialog").addEventListener("click", () => dialog.close());

function sourceLinks() {
  try {
    return JSON.parse(localStorage.getItem(sourceLinksStorageKey) || "{}");
  } catch {
    return {};
  }
}

function savedSourceLink(taskId) {
  const saved = sourceLinks()[taskId];
  if (!saved || !saved.link) return null;
  return saved;
}

function saveSourceLink(taskId, link) {
  const links = sourceLinks();
  links[taskId] = link;
  localStorage.setItem(sourceLinksStorageKey, JSON.stringify(links));
}

function forgetSourceLink(taskId) {
  const links = sourceLinks();
  delete links[taskId];
  localStorage.setItem(sourceLinksStorageKey, JSON.stringify(links));
}

function linkExpiryText(value) {
  if (!value) return "有效期未知";
  const expiresAt = new Date(value);
  if (Number.isNaN(expiresAt.getTime())) return "有效期未知";
  if (expiresAt.getTime() <= Date.now()) return "可能已过期";
  return `有效至 ${expiresAt.toLocaleString()}`;
}

async function loadConfig() {
  try {
    appConfig = { ...defaultConfig, ...(await api("/api/config", { headers: {} })) };
    aboutVersion.textContent = appConfig.version || "dev";
  } catch (error) {
    notify(error.message);
  }
}

loadAuthStatus().then(() => loadConfig()).finally(loadTasks);
setInterval(loadTasks, 3000);
