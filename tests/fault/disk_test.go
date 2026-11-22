//go:build linux

package fault

import (
	"testing"

	"github.com/pengtang1024/kore-plugin-discover/internal/probe"
)

// TestDiskFullFault 注入磁盘满，期望三个方法返回 E_INTERNAL。
func TestDiskFullFault(t *testing.T) {
	assertFault(t, probe.FaultDisk, "E_INTERNAL", "磁盘满")
}
