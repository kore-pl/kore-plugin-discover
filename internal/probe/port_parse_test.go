package probe

import (
	"os"
	"testing"
)

// TestParseHexIPv4 固化 /proc/net/tcp 的小端十六进制换算：
// 0100007F:1F90 → 127.0.0.1:8080。
func TestParseHexIPv4(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0100007F", "127.0.0.1"},
		{"00000000", "0.0.0.0"},
		{"0100A8C0", "192.168.0.1"},
		{"0000FFFF", "255.255.0.0"},
	}
	for _, tc := range cases {
		got, err := parseHexIPv4(tc.in)
		if err != nil {
			t.Fatalf("parseHexIPv4(%q) 失败: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("parseHexIPv4(%q)=%q，期望 %q", tc.in, got, tc.want)
		}
	}
}

// TestParseHexIPv6 覆盖 ::、::1 与 IPv4-mapped 地址。
func TestParseHexIPv6(t *testing.T) {
	cases := []struct{ in, want string }{
		{"00000000000000000000000000000000", "::"},
		{"00000000000000000000000001000000", "::1"},
		{"0000000000000000FFFF00000100007F", "::ffff:127.0.0.1"},
	}
	for _, tc := range cases {
		got, err := parseHexIPv6(tc.in)
		if err != nil {
			t.Fatalf("parseHexIPv6(%q) 失败: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("parseHexIPv6(%q)=%q，期望 %q", tc.in, got, tc.want)
		}
	}
}

// TestParseTCPFixture 用 fixture 文本解析真实 /proc/net/tcp 布局，验证端口/地址/族/inode，
// 并确认非 LISTEN 连接与表头被跳过。
func TestParseTCPFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/proc_net_tcp.txt")
	if err != nil {
		t.Fatalf("读取 fixture 失败: %v", err)
	}
	listeners := parseTCPFile(data)
	if len(listeners) != 2 {
		t.Fatalf("解析到 %d 条监听，期望 2 条: %+v", len(listeners), listeners)
	}
	first := listeners[0]
	if first.Port != 8080 || first.Addr != "127.0.0.1" || first.Family != "ipv4" {
		t.Fatalf("首条监听=%+v，期望 127.0.0.1:8080 ipv4", first)
	}
	if first.Inode != 20831 {
		t.Fatalf("首条 inode=%d，期望 20831", first.Inode)
	}
	second := listeners[1]
	if second.Port != 22 || second.Addr != "0.0.0.0" {
		t.Fatalf("第二条监听=%+v，期望 0.0.0.0:22", second)
	}
}

// TestParseTCP6Fixture 验证 tcp6 的 32 位小端字解析。
func TestParseTCP6Fixture(t *testing.T) {
	data, err := os.ReadFile("testdata/proc_net_tcp6.txt")
	if err != nil {
		t.Fatalf("读取 tcp6 fixture 失败: %v", err)
	}
	listeners := parseTCPFile(data)
	if len(listeners) != 3 {
		t.Fatalf("解析到 %d 条监听，期望 3 条: %+v", len(listeners), listeners)
	}
	want := []struct {
		addr string
		port int
	}{{"::", 80}, {"::1", 8081}, {"::ffff:127.0.0.1", 443}}
	for i, item := range want {
		if listeners[i].Addr != item.addr || listeners[i].Port != item.port {
			t.Fatalf("第 %d 条监听=%+v，期望 %s:%d", i, listeners[i], item.addr, item.port)
		}
		if listeners[i].Family != "ipv6" {
			t.Fatalf("第 %d 条 family=%q，期望 ipv6", i, listeners[i].Family)
		}
	}
}

// TestParseTCPLineRejectsBadInput 确认格式异常与非监听行被跳过而不是 panic。
func TestParseTCPLineRejectsBadInput(t *testing.T) {
	cases := []string{
		"",
		"  sl  local_address rem_address   st",
		"0: 0100007F:1F90 00000000:0000 01 00000000:00000000 00:00000000 00000000 1000 0 1",
		"0: ZZZZ:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000 1000 0 1",
		"0: 0100007F 00000000:0000 0A 00000000:00000000 00:00000000 00000000 1000 0 1",
	}
	for _, line := range cases {
		if listener, ok := parseTCPLine(line); ok {
			t.Fatalf("异常行不应解析成功: %q → %+v", line, listener)
		}
	}
}
