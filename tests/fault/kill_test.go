//go:build linux

package fault

import (
	"encoding/json"
	"testing"

	"github.com/pengtang1024/kore-plugin-discover/internal/probe"
)

// TestKillFault 注入 kill -9（进程被信号终止），期望三个方法返回 E_UPSTREAM。
func TestKillFault(t *testing.T) {
	assertFault(t, probe.FaultKill, "E_UPSTREAM", "信号 9")
}

// TestKillFaultContract 单独验证 kill 故障的错误体结构完整。
func TestKillFaultContract(t *testing.T) {
	runtime := newRuntime(faultSource{kind: probe.FaultKill})
	_, perr := runtime.Call("discover.services", json.RawMessage(`{}`))
	verifyErrorContract(t, perr)
}
