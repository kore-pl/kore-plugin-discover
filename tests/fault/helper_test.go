//go:build linux

// Package fault 对 discover 插件做四类故障注入（kill / timeout / disk / cgroup）：
// 每个用例把可控执行面替换为始终返回 FaultError 的实现，断言三个方法都把执行面
// 故障映射成结构化协议错误码，而不是 panic 或"空清单=成功"的伪成功。
package fault

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/pengtang1024/kore-plugin-discover/internal/probe"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// faultSource 按注入类别让服务与端口两个执行面都失败。
type faultSource struct {
	kind probe.FaultKind
}

// Services 注入故障。
func (f faultSource) Services(context.Context) ([]probe.Service, error) {
	return nil, probe.NewFaultError(f.kind, "services 执行面故障")
}

// Listeners 注入故障。
func (f faultSource) Listeners(context.Context) ([]probe.ListeningPort, error) {
	return nil, probe.NewFaultError(f.kind, "listeners 执行面故障")
}

// healthySource 是无故障执行面：证明故障断言不是"永远失败"的假通过。
type healthySource struct{}

// Services 返回含 nginx 的服务清单。
func (healthySource) Services(context.Context) ([]probe.Service, error) {
	return []probe.Service{{ID: "nginx", Name: "Nginx", Found: true, Running: true, Port: 80}}, nil
}

// Listeners 返回 80 端口监听。
func (healthySource) Listeners(context.Context) ([]probe.ListeningPort, error) {
	return []probe.ListeningPort{{Port: 80, Addr: "0.0.0.0", Family: "ipv4", Listening: true}}, nil
}

// methods 是故障注入覆盖的三个方法。
var methods = []string{"discover.services", "discover.ports", "discover.summary"}

// newRuntime 构造指向注入执行面的 Runtime。
func newRuntime(src probe.Source) *probe.Runtime {
	return probe.NewRuntime(probe.Options{GOOS: "linux"}, src)
}

// assertFault 对三个方法注入同一类故障，断言 nil 结果 + 预期错误码与类别关键词。
func assertFault(t *testing.T, kind probe.FaultKind, wantCode, wantHint string) {
	t.Helper()
	runtime := newRuntime(faultSource{kind: kind})
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			result, perr := runtime.Call(method, json.RawMessage(`{}`))
			if perr == nil {
				t.Fatalf("%s 注入 %s 后未返回错误（结果=%s）", method, kind, result)
			}
			if result != nil {
				t.Fatalf("%s 出错时不应带结果: %s", method, result)
			}
			if perr.Code != wantCode {
				t.Fatalf("%s 错误码=%q，期望 %q（message=%q）", method, perr.Code, wantCode, perr.Message)
			}
			if !contains(perr.Message, wantHint) {
				t.Fatalf("%s 错误信息 %q 未含类别关键词 %q", method, perr.Message, wantHint)
			}
		})
	}
}

// sanityNoFault 验证无故障时三个方法均正常返回，防止故障测试假通过。
func sanityNoFault(t *testing.T) {
	t.Helper()
	runtime := newRuntime(healthySource{})
	for _, method := range methods {
		result, perr := runtime.Call(method, nil)
		if perr != nil {
			t.Fatalf("无故障时 %s 意外返回错误: %+v", method, perr)
		}
		if len(result) == 0 {
			t.Fatalf("无故障时 %s 返回空结果", method)
		}
	}
}

// contains 是 strings.Contains 的本地封装，避免每个 fault 文件重复 import strings。
func contains(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// verifyErrorContract 断言错误体是标准协议结构（code 与 message 均非空）。
func verifyErrorContract(t *testing.T, perr *pluginapi.Error) {
	t.Helper()
	if perr == nil {
		t.Fatal("错误体为 nil")
	}
	if perr.Code == "" || perr.Message == "" {
		t.Fatalf("错误体字段不完整: %+v", perr)
	}
}
