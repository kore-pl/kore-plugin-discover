package probe

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// fakeSource 是可控执行面：记录调用次数，返回预置数据。
type fakeSource struct {
	mu            sync.Mutex
	servicesCalls int
	portsCalls    int
	services      []Service
	ports         []ListeningPort
}

// Services 返回预置服务清单。
func (f *fakeSource) Services(context.Context) ([]Service, error) {
	f.mu.Lock()
	f.servicesCalls++
	f.mu.Unlock()
	return f.services, nil
}

// Listeners 返回预置端口清单。
func (f *fakeSource) Listeners(context.Context) ([]ListeningPort, error) {
	f.mu.Lock()
	f.portsCalls++
	f.mu.Unlock()
	return f.ports, nil
}

// calls 返回服务/端口的探测次数。
func (f *fakeSource) calls() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.servicesCalls, f.portsCalls
}

// blockingSource 模拟执行面卡死：等待调用方整体预算到期后返回超时错误。
type blockingSource struct{}

// Services 阻塞到 ctx 结束。
func (blockingSource) Services(ctx context.Context) ([]Service, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// Listeners 阻塞到 ctx 结束。
func (blockingSource) Listeners(ctx context.Context) ([]ListeningPort, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// newFakeRuntime 构造注入假执行面的 Runtime。
func newFakeRuntime(src Source) *Runtime {
	return NewRuntime(Options{GOOS: "linux"}, src)
}

// TestCallServicesShape 验证 discover.services 输出数组形状与字段。
func TestCallServicesShape(t *testing.T) {
	src := &fakeSource{services: []Service{
		{ID: "nginx", Name: "Nginx", Found: true, Bin: "/usr/sbin/nginx", Version: "1.24.0", Running: true, Port: 80, Source: "which"},
		{ID: "docker", Name: "Docker", Found: false, Port: 2375, InstallHint: "apt install docker.io"},
	}}
	raw, perr := newFakeRuntime(src).Call("discover.services", nil)
	if perr != nil {
		t.Fatalf("discover.services 失败: %+v", perr)
	}
	var services []Service
	if err := json.Unmarshal(raw, &services); err != nil {
		t.Fatalf("结果不是数组: %v (%s)", err, raw)
	}
	if len(services) != 2 || services[0].ID != "nginx" || !services[0].Found {
		t.Fatalf("服务清单不符: %+v", services)
	}
	if services[1].Found || services[1].InstallHint == "" {
		t.Fatalf("未装项应带 install_hint: %+v", services[1])
	}
}

// TestCallPortsAndSummary 验证端口数组与汇总对象。
func TestCallPortsAndSummary(t *testing.T) {
	process := "nginx"
	src := &fakeSource{
		services: []Service{{ID: "nginx", Name: "Nginx", Found: true, Running: true, Port: 80}},
		ports:    []ListeningPort{{Port: 80, Addr: "0.0.0.0", Family: "ipv4", Listening: true, Process: &process}},
	}
	runtime := newFakeRuntime(src)
	portsRaw, perr := runtime.Call("discover.ports", nil)
	if perr != nil {
		t.Fatalf("discover.ports 失败: %+v", perr)
	}
	var ports []ListeningPort
	if err := json.Unmarshal(portsRaw, &ports); err != nil {
		t.Fatalf("端口结果不是数组: %v", err)
	}
	if len(ports) != 1 || ports[0].Port != 80 || ports[0].Process == nil || *ports[0].Process != "nginx" {
		t.Fatalf("端口清单不符: %+v", ports)
	}

	summaryRaw, perr := runtime.Call("discover.summary", nil)
	if perr != nil {
		t.Fatalf("discover.summary 失败: %+v", perr)
	}
	var summary Summary
	if err := json.Unmarshal(summaryRaw, &summary); err != nil {
		t.Fatalf("汇总结果不是对象: %v", err)
	}
	if !summary.Ready || summary.WebServer != "nginx" || len(summary.Suggestions) == 0 {
		t.Fatalf("汇总字段不符: %+v", summary)
	}
}

// TestCacheTTL 验证 30s 缓存：重复调用不打执行面，refresh 与过期会重新探测。
func TestCacheTTL(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	src := &fakeSource{services: []Service{{ID: "nginx", Name: "Nginx"}}}
	runtime := newFakeRuntime(src)
	runtime.opts.Now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if _, perr := runtime.Call("discover.services", nil); perr != nil {
			t.Fatalf("第 %d 次调用失败: %+v", i, perr)
		}
	}
	if calls, _ := src.calls(); calls != 1 {
		t.Fatalf("30s 内重复调用应命中缓存，实际探测 %d 次", calls)
	}
	if _, perr := runtime.Call("discover.services", json.RawMessage(`{"refresh":true}`)); perr != nil {
		t.Fatalf("refresh 调用失败: %+v", perr)
	}
	if calls, _ := src.calls(); calls != 2 {
		t.Fatalf("refresh 应强制重探，实际探测 %d 次", calls)
	}
	now = now.Add(31 * time.Second)
	if _, perr := runtime.Call("discover.services", nil); perr != nil {
		t.Fatalf("过期后调用失败: %+v", perr)
	}
	if calls, _ := src.calls(); calls != 3 {
		t.Fatalf("缓存过期后应重探，实际探测 %d 次", calls)
	}
}

// TestOverallTimeout 验证整次方法调用受 15s 预算约束（测试注入 30ms）。
func TestOverallTimeout(t *testing.T) {
	runtime := NewRuntime(Options{GOOS: "linux", OverallTimeout: 30 * time.Millisecond}, blockingSource{})
	start := time.Now()
	_, perr := runtime.Call("discover.services", nil)
	if perr == nil {
		t.Fatal("执行面阻塞时应返回错误")
	}
	if perr.Code != pluginapi.E_UPSTREAM || !strings.Contains(perr.Message, "超时") {
		t.Fatalf("错误码/文案不符: %+v", perr)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("整体预算未生效：耗时 %s", elapsed)
	}
}

// TestCallUnsupported 非 Linux 必须显式 E_UNSUPPORTED，不静默返回空清单。
func TestCallUnsupported(t *testing.T) {
	runtime := NewRuntime(Options{GOOS: "darwin"}, &fakeSource{})
	for _, method := range []string{"discover.services", "discover.ports", "discover.summary"} {
		_, perr := runtime.Call(method, nil)
		if perr == nil || perr.Code != pluginapi.E_UNSUPPORTED {
			t.Fatalf("%s 在 darwin 上应返回 E_UNSUPPORTED，实际 %+v", method, perr)
		}
	}
}

// TestCallRejectsNonObjectParams 参数必须是 JSON 对象。
func TestCallRejectsNonObjectParams(t *testing.T) {
	_, perr := newFakeRuntime(&fakeSource{}).Call("discover.services", json.RawMessage(`[]`))
	if perr == nil || perr.Code != pluginapi.E_INVALID {
		t.Fatalf("非法参数应返回 E_INVALID，实际 %+v", perr)
	}
}

// TestCallUnknownMethod 未知方法返回 E_NOTFOUND（防御性入口，正常由 dispatch 归一）。
func TestCallUnknownMethod(t *testing.T) {
	_, perr := newFakeRuntime(&fakeSource{}).Call("discover.bogus", nil)
	if perr == nil || perr.Code != pluginapi.E_NOTFOUND {
		t.Fatalf("未知方法应返回 E_NOTFOUND，实际 %+v", perr)
	}
}
