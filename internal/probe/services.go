package probe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
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
	// 按端口记录监听归属：docker-proxy / containerd-shim 说明端口是**容器转发**，
	// 不是宿主服务在监听——这是本轮修复「nginx/3306 误报 running」的关键依据。
	listening := make(map[int]listenerInfo, len(listeners))
	for _, listener := range listeners {
		info := listening[listener.Port]
		info.present = true
		if listener.Process != nil {
			info.process = strings.ToLower(*listener.Process)
			if isProxyProcess(info.process) {
				info.containerProxy = true
			}
		}
		listening[listener.Port] = info
	}
	hostProcs, containerProcs := s.runningProcesses()

	services := make([]Service, 0, len(catalog))
	for _, spec := range catalog {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		service, err := s.detect(ctx, spec, listening, hostProcs, containerProcs)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	services = mergeDuplicateClients(services)
	return services, nil
}

// mergeDuplicateClients 合并「同一个客户端二进制被两条 catalog 记录都命中」的重复计数。
//
// 本机典型：宿主只有 MariaDB 客户端，mysql 与 mariadb 两条记录都 found=true，
// 用户会看到两个「已安装数据库」；实际上它们是同一个客户端（`mysql --version`
// 输出的就是 MariaDB）。判定用 os.SameFile（符号链接解析到同一 inode），
// 只有确证同一文件才合并，绝不按名字猜。
func mergeDuplicateClients(services []Service) []Service {
	mysqlIndex, mariaIndex := -1, -1
	for i := range services {
		switch services[i].ID {
		case "mysql":
			mysqlIndex = i
		case "mariadb":
			mariaIndex = i
		}
	}
	if mysqlIndex < 0 || mariaIndex < 0 {
		return services
	}
	mysql, maria := services[mysqlIndex], services[mariaIndex]
	if !mysql.Found || !maria.Found || mysql.Bin == "" || maria.Bin == "" {
		return services
	}
	mysqlInfo, err1 := os.Stat(mysql.Bin)
	mariaInfo, err2 := os.Stat(maria.Bin)
	if err1 != nil || err2 != nil || !os.SameFile(mysqlInfo, mariaInfo) {
		return services
	}
	services[mysqlIndex].Found = false
	services[mysqlIndex].Running = false
	services[mysqlIndex].Bin = ""
	services[mysqlIndex].ClientVersion = ""
	services[mysqlIndex].ServerVersion = ""
	services[mysqlIndex].VersionSource = VersionSourceUnknown
	services[mysqlIndex].VersionHint = "与本机 mariadb 指向同一客户端（MariaDB），已合并为 mariadb 条目，不重复计数"
	services[mysqlIndex].RunningHint = "同一 MariaDB 客户端的重复条目，运行状态见 mariadb"
	services[mysqlIndex].InstallHint = "与本机 mariadb 为同一客户端（MariaDB），已合并计数；需要 MySQL Server 请单独安装（apt install mysql-server）"
	return services
}

// detect 探测单条服务：定位命令/unit → 判 found/source → 读配置目录 → 查版本 → 判运行中。
func (s *hostSource) detect(ctx context.Context, spec serviceSpec, listening map[int]listenerInfo, hostProcs, containerProcs map[string]bool) (Service, error) {
	service := Service{ID: spec.ID, Name: spec.Name, Port: spec.Port}
	bin, source := findExecutable(s.fs, s.opts.PathDirs, spec)
	if bin == "" && findUnit(s.fs, spec) != "" {
		source = "systemd"
	}
	hostComm, containerComm := false, false
	for _, name := range spec.ProcessNames {
		lower := strings.ToLower(name)
		if hostProcs[lower] {
			hostComm = true
		}
		if containerProcs[lower] {
			containerComm = true
		}
	}
	commFound := hostComm || containerComm
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

	info := listenerInfo{}
	if spec.Port > 0 {
		info = listening[spec.Port]
	}
	service.Running, service.RunningHint = runningDecision(spec, hostComm, containerComm || containerProcs[strings.ToLower(spec.ID)],
		info, spec.RunningPath != "" && s.fs.exists(spec.RunningPath),
		spec.EnabledConf != "" && confEnabled(s.fs, spec.EnabledConf))
	// 版本两边都没拿到时给「版本未知」的明确文案，而不是留空让用户以为字段没实现。
	if service.VersionSource == VersionSourceUnknown && service.VersionHint == "" {
		service.VersionHint = "版本未知：客户端 --version 未返回可识别版本，也未配置服务端探测凭据（见 credentials.get 的 fill_hint）"
	}
	return service, nil
}

// runningProcesses 扫描 /proc/<pid>/comm，返回（宿主进程, 容器进程）两组小写进程名。
//
// 分组依据是 /proc/<pid>/cgroup：cgroup 路径含 docker / containerd / kubepods / libpod / crio
// 的进程属于容器。容器里的 nginx 在宿主 /proc 里看得到 comm，但它的监听端口在容器
// netns 内——宿主 80 端口没有监听，把它算成「宿主服务在跑」会误导用户去连 80。
func (s *hostSource) runningProcesses() (map[string]bool, map[string]bool) {
	hostProcs := make(map[string]bool)
	containerProcs := make(map[string]bool)
	entries, err := os.ReadDir(filepath.Join(s.fs.root, "proc"))
	if err != nil {
		return hostProcs, containerProcs
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
		if name == "" {
			continue
		}
		if processInContainer(filepath.Join(s.fs.root, "proc", entry.Name())) {
			containerProcs[name] = true
			continue
		}
		hostProcs[name] = true
	}
	return hostProcs, containerProcs
}

// processInContainer 读 /proc/<pid>/cgroup 判断进程是否属于容器；读不到按宿主处理
// （保守：宁可多说一句「宿主在跑」，也不把真实宿主服务藏起来）。
func processInContainer(procDir string) bool {
	data, err := os.ReadFile(filepath.Join(procDir, "cgroup"))
	if err != nil {
		return false
	}
	text := strings.ToLower(string(data))
	for _, marker := range []string{"docker", "containerd", "kubepods", "libpod", "crio"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// listenerInfo 是某端口的监听归属：present 表示宿主有监听；containerProxy 表示监听者
// 是 docker-proxy / containerd-shim（容器转发到宿主的端口，不是宿主服务本身）。
type listenerInfo struct {
	present        bool
	process        string
	containerProxy bool
}

// isProxyProcess 判断监听者是否是容器端口转发器。
func isProxyProcess(process string) bool {
	return strings.Contains(process, "docker-proxy") || strings.Contains(process, "containerd-shim") ||
		strings.Contains(process, "rootlesskit") || strings.Contains(process, "slirp4netns")
}

// runningDecision 是 running 的唯一判定点（纯函数，便于表驱动测试两种误报场景）：
//
//   - 宿主进程 / 宿主自己监听的端口 / RunningPath / EnabledConf → running=true；
//   - 只有容器内进程，或端口的监听者是 docker-proxy → running=false + 解释文案
//     （容器服务要用发布端口访问，宿主默认端口往往没监听）。
func runningDecision(spec serviceSpec, hostProc, containerProc bool, info listenerInfo, runningPath, confEnabled bool) (bool, string) {
	if hostProc || runningPath || confEnabled {
		return true, ""
	}
	if info.present && !info.containerProxy {
		return true, ""
	}
	if containerProc || (info.present && info.containerProxy) {
		where := "容器内进程"
		if info.present && info.containerProxy {
			where = "容器端口转发（" + info.process + "）"
		}
		port := "默认端口"
		if spec.Port > 0 {
			port = "宿主 " + strconv.Itoa(spec.Port) + " 端口"
		}
		return false, "检测到 " + spec.Name + " 在" + where + "，但" + port + "没有监听：" +
			"容器服务需通过宿主机发布端口访问（用 docker port 查看实际映射），宿主上并没有该服务进程"
	}
	if spec.Port > 0 {
		return false, "未检测到 " + spec.Name + " 宿主进程，且宿主 " + strconv.Itoa(spec.Port) + " 端口未监听"
	}
	return false, ""
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
