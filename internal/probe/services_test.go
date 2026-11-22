package probe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFakeScript 写一个可执行的假命令，用于验证 PATH 探测与版本解析。
func writeFakeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建假命令目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("写假命令失败: %v", err)
	}
}

// newFakeSource 构造指向临时根的只读执行面。
func newFakeSource(t *testing.T, root string, pathDirs []string, tweak func(*Options)) *hostSource {
	t.Helper()
	opts := Options{RootFS: root, PathDirs: pathDirs}
	if tweak != nil {
		tweak(&opts)
	}
	opts = opts.normalize()
	return &hostSource{opts: opts, fs: hostFS{root: opts.RootFS}}
}

// findService 在服务清单里按 id 查找。
func findService(services []Service, id string) (Service, bool) {
	for _, service := range services {
		if service.ID == id {
			return service, true
		}
	}
	return Service{}, false
}

// TestServicesFakeNginx 用 t.TempDir 假 bin 目录探测：found + version + source + 配置目录。
func TestServicesFakeNginx(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	writeFakeScript(t, filepath.Join(binDir, "nginx"), "#!/bin/sh\necho 'nginx version: nginx/1.24.0'\n")
	if err := os.MkdirAll(filepath.Join(root, "etc", "nginx"), 0o755); err != nil {
		t.Fatalf("创建假配置目录失败: %v", err)
	}
	source := newFakeSource(t, root, []string{binDir}, nil)
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("探测服务失败: %v", err)
	}
	nginx, ok := findService(services, "nginx")
	if !ok {
		t.Fatal("服务清单缺少 nginx 条目")
	}
	if !nginx.Found || nginx.Bin != filepath.Join(binDir, "nginx") {
		t.Fatalf("nginx 探测结果不符: %+v", nginx)
	}
	if nginx.Version != "1.24.0" {
		t.Fatalf("nginx 版本=%q，期望 1.24.0", nginx.Version)
	}
	if nginx.Source != "which" {
		t.Fatalf("nginx source=%q，期望 which", nginx.Source)
	}
	if nginx.ConfigDir != filepath.Join(root, "etc", "nginx") {
		t.Fatalf("nginx config_dir=%q", nginx.ConfigDir)
	}
	if nginx.Port != 80 || nginx.ID != "nginx" || nginx.Name != "Nginx" {
		t.Fatalf("nginx 固定字段不符: %+v", nginx)
	}
}

// TestServicesNotFoundHint 不装的服务必须 found=false 且带安装提示。
func TestServicesNotFoundHint(t *testing.T) {
	root := t.TempDir()
	emptyBin := filepath.Join(root, "emptybin")
	if err := os.MkdirAll(emptyBin, 0o755); err != nil {
		t.Fatalf("创建空 bin 目录失败: %v", err)
	}
	source := newFakeSource(t, root, []string{emptyBin}, nil)
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("探测服务失败: %v", err)
	}
	for _, id := range []string{"nginx", "caddy", "mysql", "postgres", "redis", "docker", "crontab", "systemctl", "ufw"} {
		service, ok := findService(services, id)
		if !ok {
			t.Fatalf("服务清单缺少 %s", id)
		}
		if service.Found {
			t.Fatalf("%s 在空环境里不应 found: %+v", id, service)
		}
		if service.InstallHint == "" {
			t.Fatalf("%s 未提供 install_hint", id)
		}
	}
	caddy, _ := findService(services, "caddy")
	if !strings.Contains(caddy.InstallHint, "apt install caddy") {
		t.Fatalf("caddy 安装提示不含 apt 命令: %q", caddy.InstallHint)
	}
}

// TestServicesCommandTimeout 单条探测命令必须按 3s 预算截断（这里注入 100ms 验证），
// 超时只丢版本、不影响 found 结论。
func TestServicesCommandTimeout(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	writeFakeScript(t, filepath.Join(binDir, "nginx"), "#!/bin/sh\nsleep 5\necho 'nginx version: nginx/1.24.0'\n")
	source := newFakeSource(t, root, []string{binDir}, func(opts *Options) {
		opts.CommandTimeout = 100 * time.Millisecond
	})
	start := time.Now()
	services, err := source.Services(context.Background())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("探测服务失败: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("命令超时未生效：耗时 %s", elapsed)
	}
	nginx, _ := findService(services, "nginx")
	if !nginx.Found {
		t.Fatalf("命令超时不应影响 found: %+v", nginx)
	}
	if nginx.Version != "" {
		t.Fatalf("命令被超时截断时不应有版本: %q", nginx.Version)
	}
}

// TestParseVersion 覆盖各发行版/工具的真实版本输出格式。
func TestParseVersion(t *testing.T) {
	cases := []struct{ output, want string }{
		{"nginx version: nginx/1.24.0", "1.24.0"},
		{"Apache/2.4.58 (Ubuntu)", "2.4.58"},
		{"mysql  Ver 8.0.34-0ubuntu0.22.04.3 for Linux on x86_64", "8.0.34-0ubuntu0.22.04.3"},
		{"psql (PostgreSQL) 16.1 (Ubuntu 16.1-0ubuntu0.22.04.1)", "16.1"},
		{"Redis server v=7.0.11 sha=00000000:0 malloc=jemalloc-5.2.1 bits=64 build=...", "7.0.11"},
		{"v20.11.0", "20.11.0"},
		{"Python 3.11.6", "3.11.6"},
		{"Docker version 24.0.7, build afdd53b", "24.0.7"},
		{"podman version 4.9.3", "4.9.3"},
		{"v2.7.6 h1:abc", "2.7.6"},
		{"uptime is a lie", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := parseVersion(tc.output); got != tc.want {
			t.Fatalf("parseVersion(%q)=%q，期望 %q", tc.output, got, tc.want)
		}
	}
}

// TestServicesProcessOnly 只有运行中进程名（容器里的 nginx，宿主无二进制）时，
// 仍应 found=true、running=true，并用 source=process 标明来源。
func TestServicesProcessOnly(t *testing.T) {
	root := t.TempDir()
	pidDir := filepath.Join(root, "proc", "131291")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("创建假 /proc 目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "comm"), []byte("nginx\n"), 0o644); err != nil {
		t.Fatalf("写假 comm 失败: %v", err)
	}
	source := newFakeSource(t, root, []string{}, nil)
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("探测服务失败: %v", err)
	}
	nginx, _ := findService(services, "nginx")
	if !nginx.Found || !nginx.Running || nginx.Source != "process" || nginx.Bin != "" {
		t.Fatalf("进程-only 探测结果不符: %+v", nginx)
	}
}
