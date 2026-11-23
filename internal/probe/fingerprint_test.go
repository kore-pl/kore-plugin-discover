package probe

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// startFakeTCPServer 起一个只接受一次连接的本机 TCP 服务，返回端口。
// handler 拿到连接后自行写响应；listener 在测试结束时关闭。
func startFakeTCPServer(t *testing.T, handler func(conn net.Conn)) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听本机端口失败: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		handler(conn)
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

// newFingerprintSource 构造指向空根、无 PATH 的执行面（指纹探测只用 TCP）。
func newFingerprintSource(t *testing.T) *hostSource {
	t.Helper()
	return newFakeSource(t, t.TempDir(), []string{}, func(opts *Options) {
		opts.CommandTimeout = 2 * time.Second
	})
}

// TestMemcachedHandshake 校验 memcached 文本协议 `version` 握手取到真实服务端版本。
func TestMemcachedHandshake(t *testing.T) {
	source := newFingerprintSource(t)
	var gotRequest string
	port := startFakeTCPServer(t, func(conn net.Conn) {
		buf := make([]byte, 64)
		n, _ := conn.Read(buf)
		gotRequest = string(buf[:n])
		_, _ = conn.Write([]byte("VERSION 1.6.21\r\n"))
	})
	version, err := source.memcachedServerVersion(context.Background(), serviceSpec{Port: port})
	if err != nil {
		t.Fatalf("memcached 握手失败: %v", err)
	}
	if version != "1.6.21" {
		t.Fatalf("memcached server_version=%q，期望 1.6.21", version)
	}
	if !strings.Contains(gotRequest, "version") {
		t.Fatalf("握手载荷不符: %q", gotRequest)
	}
}

// TestMemcachedHandshakeUnreachable 端口不可达时返回空串 + nil（降级，不影响整表）。
func TestMemcachedHandshakeUnreachable(t *testing.T) {
	source := newFingerprintSource(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close() // 立即关闭：端口不再监听
	version, err := source.memcachedServerVersion(context.Background(), serviceSpec{Port: port})
	if err != nil || version != "" {
		t.Fatalf("不可达端口应降级为空串: version=%q err=%v", version, err)
	}
}

// TestElasticsearchHandshake 校验最小 HTTP/1.0 GET 从根端点 JSON 取 version.number。
func TestElasticsearchHandshake(t *testing.T) {
	source := newFingerprintSource(t)
	body := `{"name":"node-1","cluster_name":"kore","version":{"number":"8.13.0","build_flavor":"default"}}`
	port := startFakeTCPServer(t, func(conn net.Conn) {
		buf := make([]byte, 512)
		_, _ = conn.Read(buf)
		response := "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " +
			itoa(len(body)) + "\r\n\r\n" + body
		_, _ = conn.Write([]byte(response))
	})
	version, err := source.elasticsearchServerVersion(context.Background(), serviceSpec{Port: port})
	if err != nil {
		t.Fatalf("elasticsearch 握手失败: %v", err)
	}
	if version != "8.13.0" {
		t.Fatalf("elasticsearch server_version=%q，期望 8.13.0", version)
	}
}

// TestElasticsearchNonHTTP 非 HTTP 响应（例如普通 TCP 服务）不编造版本。
func TestElasticsearchNonHTTP(t *testing.T) {
	source := newFingerprintSource(t)
	port := startFakeTCPServer(t, func(conn net.Conn) {
		_, _ = conn.Write([]byte("not-http\r\n"))
	})
	version, err := source.elasticsearchServerVersion(context.Background(), serviceSpec{Port: port})
	if err != nil || version != "" {
		t.Fatalf("非 HTTP 响应应返回空串: version=%q err=%v", version, err)
	}
}

// TestFingerprintCatalogDispatch 校验 catalog 声明的 memcached / elasticsearch
// 确实走对应的协议指纹分支（把 catalog 端口临时替换为测试监听端口）。
func TestFingerprintCatalogDispatch(t *testing.T) {
	source := newFingerprintSource(t)
	memcachedPort := startFakeTCPServer(t, func(conn net.Conn) {
		_, _ = conn.Write([]byte("VERSION 1.6.21\r\n"))
	})
	elasticPort := startFakeTCPServer(t, func(conn net.Conn) {
		body := `{"version":{"number":"8.13.0"}}`
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: " + itoa(len(body)) + "\r\n\r\n" + body))
	})
	cases := []struct {
		id   string
		port int
		want string
	}{
		{"memcached", memcachedPort, "1.6.21"},
		{"elasticsearch", elasticPort, "8.13.0"},
	}
	for _, tc := range cases {
		spec, ok := catalogSpec(tc.id)
		if !ok {
			t.Fatalf("catalog 缺少 %s", tc.id)
		}
		spec.Port = tc.port
		got, err := source.probeServerVersion(context.Background(), spec, Service{ID: tc.id})
		if err != nil || got != tc.want {
			t.Fatalf("%s 指纹探测=%q err=%v，期望 %q", tc.id, got, err, tc.want)
		}
	}
}

// catalogSpec 按 id 查 catalog 声明。
func catalogSpec(id string) (serviceSpec, bool) {
	for _, spec := range catalog {
		if spec.ID == id {
			return spec, true
		}
	}
	return serviceSpec{}, false
}

// itoa 是测试用整数转字符串（避免再引入 strconv 与业务代码命名冲突）。
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
