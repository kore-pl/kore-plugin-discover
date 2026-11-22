// Package main 是 kore 的 discover 插件：只读探测本机运维服务与监听端口，
// 并给出面向小白的插件安装建议。
//
// 探测是纯只读的：不装包、不改配置、不启停服务；命令一律固定 argv，不接受
// 用户输入拼命令；每个探测命令 3s 超时、整次调用 15s 预算；结果缓存 30s，
// 缓存靠调用时判断过期，不启动任何后台刷新（G-19：零 ticker）。
//
// 协议面（kore.plugin.v1）：连接内核注入的 UDS → kore.hello 握手 → 会话循环读
// request、按 method 分发并回 response；stdout 留给协议，日志一律写 stderr。
package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"runtime"

	"github.com/pengtang1024/kore-plugin-discover/internal/probe"
)

// main 按退出码区分失败：0 优雅退出、1 启动或协议失败。
func main() {
	logger := newLogger(os.Getenv("KORE_LOG_LEVEL"))
	if err := run(logger); err != nil {
		logger.Error("插件退出", "err", err)
		os.Exit(1)
	}
}

// run 依次完成配置、建连、握手与会话循环；收到 kore.shutdown 后返回 nil。
func run(logger *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	discovery := probe.NewRuntime(probe.Options{GOOS: runtime.GOOS}, nil)
	conn, err := net.Dial("unix", cfg.socket)
	if err != nil {
		return fmt.Errorf("连接主壳 socket %s: %w", cfg.socket, err)
	}
	defer func() { _ = conn.Close() }()
	if err := handshake(conn, cfg); err != nil {
		return err
	}
	logger.Info("hello 完成", "plugin_id", cfg.id, "version", cfg.version)
	return serve(conn, cfg, discovery, logger)
}
