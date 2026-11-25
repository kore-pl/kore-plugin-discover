# kore-plugin-discover 使用说明

kore 的本机服务发现插件（id `discover`）：自动探测本机装了哪些运维服务、在哪、能不能用。纯只读——不装包、不改配置、不启停服务。

## 安装与兼容性

由 kore 内核的插件安装器安装本插件发布的 tar.gz；仓库可独立构建（`make build` / `make dist`）。协议 `kore.plugin.v1`，`requires_kore: >=0.1.0 <0.2.0`，`lifecycle=lazy`，深度等级 `production`。Linux 为唯一真实实现，其它平台的方法统一返回 `E_UNSUPPORTED`。

## 调用方法

| 方法 | 入参 | 结果要点 |
| --- | --- | --- |
| `kore.ping` | `{}` | `{status,uptime_s,goroutines,rss_kb}` |
| `kore.reload` | `{}` | 无状态空操作，`config_digest` 为空 |
| `kore.shutdown` | `{}` | `{ok:true}` 后优雅退出 |
| `discover.services` | `{}` 或 `{"refresh":true}` | 22 类服务数组：安装位置、客户端/服务端版本、运行状态与提示 |
| `discover.ports` | `{}` 或 `{"refresh":true}` | 本机 LISTEN 端口、地址、协议族与进程名 |
| `discover.summary` | `{}` 或 `{"refresh":true}` | `{ready,web_server,db,conclusion,suggestions[]}` 一句话结论与插件建议 |
| `discover.config.get` | `{}` | 服务端探测所需凭据的声明、注入状态与填写提示（不回传凭据值） |

结果缓存 30 秒；带 `refresh=true` 跳过缓存强制重探。

## 结果判读

- `found=false` 必带 `install_hint`（Debian/Ubuntu 与 RHEL 安装命令）；`source ∈ which|known_path|systemd|process`。
- `server_version` 只在真正连上服务端后填写；`version_source ∈ server|client|unknown`。`client_version` 只是二进制自身版本，绝不能冒充服务端版本。
- 客户端有、服务端没探到时给 `version_hint`：缺哪个凭据变量、去哪填、或服务端为何不可达。
- `running` 由监听端口、`/proc/<pid>/comm` 和特定运行标记共同判定；容器内进程与容器转发端口不算宿主服务。

## 凭据边界

MySQL / PostgreSQL / Redis 的服务端版本探测分别可用 `MYSQL_PWD` / `PGPASSWORD` / `REDISCLI_AUTH`。凭据只从进程环境变量读取，不进 argv、不落盘、不进日志；`config.get` 只回声明与状态，插件不提供 `config.set`，不接受明文密码。

## 构建与验证

```bash
make check   # gofmt + go vet + go test -timeout 200s（含 race 门禁）
make build   # 静态单二进制 bin/discover
make dist    # 确定性 tar.gz 与 .sha256
```

## 安全与贡献

漏洞不要开公开 issue，按 [SECURITY.md](SECURITY.md) 私密披露；贡献见 [CONTRIBUTING.md](CONTRIBUTING.md)。MIT 许可。
