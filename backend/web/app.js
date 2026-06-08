const list = document.querySelector("#task-list");
const statusText = document.querySelector("#status");
const toast = document.querySelector("#toast");
const dialog = document.querySelector("#link-dialog");
const generatedLink = document.querySelector("#generated-link");

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
  item.className = "task";
  const details = document.createElement("div");
  const path = task.role === "source" ? task.source_path : task.target_path;
  details.innerHTML = `<h3></h3><p></p><p class="error"></p>`;
  details.querySelector("h3").textContent = task.id;
  details.querySelector("p").textContent = `${task.role === "source" ? "源端" : "目标端"} · ${path} · `;
  const badge = document.createElement("span");
  badge.className = "badge";
  badge.textContent = stateLabel(task.state);
  details.querySelector("p").append(badge);
  details.querySelector(".error").textContent = task.last_error || "";

  const actions = document.createElement("div");
  actions.className = "actions";
  actions.append(actionButton("启动", () => taskAction(task.id, "start")));
  actions.append(actionButton("停止", () => taskAction(task.id, "stop"), true));
  if (task.role === "source") actions.append(actionButton("生成链接", () => issueLink(task.id)));
  item.append(details, actions);
  return item;
}

function stateLabel(state) {
  return ({
    created: "已创建",
    connecting: "连接中",
    syncing: "同步中",
    idle: "等待下一轮",
    failed: "失败",
    stopped: "已停止",
  })[state] || state;
}

function actionButton(label, action, secondary = false) {
  const button = document.createElement("button");
  button.textContent = label;
  if (secondary) button.className = "secondary";
  button.addEventListener("click", action);
  return button;
}

async function taskAction(id, action) {
  try {
    await api(`/api/tasks/${encodeURIComponent(id)}/${action}`, { method: "POST", body: "{}" });
    notify(action === "start" ? "任务正在启动" : "任务已停止");
    setTimeout(loadTasks, 300);
  } catch (error) { notify(error.message); }
}

async function issueLink(taskId) {
  const endpoint = prompt("请输入其他设备可以访问的 TLS 地址，例如 192.168.1.10:7443");
  if (!endpoint) return;
  const relayEndpoint = prompt("可选：请输入 Relay TLS 地址；不使用可留空", "") || "";
  try {
    const result = await api("/api/links", {
      method: "POST",
      body: JSON.stringify({ task_id: taskId, endpoint, relay_endpoint: relayEndpoint }),
    });
    generatedLink.value = result.link;
    dialog.showModal();
  } catch (error) { notify(error.message); }
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

document.querySelector("#join-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  const data = new FormData(form);
  try {
    await api("/api/links/join", {
      method: "POST",
      body: JSON.stringify(Object.fromEntries(data)),
    });
    form.reset();
    notify("已加入同步");
    loadTasks();
  } catch (error) { notify(error.message); }
});

document.querySelector("#refresh").addEventListener("click", loadTasks);
document.querySelector("#copy-link").addEventListener("click", async () => {
  await navigator.clipboard.writeText(generatedLink.value);
  notify("链接已复制");
});

loadTasks();
