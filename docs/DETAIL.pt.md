# kore-plugin-discover — guia de uso

Plugin de descoberta de serviços locais para o kore (id `discover`): detecta automaticamente quais serviços de operações estão instalados no host, onde estão e se funcionam. Estritamente somente leitura — não instala pacotes, não altera configurações nem inicia ou para serviços.

## Instalação e compatibilidade

Instale o tar.gz publicado com o instalador de plugins do kore; o repositório também pode ser compilado isoladamente (`make build` / `make dist`). Protocolo `kore.plugin.v1`, `requires_kore: >=0.1.0 <0.2.0`, `lifecycle=lazy`, nível de profundidade `production`. Linux é a única implementação real; nas demais plataformas todos os métodos retornam `E_UNSUPPORTED`.

## Métodos

| Método | Entrada | Resultado principal |
| --- | --- | --- |
| `kore.ping` | `{}` | `{status,uptime_s,goroutines,rss_kb}` |
| `kore.reload` | `{}` | Operação vazia sem estado, `config_digest` vazio |
| `kore.shutdown` | `{}` | `{ok:true}` e saída graciosa |
| `discover.services` | `{}` ou `{"refresh":true}` | Array de 22 tipos de serviços: localizações, versões de cliente/servidor, estado e dicas |
| `discover.ports` | `{}` ou `{"refresh":true}` | Portas LISTEN locais, endereços, família e nomes de processos |
| `discover.summary` | `{}` ou `{"refresh":true}` | `{ready,web_server,db,conclusion,suggestions[]}` conclusão e sugestões de plugins |
| `discover.config.get` | `{}` | Declarações, estado de injeção e dicas para credenciais de sonda (nunca os valores) |

Os resultados ficam em cache por 30 segundos; `refresh=true` força uma nova descoberta.

## Como interpretar os resultados

- `found=false` sempre traz `install_hint` (comandos para Debian/Ubuntu e RHEL); `source ∈ which|known_path|systemd|process`.
- `server_version` só é preenchida após conexão real com o servidor; `version_source ∈ server|client|unknown`. `client_version` é a versão do binário e nunca deve ser apresentada como a do servidor.
- Quando só há o cliente, `version_hint` explica qual variável de credencial falta, onde preenchê-la ou por que o servidor está inacessível.
- `running` combina portas em escuta, `/proc/<pid>/comm` e marcadores de execução; processos e portas redirecionadas de contêineres não contam como serviços do host.

## Limite de credenciais

As sondas de versão do servidor de MySQL / PostgreSQL / Redis usam `MYSQL_PWD` / `PGPASSWORD` / `REDISCLI_AUTH`. As credenciais são lidas apenas de variáveis de ambiente do processo, nunca em argv, em disco ou em logs; `config.get` retorna apenas declarações e estado. O plugin não oferece `config.set` e não aceita senhas em texto plano.

## Compilação e verificação

```bash
make check   # gofmt + go vet + go test -timeout 200s (inclui a verificação race)
make build   # binário estático único bin/discover
make dist    # tar.gz determinístico e .sha256
```

## Segurança e contribuição

Não abra issues públicas para vulnerabilidades; divulgue-as privadamente conforme [SECURITY.md](SECURITY.md). Para contribuir, veja [CONTRIBUTING.md](CONTRIBUTING.md). Licença MIT.
