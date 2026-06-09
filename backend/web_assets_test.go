package backend

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/202121000995/OneSync/internal/auth"
	"github.com/202121000995/OneSync/internal/task"
)

func TestWebScriptReferencesExistingPageElements(t *testing.T) {
	html := readWebAsset(t, "web/index.html")
	script := readWebAsset(t, "web/app.js")
	ids := matchesByGroup(`id="([^"]+)"`, html)
	for selector := range matchesByGroup(`querySelector\("#([A-Za-z0-9_-]+)"\)`, script) {
		if _, exists := ids[selector]; !exists {
			t.Fatalf("app.js references missing #%s", selector)
		}
	}
}

func TestWebScriptFormElementsExistInPage(t *testing.T) {
	html := readWebAsset(t, "web/index.html")
	script := readWebAsset(t, "web/app.js")
	names := matchesByGroup(`name="([^"]+)"`, html)
	for field := range matchesByGroup(`\.elements\.([A-Za-z0-9_]+)`, script) {
		if _, exists := names[field]; !exists {
			t.Fatalf("app.js references missing form field %q", field)
		}
	}
}

func TestWebScriptKeepsReadinessRegressionsCovered(t *testing.T) {
	script := readWebAsset(t, "web/app.js")
	required := []string{
		"direct_tls_configured",
		"direct_tls_hosts",
		"direct_tls_endpoints",
		"certificateHostWarning",
		"sourceReadinessWarning",
		"证书地址：",
		"本机地址：",
		"请填写 Relay TLS 地址",
		"源端 TLS 证书没有自动加载成功",
		"当前源端证书不包含",
		"同步链接已生成；源端任务已使用这个链接启动",
		"已加入同步，目标端任务正在启动",
		"最近生成的源端链接",
		"重启不会改变这个链接",
		"已重新扫描并启动同步",
		"参数已保存",
		"formatRate",
		"formatBytes",
		"deviceCountLabel",
		"运行-连接中",
		"运行-已连接源端",
		"初始化管理账号",
		"/api/auth/setup",
		"/api/auth/login",
		"设备详情",
		"删除",
		"不会删除你的同步目录文件",
		"/api/ignore/templates",
		"/ignore-preview",
		"诊断日志已复制",
		"openDeviceManager",
		"openConnectionManager",
		"openAppSettings",
		"taskTotals",
		"taskStatsSummary",
		"/device/kick",
		"设备已禁用",
		"设备已踢出",
		"错误分类",
		"管理页地址",
		"同步间隔",
	}
	for _, text := range required {
		if !strings.Contains(script, text) {
			t.Fatalf("app.js no longer contains %q", text)
		}
	}
}

func TestWebPageKeepsTaskTableAsMainSurface(t *testing.T) {
	html := readWebAsset(t, "web/index.html")
	required := []string{
		"同步任务",
		"设备管理",
		"连接管理",
		"任务统计正在加载",
		"管理页登录",
		"创建同步",
		"加入同步",
		"暂停/开始",
		"重新扫描",
		"参数",
		"设置",
		"删除",
		"忽略文件",
		"默认模板",
		"测试路径",
		"测试规则",
		"任务日志",
		"复制诊断",
		"下载诊断",
		"踢出设备",
		"复制全部诊断",
		"关于 OneSync",
		"版本号",
		"类型",
		"名称",
		"状态",
		"同步设备",
		"设备详情",
		"本地大小",
		"标准大小",
		"接收流量",
		"发送流量",
	}
	for _, text := range required {
		if !strings.Contains(html, text) {
			t.Fatalf("index.html no longer contains %q", text)
		}
	}
}

func TestStaticWebAssetsServed(t *testing.T) {
	manager := &fakeManager{tasks: make(map[string]task.Task)}
	credentials, err := auth.NewCredentialStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	server, err := NewServer(manager, auth.NewLinkService(), credentials)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	for _, target := range []string{"/", "/app.js", "/app.css"} {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+target, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", target, response.Code, response.Body.String())
		}
		if response.Body.Len() == 0 {
			t.Fatalf("%s returned an empty body", target)
		}
	}
}

func readWebAsset(t *testing.T, name string) string {
	t.Helper()
	data, err := webFiles.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", name, err)
	}
	return string(data)
}

func matchesByGroup(pattern, text string) map[string]struct{} {
	matches := regexp.MustCompile(pattern).FindAllStringSubmatch(text, -1)
	values := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		values[match[1]] = struct{}{}
	}
	return values
}
