//go:build !linux

package probe

import "errors"

// errPlatformUnsupported 是非 Linux 平台的显式降级错误：discover 只有 Linux 真实实现。
var errPlatformUnsupported = errors.New("discover 插件只在 Linux 读取 /proc/net")

// readListeners 在非 Linux 上显式失败，不静默返回空清单。
func readListeners(string) ([]ListeningPort, error) { return nil, errPlatformUnsupported }
