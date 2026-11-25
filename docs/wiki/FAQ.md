# 本机服务发现插件 Wiki

语言：简体中文（本 Wiki 以简体中文为主，完整七语言说明见仓库 `docs/DETAIL*.md`）。

页面：[Home](https://github.com/kore-pl/kore-plugin-discover/wiki/Home) · [Installation](https://github.com/kore-pl/kore-plugin-discover/wiki/Installation) · [Configuration](https://github.com/kore-pl/kore-plugin-discover/wiki/Configuration) · [Usage](https://github.com/kore-pl/kore-plugin-discover/wiki/Usage) · [FAQ](https://github.com/kore-pl/kore-plugin-discover/wiki/FAQ) · [Changelog](https://github.com/kore-pl/kore-plugin-discover/wiki/Changelog)

## 常见问题

### 要不要 root？

不要。全部只读探测，凭据经环境变量传子进程，不进 argv。

### 为什么 found=true 但服务端版本为空？

只探到客户端二进制，或服务端未启动/认证失败/非默认端口；看 version_hint 的具体提示。

### 能装缺失的服务吗？

不能。只给 Debian/Ubuntu 与 RHEL 的 install_hint，安装由 app-deploy 等插件负责。

### 支持哪些平台？

Linux 为唯一真实实现，其它平台返回 E_UNSUPPORTED。

### 去哪里报 bug / 提需求？

在仓库开 issue；安全漏洞不要开公开 issue，按 [SECURITY.md](https://github.com/kore-pl/kore-plugin-discover/blob/main/SECURITY.md) 私密披露。
