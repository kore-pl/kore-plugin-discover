//go:build linux

package e2e

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// buildPlugin 构建真实插件二进制到临时目录。
func buildPlugin(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "discover-plugin")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = "../.."
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("构建插件失败: %v\n%s", err, out)
	}
	return binary
}

// startPlugin 以协议环境变量启动插件进程；stderr 收集起来便于失败时定位。
func startPlugin(t *testing.T, binary, socket string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	stderr := &bytes.Buffer{}
	process := exec.Command(binary)
	process.Env = append(os.Environ(),
		"KORE_PLUGIN_SOCK="+socket,
		"KORE_PLUGIN_ID=discover",
		"KORE_PLUGIN_TOKEN=e2e-token",
	)
	process.Stdout = io.Discard
	process.Stderr = stderr
	if err := process.Start(); err != nil {
		t.Fatalf("启动插件失败: %v", err)
	}
	t.Cleanup(func() {
		if process.ProcessState == nil {
			_ = process.Process.Kill()
			_, _ = process.Process.Wait()
		}
	})
	return process, stderr
}

// waitExit 等待插件进程退出。
func waitExit(t *testing.T, process *exec.Cmd) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("插件退出异常: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("插件未在 5s 内退出")
	}
}

// listenPortsFromSS 解析 `ss -ltn` 的监听端口集合，用于与 /proc/net 结果对照。
func listenPortsFromSS(t *testing.T) (map[int]bool, bool) {
	t.Helper()
	ssBin, err := exec.LookPath("ss")
	if err != nil {
		return nil, false
	}
	out, err := exec.Command(ssBin, "-ltn").Output()
	if err != nil {
		t.Logf("E2E_SS_SKIP 执行 ss 失败: %v", err)
		return nil, false
	}
	ports := make(map[int]bool)
	for _, line := range bytes.Split(out, []byte("\n")) {
		fields := bytes.Fields(line)
		if len(fields) < 4 || string(fields[0]) != "LISTEN" {
			continue
		}
		local := string(fields[3])
		index := bytes.LastIndexByte([]byte(local), ':')
		if index < 0 {
			continue
		}
		port := 0
		for _, r := range local[index+1:] {
			if r < '0' || r > '9' {
				port = 0
				break
			}
			port = port*10 + int(r-'0')
		}
		if port > 0 {
			ports[port] = true
		}
	}
	return ports, true
}
