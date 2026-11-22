# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
