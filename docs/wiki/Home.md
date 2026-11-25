# 本机服务发现插件 Wiki

语言：简体中文（本 Wiki 以简体中文为主，完整七语言说明见仓库 `docs/DETAIL*.md`）。

页面：[Home](https://github.com/kore-pl/kore-plugin-discover/wiki/Home) · [Installation](https://github.com/kore-pl/kore-plugin-discover/wiki/Installation) · [Configuration](https://github.com/kore-pl/kore-plugin-discover/wiki/Configuration) · [Usage](https://github.com/kore-pl/kore-plugin-discover/wiki/Usage) · [FAQ](https://github.com/kore-pl/kore-plugin-discover/wiki/FAQ) · [Changelog](https://github.com/kore-pl/kore-plugin-discover/wiki/Changelog)

## 这是什么

本机服务发现插件（id `discover`）是 kore 的纯只读的本机服务发现：自动探测本机装了哪些运维服务、在哪、能不能用。不装包、不改配置、不启停服务。

## 核心能力

- 22 类服务指纹（nginx/caddy/apache、MySQL/MariaDB/PostgreSQL、Redis/Memcached、MongoDB/RabbitMQ/Elasticsearch、运行时与容器等）
- 服务端版本真实探测（version_source=server/client/unknown，绝不拿客户端版本冒充）
- 凭据只经环境变量（MYSQL_PWD / PGPASSWORD / REDISCLI_AUTH），config.get 只回声明与状态
- discover.ports 从 /proc/net/tcp(+tcp6) 读 LISTEN 端口并反查进程名
- discover.summary 给一句话就绪结论与插件建议
- 30s 读时缓存，refresh=true 强制重探；非 Linux 显式 E_UNSUPPORTED

协议 `kore.plugin.v1`，`lifecycle=lazy`，深度等级见仓库 `info.json`。内核、协议与 SDK 文档见 [kore](https://github.com/kore-pl/kore)。

## 快速开始

1. 安装：见 [Installation](https://github.com/kore-pl/kore-plugin-discover/wiki/Installation)。
2. 配置：见 [Configuration](https://github.com/kore-pl/kore-plugin-discover/wiki/Configuration)。
3. 调用：见 [Usage](https://github.com/kore-pl/kore-plugin-discover/wiki/Usage) 与仓库 README 的方法表。
4. 遇到问题：见 [FAQ](https://github.com/kore-pl/kore-plugin-discover/wiki/FAQ)。
