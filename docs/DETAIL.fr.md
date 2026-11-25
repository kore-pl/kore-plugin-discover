# kore-plugin-discover — guide d'utilisation

Plugin de découverte des services locaux pour kore (id `discover`) : détecte automatiquement les services d'exploitation installés sur l'hôte, leur emplacement et leur disponibilité. Strictement en lecture seule — aucune installation de paquet, aucune modification de configuration, aucun démarrage ou arrêt de service.

## Installation et compatibilité

Installez le tar.gz publié via l'installateur de plugins de kore ; le dépôt peut aussi être compilé seul (`make build` / `make dist`). Protocole `kore.plugin.v1`, `requires_kore: >=0.1.0 <0.2.0`, `lifecycle=lazy`, niveau de profondeur `production`. Linux est la seule implémentation réelle ; sur les autres plateformes, toutes les méthodes renvoient `E_UNSUPPORTED`.

## Méthodes

| Méthode | Entrée | Résultat clé |
| --- | --- | --- |
| `kore.ping` | `{}` | `{status,uptime_s,goroutines,rss_kb}` |
| `kore.reload` | `{}` | Opération vide sans état, `config_digest` vide |
| `kore.shutdown` | `{}` | `{ok:true}` puis arrêt propre |
| `discover.services` | `{}` ou `{"refresh":true}` | Tableau de 22 types de services : emplacements, versions client/serveur, état et conseils |
| `discover.ports` | `{}` ou `{"refresh":true}` | Ports LISTEN locaux, adresses, famille et noms de processus |
| `discover.summary` | `{}` ou `{"refresh":true}` | `{ready,web_server,db,conclusion,suggestions[]}` conclusion et suggestions de plugins |
| `discover.config.get` | `{}` | Déclarations, état d'injection et aides pour les identifiants de sonde (jamais les valeurs) |

Les résultats sont mis en cache 30 secondes ; `refresh=true` force une nouvelle détection.

## Interpréter les résultats

- `found=false` inclut toujours `install_hint` (commandes Debian/Ubuntu et RHEL) ; `source ∈ which|known_path|systemd|process`.
- `server_version` n'est renseignée qu'après une connexion réelle au serveur ; `version_source ∈ server|client|unknown`. `client_version` est la version du binaire et ne doit jamais être présentée comme celle du serveur.
- Si seul le client est présent, `version_hint` indique quelle variable d'identifiants manque, où la renseigner ou pourquoi le serveur est inaccessible.
- `running` combine les ports à l'écoute, `/proc/<pid>/comm` et des marqueurs d'exécution ; les processus et ports redirigés des conteneurs ne comptent pas comme services de l'hôte.

## Limite des identifiants

Les sondes de version serveur de MySQL / PostgreSQL / Redis utilisent `MYSQL_PWD` / `PGPASSWORD` / `REDISCLI_AUTH`. Les identifiants ne sont lus que depuis les variables d'environnement du processus, jamais dans argv, sur disque ou dans les journaux ; `config.get` ne renvoie que les déclarations et l'état. Le plugin ne propose pas `config.set` et n'accepte pas de mots de passe en clair.

## Compilation et vérification

```bash
make check   # gofmt + go vet + go test -timeout 200s (y compris le contrôle race)
make build   # binaire statique unique bin/discover
make dist    # tar.gz déterministe et .sha256
```

## Sécurité et contribution

N'ouvrez pas d'issue publique pour une vulnérabilité ; signalez-la de façon privée selon [SECURITY.md](SECURITY.md). Voir [CONTRIBUTING.md](CONTRIBUTING.md) pour contribuer. Licence MIT.
