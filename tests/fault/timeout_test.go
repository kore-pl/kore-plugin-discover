//go:build linux

package fault

import (
	"testing"

	"github.com/pengtang1024/kore-plugin-discover/internal/probe"
)

// TestTimeoutFault 注入调用超时，期望三个方法返回 E_UPSTREAM。
func TestTimeoutFault(t *testing.T) {
	assertFault(t, probe.FaultTimeout, "E_UPSTREAM", "超时")
}

// TestTimeoutSanity 无故障时三个方法正常返回，防止超时测试假通过。
func TestTimeoutSanity(t *testing.T) {
	sanityNoFault(t)
}
