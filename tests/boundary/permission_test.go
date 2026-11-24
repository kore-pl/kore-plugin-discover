// 边界类别：权限不足（permission / unauthorized / need_root）。
// 断言执行面：探测需要读 root-only 文件/目录时，必须明确降级——
// 端口枚举返回 E_UNAUTHORIZED 并说明「权限不足」，服务清单保留完毕、不 panic、不静默返回空。
package boundary_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengtang1024/kore-plugin-discover/internal/probe"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// requireNonRoot 在 root 下跳过：root 绕过 DAC 权限位，无法真实复现权限不足。
func requireNonRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("当前以 root 运行，权限位不生效，无法真实模拟操作系统的权限不足")
	}
}

// denyRead 把 fixture 文件权限改为 0000，并在测试结束前恢复，保证 TempDir 可清理。
func denyRead(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("设置只读拒绝权限失败: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
}

// TestPermission_PortsExplicitUnauthorized 验证读 /proc/net/tcp 被拒时返回 E_UNAUTHORIZED + 可读提示。
func TestPermission_PortsExplicitUnauthorized(t *testing.T) {
	requireNonRoot(t)
	root := newFixtureRoot(t)
	denyRead(t, filepath.Join(root, "proc", "net", "tcp"))

	rt := probe.NewRuntime(probe.Options{GOOS: "linux", RootFS: root, PathDirs: []string{}}, nil)
	_, perr := rt.Call("discover.ports", nil)
	if perr == nil {
		t.Fatal("权限不足时 discover.ports 必须显式报错，不能静默返回空结果")
	}
	if perr.Code != pluginapi.E_UNAUTHORIZED {
		t.Fatalf("权限不足错误码 = %s，期望 %s", perr.Code, pluginapi.E_UNAUTHORIZED)
	}
	if !strings.Contains(perr.Message, "权限不足") {
		t.Fatalf("权限不足错误信息缺少明确提示: %q", perr.Message)
	}
}

// TestPermission_ServicesDegradeWithoutPanic 验证同一个权限故障下 services 不 panic、
// 返回完整服务清单（监听判定降级），而不是整表失败或返回空数组。
func TestPermission_ServicesDegradeWithoutPanic(t *testing.T) {
	requireNonRoot(t)
	root := newFixtureRoot(t)
	denyRead(t, filepath.Join(root, "proc", "net", "tcp"))

	rt := probe.NewRuntime(probe.Options{GOOS: "linux", RootFS: root, PathDirs: []string{}}, nil)
	raw, perr := rt.Call("discover.services", nil)
	if perr != nil {
		t.Fatalf("权限不足时 services 应降级返回清单，实际报错: %+v", perr)
	}
	var services []probe.Service
	if err := json.Unmarshal(raw, &services); err != nil {
		t.Fatalf("services 输出不是数组: %v (%s)", err, raw)
	}
	if len(services) < 20 {
		t.Fatalf("权限不足时服务清单被截断: %d 条（疑似静默返回空/部分结果）", len(services))
	}
	for _, service := range services {
		if service.Found {
			t.Fatalf("权限不足时不应判定 found=true: %+v", service)
		}
		if service.Running {
			t.Fatalf("权限不足时 running 必须降级为 false: %+v", service)
		}
	}
}

// TestPermission_ConfigDirSkipped 验证探测需要读被拒绝的配置目录时，该项被跳过（ConfigDir 留空），
// 其余字段仍正常返回，不 panic、不拿父目录路径冒充配置目录。
func TestPermission_ConfigDirSkipped(t *testing.T) {
	requireNonRoot(t)
	root := newFixtureRoot(t)
	// 让 /etc 不可进入：/etc/nginx 的 stat 会 EACCES，findConfigDir 必须跳过该项。
	etcDir := filepath.Join(root, "etc")
	if err := os.MkdirAll(filepath.Join(etcDir, "nginx"), 0o755); err != nil {
		t.Fatalf("创建 fixture 配置目录失败: %v", err)
	}
	if err := os.Chmod(etcDir, 0o000); err != nil {
		t.Fatalf("设置配置目录权限失败: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(etcDir, 0o755) })

	binDir := t.TempDir()
	newFixtureExecutable(t, binDir, "nginx")
	rt := probe.NewRuntime(probe.Options{
		GOOS: "linux", RootFS: root, PathDirs: []string{binDir},
		Runner: fixedRunner("nginx version: nginx/1.24.0\n", nil),
	}, nil)

	raw, perr := rt.Call("discover.services", nil)
	if perr != nil {
		t.Fatalf("配置目录不可读不应让 services 失败: %+v", perr)
	}
	var services []probe.Service
	if err := json.Unmarshal(raw, &services); err != nil {
		t.Fatalf("services 输出不是数组: %v", err)
	}
	var nginx *probe.Service
	for i := range services {
		if services[i].ID == "nginx" {
			nginx = &services[i]
		}
	}
	if nginx == nil {
		t.Fatal("services 清单缺 nginx 条目")
	}
	if !nginx.Found {
		t.Fatalf("PATH 中有可执行文件时应 found=true: %+v", nginx)
	}
	if nginx.ConfigDir != "" {
		t.Fatalf("配置目录不可读时应跳过该项（ConfigDir 留空），实际 %q", nginx.ConfigDir)
	}
	if nginx.ClientVersion != "1.24.0" {
		t.Fatalf("配置目录故障不应影响版本探测，实际 %q", nginx.ClientVersion)
	}
}
