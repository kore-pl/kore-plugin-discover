package probe

import "fmt"

// 本文件是「服务端版本探测所需凭据」的显式声明与状态计算。
//
// 设计背景（kore §3.5 #8「插件凭据无法跨插件传递」）：discover 需要 MySQL / PostgreSQL /
// Redis 的密码才能读取服务端版本，但凭据归 db-mysql / redis 各自的配置所有，插件之间
// 不共享配置文件。内核侧的凭据注入（PLUGIN-CONFIG §6.1 候选 A：config.password →
// password_env 机械映射进子进程环境）尚未实现。
//
// 因此本插件把「需要什么凭据、从哪个环境变量读、用来做什么、缺了怎么办」显式声明出来：
//   - 静态声明在 credentialNeeds（字段与用途写死，随 discover.config.get 返回）；
//   - 运行时只从环境变量读取（值绝不进 argv、不回传、不落盘）；
//   - 缺失时在 services 条目的 version_hint 与 config.get 的 hint 里给可操作指引。
//
// 内核凭据注入落地后只会把同名环境变量注入插件进程，本插件代码零改动。

// credentialNeed 是一条凭据声明（静态、可审计）。
type credentialNeed struct {
	// ID 是稳定标识（前端/文档引用）。
	ID string
	// Label 是给人看的中文名（不含任何凭据值）。
	Label string
	// EnvVar 是读取密码的环境变量名；凭据只允许经环境变量传给子进程。
	EnvVar string
	// Services 是依赖该凭据的 catalog 服务 id。
	Services []string
	// Purpose 说明具体用途（哪个方法的哪条命令）。
	Purpose string
	// FillHint 是缺失时的可操作指引。
	FillHint string
}

// credentialNeeds 是全部凭据声明；顺序即 config.get 输出顺序。
// 只声明探测确实会读的环境变量，不声明未使用的凭据（宁缺不滥）。
var credentialNeeds = []credentialNeed{
	{
		ID:       "mysql_password",
		Label:    "MySQL / MariaDB 探测密码",
		EnvVar:   "MYSQL_PWD",
		Services: []string{"mysql", "mariadb"},
		Purpose:  "连接 127.0.0.1:3306 执行 SELECT VERSION()，读取真实服务端版本（version_source=server）",
		FillHint: "在 db-mysql 插件的配置面板填入 root 密码（db-mysql.config.password）；或等待内核凭据注入（§3.5 #8）后自动生效",
	},
	{
		ID:       "postgres_password",
		Label:    "PostgreSQL 探测密码",
		EnvVar:   "PGPASSWORD",
		Services: []string{"postgres"},
		Purpose:  "连接 127.0.0.1:5432 执行 SHOW server_version，读取真实服务端版本",
		FillHint: "在 db-postgres 插件的配置面板填入密码；或等待内核凭据注入（§3.5 #8）后自动生效",
	},
	{
		ID:       "redis_password",
		Label:    "Redis 探测密码",
		EnvVar:   "REDISCLI_AUTH",
		Services: []string{"redis"},
		Purpose:  "连接 127.0.0.1:6379 执行 INFO server，读取 redis_version",
		FillHint: "在 redis 插件的配置面板填入密码；或等待内核凭据注入（§3.5 #8）后自动生效",
	},
}

// credentialNeedFor 返回某服务对应的凭据声明；无凭据需求的服务返回 false。
func credentialNeedFor(serviceID string) (credentialNeed, bool) {
	for _, need := range credentialNeeds {
		for _, id := range need.Services {
			if id == serviceID {
				return need, true
			}
		}
	}
	return credentialNeed{}, false
}

// CredentialRequirement 是 discover.config.get 返回的单条凭据声明 + 当前注入状态。
type CredentialRequirement struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	EnvVar   string   `json:"env_var"`
	Services []string `json:"services"`
	Purpose  string   `json:"purpose"`
	Required bool     `json:"required"`
	Status   string   `json:"status"` // configured | missing
	FillHint string   `json:"fill_hint"`
}

// ConfigGetResult 是 discover.config.get 的结果：只读声明 + 状态，不含任何凭据值。
type ConfigGetResult struct {
	Plugin                 string                  `json:"plugin"`
	Persisted              bool                    `json:"persisted"`
	CredentialRequirements []CredentialRequirement `json:"credential_requirements"`
	Transport              string                  `json:"transport"`
	Hint                   string                  `json:"hint"`
}

// credentialConfigured 报告某凭据是否已从环境变量读到非空值（不返回、不记录该值）。
func credentialConfigured(lookup func(string) (string, bool), envVar string) bool {
	value, ok := lookup(envVar)
	return ok && value != ""
}

// buildConfigGet 组装 config.get 结果：凭据声明 + configured/missing 状态 + 汇总提示。
// lookup 是 Options.LookupEnv（测试可注入），保证只读且不泄露凭据值。
func buildConfigGet(lookup func(string) (string, bool)) ConfigGetResult {
	requirements := make([]CredentialRequirement, 0, len(credentialNeeds))
	configured := 0
	for _, need := range credentialNeeds {
		status := "missing"
		if credentialConfigured(lookup, need.EnvVar) {
			status = "configured"
			configured++
		}
		requirements = append(requirements, CredentialRequirement{
			ID:       need.ID,
			Label:    need.Label,
			EnvVar:   need.EnvVar,
			Services: append([]string(nil), need.Services...),
			Purpose:  need.Purpose,
			Required: false, // 可选：缺失只降级版本来源，不影响插件可用性
			Status:   status,
			FillHint: need.FillHint,
		})
	}
	result := ConfigGetResult{
		Plugin:                 "discover",
		Persisted:              false,
		CredentialRequirements: requirements,
		Transport:              "凭据只从插件进程环境变量读取（值不进 argv、不回传、不落盘）；内核凭据注入（§3.5 #8 / PLUGIN-CONFIG §6.1 候选 A）落地后注入同名变量，本插件无需改动",
	}
	if configured == len(requirements) {
		result.Hint = fmt.Sprintf("探测凭据已全部配置（%d/%d）；服务端版本可经真实连接读取", configured, len(requirements))
	} else {
		result.Hint = fmt.Sprintf("探测凭据已配置 %d/%d：缺失项会让对应服务降级为客户端版本（version_source=client）；按各条 fill_hint 补齐，或等待内核凭据注入（§3.5 #8）",
			configured, len(requirements))
	}
	return result
}

// versionHint 为「服务端版本没拿到」的服务条目生成可操作提示：
//   - 该服务需要凭据且环境变量缺失 → 明确指出缺哪个变量、去哪填；
//   - 已配置但没探到 → 说明是服务端不可达/认证失败/非默认端口；
//   - 无需凭据的探测（如 nginx）失败 → 通用提示。
//
// 只在 found=true 且 server_version 为空时调用；未安装的条目不产生本提示（由 install_hint 覆盖）。
func (s *hostSource) versionHint(spec serviceSpec, service Service) string {
	if service.ServerVersion != "" || spec.ServerProbe == "" {
		return ""
	}
	degraded := service.VersionSource
	if need, ok := credentialNeedFor(spec.ID); ok {
		if !credentialConfigured(s.opts.LookupEnv, need.EnvVar) {
			return "未配置 " + need.EnvVar + "（可选）：服务端探测可能因认证失败而降级（当前 version_source=" +
				degraded + "）；若该实例无需密码可忽略；需要时 " + need.FillHint
		}
		return "已配置 " + need.EnvVar + " 但服务端探测失败（服务未启动 / 认证失败 / 非默认端口）；版本来源降级为 " + degraded
	}
	return "服务端探测失败（服务未启动 / 需认证 / 非默认端口）；版本来源降级为 " + degraded
}
