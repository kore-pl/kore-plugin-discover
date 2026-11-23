package probe

import (
	"context"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 本文件实现「不依赖客户端二进制」的服务指纹：直接对本机服务端口做最小协议握手。
//
// 约束：
//   - 只用标准库 net（不引入任何第三方协议库）；
//   - 只连 127.0.0.1，不扫网段（discover 只发现本机）；
//   - 读有上限、写固定载荷、连接与读都有 deadline（复用 CommandTimeout）；
//   - 握手失败一律返回空串 + nil，由 detect 降级为 client/unknown，不让整表报错。

// maxFingerprintBytes 是单次握手响应读取上限（版本串都很短，防御性截断）。
const maxFingerprintBytes = 64 << 10

// dialProbe 连接本机服务端口，带连接超时与读 deadline。
func (s *hostSource) dialProbe(ctx context.Context, port int) (net.Conn, error) {
	timeout := s.opts.CommandTimeout
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(probeHost, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)
	return conn, nil
}

// textHandshake 发送固定载荷并读取响应（有上限）；失败返回 nil。
func (s *hostSource) textHandshake(ctx context.Context, port int, request string) []byte {
	conn, err := s.dialProbe(ctx, port)
	if err != nil {
		return nil
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, request); err != nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(conn, maxFingerprintBytes))
	if err != nil && len(data) == 0 {
		return nil
	}
	return data
}

// memcachedServerVersion 用 memcached 文本协议 `version\r\n` 读取服务端版本
// （响应形如 `VERSION 1.6.21\r\n`）。这是真实服务端版本，不是客户端版本。
func (s *hostSource) memcachedServerVersion(ctx context.Context, spec serviceSpec) (string, error) {
	data := s.textHandshake(ctx, spec.Port, "version\r\n")
	if len(data) == 0 {
		return "", nil
	}
	return parseMemcachedVersion(string(data)), nil
}

// parseMemcachedVersion 从 memcached 响应里取 VERSION 行。
func parseMemcachedVersion(output string) string {
	for _, line := range splitLines(output) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(line), "VERSION ") {
			continue
		}
		return parseVersion(strings.TrimSpace(line[len("VERSION "):]))
	}
	return ""
}

// elasticsearchServerVersion 用最小 HTTP/1.0 GET 读 Elasticsearch 根端点
// （响应 JSON 里 version.number 即服务端版本；http 端口通常无需认证）。
func (s *hostSource) elasticsearchServerVersion(ctx context.Context, spec serviceSpec) (string, error) {
	data := s.textHandshake(ctx, spec.Port,
		"GET / HTTP/1.0\r\nHost: "+probeHost+"\r\nAccept: application/json\r\nConnection: close\r\n\r\n")
	if len(data) == 0 || !strings.HasPrefix(string(data), "HTTP/") {
		return "", nil
	}
	return parseElasticsearchVersion(string(data)), nil
}

// elasticsearchVersionPattern 匹配根端点 JSON 里的 version.number 字段。
var elasticsearchVersionPattern = regexp.MustCompile(`"number"\s*:\s*"([^"]+)"`)

// parseElasticsearchVersion 从 HTTP 响应（头 + JSON 体）里取 version.number。
// 只认 `"number"` 键，避免把 cluster_uuid 等字段误当版本。
func parseElasticsearchVersion(raw string) string {
	match := elasticsearchVersionPattern.FindStringSubmatch(raw)
	if len(match) != 2 {
		return ""
	}
	return parseVersion(match[1])
}
