package backend

import (
	"os/exec"
	"strings"
	"testing"
)

func TestWebAppBehaviorWithMinimalDOM(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not available")
	}
	script := readWebAsset(t, "web/app.js")
	testScript := minimalDOMHarness() + "\n" + script + `
tasksCache = [
  {
    id: "source-a",
    role: "source",
    state: "idle",
    device_trusted: true,
    device_disabled: false,
    logs: [
      { time: "2026-06-09T10:00:00Z", level: "info", message: "会话=aaa111 连接成功" },
      { time: "2026-06-09T10:01:00Z", level: "warning", message: "会话=bbb222 Relay 暂不可用" }
    ],
    devices: { alias: "办公室电脑", peer_id: "peer-source", connected: 1, total: 1, connection: "Relay", last_seen: "2026-06-09T10:02:00Z" }
  },
  {
    id: "target-b",
    role: "target",
    state: "stopped",
    device_trusted: false,
    device_disabled: true,
    logs: [{ time: "2026-06-09T10:03:00Z", level: "error", message: "authentication failed" }],
    devices: { peer_id: "peer-target", connected: 0, total: 1, connection: "直连" }
  }
];
selectedTaskId = "";
logLevelFilter.value = "warning";
logSearch.value = "relay";
renderLogs();
if (visibleLogs.length !== 1 || !visibleLogs[0].message.includes("Relay")) {
  throw new Error("log filter did not keep the Relay warning");
}
const summaries = globalDeviceSummaries();
if (summaries.length !== 2) throw new Error("global device summaries length = " + summaries.length);
const trusted = summaries.find((item) => item.name === "办公室电脑");
if (!trusted || trusted.trusted !== 1 || trusted.connected !== 1) {
  throw new Error("trusted global device summary is wrong");
}
const disabled = summaries.find((item) => item.disabled === 1);
if (!disabled || disabled.tasks[0].id !== "target-b") {
  throw new Error("disabled global device summary is wrong");
}
console.log("ok");
`
	command := exec.Command("node", "-")
	command.Stdin = strings.NewReader(testScript)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("node web behavior test failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "ok") {
		t.Fatalf("node web behavior output = %s", output)
	}
}

func minimalDOMHarness() string {
	return `
class FakeNode {}
class FakeElement extends FakeNode {
  constructor(selector) {
    super();
    this.selector = selector;
    this.value = "";
    this.textContent = "";
    this.hidden = false;
    this.disabled = false;
    this.checked = false;
    this.children = [];
    this.className = "";
    this.required = false;
    this.open = false;
    this.classList = { add() {}, remove() {} };
    this.elements = new Proxy({}, { get: (target, name) => {
      if (!target[name]) target[name] = new FakeElement(String(name));
      return target[name];
    }});
  }
  addEventListener() {}
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  showModal() { this.open = true; }
  close() { this.open = false; }
  reset() {}
  click() {}
}
const elementMap = new Map();
function element(selector) {
  if (!elementMap.has(selector)) elementMap.set(selector, new FakeElement(selector));
  return elementMap.get(selector);
}
globalThis.Node = FakeNode;
globalThis.document = {
  querySelector: element,
  createElement: (name) => new FakeElement(name),
};
globalThis.navigator = { clipboard: { writeText: async () => {} } };
globalThis.localStorage = { getItem: () => "{}", setItem() {}, removeItem() {} };
globalThis.fetch = async (path) => ({
  ok: true,
  status: 200,
  json: async () => path.includes("/api/config")
    ? { sync_port: 7443, version: "test" }
    : path.includes("/api/auth/status")
      ? { enabled: false, configured: false, authenticated: true }
      : { tasks: [] },
  text: async () => "",
});
globalThis.setTimeout = () => 0;
globalThis.setInterval = () => 0;
globalThis.confirm = () => true;
globalThis.prompt = (_message, value) => value || "";
`
}
