# kore-plugin-discover

kore 的本机服务发现插件（production）：**自动探测本机装了哪些运维服务、在哪、能不能用**，
让用户不用手填环境细节。纯只读：不装包、不改配置、不启停服务。

## 协议方法（kore.plugin.v1）

| 方法 | 入参 | 结果 |
| --- | --- | --- |
| `kore.ping` | `{}` | `{status,uptime_s,goroutines,rss_kb}` |
| `kore.reload` | `{}` | `{ok:true,config_digest:""}`（无状态，空操作） |
| `kore.shutdown` | `{}` | `{ok:true}`，随后优雅退出 |
| `discover.services` | `{}` 或 `{"refresh":true}` | 服务数组，每项见下 |
| `discover.ports` | `{}` 或 `{"refresh":true}` | 监听端口数组 |
| `discover.summary` | `{}` 或 `{"refresh":true}` | `{ready,web_server,db,conclusion,suggestions[]}` |

### discover.services

返回数组（顺序固定为探测目标表顺序），每项：

```json
{"id":"nginx","name":"Nginx","found":true,"bin":"/usr/sbin/nginx","version":"1.24.0",
 "config_dir":"/etc/nginx","running":false,"port":80,"source":"which"}
```

- `found=false` 时必带 `install_hint`（含 Debian/Ubuntu 与 RHEL 的安装命令）。
- `source ∈ which|known_path|systemd|process`：分别表示 PATH 命中、常见路径命中、
systemd unit 命中、仅通过运行中进程名识别（例如容器里的 nginx，宿主看不到二进制）。
- `running` 由 **监听端口** + `/proc/<pid>/comm` 进程名 + 特定运行标记（docker.sock、
  `/run/systemd/system`、ufw 的 `ENABLED=yes`）共同判定，任一成立即为 true。
- `version` 由固定 argv 查询（nginx/apache 用 `-v`，caddy 用 `version`，其余 `--version`），
  命令失败/超时只丢版本，不影响 `found`。
- 结果缓存 30s；`{"refresh":true}` 跳过缓存强制重探。

探测目标（每个都会出现在数组里）：

| 类别 | 目标 | 命令 | 常见路径 | 配置目录 | 端口 |
| --- | --- | --- | --- | --- | --- |
| web server | nginx | `nginx` | `/usr/sbin/nginx`、`/usr/local/nginx/sbin/nginx` 等 | `/etc/nginx` 等 | 80 |
| web server | caddy | `caddy` | `/usr/bin/caddy`、`/usr/local/bin/caddy` | `/etc/caddy` | 80 |
| web server | apache2 | `apache2`/`apachectl` | `/usr/sbin/apache2` | `/etc/apache2` | 80 |
| web server | httpd | `httpd` | `/usr/sbin/httpd`、`/usr/local/apache2/bin/httpd` | `/etc/httpd` | 80 |
| 数据库 | mysql | `mysql`/`mysqld` | `/usr/bin/mysql`、`/usr/local/mysql/bin/mysql` | `/etc/mysql` | 3306 |
| 数据库 | mariadb | `mariadb`/`mariadbd` | `/usr/bin/mariadb` | `/etc/mysql/mariadb.conf.d` 等 | 3306 |
| 数据库 | postgres | `psql`/`postgres`/`pg_ctl` | `/usr/bin/psql`、`/usr/lib/postgresql/*/bin/postgres` | `/etc/postgresql` | 5432 |
| 缓存 | redis | `redis-server`/`redis-cli` | `/usr/bin/redis-server` | `/etc/redis` | 6379 |
| 运行时 | php | `php` | `/usr/bin/php` | `/etc/php` | — |
| 运行时 | node | `node`/`nodejs` | `/usr/bin/node`、`/usr/local/bin/node` | — | — |
| 运行时 | python3 | `python3` | `/usr/bin/python3` | — | — |
| 容器 | docker | `docker` | `/usr/bin/docker`、`/snap/bin/docker` | `/etc/docker` | 2375 |
| 容器 | podman | `podman` | `/usr/bin/podman` | `/etc/containers` | 2375 |
| 其他 | crontab | `crontab` | `/usr/bin/crontab` | `/etc/cron.d` | — |
| 其他 | systemctl | `systemctl` | `/usr/bin/systemctl` | `/etc/systemd/system` | — |
| 其他 | ufw | `ufw` | `/usr/sbin/ufw` | `/etc/ufw` | — |
| 其他 | iptables | `iptables` | `/usr/sbin/iptables` | — | — |
| 其他 | firewall-cmd | `firewall-cmd` | `/usr/bin/firewall-cmd` | `/etc/firewalld` | — |

"命令 + 常见路径 + systemd unit"三条线索任一命中即 `found=true`；数据库/缓存的
`running` 额外看 3306/5432/6379 是否真的在监听，避免把"只装了客户端"误判成服务可用。

### discover.ports

读 `/proc/net/tcp` + `/proc/net/tcp6`，只保留 LISTEN（状态 `0A`）条目：

```json
[{"port":8080,"addr":"127.0.0.1","family":"ipv4","listening":true,"process":"nginx"}]
```

- 本地地址是**小端十六进制**：`0100007F:1F90` → `127.0.0.1:8080`；tcp6 按 4 个 32 位
  小端字还原，IPv4-mapped 地址保留 `::ffff:` 前缀。
- 进程名通过 inode 反查：`/proc/net/tcp` 的 inode 与 `/proc/*/fd` 的 `socket:[inode]` 匹配。
  非 root 只能看到同 uid 进程，查不到时 `process` 为 `null`（端口本身仍然返回）。
- 输出按端口、协议族、地址稳定排序；`family ∈ ipv4|ipv6`。

### discover.summary

给小白的一句话结论 + 插件建议：

```json
{"ready":true,"web_server":"nginx","db":"mysql",
 "conclusion":"本机已就绪：检测到 Nginx 与 MySQL，可直接安装站点与数据库插件。",
 "suggestions":[{"plugin":"site","reason":"已检测到 Nginx，可安装站点插件管理网站"},
                {"plugin":"db-mysql","reason":"已检测到 MySQL 服务端（127.0.0.1:3306 监听）"}]}
```

- `ready = web_server != "" || db != ""`；`web_server` 优先级 nginx → caddy → apache2 → httpd，
  `db` 优先级 mysql → mariadb → postgres。
- 建议按 web → db → cache → container → runtime → cron → firewall 的固定顺序生成；
  空机器会给出"先装 nginx"的可执行提示。

## 只读与资源边界

| 边界 | 实现 |
| --- | --- |
| 不装包/不改配置/不启停服务 | 只做 stat/read/glob 与固定 argv 版本查询 |
| 命令注入 | 全部 `exec.CommandContext(bin, args...)`，bin/args 来自常量表，不接收用户输入 |
| 超时 | 单条命令 3s，整次方法调用 15s（`context.WithTimeout`） |
| 缓存 | 30s 内存缓存 + mutex，调用时判断过期；无后台刷新、无 ticker（G-19） |
| 平台隔离 | `/proc/net/*` 只在 `internal/probe/ports_linux.go`（`//go:build linux`），其它平台 stub |
| 非 Linux | 三个方法统一返回 `E_UNSUPPORTED`，不静默降级成空清单 |
| 数据形状 | 对外结构体全部显式字段，无 `any` / `object` / `map[string]any` |

## 构建与测试

```bash
make check                 # gofmt + go vet + go test -timeout 200s ./...
make build                 # CGO_ENABLED=0 GOAMD64=v1 -trimpath -buildvcs=false -s -w
make dist                  # 确定性 tar.gz（首条目 info.json，size_bytes 回填）
go test -v ./tests/e2e     # 真实二进制 E2E：services / ports（ss -ltn 对照）/ summary
go test ./tests/fault      # kill / timeout / disk / cgroup 四类故障注入
```

单测覆盖：

- 假 bin 目录（`t.TempDir()` + 假 `nginx` 脚本）→ found + version + config_dir + source；
- 未安装服务 → `found=false` + `install_hint`；
- `/proc/net/tcp` fixture 十六进制换算（`0100007F:1F90` → `127.0.0.1:8080`）；
- tcp6（`::` / `::1` / IPv4-mapped）解析；
- summary 有 nginx → suggestions 含 `site`；
- 缓存 30s、`refresh`、整体超时、非 Linux `E_UNSUPPORTED`。

## 平台

Linux 为唯一真实实现（v1.0 GA 口径）。macOS / Windows 返回 `E_UNSUPPORTED`，
非 Linux 编译单元只提供显式 stub，不静默降级。
