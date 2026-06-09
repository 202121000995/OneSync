const list = document.querySelector("#task-list");
const statusText = document.querySelector("#status");
const toast = document.querySelector("#toast");
const dialog = document.querySelector("#link-dialog");
const createDialog = document.querySelector("#create-dialog");
const joinDialog = document.querySelector("#join-dialog");
const settingsDialog = document.querySelector("#settings-dialog");
const logsDialog = document.querySelector("#logs-dialog");
const devicesDialog = document.querySelector("#devices-dialog");
const aboutDialog = document.querySelector("#about-dialog");
const settingsForm = document.querySelector("#settings-form");
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
const devicesTitle = document.querySelector("#devices-title");
const devicesBody = document.querySelector("#devices-body");
const aboutVersion = document.querySelector("#about-version");
const defaultConfig = { sync_port: 7443, direct_tls_configured: false, direct_tls_hosts: [], direct_tls_endpoints: [], version: "dev" };
const sourceLinksStorageKey = "onesync.sourceLinks.v1";
let appConfig = { ...defaultConfig };
let tasksCache = [];
let selectedTaskId = "";
let trafficSnapshot = { time: Date.now(), tasks: {} };

async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
  });
  const body = await response.json();
  if (!response.ok) throw new Error(body.error || "请求失败");
  return body;
}

function notify(message) {
  toast.textContent = message;
  toast.classList.add("show");
  setTimeout(() => toast.classList.remove("show"), 2600);
}

async function loadTasks() {
  statusText.textContent = "正在加载";
  try {
    const { tasks } = await api("/api/tasks", { headers: {} });
    const rates = trafficRates(tasks || []);
    tasksCache = tasks || [];
    if (selectedTaskId && !tasksCache.some((task) => task.id === selectedTaskId)) selectedTaskId = "";
    list.replaceChildren(...(tasksCache.length ? tasksCache.map((task) => renderTaskRow(task, rates[task.id] || {})) : [emptyTaskRow()]));
    statusText.textContent = `${tasks.length} 个任务`;
    updateToolbar();
  } catch (error) {
    statusText.textContent = "加载失败";
    notify(error.message);
  }
}

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
  badge.textContent = stateLabel(task.state);
  box.append(badge);
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
  if (task.state === "connecting") return "运行中：正在等待对端连接。源端可直接复制最近链接给目标端。";
  if (task.state === "syncing") return "运行中：正在同步文件。";
  if (task.state === "idle") return "运行中：等待下一轮同步或新的对端连接。";
  if (task.state === "failed") return "已失败：请查看下方错误信息。";
  if (task.state === "stopped") return "已停止：需要时可以手动启动。";
  return "未启动：源端请先生成链接，目标端请先加入链接。";
}

function renderSavedSourceLink(taskId, savedLink) {
  const box = document.createElement("div");
  box.className = "source-link";
  const summary = document.createElement("p");
  summary.textContent = `最近生成的源端链接：${savedLink.endpoint || "未记录地址"} · ${linkExpiryText(savedLink.expires_at)} · 重启不会改变这个链接`;
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
  if (!progress || progress.total_files === 0) return "";
  const base = `本轮进度：${progress.completed_files}/${progress.total_files}`;
  if (!progress.current_path) return base;
  return `${base} · 正在处理 ${progress.current_path}`;
}

function stateLabel(state) {
  return ({
    created: "未启动",
    connecting: "运行中：连接中",
    syncing: "运行中：同步中",
    idle: "运行中：等待",
    failed: "失败",
    stopped: "已停止",
  })[state] || state;
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
    await restartTask(task.id);
    notify("已重新扫描并启动同步");
    setTimeout(loadTasks, 300);
  } catch (error) { notify(error.message); }
}

function openSettingsForSelectedTask() {
  const task = selectedTask();
  if (!task) return;
  settingsSummary.textContent = `当前任务：${task.id} · ${roleLabel(task)} · ${task.role === "source" ? task.source_path : task.target_path}`;
  settingsForm.elements.ignore_rules.value = (task.ignore_rules || []).join("\n");
  settingsDialog.showModal();
}

async function saveSettings(event) {
  event.preventDefault();
  const task = selectedTask();
  if (!task) return;
  const rules = settingsForm.elements.ignore_rules.value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line && !line.startsWith("#"));
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

function openLogsDialog() {
  const task = selectedTask();
  const logs = task ? (task.logs || []) : tasksCache.flatMap((item) => (item.logs || []).map((entry) => ({ ...entry, task_id: item.id })));
  logsList.replaceChildren(...(logs.length ? logs.slice(-200).reverse().map(renderLogEntry) : [emptyLogEntry()]));
  logsDialog.showModal();
}

function openDevicesDialog(task) {
  devicesTitle.textContent = `设备详情 - ${task.id}`;
  const devices = task.devices || {};
  const rows = [
    ["状态", stateLabel(task.state)],
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

function deviceCountLabel(task) {
  const devices = task.devices || {};
  const total = Math.max(Number(devices.total || 0), 1);
  const connected = Math.min(Number(devices.connected || 0), total);
  return `${connected} / ${total}`;
}

function renderLogEntry(entry) {
  const row = document.createElement("p");
  row.className = `log-entry ${entry.level || "info"}`;
  const time = entry.time ? new Date(entry.time).toLocaleString() : "";
  row.textContent = `${time}${entry.task_id ? ` · ${entry.task_id}` : ""} · ${entry.message || ""}`;
  return row;
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
  if (!appConfig.direct_tls_configured && !relayEndpoint) {
    notify("源端 TLS 证书没有自动加载成功，请重启 OneSync；临时只用 Relay 时请填写 Relay TLS 地址。");
    return;
  }
  try {
    const result = await api("/api/links", {
      method: "POST",
      body: JSON.stringify({
        task_id: data.get("task_id"),
        endpoint,
        relay_endpoint: relayEndpoint,
      }),
    });
    generatedLink.value = result.link;
    saveSourceLink(data.get("task_id"), {
      link: result.link,
      endpoint,
      relay_endpoint: relayEndpoint,
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
document.querySelector("#open-about").addEventListener("click", () => aboutDialog.showModal());
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

loadConfig().finally(loadTasks);
setInterval(loadTasks, 3000);
