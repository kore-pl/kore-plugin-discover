package probe

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
)

// 本文件实现服务端版本探测：连上真实服务端问版本，而不是解析客户端 `--version`。
//
// 纪律：
//   - 能探到就探，探不到留空（由调用方降级为 version_source=client），不报错、不编造；
//   - 凭据只从环境变量读取并经 Runner 的 env 传给子进程，绝不进入 argv
//     （/proc/<pid>/cmdline 对同机其他用户可见）；
//   - 命令仍是固定 argv，不接受任何用户输入拼接。
const (
	// probeHost 是服务端版本探测连接的本机地址（只探本机，不扫网段）。
	probeHost = "127.0.0.1"
	// defaultMySQLUser 是 MySQL/MariaDB 服务端探测的默认用户。
	defaultMySQLUser = "root"
	// defaultPostgresUser 是 PostgreSQL 服务端探测的默认用户。
	defaultPostgresUser = "postgres"
)

// probeServerVersion 按 catalog 的 ServerProbe 选择探测方式。
// 非故障注入的失败一律返回空串 + nil，交由 detect 降级为 client。
func (s *hostSource) probeServerVersion(ctx context.Context, spec serviceSpec, service Service) (string, error) {
	switch spec.ServerProbe {
	case "mysql":
		return s.mysqlServerVersion(ctx, spec)
	case "postgres":
		return s.postgresServerVersion(ctx, spec)
	case "redis":
		return s.redisServerVersion(ctx, spec)
	case "nginx":
		return s.nginxServerVersion(ctx, service)
	default:
		return "", nil
	}
}

// mysqlServerVersion 用 mysql/mariadb 客户端执行 SELECT VERSION()。
// 密码从 MYSQL_PWD 读取；未设置时按空密码尝试（与服务端未设 root 密码的默认安装一致）。
func (s *hostSource) mysqlServerVersion(ctx context.Context, spec serviceSpec) (string, error) {
	bin := s.findClientExecutable(spec)
	if bin == "" {
		return "", nil
	}
	output, err := s.opts.Runner(ctx, bin, s.secretEnv("MYSQL_PWD"),
		"-h", probeHost,
		"-P", strconv.Itoa(spec.Port),
		"--protocol=TCP",
		"--connect-timeout=3",
		"-u", defaultMySQLUser,
		"-e", "SELECT VERSION()",
		"-N", "-B",
	)
	if err != nil {
		var fault *FaultError
		if errors.As(err, &fault) {
			return "", err
		}
		return "", nil
	}
	version := parseStandaloneVersion(output)
	// SELECT VERSION() 会告诉我们 3306 上到底是 MySQL 还是 MariaDB；条目按服务端
	// 类型归属，避免把 MySQL 服务端挂到 mariadb 条目下（反过来也一样）。
	if !mysqlFlavorMatches(spec.ID, version) {
		return "", nil
	}
	return version, nil
}

// mysqlFlavorMatches 核对服务端类型与 catalog 条目是否一致：
// MariaDB 的版本字符串含 "MariaDB"，MySQL 不含。
func mysqlFlavorMatches(id, version string) bool {
	if version == "" {
		return false
	}
	maria := strings.Contains(strings.ToLower(version), "mariadb")
	if id == "mariadb" {
		return maria
	}
	return !maria
}

// postgresServerVersion 用 psql 执行 SHOW server_version。
// 连接超时走 PGCONNECT_TIMEOUT（psql 没有 --connect-timeout 选项，传该 flag 会让
// 真实 psql 直接报 invalid option）；密码从 PGPASSWORD 读取。
func (s *hostSource) postgresServerVersion(ctx context.Context, spec serviceSpec) (string, error) {
	bin := s.findClientExecutable(spec)
	if bin == "" {
		return "", nil
	}
	env := []string{"PGCONNECT_TIMEOUT=3"}
	env = append(env, s.secretEnv("PGPASSWORD")...)
	output, err := s.opts.Runner(ctx, bin, env,
		"-h", probeHost,
		"-p", strconv.Itoa(spec.Port),
		"-U", defaultPostgresUser,
		"-tAc", "SHOW server_version",
	)
	if err != nil {
		var fault *FaultError
		if errors.As(err, &fault) {
			return "", err
		}
		return "", nil
	}
	return parseVersion(output), nil
}

// redisServerVersion 优先进程内 redis-cli 查 INFO server；宿主机没有 redis-cli
// （Redis 只跑在容器里）时回退到 docker exec 同款命令。
func (s *hostSource) redisServerVersion(ctx context.Context, spec serviceSpec) (string, error) {
	env := s.secretEnv("REDISCLI_AUTH")
	if bin := s.findClientExecutable(spec); bin != "" {
		output, err := s.opts.Runner(ctx, bin, env,
			"-h", probeHost,
			"-p", strconv.Itoa(spec.Port),
			"--no-auth-warning",
			"INFO", "server",
		)
		if err != nil {
			var fault *FaultError
			if errors.As(err, &fault) {
				return "", err
			}
		} else if version := parseRedisVersion(output); version != "" {
			return version, nil
		}
	}
	return s.redisContainerVersion(ctx, spec, env)
}

// redisContainerVersion 在宿主机没有 redis-cli 时通过容器内的 redis-cli 探测。
// 凭据只传变量名（`docker exec -e REDISCLI_AUTH`），值由 Docker CLI 从本进程环境
// 读取后注入容器——密码不落在 argv 里。
func (s *hostSource) redisContainerVersion(ctx context.Context, spec serviceSpec, env []string) (string, error) {
	dockerBin, container := s.findContainer(ctx, "redis")
	if dockerBin == "" || container == "" {
		return "", nil
	}
	args := []string{"exec"}
	if len(env) > 0 {
		args = append(args, "-e", "REDISCLI_AUTH")
	}
	args = append(args, container,
		"redis-cli",
		"-h", probeHost,
		"-p", strconv.Itoa(spec.Port),
		"--no-auth-warning",
		"INFO", "server",
	)
	output, err := s.opts.Runner(ctx, dockerBin, env, args...)
	if err != nil {
		var fault *FaultError
		if errors.As(err, &fault) {
			return "", err
		}
		return "", nil
	}
	return parseRedisVersion(output), nil
}

// nginxServerVersion 处理 nginx 的服务端版本：
//   - 宿主机装了 nginx 时，nginx 二进制本身就是服务端，`-v` 输出即服务端版本；
//   - bin 为空说明 nginx 跑在容器里（宿主只看得到 comm=nginx），用
//     `docker exec <容器> nginx -v` 取容器内版本。
func (s *hostSource) nginxServerVersion(ctx context.Context, service Service) (string, error) {
	if service.Bin != "" {
		return service.ClientVersion, nil
	}
	dockerBin, container := s.findContainer(ctx, "nginx")
	if dockerBin == "" || container == "" {
		return "", nil
	}
	output, err := s.opts.Runner(ctx, dockerBin, nil, "exec", container, "nginx", "-v")
	if err != nil {
		var fault *FaultError
		if errors.As(err, &fault) {
			return "", err
		}
		return "", nil
	}
	return parseVersion(output), nil
}

// findClientExecutable 按 ClientBins（缺省回退 Bins）在 PATH 与常见路径里定位
// 服务端探测用的客户端命令；找不到返回空串。
func (s *hostSource) findClientExecutable(spec serviceSpec) string {
	names := spec.ClientBins
	if len(names) == 0 {
		names = spec.Bins
	}
	if bin, _ := findExecutable(s.fs, s.opts.PathDirs, serviceSpec{Bins: names}); bin != "" {
		return bin
	}
	for _, pattern := range spec.KnownPaths {
		for _, candidate := range s.fs.glob(pattern) {
			base := filepath.Base(candidate)
			for _, name := range names {
				if base == name && s.fs.isExecutable(candidate) {
					return candidate
				}
			}
		}
	}
	return ""
}

// findContainer 用 docker ps 找第一个名称/镜像含 keyword 的运行中容器，
// 返回 docker 二进制与容器 ID；docker 不可用或无匹配时返回空串。
func (s *hostSource) findContainer(ctx context.Context, keyword string) (string, string) {
	dockerBin := s.findTool("docker")
	if dockerBin == "" {
		return "", ""
	}
	output, err := s.opts.Runner(ctx, dockerBin, nil,
		"ps", "--format", "{{.ID}} {{.Names}} {{.Image}}")
	if err != nil {
		return "", ""
	}
	keyword = strings.ToLower(keyword)
	for _, line := range splitLines(output) {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if strings.Contains(strings.ToLower(line), keyword) {
			return dockerBin, fields[0]
		}
	}
	return "", ""
}

// findTool 在 PATH 里定位单个工具命令。
func (s *hostSource) findTool(name string) string {
	bin, _ := findExecutable(s.fs, s.opts.PathDirs, serviceSpec{Bins: []string{name}})
	return bin
}

// secretEnv 读取凭据并转成子进程环境变量；空值视为未设置（走空密码/无认证尝试）。
func (s *hostSource) secretEnv(key string) []string {
	if value, ok := s.opts.LookupEnv(key); ok && value != "" {
		return []string{key + "=" + value}
	}
	return nil
}

// parseStandaloneVersion 只接受"整行就是一个版本号"的输出（MySQL/MariaDB 的
// `-N -B` 形式），跳过警告/错误行，避免把 stderr 里的无关数字当版本。
func parseStandaloneVersion(output string) string {
	for _, line := range splitLines(output) {
		line = strings.TrimSpace(line)
		if line == "" || strings.ContainsAny(line, " \t") {
			continue
		}
		if version := parseVersion(line); version != "" {
			return version
		}
	}
	return ""
}

// parseRedisVersion 从 `INFO server` 的文本里取 redis_version 字段。
func parseRedisVersion(output string) string {
	const prefix = "redis_version:"
	for _, line := range splitLines(output) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		return parseVersion(value)
	}
	return ""
}
