# Changelog

## 0.3.1 (2026-11-23)

- fix: **running 判定不再把容器内进程/容器转发端口当成宿主服务**——`/proc/<pid>/cgroup` 区分宿主进程与容器进程（docker / containerd / kubepods / libpod / crio），端口监听者若是 `docker-proxy` / `containerd-shim` 只算「容器转发」；两类都置 `running=false` 并新增 `running_hint` 说明「容器内运行、宿主默认端口未监听、请用 docker port 查发布端口」
- fix: **只有客户端没有服务端**（`mariadb` 只有客户端二进制、无 `mariadbd` 宿主进程且端口由容器转发）不再报 `running=true`
- fix: **mysql / mariadb 重复计数合并**——同一客户端二进制（`os.SameFile` 确证）只保留一条，另一条 `found=false` 并说明是同一 MariaDB 客户端
- fix: **版本未知显式化**——`version_source=unknown` 时 `version_hint` 明确写「版本未知：客户端 --version 未返回可识别版本…」，不再留空字符串
- test: 新增表驱动回归（容器内进程 running=false + 解释、docker-proxy 转发端口 running=false、宿主进程/宿主监听 running=true、容器 cgroup 端到端、版本未知文案）

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.0] - 2026-11-23

### 新增

- **方法级能力（§3.5 #12）**：`info.json` 新增 `method_capabilities`，4 条全部 `system:read`
  （`discover.services` / `discover.ports` / `discover.summary` / `discover.config.get`）。
  本插件全只读，因此没有 `system:admin` 映射；`declaration_test.go` 双向锁定「声明集合 == 实现集合」、
  键语法/插件域/6 项能力白名单，并断言 `config.set` 既未声明也不被方法分发接受。
- **`discover.config.get`**：服务端探测凭据的只读声明 + 注入状态视图——列出
  `MYSQL_PWD` / `PGPASSWORD` / `REDISCLI_AUTH` 三条凭据（适用服务、用途、`configured|missing`、
  `fill_hint`、`transport`），**绝不回传凭据值**，也不提供 `config.set`（不接受明文凭据）。
  这是 §3.5 #8「插件凭据无法跨插件传递」的 discover 侧前置能力：内核凭据注入落地后
  只需注入同名环境变量，本插件无需改动。
- **`services` 条目新增 `version_hint`**：found=true 但服务端版本没拿到时给可操作提示——
  缺哪个凭据变量、去哪填（db-mysql / db-postgres / redis 配置面板）、或服务端为何不可达；
  凭据缺失明确标注为「可选」（缺失只降级版本来源，不影响插件可用性）。
- **四类新服务指纹**（端口 + 协议握手/版本串，不引入第三方依赖）：
  - `memcached`：原始 TCP 文本协议 `version` → `VERSION <x.y.z>`（真实服务端版本）；
  - `elasticsearch`：原始 TCP 最小 HTTP/1.0 `GET /` → JSON `version.number`（真实服务端版本）；
  - `mongodb`：`mongosh`/`mongo` 固定 argv `--eval "db.version()"`；
  - `rabbitmq`：`rabbitmqctl version`，回退 `rabbitmq-diagnostics -q server_version`。
  四者均只在 127.0.0.1 上探测，失败/需认证一律降级为 client/unknown，不让整表报错。

### 变更

- 探测目标从 18 项扩到 22 项（新增 memcached / mongodb / rabbitmq / elasticsearch）；
  原有服务的探测逻辑、`/proc/net` 端口反查、30s 读时缓存与 `version_source` 三态语义不变。
- 凭据仍只从进程环境变量读取（值不进 argv、不回传、不落盘）；临时验证方式仍是启动 kored 前 export，
  产品路径等内核凭据注入（§3.5 #8）。
- 版本 0.3.0；README 补方法级能力、凭据声明、新指纹与「安装动作判定不做」的裁决。

### 未做（判定）

- **安装动作不做**：装包是写操作且需要 root，属部署编排（app-deploy / 包管理器插件）；
  discover 只读发现 + `install_hint` 指向，避免把只读插件变成提权安装器。
  若将来要做，必须先有 ADR-0018 的 root 通道与审计、失败回滚设计（见 README「安装动作」节）。

## [0.2.0] - 2026-11-23

### Added

- `discover.services` 新增 `server_version`（真实服务端版本，探测不到为空）与
  `version_source`（`server|client|unknown`）：MySQL/MariaDB 走 `SELECT VERSION()`、
  PostgreSQL 走 `SHOW server_version`、Redis 走 `INFO server` 的 `redis_version`、
  nginx 走 `-v`（容器内 `docker exec nginx -v`）。
- 服务端探测按目标逐个实现，失败/超时/无凭据一律留空并降级 `version_source=client`，
  不让整张服务清单报错。
- 宿主没有 `redis-cli`、Redis 只在容器里时回退 `docker exec`；nginx 仅容器进程时同样
  走 `docker exec`。
- MySQL/MariaDB 共用的 3306 按 `SELECT VERSION()` 的服务端类型归属，避免把 MySQL
  服务端挂到 mariadb 条目下（反之亦然）。
- 单元测试覆盖 mysql / psql / redis-cli 三支的成功、失败、超时，并断言
  `MYSQL_PWD` / `PGPASSWORD` / `REDISCLI_AUTH` 只经环境变量传递、不进入 argv；
  E2E 增加版本语义断言与真机期望值校验。

### Changed

- **字段破坏性变更**：`discover.services` 的 `version` 更名为 `client_version`。
  消费方（数据库/Redis 页自动检测横幅）应改用 `server_version` 展示服务端版本；
  只有客户端版本时提示"未连上服务端，需要密码"。

### Fixed

- 自动检测横幅曾把 `mysql --version` 的客户端版本（如 MariaDB 11.8.6）当服务端版本，
  与实例卡片的真实服务端版本（如 MySQL 9.7.1）并存，造成"本机有两套库"的误读。

## [0.1.0] - 2026-11-23

### Added

- 本机服务发现插件（production，lazy）：`discover.services` / `discover.ports` /
  `discover.summary` 三个只读方法。
- `discover.services`：探测 nginx / caddy / apache2 / httpd、mysql / mariadb / postgres、
  redis、php / node / python3、docker / podman、crontab / systemctl / ufw / iptables /
  firewall-cmd；返回 `id/name/found/bin/version/config_dir/running/port/source`，
  未安装项带 Debian/Ubuntu + RHEL 的 `install_hint`。
- 探测线索：PATH（等效只读 which）→ 常见安装路径（含 glob）→ systemd unit；
  `source ∈ which|known_path|systemd`。数据库/缓存的 `running` 结合监听端口判定。
- 版本探测固定 argv（nginx/apache `-v`、caddy `version`、其余 `--version`），
  单条命令 3s 超时；版本输出解析覆盖 nginx/apache/mysql/postgres/redis/node/python/
  docker/podman/caddy 等真实格式。
- `discover.ports`：读 `/proc/net/tcp` + `/proc/net/tcp6`，解析小端十六进制地址，
  只返回 LISTEN 条目；按 inode 匹配 `/proc/*/fd` 反查进程名（非 root 查不到时为 null）。
  输出按端口/协议族/地址稳定排序。
- `discover.summary`：一句话结论 + 按优先级生成的插件建议（site / db-mysql /
  db-postgres / redis / container / runtime-* / cron / firewall）。
- 缓存：30s 内存缓存 + mutex，调用时判断过期，零 ticker、无后台刷新；
  `{"refresh":true}` 可强制重探。整次方法调用 15s 预算。
- 平台隔离：`/proc/net/*` 读取只在 Linux 编译单元；非 Linux 统一 `E_UNSUPPORTED`。
- 测试：假 bin 目录单元测试、`/proc/net/tcp` + tcp6 fixture、summary 建议、
  缓存/超时/平台分支；四类故障注入（kill / timeout / disk / cgroup）；
  真实二进制 E2E（services → ports 与 `ss -ltn` 对照 → summary → shutdown）。
- 构建：确定性打包（显式条目 + 固定 mtime/owner + gzip -9n），`size_bytes` 回填，
  两次 `make dist` 的 sha256 一致。
