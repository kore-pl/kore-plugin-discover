// 边界测试公共夹具：用只读 fixture 根目录模拟探测目标，绝不扫描真实宿主服务。
package boundary_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pengtang1024/kore-plugin-discover/internal/probe"
)

// /proc/net/tcp 表头与一条真实形状的 LISTEN 行（127.0.0.1:8080，inode=12345）。
const tcpHeader = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode"
const tcpListenerLine = "   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345"

// writeFixtureFile 写入 fixture 文件并显式 chmod（不受调用方 umask 影响）。
func writeFixtureFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建 fixture 目录失败: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("写入 fixture 文件失败: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("设置 fixture 权限失败: %v", err)
	}
}

// newFixtureRoot 构造最小可探测根：/proc/net/tcp 有一条监听，tcp6 为空。
func newFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "proc", "net", "tcp"), tcpHeader+"\n"+tcpListenerLine+"\n", 0o644)
	writeFixtureFile(t, filepath.Join(root, "proc", "net", "tcp6"), tcpHeader+"\n", 0o644)
	return root
}

// newFixtureExecutable 在 dir 下创建可执行文件（模拟 PATH 中已安装的命令）。
func newFixtureExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	writeFixtureFile(t, path, "#!/bin/sh\nexit 0\n", 0o755)
	return path
}

// fixedRunner 返回固定输出/错误的探测执行面，避免真的执行宿主命令。
func fixedRunner(output string, err error) probe.Runner {
	return func(context.Context, string, []string, ...string) (string, error) {
		return output, err
	}
}
