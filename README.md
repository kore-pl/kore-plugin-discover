# kore-plugin-discover

语言：[简体中文](docs/DETAIL.md) · [English](docs/DETAIL.en.md) · [Español](docs/DETAIL.es.md) · [Français](docs/DETAIL.fr.md) · [Deutsch](docs/DETAIL.de.md) · [Русский](docs/DETAIL.ru.md) · [Português](docs/DETAIL.pt.md)

kore 的本机服务发现插件（production）：**自动探测本机装了哪些运维服务、在哪、能不能用**，
让用户不用手填环境细节。纯只读：不装包、不改配置、不启停服务。
0.3.0 起补：方法级能力声明（§3.5 #12）、服务端探测凭据的显式声明与缺失提示（§3.5 #8 前置）、
memcached / MongoDB / RabbitMQ / Elasticsearch 四类新指纹（端口 + 协议握手/版本串，无第三方依赖）。

## 协议方法（kore.plugin.v1）

| 方法 | 入参 | 结果 |
| --- | --- | --- |
| `kore.ping` | `{}` | `{status,uptime_s,goroutines,rss_kb}` |
| `kore.reload` | `{}` | `{ok:true,config_digest:""}`（无状态，空操作） |
| `kore.shutdown` | `{}` | `{ok:true}`，随后优雅退出 |
| `discover.services` | `{}` 或 `{"refresh":true}` | 服务数组，每项见下 |
| `discover.ports` | `{}` 或 `{"refresh":true}` | 监听端口数组 |
| `discover.summary` | `{}` 或 `{"refresh":true}` | `{ready,web_server,db,conclusion,suggestions[]}` |
| `discover.config.get` | `{}` | `{plugin,persisted,credential_requirements[],transport,hint}`：服务端探测所需凭据的**声明与注入状态**（不回传凭据值） |

### discover.services

返回数组（顺序固定为探测目标表顺序），每项：

```json
{"id":"mysql","name":"MySQL","found":true,"bin":"/usr/bin/mysql",
 "client_version":"11.8.6-MariaDB","server_version":"9.7.1","version_source":"server",
 "config_dir":"/etc/mysql","running":true,"port":3306,"source":"which"}
```

- `found=false` 时必带 `install_hint`（含 Debian/Ubuntu 与 RHEL 的安装命令）。
- `source ∈ which|known_path|systemd|process`：分别表示 PATH 命中、常见路径命中、
systemd unit 命中、仅通过运行中进程名识别（例如容器里的 nginx，宿主看不到二进制）。
- `running` 由 **监听端口** + `/proc/<pid>/comm` 进程名 + 特定运行标记（docker.sock、
  `/run/systemd/system`、ufw 的 `ENABLED=yes`）共同判定，任一成立即为 true。
- `client_version` 由固定 argv 查询（nginx/apache 用 `-v`，caddy 用 `version`，其余
  `--version`），是**客户端/二进制自身版本**；命令失败/超时只丢该字段，不影响 `found`。
- `server_version` 是**连上真实服务端后查到的版本**，探不到就为空字符串。绝不用
  `client_version` 冒充服务端版本（旧版把 `mysql --version` 的客户端版本当服务端展示，
  会出现"两台库"的误读）。
- `version_source ∈ server|client|unknown`：`server` = 服务端探测成功（信 `server_version`）；
  `client` = 只有客户端/二进制版本（`server_version` 为空，前端应显示"未连上服务端"）；
  `unknown` = 两端都没有。
- `version_hint`（0.3.0 新增，仅在 found=true 且服务端版本没拿到时出现）：可操作提示——
  缺哪个凭据环境变量、去哪填、或服务端为何不可达（未启动 / 认证失败 / 非默认端口）。
  例：`未配置 MYSQL_PWD（可选）：服务端探测可能因认证失败而降级（当前 version_source=client）；
  若该实例无需密码可忽略；需要时 在 db-mysql 插件的配置面板填入 root 密码…或等待内核凭据注入（§3.5 #8）`。
- 服务端探测（每个目标独立，失败即降级，不报错）：

  | 目标 | 命令 | 凭据来源 |
  | --- | --- | --- |
  | MySQL / MariaDB | `mysql -h 127.0.0.1 -P 3306 --protocol=TCP --connect-timeout=3 -u root -e "SELECT VERSION()" -N -B` | `MYSQL_PWD`（未设置则按空密码尝试） |
  | PostgreSQL | `psql -h 127.0.0.1 -p 5432 -U postgres -tAc "SHOW server_version"` + `PGCONNECT_TIMEOUT=3` | `PGPASSWORD` |
  | Redis | `redis-cli -h 127.0.0.1 -p 6379 --no-auth-warning INFO server`（取 `redis_version`） | `REDISCLI_AUTH` |
  | nginx | 宿主有二进制时 `nginx -v` 即服务端版本；只有容器进程时 `docker exec <容器> nginx -v` | — |
  | Memcached | 原始 TCP 文本协议：连 `127.0.0.1:11211` 发 `version\r\n`，取 `VERSION <x.y.z>` | —（通常无认证） |
  | Elasticsearch | 原始 TCP 最小 HTTP/1.0 `GET /`（`127.0.0.1:9200`），取响应 JSON 的 `version.number` | —（HTTP 端口通常无认证） |
  | MongoDB | `mongosh --quiet --host 127.0.0.1 --port 27017 --eval "db.version()"`；旧版回退 `mongo` | 由 mongosh 自身认证配置决定（本插件不传密码） |
  | RabbitMQ | `rabbitmqctl version`；回退 `rabbitmq-diagnostics -q server_version` | 依赖 erlang cookie 可读（通常只有 rabbitmq 用户可读，失败即降级） |

  MySQL/MariaDB 共用 3306，服务端类型按 `SELECT VERSION()` 归属：版本串含 `MariaDB`
  记到 `mariadb` 条目，不含记到 `mysql` 条目；另一个条目只保留 `client_version`
  （避免把 MySQL 服务端显示成 MariaDB）。
  凭据**只经环境变量**传给子进程，不进入 argv（`/proc/<pid>/cmdline` 对同机其他用户可见）。
  宿主没有 `redis-cli` 而 Redis 跑在容器里时，回退 `docker exec -e REDISCLI_AUTH <容器>
  redis-cli ...`（只传变量名，值由 Docker CLI 从进程环境读取）。
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
| 缓存 | memcached | `memcached` | `/usr/bin/memcached` | —（Debian 为 `/etc/memcached.conf` 单文件） | 11211 |
| 数据库 | mongodb | `mongod`/`mongosh`/`mongo` | `/usr/bin/mongod`、`/usr/local/bin/mongosh` | `/etc/mongodb` | 27017 |
| 消息队列 | rabbitmq | `rabbitmqctl`/`rabbitmq-server` | `/usr/sbin/rabbitmqctl` | `/etc/rabbitmq` | 5672 |
| 搜索 | elasticsearch | `elasticsearch` | `/usr/share/elasticsearch/bin/elasticsearch` | `/etc/elasticsearch` | 9200 |
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

## 方法级能力（method_capabilities，§3.5 #12）

`info.json` 声明 4 条方法级能力，全部只读（内核 `POST /api/v1/invoke` 命中即单独生效，
未命中回退插件级 `capabilities`）：

```json
"capabilities": ["system:read", "proc:read"],
"method_capabilities": {
  "discover.services": "system:read",
  "discover.ports": "system:read",
  "discover.summary": "system:read",
  "discover.config.get": "system:read"
}
```

- 本插件所有业务方法都是只读探测，因此没有 `system:admin` 映射；审计侧也不会为这些方法写
  `invoke.*` 记录（协议 §3.1.2.1 的只读口径）。
- **没有 `config.set`**：本插件不接受明文凭据（见下节），因此不存在 `config.set` 的
  `system:admin` 映射。`info.json` 与代码两侧都由 `declaration_test.go` 双向锁定（声明集合 ==
  实现集合、键语法/插件域/能力白名单、`config.set` 不得出现）。
- 插件级 `capabilities` 仍保留 `system:read` + `proc:read`（兜底 + `/proc/net` 读取语义），
  方法级只在只读侧收窄，不是替代。

## 服务端探测凭据（§3.5 #8 前置声明）

discover 读 MySQL / PostgreSQL / Redis 的**服务端版本**需要密码，但凭据归
`db-mysql` / `db-postgres` / `redis` 各自配置所有，插件之间不共享配置文件；内核凭据注入
（[PLUGIN-CONFIG §6.1](../kore/docs/PLUGIN-CONFIG.md) 候选 A）尚未实现。本插件因此把需求
**显式声明**出来（`discover.config.get`）：

| id | 环境变量 | 适用服务 | 用途 |
| --- | --- | --- | --- |
| `mysql_password` | `MYSQL_PWD` | mysql / mariadb | 连 127.0.0.1:3306 执行 `SELECT VERSION()` |
| `postgres_password` | `PGPASSWORD` | postgres | 连 127.0.0.1:5432 执行 `SHOW server_version` |
| `redis_password` | `REDISCLI_AUTH` | redis | 连 127.0.0.1:6379 执行 `INFO server` |

- `config.get` 只回**声明与状态**（`configured` / `missing`）+ `fill_hint`，**绝不回传凭据值**；
  凭据只从进程环境变量读取，值不进 argv（`/proc/<pid>/cmdline` 对同机其他用户可见）、
  不落盘、不进日志。
- 凭据是**可选**的：缺失只让对应服务降级为 `version_source=client/unknown`，不影响插件可用性；
  `services` 条目的 `version_hint` 会点名缺哪个变量、去哪填。
- 内核凭据注入落地后只需把密码注入**同名环境变量**（`MYSQL_PWD` / `PGPASSWORD` /
  `REDISCLI_AUTH`），本插件代码零改动；`transport` 字段已写明该契约。
- **不提供 `config.set`**：让凭据经插件 API 明文写入等于把密码搬进插件内存/日志面，
  与「凭据只存 0600 配置文件 + 内核机械注入」的方向相反。需要临时验证时，
  在启动 kored 前 `export` 对应环境变量（重启即失效，运维手工手段，不是产品能力）。

## 安装动作：判定不做

`PLUGIN-MATURITY` §2 把「安装动作」列为 discover 的缺口。本插件**不实现安装动作**，理由：

1. 安装软件包是**写操作且需要 root**：要么新增 root_actions（固定 argv 的包管理器调用），
   要么依赖宿主提权通道；discover 的定位是「只读发现 + 给建议」，一旦能装包就变成
   系统管理插件，与 `depth_level=production` 的只读契约冲突。
2. 装什么、从哪个源装（发行版仓库 / 官方源 / 容器）属于**部署编排**，应由 `app-deploy`
   或包管理器类插件负责；discover 只把「未安装 + Debian/RHEL 安装命令」通过 `install_hint`
   给出，并指向对应插件（`site` / `db-mysql` / `redis` / `container` 等）。
3. 若将来要做，必须先有 root 通道与审计（ADR-0018 的 `root_actions` + `root.action` 审计）
   和失败回滚设计；不满足这些前提的「一键安装」是更危险的功能，而不是更全的功能。

## 只读与资源边界

| 边界 | 实现 |
| --- | --- |
| 不装包/不改配置/不启停服务 | 只做 stat/read/glob 与固定 argv 版本查询 |
| 命令注入 | 全部 `exec.CommandContext(bin, args...)`，bin/args 来自常量表，不接收用户输入 |
| 超时 | 单条命令 3s，整次方法调用 15s（`context.WithTimeout`） |
| 缓存 | 30s 内存缓存 + mutex，调用时判断过期；无后台刷新、无 ticker（G-19） |
| 网络 | 只连 `127.0.0.1` 的服务端口（MySQL/PG/Redis/Memcached/ES + docker socket 命令）；不扫网段、不访问外网 |
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

- 假 bin 目录（`t.TempDir()` + 假 `nginx` 脚本）→ found + client_version + config_dir + source；
- 服务端版本三支（假 `mysql` / `psql` / `redis-cli`）：成功 → `server_version` + `version_source=server`；
  失败/超时 → `server_version=""` + `version_source=client`；并断言密码只走环境变量、不进 argv；
- 未安装服务 → `found=false` + `install_hint`；
- `/proc/net/tcp` fixture 十六进制换算（`0100007F:1F90` → `127.0.0.1:8080`）；
- tcp6（`::` / `::1` / IPv4-mapped）解析；
- summary 有 nginx → suggestions 含 `site`；
- 缓存 30s、`refresh`、整体超时、非 Linux `E_UNSUPPORTED`；
- 0.3.0 新增：方法级能力一致性（声明 == 实现、键语法/域/白名单、无 config.set）、
  `config.get` 三态（全缺 / 部分配置 / 不回传凭据值）、`version_hint` 三态
  （缺凭据 / 已配置但不可达 / 探到服务端版本时不出现）、
  memcached 文本握手与 Elasticsearch HTTP 指纹（本机临时监听端口模拟）、
  MongoDB（`mongosh --eval`）与 RabbitMQ（`rabbitmqctl version`）固定 argv 分支、
  新指纹未安装时 `install_hint` 齐全。

## 平台

Linux 为唯一真实实现（v1.0 GA 口径）。macOS / Windows 返回 `E_UNSUPPORTED`，
非 Linux 编译单元只提供显式 stub，不静默降级。

<!-- oss:begin -->
## 开源与贡献

[![CI](https://github.com/kore-pl/kore-plugin-discover/actions/workflows/ci.yml/badge.svg)](https://github.com/kore-pl/kore-plugin-discover/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)

本仓库是 **kore** 的 `本机服务发现插件`（id `discover`），独立仓库、独立版本线、独立 CI：GitHub Actions 只手动触发（`workflow_dispatch`），按版本 tag 自动触发在 Gitea Actions（`.gitea/workflows/ci.yml`，push `v*` + 手动）。内核与协议见 [kore](https://github.com/kore-pl/kore)。

- **许可**：MIT（inbound = outbound，不要求 CLA）；提交用 `git commit -s` 签 DCO。
- **贡献**：[CONTRIBUTING.md](CONTRIBUTING.md)（构建、门禁、发版、边界）。
- **安全**：[SECURITY.md](SECURITY.md)——漏洞不要开公开 issue。
- **行为准则**：[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。
- **兼容性**：`protocol: kore.plugin.v1` · `requires_kore: >=0.1.0 <0.2.0` · `lifecycle: lazy` · 深度等级 `production`。
- **验证方式**：本地 `make check` 优先（GitHub CI 只手动触发，Gitea CI 按版本 tag 触发；发布前 `make dist` 连跑两次核对 sha 一致）。
- **发布产物**：`make dist` → `dist/discover-0.3.1.tar.gz` + `.sha256`（位级可复现，连跑两次 sha 相同）。
- **相关仓库**：[索引 kore-plugins](https://github.com/kore-pl/kore-plugins) · [协议文档](https://github.com/kore-pl/kore/blob/main/docs/PLUGIN-PROTOCOL.md) · [SDK 指南](https://github.com/kore-pl/kore/blob/main/docs/PLUGIN-SDK.md)

<!-- oss:end -->

## 支持本项目

如果这个插件帮你省下了手工排查服务环境的功夫，欢迎去 [GitHub 仓库](https://github.com/kore-pl/kore-plugin-discover) 点个 star ⭐——你的支持就是我继续更新的动力。
