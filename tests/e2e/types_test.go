//go:build linux

package e2e

import (
	"encoding/json"
	"testing"
)

// serviceItem 是 discover.services 的条目子集。
type serviceItem struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Found         bool   `json:"found"`
	Bin           string `json:"bin"`
	ClientVersion string `json:"client_version"`
	ServerVersion string `json:"server_version"`
	VersionSource string `json:"version_source"`
	VersionHint   string `json:"version_hint"`
	ConfigDir     string `json:"config_dir"`
	Running       bool   `json:"running"`
	Port          int    `json:"port"`
	Source        string `json:"source"`
	InstallHint   string `json:"install_hint"`
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

// credentialRequirementItem 是 discover.config.get 的单条凭据声明子集。
type credentialRequirementItem struct {
	ID       string   `json:"id"`
	EnvVar   string   `json:"env_var"`
	Services []string `json:"services"`
	Purpose  string   `json:"purpose"`
	Required bool     `json:"required"`
	Status   string   `json:"status"`
	FillHint string   `json:"fill_hint"`
}

// configGetItem 是 discover.config.get 的结果子集。
type configGetItem struct {
	Plugin                 string                      `json:"plugin"`
	Persisted              bool                        `json:"persisted"`
	CredentialRequirements []credentialRequirementItem `json:"credential_requirements"`
	Transport              string                      `json:"transport"`
	Hint                   string                      `json:"hint"`
}

// parseConfigGet 解析凭据声明结果。
func parseConfigGet(t *testing.T, raw string) configGetItem {
	t.Helper()
	var result configGetItem
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("解析 discover.config.get 失败: %v (%s)", err, raw)
	}
	return result
}
