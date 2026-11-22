package probe

import (
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// rawListener 是 /proc/net/tcp* 单行的解析结果（尚未反查进程名）。
type rawListener struct {
	Port   int
	Addr   string
	Family string
	Inode  uint64
}

// parseHexIPv4 把 /proc/net/tcp 的小端十六进制 IPv4 地址转成点分十进制：
// "0100007F" → "127.0.0.1"。
func parseHexIPv4(value string) (string, error) {
	if len(value) != 8 {
		return "", fmt.Errorf("IPv4 地址长度应为 8，实际 %q", value)
	}
	raw, err := hex.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("IPv4 地址不是合法十六进制 %q: %w", value, err)
	}
	addr := make(net.IP, net.IPv4len)
	for i := 0; i < net.IPv4len; i++ {
		addr[i] = raw[net.IPv4len-1-i]
	}
	return addr.String(), nil
}

// parseHexIPv6 把 /proc/net/tcp6 的地址转成规范文本：地址由 4 个 32 位小端字组成，
// 每个 8 位十六进制组内部字节序反转后拼接。
func parseHexIPv6(value string) (string, error) {
	if len(value) != 32 {
		return "", fmt.Errorf("IPv6 地址长度应为 32，实际 %q", value)
	}
	addr := make(net.IP, 0, net.IPv6len)
	for group := 0; group < 4; group++ {
		raw, err := hex.DecodeString(value[group*8 : (group+1)*8])
		if err != nil {
			return "", fmt.Errorf("IPv6 地址不是合法十六进制 %q: %w", value, err)
		}
		for i := len(raw) - 1; i >= 0; i-- {
			addr = append(addr, raw[i])
		}
	}
	// net.IP.String() 会把 IPv4-mapped 地址折叠成点分 IPv4；这里保留 ::ffff: 前缀，
	// 让调用方在 family=ipv6 时不会误读成纯 IPv4 监听。
	if isIPv4Mapped(addr) {
		return "::ffff:" + net.IP(addr[12:]).String(), nil
	}
	return addr.String(), nil
}

// isIPv4Mapped 判断 16 字节地址是否为 ::ffff:a.b.c.d。
func isIPv4Mapped(addr net.IP) bool {
	if len(addr) != net.IPv6len {
		return false
	}
	for _, b := range addr[:10] {
		if b != 0 {
			return false
		}
	}
	return addr[10] == 0xff && addr[11] == 0xff
}

// parseLocalAddress 解析 "0100007F:1F90" 形式的本地地址，返回地址、端口与协议族。
func parseLocalAddress(field string) (string, int, string, error) {
	hexAddr, hexPort, ok := strings.Cut(field, ":")
	if !ok {
		return "", 0, "", fmt.Errorf("本地地址缺少端口分隔符 %q", field)
	}
	port, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return "", 0, "", fmt.Errorf("端口不是合法十六进制 %q: %w", hexPort, err)
	}
	switch len(hexAddr) {
	case 8:
		addr, addrErr := parseHexIPv4(hexAddr)
		return addr, int(port), "ipv4", addrErr
	case 32:
		addr, addrErr := parseHexIPv6(hexAddr)
		return addr, int(port), "ipv6", addrErr
	default:
		return "", 0, "", fmt.Errorf("本地地址长度非法 %q", hexAddr)
	}
}

// parseTCPLine 解析 /proc/net/tcp 的一行；只保留 LISTEN（状态 0A）条目。
// 表头、格式异常、非监听连接一律跳过，不让单行问题中断整次解析。
func parseTCPLine(line string) (rawListener, bool) {
	fields := strings.Fields(line)
	if len(fields) < 10 {
		return rawListener{}, false
	}
	if !strings.EqualFold(fields[3], "0A") {
		return rawListener{}, false
	}
	addr, port, family, err := parseLocalAddress(fields[1])
	if err != nil {
		return rawListener{}, false
	}
	inode, _ := strconv.ParseUint(fields[9], 10, 64)
	return rawListener{Port: port, Addr: addr, Family: family, Inode: inode}, true
}

// parseTCPFile 解析整份 /proc/net/tcp 文本，只返回监听条目。
func parseTCPFile(data []byte) []rawListener {
	listeners := make([]rawListener, 0, 32)
	for _, line := range strings.Split(string(data), "\n") {
		if listener, ok := parseTCPLine(line); ok {
			listeners = append(listeners, listener)
		}
	}
	return listeners
}

// socketInode 从 "socket:[12345]" 链接目标里取出 inode。
func socketInode(target string) (uint64, bool) {
	const prefix = "socket:["
	if !strings.HasPrefix(target, prefix) || !strings.HasSuffix(target, "]") {
		return 0, false
	}
	inode, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(target, prefix), "]"), 10, 64)
	if err != nil {
		return 0, false
	}
	return inode, true
}
