# kore-plugin-discover: guía de uso

Plugin de descubrimiento de servicios locales para kore (id `discover`): detecta automáticamente qué servicios de operaciones hay instalados en el host, dónde están y si funcionan. Es estrictamente de solo lectura: no instala paquetes, no cambia configuraciones ni inicia o detiene servicios.

## Instalación y compatibilidad

Instale el tar.gz publicado con el instalador de plugins de kore; el repositorio también se puede compilar por separado (`make build` / `make dist`). Protocolo `kore.plugin.v1`, `requires_kore: >=0.1.0 <0.2.0`, `lifecycle=lazy`, nivel de profundidad `production`. Linux es la única implementación real; en otras plataformas todos los métodos devuelven `E_UNSUPPORTED`.

## Métodos

| Método | Entrada | Resultado clave |
| --- | --- | --- |
| `kore.ping` | `{}` | `{status,uptime_s,goroutines,rss_kb}` |
| `kore.reload` | `{}` | Operación vacía sin estado, `config_digest` vacío |
| `kore.shutdown` | `{}` | `{ok:true}` y salida graceful |
| `discover.services` | `{}` o `{"refresh":true}` | Array de 22 tipos de servicios: ubicaciones, versiones de cliente/servidor, estado y sugerencias |
| `discover.ports` | `{}` o `{"refresh":true}` | Puertos LISTEN locales, direcciones, familia y nombres de proceso |
| `discover.summary` | `{}` o `{"refresh":true}` | `{ready,web_server,db,conclusion,suggestions[]}` conclusión y sugerencias de plugins |
| `discover.config.get` | `{}` | Declaraciones, estado de inyección y pistas para credenciales de sondeo (nunca devuelve valores) |

Los resultados se almacenan en caché 30 segundos; `refresh=true` fuerza una nueva detección.

## Cómo interpretar los resultados

- `found=false` incluye siempre `install_hint` (comandos para Debian/Ubuntu y RHEL); `source ∈ which|known_path|systemd|process`.
- `server_version` solo se rellena tras conectar realmente con el servidor; `version_source ∈ server|client|unknown`. `client_version` es la versión del binario y nunca debe presentarse como la del servidor.
- Si solo hay cliente, `version_hint` explica qué variable de credencial falta, dónde rellenarla o por qué el servidor es inalcanzable.
- `running` combina puertos en escucha, `/proc/<pid>/comm` y marcadores de ejecución; los procesos y puertos reenviados de contenedores no cuentan como servicios del host.

## Límite de credenciales

Las sondas de versión del servidor de MySQL / PostgreSQL / Redis usan `MYSQL_PWD` / `PGPASSWORD` / `REDISCLI_AUTH`. Las credenciales solo se leen de variables de entorno del proceso, nunca aparecen en argv, en disco ni en registros; `config.get` devuelve solo declaraciones y estado. El plugin no ofrece `config.set` y no acepta contraseñas en texto plano.

## Compilación y verificación

```bash
make check   # gofmt + go vet + go test -timeout 200s (incluye la comprobación race)
make build   # binario estático único bin/discover
make dist    # tar.gz determinista y .sha256
```

## Seguridad y contribución

No abra issues públicos para vulnerabilidades; infórmelas de forma privada según [SECURITY.md](SECURITY.md). Para contribuir, vea [CONTRIBUTING.md](CONTRIBUTING.md). Licencia MIT.
