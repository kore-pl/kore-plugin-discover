//go:build linux

package e2e

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// requiredServices 是 discover.services 必须覆盖的探测目标（任务口径）。
var requiredServices = []string{
	"nginx", "caddy", "apache2", "httpd",
	"mysql", "mariadb", "postgres",
	"redis",
	"php", "node", "python3",
	"docker", "podman",
	"crontab", "systemctl", "ufw", "iptables", "firewall-cmd",
}

// TestDiscoverE2E 真实插件进程全链路：services → ports（与 ss -ltn 对照）→ summary → shutdown。
func TestDiscoverE2E(t *testing.T) {
	tmp := t.TempDir()
	binary := buildPlugin(t)
	socket := filepath.Join(tmp, "discover.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("监听 UDS 失败: %v", err)
	}
	defer func() { _ = listener.Close() }()
	_ = listener.(*net.UnixListener).SetDeadline(time.Now().Add(120 * time.Second))

	process, stderr := startPlugin(t, binary, socket)
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("接受插件连接失败: %v (stderr=%s)", err, stderr.String())
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(120 * time.Second))
	handshake(t, conn)

	// 1) 真实服务探测：清单必须覆盖所有目标，未装项 found=false + install_hint。
	services := parseServices(t, mustCall(t, conn, "e2e-services", "discover.services", map[string]any{}))
	if len(services) < len(requiredServices) {
		t.Fatalf("服务条目=%d，少于探测目标 %d", len(services), len(requiredServices))
	}
	for _, id := range requiredServices {
		service, ok := findService(services, id)
		if !ok {
			t.Fatalf("服务清单缺少目标 %s", id)
		}
		if !service.Found && service.InstallHint == "" {
			t.Fatalf("%s 未安装但没有 install_hint", id)
		}
	}
	found := 0
	for _, service := range services {
		if service.Found {
			found++
		}
	}
	t.Logf("E2E_SERVICES_SUMMARY total=%d found=%d", len(services), found)

	// 贴任务要求的关键条目：nginx / mysql / docker / crontab 的真实探测结果。
	for _, id := range []string{"nginx", "mysql", "docker", "crontab"} {
		service, _ := findService(services, id)
		raw, err := json.Marshal(service)
		if err != nil {
			t.Fatalf("序列化 %s 证据失败: %v", id, err)
		}
		t.Logf("E2E_SERVICE_%s %s", strings.ToUpper(id), raw)
	}

	// 1b) 版本语义：server_version 只能来自真实服务端探测；只拿到客户端版本时
	// version_source 必须降级为 client，二者不得混淆。
	for _, service := range services {
		switch service.VersionSource {
		case "server":
			if service.ServerVersion == "" {
				t.Fatalf("%s version_source=server 但 server_version 为空", service.ID)
			}
		case "client":
			if service.ServerVersion != "" {
				t.Fatalf("%s version_source=client 却带 server_version=%q", service.ID, service.ServerVersion)
			}
			if service.ClientVersion == "" {
				t.Fatalf("%s version_source=client 但 client_version 为空", service.ID)
			}
		case "unknown":
			if service.ClientVersion != "" || service.ServerVersion != "" {
				t.Fatalf("%s version_source=unknown 却带版本: %+v", service.ID, service)
			}
		default:
			t.Fatalf("%s version_source 非法: %q", service.ID, service.VersionSource)
		}
	}
	for _, id := range []string{"mysql", "mariadb", "postgres", "redis", "nginx"} {
		service, _ := findService(services, id)
		raw, err := json.Marshal(service)
		if err != nil {
			t.Fatalf("序列化 %s 版本证据失败: %v", id, err)
		}
		t.Logf("E2E_VERSION_%s %s", strings.ToUpper(id), raw)
	}
	// 真机 E2E：提供了凭据就必须探到真实服务端版本；探测失败是失败，不是静默降级。
	assertExpectedServerVersion(t, services, "mysql", "MYSQL_PWD", "E2E_EXPECT_MYSQL_SERVER_VERSION")
	assertExpectedServerVersion(t, services, "redis", "REDISCLI_AUTH", "E2E_EXPECT_REDIS_SERVER_VERSION")

	// 2) 真实监听端口：字段完整，并与 ss -ltn 对得上。
	ports := parsePorts(t, mustCall(t, conn, "e2e-ports", "discover.ports", map[string]any{}))
	if len(ports) == 0 {
		t.Fatal("discover.ports 未返回任何监听端口")
	}
	pluginPorts := make(map[int]bool)
	for _, port := range ports {
		if port.Port < 1 || port.Port > 65535 {
			t.Fatalf("端口越界: %+v", port)
		}
		if port.Addr == "" || !port.Listening {
			t.Fatalf("端口字段不完整: %+v", port)
		}
		if port.Family != "ipv4" && port.Family != "ipv6" {
			t.Fatalf("端口 family 非法: %+v", port)
		}
		pluginPorts[port.Port] = true
	}
	head := ports
	if len(head) > 5 {
		head = head[:5]
	}
	headRaw, err := json.Marshal(head)
	if err != nil {
		t.Fatalf("序列化端口证据失败: %v", err)
	}
	t.Logf("E2E_PORTS_SUMMARY total=%d unique_ports=%d", len(ports), len(pluginPorts))
	t.Logf("E2E_PORTS_HEAD %s", headRaw)

	if ssPorts, ok := listenPortsFromSS(t); ok {
		missing := make([]int, 0)
		for port := range pluginPorts {
			if !ssPorts[port] {
				missing = append(missing, port)
			}
		}
		sort.Ints(missing)
		t.Logf("E2E_SS_COMPARE plugin_ports=%s ss_ports=%s missing=%v",
			portsText(pluginPorts), portsText(ssPorts), missing)
		if len(missing) > 0 {
			t.Fatalf("插件端口在 ss -ltn 中不存在: %v", missing)
		}
	} else {
		t.Logf("E2E_SS_SKIP 未找到 ss 命令，跳过对照")
	}

	// 3) 汇总建议：结论非空，web/db 与真实探测结果一致。
	summary := parseSummary(t, mustCall(t, conn, "e2e-summary", "discover.summary", map[string]any{}))
	if summary.Conclusion == "" {
		t.Fatal("discover.summary 未返回结论")
	}
	expectedWeb := firstFoundService(services, "nginx", "caddy", "apache2", "httpd")
	expectedDB := firstFoundService(services, "mysql", "mariadb", "postgres")
	if summary.WebServer != expectedWeb {
		t.Fatalf("summary.web_server=%q，探测结果=%q", summary.WebServer, expectedWeb)
	}
	if summary.DB != expectedDB {
		t.Fatalf("summary.db=%q，探测结果=%q", summary.DB, expectedDB)
	}
	if summary.Ready != (expectedWeb != "" || expectedDB != "") {
		t.Fatalf("summary.ready=%v 与探测结果不一致", summary.Ready)
	}
	if summary.Ready && len(summary.Suggestions) == 0 {
		t.Fatal("ready=true 但没有任何插件建议")
	}
	summaryRaw, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("序列化汇总证据失败: %v", err)
	}
	t.Logf("E2E_SUMMARY %s", summaryRaw)

	// 4) ping 与优雅退出。
	ping := mustCall(t, conn, "e2e-ping", "kore.ping", map[string]any{})
	if !strings.Contains(ping, `"status":"ready"`) {
		t.Fatalf("kore.ping 结果不符: %s", ping)
	}
	shutdown := mustCall(t, conn, "e2e-shutdown", "kore.shutdown", map[string]any{})
	if !strings.Contains(shutdown, `"ok":true`) {
		t.Fatalf("shutdown 结果不符: %s", shutdown)
	}
	waitExit(t, process)
	if strings.Contains(stderr.String(), "panic:") {
		t.Fatalf("插件 stderr 出现 panic: %s", stderr.String())
	}
	t.Logf("E2E_SHUTDOWN %s", shutdown)
}

// firstFoundService 按优先级返回第一个 found 的服务 id。
func firstFoundService(services []serviceItem, ids ...string) string {
	for _, id := range ids {
		if service, ok := findService(services, id); ok && service.Found {
			return id
		}
	}
	return ""
}

// portsText 把端口集合格式化为排序后的逗号串，便于证据对照。
func portsText(ports map[int]bool) string {
	values := make([]int, 0, len(ports))
	for port := range ports {
		values = append(values, port)
	}
	sort.Ints(values)
	parts := make([]string, 0, len(values))
	for _, port := range values {
		parts = append(parts, strconv.Itoa(port))
	}
	return fmt.Sprintf("[%s]", strings.Join(parts, ","))
}

// assertExpectedServerVersion 校验"有凭据 → 必须拿到真实服务端版本"。
// 只有同时提供 credEnv 或 expectEnv 时才启用，避免无数据库的 CI 误报。
func assertExpectedServerVersion(t *testing.T, services []serviceItem, id, credEnv, expectEnv string) {
	t.Helper()
	want := os.Getenv(expectEnv)
	_, hasCred := os.LookupEnv(credEnv)
	if want == "" && !hasCred {
		t.Logf("E2E_VERSION_%s_SKIP 未提供 %s / %s", strings.ToUpper(id), credEnv, expectEnv)
		return
	}
	service, ok := findService(services, id)
	if !ok {
		t.Fatalf("服务清单缺少 %s", id)
	}
	if !service.Found {
		t.Fatalf("%s 未 found，但已提供 %s，无法验证服务端版本", id, credEnv)
	}
	if service.ServerVersion == "" || service.VersionSource != "server" {
		t.Fatalf("%s 提供了 %s 仍未探到服务端版本: %+v", id, credEnv, service)
	}
	if want != "" && service.ServerVersion != want {
		t.Fatalf("%s server_version=%q，期望 %q", id, service.ServerVersion, want)
	}
}
