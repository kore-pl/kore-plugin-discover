package probe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Services 探测 catalog 中的全部服务；单个服务探测失败不中断整表（除故障注入错误）。
// Listeners 读取 /proc/net/tcp* 的监听端口；错误原样返回给方法层分类。
func (s *hostSource) Listeners(ctx context.Context) ([]ListeningPort, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return readListeners(s.fs.root)
}

// Services 探测 catalog 中的全部服务；单个服务探测失败不中断整表（除故障注入错误）。
func (s *hostSource) Services(ctx context.Context) ([]Service, error) {
	// 服务端是否在跑由监听端口 + 进程名共同判定；读不到 /proc/net 时退化为"未监听"，
	// 不因此让整张服务清单失败（最小容器里也可能没有 /proc/net/tcp6）。
	listeners, _ := readListeners(s.fs.root)
	listening := make(map[int]bool, len(listeners))
	for _, listener := range listeners {
		listening[listener.Port] = true
	}
	procs := s.runningProcesses()

	services := make([]Service, 0, len(catalog))
	for _, spec := range catalog {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		service, err := s.detect(ctx, spec, listening, procs)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, nil
}

// detect 探测单条服务：定位命令/unit → 判 found/source → 读配置目录 → 查版本 → 判运行中。
func (s *hostSource) detect(ctx context.Context, spec serviceSpec, listening map[int]bool, procs map[string]bool) (Service, error) {
	service := Service{ID: spec.ID, Name: spec.Name, Port: spec.Port}
	bin, source := findExecutable(s.fs, s.opts.PathDirs, spec)
	if bin == "" && findUnit(s.fs, spec) != "" {
		source = "systemd"
	}
	commFound := false
	for _, name := range spec.ProcessNames {
		if procs[strings.ToLower(name)] {
			commFound = true
			break
		}
	}
	// 进程在跑但命令/unit 都找不到（例如容器里的 nginx，宿主只看得到 comm）时，
	// 仍算 found，source 明确标 process，避免出现"found 但没有来源"的空字段。
	if source == "" && commFound {
		source = "process"
	}
	service.Found = bin != "" || commFound || source == "systemd"
	if !service.Found {
		// 未安装/未运行：没有任何版本来源，显式标 unknown（字段始终出现）。
		service.VersionSource = VersionSourceUnknown
		service.InstallHint = spec.InstallHint
		return service, nil
	}

	service.Bin = bin
	service.Source = source
	service.ConfigDir = findConfigDir(s.fs, spec)
	if bin != "" {
		output, err := s.opts.Runner(ctx, bin, nil, spec.versionArgs()...)
		if err != nil {
			var fault *FaultError
			if errors.As(err, &fault) {
				return Service{}, err
			}
			if ctx.Err() != nil {
				return Service{}, ctx.Err()
			}
			// 命令不存在/超时/非零退出：客户端版本留空，不影响 found 结论。
		} else {
			service.ClientVersion = parseVersion(output)
		}
	}

	// 服务端版本单独探测：只对 catalog 声明了 ServerProbe 的目标执行；探测失败、
	// 无凭据、服务端不可达一律留空，由 VersionSource=client 明确降级——绝不像旧版
	// 那样把客户端 `--version` 输出当成服务端版本展示。
	if spec.ServerProbe != "" {
		serverVersion, probeErr := s.probeServerVersion(ctx, spec, service)
		if probeErr != nil {
			var fault *FaultError
			if errors.As(probeErr, &fault) {
				return Service{}, probeErr
			}
			if ctx.Err() != nil {
				return Service{}, ctx.Err()
			}
		} else {
			service.ServerVersion = serverVersion
		}
	}
	switch {
	case service.ServerVersion != "":
		service.VersionSource = VersionSourceServer
	case service.ClientVersion != "":
		service.VersionSource = VersionSourceClient
	default:
		service.VersionSource = VersionSourceUnknown
	}
	// 没拿到服务端版本时给可操作提示（缺哪个凭据环境变量 / 去哪填 / 服务端为何不可达）。
	// 只对已找到的服务生成；未安装项由 install_hint 覆盖。
	service.VersionHint = s.versionHint(spec, service)

	service.Running = commFound
	if spec.Port > 0 && listening[spec.Port] {
		service.Running = true
	}
	if spec.RunningPath != "" && s.fs.exists(spec.RunningPath) {
		service.Running = true
	}
	if spec.EnabledConf != "" && confEnabled(s.fs, spec.EnabledConf) {
		service.Running = true
	}
	return service, nil
}

// runningProcesses 扫描 /proc/<pid>/comm，返回小写进程名集合；读取失败直接跳过
// （非 root 也能读 comm，但容器里可能没有 /proc）。
func (s *hostSource) runningProcesses() map[string]bool {
	procs := make(map[string]bool)
	entries, err := os.ReadDir(filepath.Join(s.fs.root, "proc"))
	if err != nil {
		return procs
	}
	for _, entry := range entries {
		if _, ok := parsePID(entry.Name()); !ok {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(s.fs.root, "proc", entry.Name(), "comm"))
		if readErr != nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(string(data)))
		if name != "" {
			procs[name] = true
		}
	}
	return procs
}

// parsePID 只接受纯数字目录名（/proc/<pid>），其它条目一律跳过。
func parsePID(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	pid := 0
	for _, r := range name {
		if r < '0' || r > '9' {
			return 0, false
		}
		pid = pid*10 + int(r-'0')
	}
	return pid, pid > 0
}
