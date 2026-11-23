package probe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件覆盖「宿主没监听 / 只有客户端」两类误报的判定（本轮修复的回归测试）。

func nginxSpec() serviceSpec {
	for _, spec := range catalog {
		if spec.ID == "nginx" {
			return spec
		}
	}
	return serviceSpec{ID: "nginx", Name: "Nginx", Port: 80}
}

func mariadbSpec() serviceSpec {
	for _, spec := range catalog {
		if spec.ID == "mariadb" {
			return spec
		}
	}
	return serviceSpec{ID: "mariadb", Name: "MariaDB", Port: 3306}
}

// TestRunningDecisionContainerProcess 只有容器内进程、宿主没监听 → running=false + 解释。
func TestRunningDecisionContainerProcess(t *testing.T) {
	running, hint := runningDecision(nginxSpec(), false, true, listenerInfo{}, false, false)
	if running {
		t.Fatal("容器内进程不得判成宿主服务运行中")
	}
	for _, want := range []string{"容器", "80", "发布端口"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("解释文案缺 %q: %q", want, hint)
		}
	}
}

// TestRunningDecisionDockerProxyListener 端口由 docker-proxy 转发、宿主无服务端进程 → running=false。
func TestRunningDecisionDockerProxyListener(t *testing.T) {
	info := listenerInfo{present: true, process: "docker-proxy", containerProxy: true}
	running, hint := runningDecision(mariadbSpec(), false, false, info, false, false)
	if running {
		t.Fatal("docker-proxy 转发的端口不得判成宿主 MariaDB 在运行")
	}
	if !strings.Contains(hint, "端口转发") {
		t.Fatalf("应说明端口由容器转发: %q", hint)
	}
}

// TestRunningDecisionHostEvidence 宿主进程或宿主自己监听 → running=true（不能过度收紧）。
func TestRunningDecisionHostEvidence(t *testing.T) {
	if running, _ := runningDecision(nginxSpec(), true, false, listenerInfo{}, false, false); !running {
		t.Fatal("宿主进程存在应判运行中")
	}
	if running, _ := runningDecision(mariadbSpec(), false, false,
		listenerInfo{present: true, process: "mysqld"}, false, false); !running {
		t.Fatal("宿主服务自己监听端口应判运行中")
	}
}

// TestServicesContainerProcessFoundNotRunning 端到端：假 /proc 里 nginx 进程带 docker cgroup。
func TestServicesContainerProcessFoundNotRunning(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc", "424242")
	if err := os.MkdirAll(proc, 0o755); err != nil {
		t.Fatalf("建假 proc 失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(proc, "comm"), []byte("nginx\n"), 0o644); err != nil {
		t.Fatalf("写 comm 失败: %v", err)
	}
	cgroup := "0::/system.slice/docker-0123456789abcdef.scope\n"
	if err := os.WriteFile(filepath.Join(proc, "cgroup"), []byte(cgroup), 0o644); err != nil {
		t.Fatalf("写 cgroup 失败: %v", err)
	}
	source := newFakeSource(t, root, nil, nil)
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	nginx, ok := findService(services, "nginx")
	if !ok {
		t.Fatal("缺少 nginx 条目")
	}
	if !nginx.Found || nginx.Running {
		t.Fatalf("容器内 nginx 应 found=true/running=false: %+v", nginx)
	}
	if !strings.Contains(nginx.RunningHint, "容器") {
		t.Fatalf("应给出容器解释: %q", nginx.RunningHint)
	}
}

// TestServicesVersionUnknownText 两者都拿不到版本时给「版本未知」明说，不留空。
func TestServicesVersionUnknownText(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	// node 的 --version 输出无法解析出版本号（模拟命令存在但输出异常）。
	writeFakeScript(t, filepath.Join(binDir, "node"), "#!/bin/sh\necho 'unexpected output'\n")
	source := newFakeSource(t, root, []string{binDir}, nil)
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	node, ok := findService(services, "node")
	if !ok {
		t.Fatal("缺少 node 条目")
	}
	if node.VersionSource != VersionSourceUnknown {
		t.Fatalf("version_source=%q，期望 unknown", node.VersionSource)
	}
	if !strings.Contains(node.VersionHint, "版本未知") {
		t.Fatalf("版本未知时应明写文案: %q", node.VersionHint)
	}
}
