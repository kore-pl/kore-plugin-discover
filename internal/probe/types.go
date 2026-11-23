// Package probe 实现 discover 插件的方法级执行面：本机服务探测（discover.services）、
// 监听端口枚举（discover.ports）与面向小白的汇总建议（discover.summary）。
//
// 边界与纪律：
//   - 全部探测只读：不装包、不改配置、不启停服务；
//   - 命令一律固定 argv（exec.Command(bin, args...)），不接受用户输入拼命令；
//   - 每个探测命令 3s 超时、整次方法调用 15s 预算；
//   - 结果缓存 30s（内存 + mutex，调用时判断过期），不启动任何后台刷新/ticker；
//   - /proc/net 读取只在 Linux 编译单元（ports_linux.go）里，其它平台显式 E_UNSUPPORTED。
package probe

import "fmt"

// 版本来源取值：server 表示真实服务端探测成功；client 表示只拿到客户端/二进制版本；
// unknown 表示两者都没有。禁止用客户端版本冒充服务端版本。
const (
	VersionSourceServer  = "server"
	VersionSourceClient  = "client"
	VersionSourceUnknown = "unknown"
)

// Service 是 discover.services 返回的单个服务条目。字段全部显式，不使用 any/object
// （AGENTS.md 数据形状纪律）；not found 时 Found=false、Bin/ClientVersion/
// ServerVersion/ConfigDir 为空、InstallHint 必填。
//
// 版本语义（2026-11-23 起）：
//   - ClientVersion 是客户端/二进制自身的版本（`mysql --version` 等 `--version` 输出）；
//   - ServerVersion 是连上真实服务端后查到的版本（`SELECT VERSION()` / `SHOW
//     server_version` / Redis INFO），探不到就留空，绝不拿 ClientVersion 顶替；
//   - VersionSource 明确告诉前端该信哪个：server / client / unknown。
type Service struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Found         bool   `json:"found"`
	Bin           string `json:"bin,omitempty"`
	ClientVersion string `json:"client_version,omitempty"`
	ServerVersion string `json:"server_version,omitempty"`
	VersionSource string `json:"version_source"`
	// VersionHint 是「服务端版本没拿到」时的可操作提示（缺哪个环境变量 / 去哪填 /
	// 服务端为何不可达）；探到服务端版本、或该服务不需要服务端探测时为空。
	VersionHint string `json:"version_hint,omitempty"`
	ConfigDir   string `json:"config_dir,omitempty"`
	Running     bool   `json:"running"`
	Port        int    `json:"port"`
	Source      string `json:"source,omitempty"`
	InstallHint string `json:"install_hint,omitempty"`
}

// ListeningPort 是 discover.ports 返回的监听套接字条目。
// Process 为 nil 时编码为 JSON null（非 root 只能看到同 uid 进程的 socket inode）。
type ListeningPort struct {
	Port      int     `json:"port"`
	Addr      string  `json:"addr"`
	Family    string  `json:"family"`
	Listening bool    `json:"listening"`
	Process   *string `json:"process"`
}

// Suggestion 是一条面向小白的插件安装建议。
type Suggestion struct {
	Plugin string `json:"plugin"`
	Reason string `json:"reason"`
}

// Summary 是 discover.summary 的结果：一句话结论 + 建议列表。
type Summary struct {
	Ready       bool         `json:"ready"`
	WebServer   string       `json:"web_server"`
	DB          string       `json:"db"`
	Conclusion  string       `json:"conclusion"`
	Suggestions []Suggestion `json:"suggestions"`
}

// FaultKind 是四类故障注入的类别标识（与 file / monitor / process 插件同一口径）。
type FaultKind string

// 四类故障注入类别。
const (
	FaultKill    FaultKind = "kill"
	FaultTimeout FaultKind = "timeout"
	FaultDisk    FaultKind = "disk"
	FaultCgroup  FaultKind = "cgroup"
)

// FaultError 是执行面注入的故障错误，classify 将其映射为协议错误码。
type FaultError struct {
	Kind    FaultKind
	Message string
}

// Error 实现 error 接口。
func (e *FaultError) Error() string { return fmt.Sprintf("%s: %s", e.Kind, e.Message) }

// NewFaultError 构造给定类别的故障错误，供故障注入 Source 使用。
func NewFaultError(kind FaultKind, message string) error {
	return &FaultError{Kind: kind, Message: message}
}
