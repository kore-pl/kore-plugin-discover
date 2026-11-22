package probe

import "fmt"

// BuildSummary 把服务清单与监听端口汇总成小白可读的一句话结论 + 插件建议。
// 判定顺序固定（web → db → cache → container → runtime → cron → firewall），输出确定。
func BuildSummary(services []Service, ports []ListeningPort) Summary {
	found := make(map[string]Service, len(services))
	for _, service := range services {
		if service.Found {
			found[service.ID] = service
		}
	}
	listening := make(map[int]string, len(ports))
	for _, port := range ports {
		if _, exists := listening[port.Port]; !exists {
			listening[port.Port] = port.Addr
		}
	}

	summary := Summary{
		WebServer:   firstFound(found, "nginx", "caddy", "apache2", "httpd"),
		DB:          firstFound(found, "mysql", "mariadb", "postgres"),
		Suggestions: make([]Suggestion, 0, 8),
	}
	summary.Ready = summary.WebServer != "" || summary.DB != ""
	summary.Conclusion = conclusionFor(summary, found)
	summary.Suggestions = suggestionsFor(found, listening)
	return summary
}

// conclusionFor 生成一句话结论（面向不熟悉运维的用户）。
func conclusionFor(summary Summary, found map[string]Service) string {
	switch {
	case summary.WebServer != "" && summary.DB != "":
		return fmt.Sprintf("本机已就绪：检测到 %s 与 %s，可直接安装站点与数据库插件。",
			displayName(found, summary.WebServer), displayName(found, summary.DB))
	case summary.WebServer != "":
		return fmt.Sprintf("本机已就绪：检测到 %s，可先安装站点插件；未检测到数据库服务端。",
			displayName(found, summary.WebServer))
	case summary.DB != "":
		return fmt.Sprintf("本机已就绪：检测到 %s，可先安装数据库插件；未检测到 Web 服务器。",
			displayName(found, summary.DB))
	default:
		return "本机暂未检测到可用的 Web 服务或数据库，建议先安装 nginx 或 Docker 再安装对应插件。"
	}
}

// suggestionsFor 按固定优先级生成建议，同一插件只出现一次。
func suggestionsFor(found map[string]Service, listening map[int]string) []Suggestion {
	suggestions := make([]Suggestion, 0, 8)
	web := firstFound(found, "nginx", "caddy", "apache2", "httpd")
	if web != "" {
		suggestions = append(suggestions, Suggestion{
			Plugin: "site",
			Reason: fmt.Sprintf("已检测到 %s，可安装站点插件管理网站", displayName(found, web)),
		})
	}
	switch db := firstFound(found, "mysql", "mariadb", "postgres"); db {
	case "mysql", "mariadb":
		suggestions = append(suggestions, Suggestion{
			Plugin: "db-mysql",
			Reason: databaseReason("MySQL", listening, 3306),
		})
	case "postgres":
		suggestions = append(suggestions, Suggestion{
			Plugin: "db-postgres",
			Reason: databaseReason("PostgreSQL", listening, 5432),
		})
	}
	if _, ok := found["redis"]; ok {
		suggestions = append(suggestions, Suggestion{
			Plugin: "redis",
			Reason: "已检测到 Redis，可安装 Redis 插件管理缓存服务",
		})
	}
	switch {
	case found["docker"].Found:
		suggestions = append(suggestions, Suggestion{
			Plugin: "container",
			Reason: "已检测到 Docker，可安装容器插件管理容器应用",
		})
	case found["podman"].Found:
		suggestions = append(suggestions, Suggestion{
			Plugin: "container",
			Reason: "已检测到 Podman，可安装容器插件管理容器应用",
		})
	}
	for _, runtime := range []struct{ id, plugin, name string }{
		{"php", "runtime-php", "PHP"},
		{"node", "runtime-node", "Node.js"},
		{"python3", "runtime-python", "Python 3"},
	} {
		if _, ok := found[runtime.id]; ok {
			suggestions = append(suggestions, Suggestion{
				Plugin: runtime.plugin,
				Reason: fmt.Sprintf("已检测到 %s 运行时，可安装对应运行时插件管理版本与依赖", runtime.name),
			})
		}
	}
	if _, ok := found["crontab"]; ok {
		suggestions = append(suggestions, Suggestion{
			Plugin: "cron",
			Reason: "已检测到 crontab，可安装定时任务插件统一管理计划任务",
		})
	}
	for _, firewall := range []struct{ id, name string }{
		{"ufw", "UFW"},
		{"firewall-cmd", "firewalld"},
		{"iptables", "iptables"},
	} {
		if service, ok := found[firewall.id]; ok {
			state := "已安装"
			if service.Running {
				state = "已启用"
			}
			suggestions = append(suggestions, Suggestion{
				Plugin: "firewall",
				Reason: fmt.Sprintf("检测到 %s %s，可安装防火墙插件统一管理规则", firewall.name, state),
			})
			break
		}
	}
	if web == "" && firstFound(found, "mysql", "mariadb", "postgres") == "" {
		suggestions = append([]Suggestion{{
			Plugin: "site",
			Reason: "建议先安装 nginx（Debian/Ubuntu: apt install nginx），再安装站点插件管理网站",
		}}, suggestions...)
	}
	return suggestions
}

// databaseReason 根据是否监听服务端端口给出不同建议文案。
func databaseReason(label string, listening map[int]string, port int) string {
	if addr, ok := listening[port]; ok {
		return fmt.Sprintf("已检测到 %s 服务端（%s:%d 监听）", label, addr, port)
	}
	return fmt.Sprintf("已检测到 %s 客户端（未发现 %d 监听），可安装数据库插件管理连接", label, port)
}

// firstFound 返回按优先级第一个 found 的服务 id。
func firstFound(found map[string]Service, ids ...string) string {
	for _, id := range ids {
		if service, ok := found[id]; ok && service.Found {
			return id
		}
	}
	return ""
}

// displayName 取服务展示名；表里缺失时回退到 id。
func displayName(found map[string]Service, id string) string {
	if service, ok := found[id]; ok && service.Name != "" {
		return service.Name
	}
	return id
}
