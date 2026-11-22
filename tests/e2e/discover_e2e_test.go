//go:build linux

package e2e

import (
	"encoding/json"
	"fmt"
	"net"
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
