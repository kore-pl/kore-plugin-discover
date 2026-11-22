package probe

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/pengtang1024/kore/pkg/pluginapi"
)

// 探测预算与缓存窗口（任务口径：命令 3s、整体 15s、缓存 30s）。
const (
	// DefaultCommandTimeout 是单条探测命令的上限。
	DefaultCommandTimeout = 3 * time.Second
	// DefaultOverallTimeout 是单次方法调用的整体预算。
	DefaultOverallTimeout = 15 * time.Second
	// DefaultCacheTTL 是探测结果的缓存有效期。
	DefaultCacheTTL = 30 * time.Second
)

// Runner 执行一次固定 argv 的子命令并返回合并输出；测试可注入假实现。
// env 是追加到子进程的环境变量（KEY=VALUE），用于把凭据从环境变量传给客户端
// （如 MYSQL_PWD / PGPASSWORD / REDISCLI_AUTH）——凭据禁止进入 argv。
type Runner func(ctx context.Context, bin string, env []string, args ...string) (string, error)

// Options 是 Runtime 的运行期配置：真实运行取默认值，测试可注入根目录/PATH/执行面。
type Options struct {
	// GOOS 为空时取 runtime.GOOS；测试可注入 "darwin" 验证 E_UNSUPPORTED。
	GOOS string
	// RootFS 是只读文件系统根，默认 "/"；测试可指向 t.TempDir()。
	RootFS string
	// PathDirs 是查找命令的 PATH 目录；nil 时取进程 PATH，显式空切片表示不查 PATH。
	PathDirs []string
	// Runner 是命令执行面；nil 时使用带超时的 exec 实现。
	Runner Runner
	// LookupEnv 读取环境变量（凭据只从环境变量取）；nil 时使用 os.LookupEnv。
	LookupEnv func(string) (string, bool)
	// Now 为空时取 time.Now；测试可注入假时钟验证缓存过期。
	Now func() time.Time
	// TTL 是结果缓存窗口，默认 30s。
	TTL time.Duration
	// CommandTimeout 是单条命令超时，默认 3s。
	CommandTimeout time.Duration
	// OverallTimeout 是单次方法调用整体预算，默认 15s。
	OverallTimeout time.Duration
}

// normalize 填充默认值；显式空 PathDirs 保持为空（测试用）。
func (o Options) normalize() Options {
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.RootFS == "" {
		o.RootFS = "/"
	}
	if o.PathDirs == nil {
		o.PathDirs = filepath.SplitList(os.Getenv("PATH"))
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.TTL <= 0 {
		o.TTL = DefaultCacheTTL
	}
	if o.CommandTimeout <= 0 {
		o.CommandTimeout = DefaultCommandTimeout
	}
	if o.OverallTimeout <= 0 {
		o.OverallTimeout = DefaultOverallTimeout
	}
	if o.Runner == nil {
		o.Runner = newExecRunner(o.CommandTimeout)
	}
	if o.LookupEnv == nil {
		o.LookupEnv = os.LookupEnv
	}
	return o
}

// Source 是探测执行面：服务清单与监听端口两个只读数据源。
// 故障注入测试用一个始终返回 FaultError 的实现覆盖三个方法。
type Source interface {
	// Services 返回本机服务清单（catalog 全量，未装项 found=false）。
	Services(ctx context.Context) ([]Service, error)
	// Listeners 返回本机 TCP 监听端口清单。
	Listeners(ctx context.Context) ([]ListeningPort, error)
}

// hostSource 是真实执行面：只读文件系统 + 固定 argv 探测命令。
type hostSource struct {
	opts Options
	fs   hostFS
}

// Runtime 是三个 discover 方法的生命周期容器：持有执行面与 30s 缓存。
// 缓存只在调用时判断过期，不启动后台 goroutine/ticker（G-19）。
type Runtime struct {
	opts Options
	src  Source

	mu            sync.Mutex
	servicesCache timedCache[[]Service]
	portsCache    timedCache[[]ListeningPort]
	summaryCache  timedCache[Summary]
}

// timedCache 是带写入时刻的结果缓存；valid=false 表示尚未计算。
type timedCache[T any] struct {
	valid bool
	at    time.Time
	data  T
}

// NewRuntime 构造 Runtime；src 为 nil 时使用真实只读执行面。
func NewRuntime(opts Options, src Source) *Runtime {
	opts = opts.normalize()
	if src == nil {
		src = &hostSource{opts: opts, fs: hostFS{root: opts.RootFS}}
	}
	return &Runtime{opts: opts, src: src}
}

// Call 执行一个 discover 方法（方法名已是 discover.<verb> 规范形式）。
func (r *Runtime) Call(method string, params json.RawMessage) (json.RawMessage, *pluginapi.Error) {
	if r.opts.GOOS != "linux" {
		return nil, unsupported("discover 插件只在 Linux 提供真实实现（当前 " + r.opts.GOOS + "）")
	}
	refresh, perr := parseParams(params)
	if perr != nil {
		return nil, perr
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.opts.OverallTimeout)
	defer cancel()
	switch method {
	case "discover.services":
		services, err := r.loadServices(ctx, refresh)
		if err != nil {
			return nil, classify(err)
		}
		return encode(services)
	case "discover.ports":
		ports, err := r.loadPorts(ctx, refresh)
		if err != nil {
			return nil, classify(err)
		}
		return encode(ports)
	case "discover.summary":
		summary, err := r.loadSummary(ctx, refresh)
		if err != nil {
			return nil, classify(err)
		}
		return encode(summary)
	default:
		return nil, notFound("未知方法: " + method)
	}
}

// loadServices 取服务清单：缓存未过期直接返回，否则探测并写回。
func (r *Runtime) loadServices(ctx context.Context, refresh bool) ([]Service, error) {
	if !refresh {
		r.mu.Lock()
		cached := r.servicesCache
		r.mu.Unlock()
		if cached.valid && r.opts.Now().Sub(cached.at) < r.opts.TTL {
			return cached.data, nil
		}
	}
	services, err := r.src.Services(ctx)
	if err != nil {
		return nil, err
	}
	if services == nil {
		services = []Service{}
	}
	r.mu.Lock()
	r.servicesCache = timedCache[[]Service]{valid: true, at: r.opts.Now(), data: services}
	r.mu.Unlock()
	return services, nil
}

// loadPorts 取监听端口清单，缓存语义与 loadServices 一致。
func (r *Runtime) loadPorts(ctx context.Context, refresh bool) ([]ListeningPort, error) {
	if !refresh {
		r.mu.Lock()
		cached := r.portsCache
		r.mu.Unlock()
		if cached.valid && r.opts.Now().Sub(cached.at) < r.opts.TTL {
			return cached.data, nil
		}
	}
	ports, err := r.src.Listeners(ctx)
	if err != nil {
		return nil, err
	}
	if ports == nil {
		ports = []ListeningPort{}
	}
	r.mu.Lock()
	r.portsCache = timedCache[[]ListeningPort]{valid: true, at: r.opts.Now(), data: ports}
	r.mu.Unlock()
	return ports, nil
}

// loadSummary 用（可能命中缓存的）服务与端口清单汇总，并缓存汇总结果。
func (r *Runtime) loadSummary(ctx context.Context, refresh bool) (Summary, error) {
	if !refresh {
		r.mu.Lock()
		cached := r.summaryCache
		r.mu.Unlock()
		if cached.valid && r.opts.Now().Sub(cached.at) < r.opts.TTL {
			return cached.data, nil
		}
	}
	services, err := r.loadServices(ctx, refresh)
	if err != nil {
		return Summary{}, err
	}
	ports, err := r.loadPorts(ctx, refresh)
	if err != nil {
		return Summary{}, err
	}
	summary := BuildSummary(services, ports)
	r.mu.Lock()
	r.summaryCache = timedCache[Summary]{valid: true, at: r.opts.Now(), data: summary}
	r.mu.Unlock()
	return summary, nil
}

// parseParams 只接受空参数或 JSON 对象；refresh=true 跳过缓存强制重探。
func parseParams(raw json.RawMessage) (bool, *pluginapi.Error) {
	if len(raw) == 0 || string(raw) == "null" {
		return false, nil
	}
	if raw[0] != '{' {
		return false, invalid("params 必须是 JSON 对象")
	}
	var params struct {
		Refresh bool `json:"refresh"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return false, invalid("params 解析失败: " + err.Error())
	}
	return params.Refresh, nil
}

// newExecRunner 构造带 3s 命令超时的真实执行器：固定 argv，不经过 shell。
// env 非空时追加到继承的环境变量之后（同名变量后者生效），凭据因此只存在于进程环境。
func newExecRunner(timeout time.Duration) Runner {
	return func(ctx context.Context, bin string, env []string, args ...string) (string, error) {
		commandCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		command := exec.CommandContext(commandCtx, bin, args...)
		if len(env) > 0 {
			command.Env = append(os.Environ(), env...)
		}
		command.WaitDelay = time.Second
		output, err := command.CombinedOutput()
		if err != nil && commandCtx.Err() == context.DeadlineExceeded {
			return string(output), commandCtx.Err()
		}
		return string(output), err
	}
}

// classify 把执行面错误映射为协议错误码：故障注入类别优先，其次超时/权限。
func classify(err error) *pluginapi.Error {
	var fault *FaultError
	if errors.As(err, &fault) {
		switch fault.Kind {
		case FaultKill:
			return upstream("进程被信号 9 终止: " + fault.Message)
		case FaultTimeout:
			return upstream("调用超时: " + fault.Message)
		case FaultDisk:
			return internalErr("磁盘满: " + fault.Message)
		case FaultCgroup:
			return unsupported("cgroup 限额触发: " + fault.Message)
		}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return upstream("探测超时：超出整体预算，请重试或缩小探测范围")
	case errors.Is(err, os.ErrPermission):
		return unauthorized("权限不足：无法读取探测目标")
	default:
		return internalErr("本机探测执行面失败")
	}
}

// encode 把结果编码为 JSON；序列化失败归 E_INTERNAL。
func encode(v any) (json.RawMessage, *pluginapi.Error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, internalErr("结果序列化失败: " + err.Error())
	}
	return data, nil
}

// 协议错误构造器（§9）；message 不拼接宿主绝对路径。
func invalid(msg string) *pluginapi.Error {
	return &pluginapi.Error{Code: pluginapi.E_INVALID, Message: msg}
}

func notFound(msg string) *pluginapi.Error {
	return &pluginapi.Error{Code: pluginapi.E_NOTFOUND, Message: msg}
}

func unauthorized(msg string) *pluginapi.Error {
	return &pluginapi.Error{Code: pluginapi.E_UNAUTHORIZED, Message: msg}
}

func unsupported(msg string) *pluginapi.Error {
	return &pluginapi.Error{Code: pluginapi.E_UNSUPPORTED, Message: msg}
}

func upstream(msg string) *pluginapi.Error {
	return &pluginapi.Error{Code: pluginapi.E_UPSTREAM, Message: msg}
}

func internalErr(msg string) *pluginapi.Error {
	return &pluginapi.Error{Code: pluginapi.E_INTERNAL, Message: msg}
}

// 编译期断言：hostSource 实现 Source。
var _ Source = (*hostSource)(nil)
