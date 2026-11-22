//go:build linux

package e2e

import (
	"encoding/json"
	"testing"
)

// serviceItem 是 discover.services 的条目子集。
type serviceItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Found       bool   `json:"found"`
	Bin         string `json:"bin"`
	Version     string `json:"version"`
	ConfigDir   string `json:"config_dir"`
	Running     bool   `json:"running"`
	Port        int    `json:"port"`
	Source      string `json:"source"`
	InstallHint string `json:"install_hint"`
}

// portItem 是 discover.ports 的条目子集。
type portItem struct {
	Port      int     `json:"port"`
	Addr      string  `json:"addr"`
	Family    string  `json:"family"`
	Listening bool    `json:"listening"`
	Process   *string `json:"process"`
}

// suggestionItem 是 discover.summary.suggestions 的条目子集。
type suggestionItem struct {
	Plugin string `json:"plugin"`
	Reason string `json:"reason"`
}

// summaryItem 是 discover.summary 的结果子集。
type summaryItem struct {
	Ready       bool             `json:"ready"`
	WebServer   string           `json:"web_server"`
	DB          string           `json:"db"`
	Conclusion  string           `json:"conclusion"`
	Suggestions []suggestionItem `json:"suggestions"`
}

// parseServices 解析服务清单。
func parseServices(t *testing.T, raw string) []serviceItem {
	t.Helper()
	var services []serviceItem
	if err := json.Unmarshal([]byte(raw), &services); err != nil {
		t.Fatalf("解析 discover.services 失败: %v (%s)", err, raw)
	}
	return services
}

// parsePorts 解析端口清单。
func parsePorts(t *testing.T, raw string) []portItem {
	t.Helper()
	var ports []portItem
	if err := json.Unmarshal([]byte(raw), &ports); err != nil {
		t.Fatalf("解析 discover.ports 失败: %v (%s)", err, raw)
	}
	return ports
}

// parseSummary 解析汇总结果。
func parseSummary(t *testing.T, raw string) summaryItem {
	t.Helper()
	var summary summaryItem
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		t.Fatalf("解析 discover.summary 失败: %v (%s)", err, raw)
	}
	return summary
}

// findService 在服务清单里按 id 查找。
func findService(services []serviceItem, id string) (serviceItem, bool) {
	for _, service := range services {
		if service.ID == id {
			return service, true
		}
	}
	return serviceItem{}, false
}
