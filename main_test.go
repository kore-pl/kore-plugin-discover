package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"github.com/pengtang1024/kore-plugin-discover/internal/probe"
	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// stubSource 是 main 包测试用执行面：返回固定服务与空端口清单。
type stubSource struct{}

// Services 返回一条 nginx 记录。
func (stubSource) Services(context.Context) ([]probe.Service, error) {
	return []probe.Service{{ID: "nginx", Name: "Nginx", Found: true, Running: true, Port: 80}}, nil
}

// Listeners 返回空端口清单。
func (stubSource) Listeners(context.Context) ([]probe.ListeningPort, error) {
	return []probe.ListeningPort{}, nil
}

// testConfig 构造握手与会话循环共用的一致配置（不依赖环境变量）。
func testConfig() config {
	return config{
		id:           "discover",
		version:      "0.1.0",
		requiresKore: ">=0.1.0 <0.2.0",
		capabilities: []string{"system:read", "proc:read"},
		startedAt:    time.Now(),
	}
}

// testLogger 丢弃日志，测试输出只保留断言信息。
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestRuntime 构造注入固定执行面的 Runtime。
func newTestRuntime() *probe.Runtime {
	return probe.NewRuntime(probe.Options{GOOS: "linux"}, stubSource{})
}

// readFrame 读取并解码一帧。
func readFrame(t *testing.T, conn net.Conn) any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	payload, err := pluginapi.ReadFrame(conn, pluginapi.MaxFrameSize)
	if err != nil {
		t.Fatalf("读帧失败: %v", err)
	}
	env, err := pluginapi.DecodeFrame(payload)
	if err != nil {
		t.Fatalf("解码帧失败: %v", err)
	}
	return env
}

// readRequest 断言下一帧是 request。
func readRequest(t *testing.T, conn net.Conn) *pluginapi.Request {
	t.Helper()
	request, ok := readFrame(t, conn).(*pluginapi.Request)
	if !ok {
		t.Fatal("帧类型不是 request")
	}
	return request
}

// writeResponse 写一个成功响应帧。
func writeResponse(t *testing.T, conn net.Conn, id, reqID string, result json.RawMessage) {
	t.Helper()
	if err := pluginapi.WriteJSON(conn, &pluginapi.Response{
		Type: pluginapi.TypeResponse, ID: id, ReqID: reqID, Result: result,
	}); err != nil {
		t.Fatalf("写响应失败: %v", err)
	}
}

// TestHandshake 校验 hello 首帧字段与三步握手。
func TestHandshake(t *testing.T) {
	pluginConn, kernelConn := net.Pipe()
	defer func() { _ = kernelConn.Close() }()
	done := make(chan error, 1)
	cfg := testConfig()
	go func() { done <- handshake(pluginConn, cfg); _ = pluginConn.Close() }()

	request := readRequest(t, kernelConn)
	if request.Method != helloMethod {
		t.Fatalf("首帧方法=%q，期望 %q", request.Method, helloMethod)
	}
	var params helloParams
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatalf("解析 hello 参数失败: %v", err)
	}
	if params.PluginID != "discover" || params.ID != "discover" {
		t.Fatalf("plugin_id/id=%q/%q，期望 discover", params.PluginID, params.ID)
	}
	if params.Protocol != pluginapi.Protocol || params.Version != "0.1.0" {
		t.Fatalf("protocol/version=%q/%q 不符合 §5", params.Protocol, params.Version)
	}
	if params.PID != os.Getpid() {
		t.Fatalf("pid=%d，期望 %d", params.PID, os.Getpid())
	}
	if len(params.Capabilities) != 2 || params.Capabilities[0] != "system:read" || params.Capabilities[1] != "proc:read" {
		t.Fatalf("capabilities 不符: %+v", params.Capabilities)
	}
	result, err := json.Marshal(helloResult{OK: true, Protocol: pluginapi.Protocol})
	if err != nil {
		t.Fatalf("序列化握手响应失败: %v", err)
	}
	writeResponse(t, kernelConn, request.ID, request.ReqID, result)
	if err := <-done; err != nil {
		t.Fatalf("握手返回错误: %v", err)
	}
}

// TestDispatchMethods 裸方法名与全名都能命中，结果形状正确。
func TestDispatchMethods(t *testing.T) {
	for _, method := range []string{"discover.services", "services"} {
		response := dispatchCall(t, method, nil)
		if response.Error != nil {
			t.Fatalf("%s 失败: %+v", method, response.Error)
		}
		if len(response.Result) == 0 || response.Result[0] != '[' {
			t.Fatalf("%s 结果应为数组: %s", method, response.Result)
		}
	}
	for _, method := range []string{"discover.ports", "ports"} {
		response := dispatchCall(t, method, nil)
		if response.Error != nil {
			t.Fatalf("%s 失败: %+v", method, response.Error)
		}
		if len(response.Result) == 0 || response.Result[0] != '[' {
			t.Fatalf("%s 结果应为数组: %s", method, response.Result)
		}
	}
	for _, method := range []string{"discover.summary", "summary"} {
		response := dispatchCall(t, method, nil)
		if response.Error != nil {
			t.Fatalf("%s 失败: %+v", method, response.Error)
		}
		if len(response.Result) == 0 || response.Result[0] != '{' {
			t.Fatalf("%s 结果应为对象: %s", method, response.Result)
		}
	}
	// 0.3.0 新增：凭据声明 + 注入状态的只读视图（裸名/全名都可调）。
	for _, method := range []string{"discover.config.get", "config.get"} {
		response := dispatchCall(t, method, nil)
		if response.Error != nil {
			t.Fatalf("%s 失败: %+v", method, response.Error)
		}
		if len(response.Result) == 0 || response.Result[0] != '{' {
			t.Fatalf("%s 结果应为对象: %s", method, response.Result)
		}
	}
}

// dispatchCall 把方法调用打到 dispatch 并返回响应。
func dispatchCall(t *testing.T, method string, params json.RawMessage) *pluginapi.Response {
	t.Helper()
	request := &pluginapi.Request{
		Type: pluginapi.TypeRequest, ID: "id", ReqID: "r",
		Method: method, Params: params,
	}
	response, shutdown := dispatch(request, testConfig(), newTestRuntime(), testLogger())
	if shutdown {
		t.Fatalf("%s 不应触发 shutdown", method)
	}
	return response
}

// TestServeShutdown 会话循环收到 kore.shutdown 后优雅退出。
func TestServeShutdown(t *testing.T) {
	pluginConn, kernelConn := net.Pipe()
	defer func() { _ = kernelConn.Close() }()
	done := make(chan error, 1)
	go func() {
		done <- serve(pluginConn, testConfig(), newTestRuntime(), testLogger())
		_ = pluginConn.Close()
	}()

	request := &pluginapi.Request{
		Type: pluginapi.TypeRequest, ID: "id-1", ReqID: "r-1", Method: "kore.shutdown",
	}
	if err := pluginapi.WriteJSON(kernelConn, request); err != nil {
		t.Fatalf("写请求失败: %v", err)
	}
	env := readFrame(t, kernelConn)
	response, ok := env.(*pluginapi.Response)
	if !ok || response.ID != "id-1" {
		t.Fatalf("响应帧异常: %+v", env)
	}
	if string(response.Result) != `{"ok":true}` {
		t.Fatalf("shutdown 结果=%s", response.Result)
	}
	if err := <-done; err != nil {
		t.Fatalf("serve 返回错误: %v", err)
	}
}
