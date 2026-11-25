# kore-plugin-discover — Anwendungshandbuch

Ein Plugin zur Erkennung lokaler Dienste für kore (id `discover`): es erkennt automatisch, welche Betriebsdienste auf dem Host installiert sind, wo sie sich befinden und ob sie funktionieren. Streng lesend — keine Paketinstallation, keine Konfigurationsänderung, kein Starten oder Stoppen von Diensten.

## Installation und Kompatibilität

Installieren Sie das veröffentlichte tar.gz mit dem kore-Plugin-Installer; das Repository kann auch eigenständig gebaut werden (`make build` / `make dist`). Protokoll `kore.plugin.v1`, `requires_kore: >=0.1.0 <0.2.0`, `lifecycle=lazy`, Tiefe `production`. Linux ist die einzige echte Implementierung; auf anderen Plattformen liefern alle Methoden `E_UNSUPPORTED`.

## Methoden

| Methode | Eingabe | Wichtigstes Ergebnis |
| --- | --- | --- |
| `kore.ping` | `{}` | `{status,uptime_s,goroutines,rss_kb}` |
| `kore.reload` | `{}` | Zustandslose Leeroperation, `config_digest` leer |
| `kore.shutdown` | `{}` | `{ok:true}`, dann sauberer Exit |
| `discover.services` | `{}` oder `{"refresh":true}` | Array von 22 Diensttypen: Pfade, Client-/Serverversionen, Laufzustand und Hinweise |
| `discover.ports` | `{}` oder `{"refresh":true}` | Lokale LISTEN-Ports, Adressen, Familie und Prozessnamen |
| `discover.summary` | `{}` oder `{"refresh":true}` | `{ready,web_server,db,conclusion,suggestions[]}` Ein-Satz-Fazit und Plugin-Empfehlungen |
| `discover.config.get` | `{}` | Deklarationen, Injektionsstatus und Hinweise für Sondierungs-Zugangsdaten (nie die Werte) |

Ergebnisse werden 30 Sekunden zwischengespeichert; `refresh=true` erzwingt eine neue Erkennung.

## Ergebnisse lesen

- `found=false` enthält immer `install_hint` (Befehle für Debian/Ubuntu und RHEL); `source ∈ which|known_path|systemd|process`.
- `server_version` wird nur nach echter Serververbindung gefüllt; `version_source ∈ server|client|unknown`. `client_version` ist die Version der Binary und darf niemals als Serverversion ausgegeben werden.
- Fehlt der Server, erklärt `version_hint`, welche Zugangsdatenvariable fehlt, wo sie einzutragen ist oder warum der Server unreachable ist.
- `running` kombiniert lauschende Ports, `/proc/<pid>/comm` und Laufmarker; Container-Prozesse und weitergeleitete Container-Ports zählen nicht als Host-Dienste.

## Zugangsdaten-Grenze

Die Serverversions-Sonden für MySQL / PostgreSQL / Redis nutzen `MYSQL_PWD` / `PGPASSWORD` / `REDISCLI_AUTH`. Zugangsdaten werden nur aus Prozessumgebungsvariablen gelesen, nie in argv, auf Platte oder in Logs; `config.get` liefert nur Deklarationen und Status. Das Plugin bietet kein `config.set` und akzeptiert keine Klartextpasswörter.

## Bauen und prüfen

```bash
make check   # gofmt + go vet + go test -timeout 200s (inkl. Race-Gate)
make build   # statische Einzel-Binary bin/discover
make dist    # deterministisches tar.gz und .sha256
```

## Sicherheit und Mitwirkung

Melden Sie Schwachstellen nicht in öffentlichen Issues, sondern privat gemäß [SECURITY.md](SECURITY.md). Beiträge: [CONTRIBUTING.md](CONTRIBUTING.md). MIT-Lizenz.
