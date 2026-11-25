# kore-plugin-discover Usage

A local service discovery plugin for kore (id `discover`): automatically detects which operations services are installed on the host, where they are, and whether they work. Strictly read-only — no package installation, no configuration changes, no service start/stop.

## Installation and compatibility

Install the released tar.gz with the kore plugin installer; the repository can also be built standalone (`make build` / `make dist`). Protocol `kore.plugin.v1`, `requires_kore: >=0.1.0 <0.2.0`, `lifecycle=lazy`, depth level `production`. Linux is the only real implementation; all methods return `E_UNSUPPORTED` on other platforms.

## Methods

| Method | Input | Key result |
| --- | --- | --- |
| `kore.ping` | `{}` | `{status,uptime_s,goroutines,rss_kb}` |
| `kore.reload` | `{}` | Stateless no-op with empty `config_digest` |
| `kore.shutdown` | `{}` | `{ok:true}`, then graceful exit |
| `discover.services` | `{}` or `{"refresh":true}` | Array of 22 service types: locations, client/server versions, running state and hints |
| `discover.ports` | `{}` or `{"refresh":true}` | Local LISTEN ports, addresses, family and process names |
| `discover.summary` | `{}` or `{"refresh":true}` | `{ready,web_server,db,conclusion,suggestions[]}` one-line conclusion and plugin suggestions |
| `discover.config.get` | `{}` | Declarations, injection status and fill hints for probe credentials (values never returned) |

Results are cached for 30 seconds; `refresh=true` forces a fresh probe.

## Reading results

- `found=false` always includes `install_hint` (Debian/Ubuntu and RHEL commands); `source ∈ which|known_path|systemd|process`.
- `server_version` is filled only after a successful real server connection; `version_source ∈ server|client|unknown`. `client_version` is the binary's own version and must never be passed off as the server version.
- When only a client is present, `version_hint` explains which credential variable is missing, where to fill it, or why the server is unreachable.
- `running` combines listening ports, `/proc/<pid>/comm` and specific runtime markers; container processes and forwarded container ports do not count as host services.

## Credential boundary

Server version probes for MySQL / PostgreSQL / Redis use `MYSQL_PWD` / `PGPASSWORD` / `REDISCLI_AUTH` respectively. Credentials are read from process environment variables only, never in argv, on disk, or in logs; `config.get` returns declarations and status only. The plugin has no `config.set` and does not accept plaintext passwords.

## Build and verification

```bash
make check   # gofmt + go vet + go test -timeout 200s (including race gate)
make build   # static single binary bin/discover
make dist    # deterministic tar.gz and .sha256
```

## Security and contributing

Do not open public issues for vulnerabilities; disclose privately per [SECURITY.md](SECURITY.md). See [CONTRIBUTING.md](CONTRIBUTING.md) for contributions. MIT licensed.
