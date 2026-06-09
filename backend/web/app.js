const list = document.querySelector("#task-list");
const statusText = document.querySelector("#status");
const toast = document.querySelector("#toast");
const dialog = document.querySelector("#link-dialog");
const linkForm = document.querySelector("#link-form");
const generatedLink = document.querySelector("#generated-link");
const endpointSuggestions = document.querySelector("#endpoint-suggestions");
const connectionResult = document.querySelector("#connection-result");
const testLinkButton = document.querySelector("#test-link");
const linkReadinessHint = document.querySelector("#link-readiness-hint");
const defaultConfig = { sync_port: 7443, direct_tls_configured: false, direct_tls_hosts: [], direct_tls_endpoints: [] };
const sourceLinksStorageKey = "onesync.sourceLinks.v1";
let appConfig = { ...defaultConfig };

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
    list.replaceChildren(...tasks.map(renderTask));
    if (!tasks.length) list.innerHTML = "<p>还没有同步任务。</p>";
    statusText.textContent = `${tasks.length} 个任务`;
  } catch (error) {
    statusText.textContent = "加载失败";
    notify(error.message);
  }
}

function renderTask(task) {
  const item = document.createElement("article");
  item.className = `task ${taskClass(task)}`;
  const details = document.createElement("div");
  const path = task.role === "source" ? task.source_path : task.target_path;
  details.innerHTML = `<h3></h3><p></p><p class="state-detail"></p><p class="warning"></p><p class="progress"></p><p class="error"></p>`;
  details.querySelector("h3").textContent = task.id;
  details.querySelector("p").textContent = `${task.role === "source" ? "源端" : "目标端"} · ${path} · `;
  const badge = document.createElement("span");
  badge.className = `badge ${task.state || "unknown"}`;
  badge.textContent = stateLabel(task.state);
  details.querySelector("p").append(badge);
  details.querySelector(".state-detail").textContent = taskStateDetail(task);
  details.querySelector(".warning").textContent = sourceReadinessWarning(task);
  details.querySelector(".progress").textContent = progressLabel(task.progress);
  details.querySelector(".error").textContent = task.last_error || "";
  if (task.role === "source") {
    const savedLink = savedSourceLink(task.id);
    if (savedLink) details.append(renderSavedSourceLink(task.id, savedLink));
  }

  const actions = document.createElement("div");
  actions.className = "actions";
  actions.append(actionButton("启动", () => taskAction(task.id, "start"), { disabled: isRunningTask(task) }));
  actions.append(actionButton("停止", () => taskAction(task.id, "stop"), { secondary: true, disabled: !isRunningTask(task) }));
  if (task.role === "source") actions.append(actionButton(savedSourceLink(task.id) ? "重新生成链接" : "生成链接并启动", () => issueLink(task.id)));
  actions.append(actionButton("删除", () => deleteTask(task), { danger: true }));
  item.append(details, actions);
  return item;
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
    generatedLink.value = savedLink.link;
    linkForm.elements.task_id.value = taskId;
    dialog.showModal();
  }, { secondary: true }));
  actions.append(actionButton("清除记录", () => {
    forgetSourceLink(taskId);
    loadTasks();
  }, { secondary: true }));
  box.append(summary, actions);
  return box;
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
  } catch (error) {
    notify(error.message);
  }
}

loadConfig().finally(loadTasks);
setInterval(loadTasks, 3000);
