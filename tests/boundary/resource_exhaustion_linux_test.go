//go:build linux

// 边界类别：资源耗尽（资源耗尽 / exhaust / ulimit / fd 耗尽）。
// 断言执行面：用真实 `ulimit -n` 在子进程耗尽文件描述符后再探测，
// 必须显式降级（services 保留清单、ports 返回 E_INTERNAL），不 panic、不挂死。
package boundary_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/pengtang1024/kore-plugin-discover/internal/probe"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// TestResourceExhaustion_FDLimitRealInjection 在子进程里真实注入 `ulimit -n 128`，
// 耗尽 fd 后调用探测方法，断言降级契约；父进程校验子进程退出码与哨兵输出。
func TestResourceExhaustion_FDLimitRealInjection(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("无 bash，无法注入 ulimit")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("定位测试二进制失败: %v", err)
	}
	root := newFixtureRoot(t)

	// 子进程必须与父进程使用同一个（可能带 -race 的）测试二进制。
	script := `set -eu; ulimit -n 128; exec "$1" -test.run '^TestHelperProcessFDExhaustion$' -test.v`
	command := exec.Command("bash", "-c", script, "boundary", executable)
	command.Env = append(os.Environ(),
		"BOUNDARY_FD_HELPER=1",
		"BOUNDARY_FIXTURE_ROOT="+root,
	)
	output, runErr := command.CombinedOutput()
	if runErr != nil {
		t.Fatalf("fd 耗尽子进程失败: %v\n%s", runErr, output)
	}
	text := string(output)
	if !strings.Contains(text, "BOUNDARY_FD_LIMIT_OK") {
		t.Fatalf("fd 耗尽子进程未给出降级成功哨兵:\n%s", text)
	}
	match := regexp.MustCompile(`held_fds=(\d+) limit=(\d+)`).FindStringSubmatch(text)
	if match == nil {
		t.Fatalf("fd 耗尽哨兵缺少计数信息:\n%s", text)
	}
	held, _ := strconv.Atoi(match[1])
	limit, _ := strconv.Atoi(match[2])
	if held <= 0 {
		t.Fatalf("未真实耗尽 fd（held_fds=%d）", held)
	}
	if limit != 128 {
		t.Fatalf("ulimit 未生效: limit=%d，期望 128", limit)
	}
}

// TestHelperProcessFDExhaustion 只在 ulimit 子进程中运行：先占满 fd，再验证探测降级。
func TestHelperProcessFDExhaustion(t *testing.T) {
	if os.Getenv("BOUNDARY_FD_HELPER") != "1" {
		t.Skip("仅作为 ulimit 子进程 helper 运行")
	}
	root := os.Getenv("BOUNDARY_FIXTURE_ROOT")
	if root == "" {
		t.Fatal("缺少 BOUNDARY_FIXTURE_ROOT")
	}

	// 真实耗尽 fd：一直打开 /dev/null 直到 EMFILE，并保持持有。
	held := make([]*os.File, 0, 256)
	for index := 0; index < 1<<16; index++ {
		file, openErr := os.Open(os.DevNull)
		if openErr != nil {
			break
		}
		held = append(held, file)
	}
	if extra, openErr := os.Open(os.DevNull); openErr == nil {
		_ = extra.Close()
		t.Fatal("未能耗尽文件描述符，ulimit 注入无效")
	}
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatalf("读取 RLIMIT_NOFILE 失败: %v", err)
	}

	rt := probe.NewRuntime(probe.Options{GOOS: "linux", RootFS: root, PathDirs: []string{}}, nil)
	raw, perr := rt.Call("discover.services", nil)
	if perr != nil {
		t.Fatalf("fd 耗尽下 services 必须降级返回清单，实际报错: %+v", perr)
	}
	var services []probe.Service
	if err := json.Unmarshal(raw, &services); err != nil {
		t.Fatalf("services 输出不是数组: %v (%s)", err, raw)
	}
	if len(services) < 20 {
		t.Fatalf("fd 耗尽下服务清单被截断: %d 条", len(services))
	}
	for _, service := range services {
		if service.Found {
			t.Fatalf("fd 耗尽下不应判定 found=true: %+v", service)
		}
	}

	_, portErr := rt.Call("discover.ports", nil)
	if portErr == nil {
		t.Fatal("fd 耗尽下 discover.ports 必须显式报错，不能静默返回空结果")
	}
	if portErr.Code != pluginapi.E_INTERNAL {
		t.Fatalf("fd 耗尽错误码 = %s，期望 %s", portErr.Code, pluginapi.E_INTERNAL)
	}

	fmt.Printf("BOUNDARY_FD_LIMIT_OK held_fds=%d limit=%d\n", len(held), limit.Cur)
	for _, file := range held {
		_ = file.Close()
	}
}
