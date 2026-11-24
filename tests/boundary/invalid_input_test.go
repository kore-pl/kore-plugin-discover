// 边界类别：异常输入（非法输入 / 非法参数 / malformed / 未知服务名 / 超长路径）。
// 断言执行面：非法探测目标、格式错误的探测输出、未知服务名、超长路径都必须显式降级，
// 不允许 panic，也不允许把解析失败伪装成成功结果。
package boundary_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pengtang1024/kore-plugin-discover/internal/probe"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// TestInvalid_MalformedParamsRejected 验证非法参数（数组/标量/截断 JSON/类型错误）返回 E_INVALID。
func TestInvalid_MalformedParamsRejected(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"数组参数", "[]"},
		{"字符串参数", "\"discover\""},
		{"数字参数", "42"},
		{"截断的 JSON", "{oops"},
		{"refresh 类型错误", `{"refresh":"yes"}`},
	}
	rt := probe.NewRuntime(probe.Options{GOOS: "linux", RootFS: t.TempDir(), PathDirs: []string{}}, nil)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, perr := rt.Call("discover.services", json.RawMessage(tc.raw)); perr == nil {
				t.Fatalf("非法参数 %q 未被拒绝", tc.raw)
			} else if perr.Code != pluginapi.E_INVALID {
				t.Fatalf("非法参数 %q 错误码 = %s，期望 %s", tc.raw, perr.Code, pluginapi.E_INVALID)
			}
		})
	}
}

// TestInvalid_UnknownMethodNotFound 验证未知方法名（未知服务名/未知动作）返回 E_NOTFOUND。
func TestInvalid_UnknownMethodNotFound(t *testing.T) {
	rt := probe.NewRuntime(probe.Options{GOOS: "linux", RootFS: t.TempDir(), PathDirs: []string{}}, nil)
	for _, method := range []string{"discover.unknown", "discover.", "discover.services.extra"} {
		_, perr := rt.Call(method, nil)
		if perr == nil {
			t.Fatalf("未知方法 %q 未被拒绝", method)
		}
		if perr.Code != pluginapi.E_NOTFOUND {
			t.Fatalf("未知方法 %q 错误码 = %s，期望 %s", method, perr.Code, pluginapi.E_NOTFOUND)
		}
	}
}

// TestInvalid_NonexistentProbeTarget 验证探测根不存在时：服务清单降级为全量未安装，端口显式报错。
func TestInvalid_NonexistentProbeTarget(t *testing.T) {
	missingRoot := filepath.Join(t.TempDir(), "no-such-root")
	rt := probe.NewRuntime(probe.Options{GOOS: "linux", RootFS: missingRoot, PathDirs: []string{}}, nil)

	raw, perr := rt.Call("discover.services", nil)
	if perr != nil {
		t.Fatalf("探测根缺失时 services 应降级返回清单，实际报错: %+v", perr)
	}
	var services []probe.Service
	if err := json.Unmarshal(raw, &services); err != nil {
		t.Fatalf("services 输出不是数组: %v (%s)", err, raw)
	}
	if len(services) < 20 {
		t.Fatalf("services 清单不完整: %d 条", len(services))
	}
	for _, service := range services {
		if service.Found {
			t.Fatalf("探测根缺失时不应判定 found=true: %+v", service)
		}
		if service.VersionSource != probe.VersionSourceUnknown {
			t.Fatalf("%s 版本来源应为 unknown，实际 %s", service.ID, service.VersionSource)
		}
	}

	_, portErr := rt.Call("discover.ports", nil)
	if portErr == nil {
		t.Fatal("探测根缺失时 discover.ports 应返回显式错误，不能静默返回空结果")
	}
	if portErr.Code != pluginapi.E_INTERNAL {
		t.Fatalf("discover.ports 错误码 = %s，期望 %s", portErr.Code, pluginapi.E_INTERNAL)
	}
	if portErr.Message == "" {
		t.Fatal("discover.ports 错误信息不应为空")
	}
}

// TestInvalid_MalformedProbeOutput 验证格式错误的探测输出（坏 /proc/net 行、垃圾版本输出）不 panic。
func TestInvalid_MalformedProbeOutput(t *testing.T) {
	root := t.TempDir()
	garbage := strings.Join([]string{
		tcpHeader,
		"这不是 /proc/net/tcp 的一行",
		"0: ZZZZ:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 1",
		"0: 0100007F:ZZZZ 00000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 1",
		"0: 0100007F:1F90",
	}, "\n")
	writeFixtureFile(t, filepath.Join(root, "proc", "net", "tcp"), garbage, 0o644)
	writeFixtureFile(t, filepath.Join(root, "proc", "net", "tcp6"), tcpHeader+"\n", 0o644)

	binDir := t.TempDir()
	newFixtureExecutable(t, binDir, "node")

	rt := probe.NewRuntime(probe.Options{
		GOOS: "linux", RootFS: root, PathDirs: []string{binDir},
		Runner: fixedRunner("\x00\xff 这不是版本号 \n", nil),
	}, nil)

	portsRaw, perr := rt.Call("discover.ports", nil)
	if perr != nil {
		t.Fatalf("坏 /proc/net 行不应让整次解析失败: %+v", perr)
	}
	var ports []probe.ListeningPort
	if err := json.Unmarshal(portsRaw, &ports); err != nil {
		t.Fatalf("ports 输出不是数组: %v", err)
	}
	if len(ports) != 0 {
		t.Fatalf("坏行不应被解析成监听端口，实际 %d 条", len(ports))
	}

	servicesRaw, perr := rt.Call("discover.services", nil)
	if perr != nil {
		t.Fatalf("垃圾版本输出不应让 services 失败: %+v", perr)
	}
	var services []probe.Service
	if err := json.Unmarshal(servicesRaw, &services); err != nil {
		t.Fatalf("services 输出不是数组: %v", err)
	}
	var node *probe.Service
	for i := range services {
		if services[i].ID == "node" {
			node = &services[i]
		}
	}
	if node == nil {
		t.Fatal("services 清单缺 node 条目")
	}
	if node.ClientVersion != "" {
		t.Fatalf("垃圾输出不应被解析出版本，实际 %q", node.ClientVersion)
	}
	if node.VersionSource != probe.VersionSourceUnknown {
		t.Fatalf("垃圾输出时版本来源应为 unknown，实际 %s", node.VersionSource)
	}
	if node.VersionHint == "" {
		t.Fatal("版本未知时应给出可操作提示，不能静默留空")
	}
}

// TestInvalid_OverlongPath 验证超长路径（PATH 目录 / 探测根）不 panic、及时降级。
func TestInvalid_OverlongPath(t *testing.T) {
	overlong := strings.Repeat("a", 4096)
	longPathDir := filepath.Join(t.TempDir(), overlong)
	root := newFixtureRoot(t)

	started := time.Now()
	rt := probe.NewRuntime(probe.Options{
		GOOS: "linux", RootFS: root, PathDirs: []string{longPathDir, longPathDir + "/nested"},
	}, nil)
	raw, perr := rt.Call("discover.services", nil)
	if perr != nil {
		t.Fatalf("超长 PATH 目录不应让 services 失败: %+v", perr)
	}
	var services []probe.Service
	if err := json.Unmarshal(raw, &services); err != nil {
		t.Fatalf("services 输出不是数组: %v", err)
	}
	if len(services) < 20 {
		t.Fatalf("超长路径下 services 清单不完整: %d 条", len(services))
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("超长路径探测耗时异常: %s", elapsed)
	}

	overlongRoot := root + strings.Repeat("/x", 3000)
	rtLongRoot := probe.NewRuntime(probe.Options{GOOS: "linux", RootFS: overlongRoot, PathDirs: []string{}}, nil)
	if _, perr := rtLongRoot.Call("discover.services", nil); perr != nil {
		t.Fatalf("超长探测根不应让 services 失败: %+v", perr)
	}
}

// TestInvalid_UnknownServiceNameAccepted 验证未知服务名（Source 返回 catalog 外 ID）不 panic、不误判就绪。
func TestInvalid_UnknownServiceNameAccepted(t *testing.T) {
	summary := probe.BuildSummary(
		[]probe.Service{{ID: "unknown-service-xyz", Name: "未知服务", Found: true, VersionSource: probe.VersionSourceUnknown}},
		[]probe.ListeningPort{{Port: 65535, Addr: "127.0.0.1", Family: "ipv4", Listening: true}},
	)
	if summary.Ready {
		t.Fatal("未知服务名不应被判为已就绪")
	}
	if summary.WebServer != "" || summary.DB != "" {
		t.Fatalf("未知服务名不应填充 web/db 字段: %+v", summary)
	}
	if summary.Conclusion == "" {
		t.Fatal("未知服务名时仍应给出一句话结论")
	}
	if len(summary.Suggestions) == 0 {
		t.Fatal("未知服务名时仍应给出建议")
	}
}
