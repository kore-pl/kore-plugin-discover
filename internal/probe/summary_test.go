package probe

import (
	"strings"
	"testing"
)

// TestSummaryWithNginx 有 nginx 时建议里必须含 site 插件，且 web_server=nginx、ready=true。
func TestSummaryWithNginx(t *testing.T) {
	services := []Service{
		{ID: "nginx", Name: "Nginx", Found: true, Bin: "/usr/sbin/nginx", Running: true, Port: 80, Source: "which"},
		{ID: "mysql", Name: "MySQL", Found: true, Running: true, Port: 3306, Source: "which"},
		{ID: "docker", Name: "Docker", Found: false, Port: 2375, InstallHint: "hint"},
	}
	ports := []ListeningPort{
		{Port: 3306, Addr: "127.0.0.1", Family: "ipv4", Listening: true},
	}
	summary := BuildSummary(services, ports)
	if !summary.Ready || summary.WebServer != "nginx" || summary.DB != "mysql" {
		t.Fatalf("汇总字段不符: %+v", summary)
	}
	if summary.Conclusion == "" || !strings.Contains(summary.Conclusion, "Nginx") {
		t.Fatalf("结论不完整: %q", summary.Conclusion)
	}
	site, ok := findSuggestion(summary.Suggestions, "site")
	if !ok {
		t.Fatalf("缺少 site 建议: %+v", summary.Suggestions)
	}
	if !strings.Contains(site.Reason, "Nginx") || !strings.Contains(site.Reason, "站点插件") {
		t.Fatalf("site 建议文案不符: %q", site.Reason)
	}
	db, ok := findSuggestion(summary.Suggestions, "db-mysql")
	if !ok {
		t.Fatalf("缺少 db-mysql 建议: %+v", summary.Suggestions)
	}
	if !strings.Contains(db.Reason, "127.0.0.1:3306") {
		t.Fatalf("db-mysql 建议未体现服务端监听: %q", db.Reason)
	}
}

// TestSummaryEmptyHost 空机器 ready=false，且给出可执行的下一步建议。
func TestSummaryEmptyHost(t *testing.T) {
	services := []Service{
		{ID: "nginx", Name: "Nginx", Found: false, InstallHint: "hint"},
		{ID: "mysql", Name: "MySQL", Found: false, InstallHint: "hint"},
	}
	summary := BuildSummary(services, nil)
	if summary.Ready || summary.WebServer != "" || summary.DB != "" {
		t.Fatalf("空机器汇总字段不符: %+v", summary)
	}
	if len(summary.Suggestions) == 0 || summary.Suggestions[0].Plugin != "site" {
		t.Fatalf("空机器首个建议应为 site: %+v", summary.Suggestions)
	}
	if !strings.Contains(summary.Suggestions[0].Reason, "apt install nginx") {
		t.Fatalf("空机器建议未给出安装命令: %q", summary.Suggestions[0].Reason)
	}
}

// TestSummaryPostgres 验证数据库优先级与 db-postgres 建议。
func TestSummaryPostgres(t *testing.T) {
	services := []Service{
		{ID: "caddy", Name: "Caddy", Found: true, Running: true, Port: 80},
		{ID: "postgres", Name: "PostgreSQL", Found: true, Running: true, Port: 5432},
	}
	ports := []ListeningPort{{Port: 5432, Addr: "::1", Family: "ipv6", Listening: true}}
	summary := BuildSummary(services, ports)
	if summary.WebServer != "caddy" || summary.DB != "postgres" || !summary.Ready {
		t.Fatalf("汇总字段不符: %+v", summary)
	}
	if _, ok := findSuggestion(summary.Suggestions, "db-postgres"); !ok {
		t.Fatalf("缺少 db-postgres 建议: %+v", summary.Suggestions)
	}
}

// findSuggestion 按插件名查建议。
func findSuggestion(suggestions []Suggestion, plugin string) (Suggestion, bool) {
	for _, suggestion := range suggestions {
		if suggestion.Plugin == plugin {
			return suggestion, true
		}
	}
	return Suggestion{}, false
}
