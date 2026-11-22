//go:build linux

package fault

import (
	"testing"

	"github.com/pengtang1024/kore-plugin-discover/internal/probe"
)

// TestCgroupLimitFault 注入 cgroup 超限，期望三个方法返回 E_UNSUPPORTED。
func TestCgroupLimitFault(t *testing.T) {
	assertFault(t, probe.FaultCgroup, "E_UNSUPPORTED", "cgroup")
}
