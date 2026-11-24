//go:build linux

// 边界类别：资源耗尽（资源耗尽 / exhaust / ulimit / fd 耗尽）。
// 断言执行面：用真实 `ulimit -n` 在子进程耗尽文件描述符后再探测，
// 必须显式降级（services 保留清单、ports 返回 E_INTERNAL），不 panic、不挂死。
package boundary_test

import (
	"encoding/json"
	"errors"
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
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("定位测试二进制失败: %v", err)
	}
	root := newFixtureRoot(t)

	// 子进程必须与父进程使用同一个（可能带 -race 的）测试二进制。
	// 不再用 `ulimit -n 128` 设绝对预算：写死数字会随父进程继承的 fd 漂移
	// （继承多 → 预算不够用/loader 就 EMFILE；继承少 → 预算耗不尽），
	// 预算改由 helper 按自身 fd 表计算，见 dropInheritedFDs。
	command := exec.Command(executable, "-test.run", "^TestHelperProcessFDExhaustion$", "-test.v")
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
	match := regexp.MustCompile(`held_fds=(\d+) limit=(\d+) budget=(\d+)`).FindStringSubmatch(text)
	if match == nil {
		t.Fatalf("fd 耗尽哨兵缺少计数信息:\n%s", text)
	}
	held, _ := strconv.Atoi(match[1])
	limit, _ := strconv.Atoi(match[2])
	budget, _ := strconv.Atoi(match[3])
	if held <= 0 {
		t.Fatalf("未真实耗尽 fd（held_fds=%d）", held)
	}
	// 回读值必须等于 helper 算出的相对预算：不相等说明限额没生效，测到的是假路径。
	if limit != budget {
		t.Fatalf("RLIMIT_NOFILE 回读=%d，期望预算 %d（预算未生效）", limit, budget)
	}
}

// dropInheritedFDs 关闭从外层 shell / make 链继承来的 fd（fd ≥ 3 且未置 FD_CLOEXEC）。
//
// 预算按「自身已开 fd + headroom」计算，继承 fd 虽已计入总数，却会挤占 Go runtime /
// race detector 可能懒创建的余量；外层嵌套越深（make check → sh → make → go test），
// 继承 fd 越多，余量越不可预测。关掉之后 fd 表只由本进程决定，预算与宿主继承 fd、
// shell 层数彻底解耦。Go 自己开的 fd 一律带 FD_CLOEXEC，不会被误关。
func dropInheritedFDs(t *testing.T) {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("读取自身 fd 表失败: %v", err)
	}
	dropped := 0
	for _, ent := range ents {
		fd, convErr := strconv.Atoi(ent.Name())
		if convErr != nil || fd < 3 {
			continue
		}
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
		if errno != 0 || flags&syscall.FD_CLOEXEC != 0 {
			continue
		}
		if closeErr := syscall.Close(fd); closeErr == nil {
			dropped++
		}
	}
	t.Logf("关闭继承 fd %d 个（预算只按本进程 fd 表计算）", dropped)
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

	// 预算相对自身已开 fd：不写死绝对数（旧版 `ulimit -n 128` 的坑）。
	// 先清掉外层继承的 fd，再以「当前 fd 数 + 余量」为预算，最后回读内核账本确认生效。
	dropInheritedFDs(t)
	var orig syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &orig); err != nil {
		t.Fatalf("读取 RLIMIT_NOFILE 失败: %v", err)
	}
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("读取自身 fd 表失败: %v", err)
	}
	const headroom = 8
	budget := uint64(len(ents)) + headroom
	if budget > orig.Max {
		t.Fatalf("预算 %d 超过外层硬限 %d：环境把 RLIMIT_NOFILE 压得过低", budget, orig.Max)
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{Cur: budget, Max: orig.Max}); err != nil {
		t.Fatalf("收紧 RLIMIT_NOFILE 到 %d 失败: %v", budget, err)
	}
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatalf("读取 RLIMIT_NOFILE 失败: %v", err)
	}
	if limit.Cur != budget {
		t.Fatalf("RLIMIT_NOFILE 回读=%d，期望 %d（预算未生效）", limit.Cur, budget)
	}
	// 正控：必须真的打开到 EMFILE 才算耗尽，否则测到的是「预算够用」的假路径。
	held := make([]*os.File, 0, 64)
	for index := 0; index < 1<<16; index++ {
		file, openErr := os.Open(os.DevNull)
		if openErr != nil {
			if !errors.Is(openErr, syscall.EMFILE) {
				t.Fatalf("耗尽 fd 时 open 失败原因不是 EMFILE: %v", openErr)
			}
			break
		}
		held = append(held, file)
	}
	if len(held) == 0 {
		t.Fatal("预算未生效：没有任何空位被耗尽")
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

	fmt.Printf("BOUNDARY_FD_LIMIT_OK held_fds=%d limit=%d budget=%d\n", len(held), limit.Cur, budget)
	for _, file := range held {
		_ = file.Close()
	}
}
