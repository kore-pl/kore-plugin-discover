package probe

// serviceSpec 是一条服务探测规则：命令名、常见路径、systemd unit、配置文件目录、
// 版本参数、服务端端口、进程名与安装提示。所有探测都是只读路径/命令检查。
type serviceSpec struct {
	// ID / Name 是输出条目的稳定标识与展示名。
	ID   string
	Name string
	// Bins 是按 PATH 查找的命令名（等价 which 的只读实现）。
	Bins []string
	// KnownPaths 是常见安装路径；支持 glob（如 postgres 的版本化目录）。
	KnownPaths []string
	// Units 是 systemd unit 名；命中即 found=true、source=systemd。
	Units []string
	// ConfigDirs 是按顺序检查的配置文件目录，取第一个存在项。
	ConfigDirs []string
	// VersionArgs 是固定版本参数；为空时取 --version。
	VersionArgs []string
	// ServerProbe 是服务端版本探测方式：mysql / postgres / redis / nginx；
	// 空表示该目标没有独立服务端（只报客户端版本）。
	ServerProbe string
	// ClientBins 是服务端探测优先使用的客户端命令名；为空时回退到 Bins。
	ClientBins []string
	// Port 是服务端默认端口；0 表示不按端口判定。
	Port int
	// ProcessNames 是 /proc/<pid>/comm 里代表该服务在运行的进程名（小写）。
	ProcessNames []string
	// RunningPath 存在即视为运行中（如 docker.sock、/run/systemd/system）。
	RunningPath string
	// EnabledConf 是内容含 ENABLED=yes 即视为启用的配置文件（ufw）。
	EnabledConf string
	// InstallHint 是 found=false 时给小白看的安装提示（Debian/Ubuntu + RHEL）。
	InstallHint string
}

// versionArgs 返回该服务的版本查询参数。
func (s serviceSpec) versionArgs() []string {
	if len(s.VersionArgs) > 0 {
		return s.VersionArgs
	}
	return []string{"--version"}
}

// catalog 是 discover.services 的探测目标表，顺序即输出顺序。
// 覆盖任务要求的全部目标：web server / 数据库 / 缓存 / 运行时 / 容器 / 系统工具。
var catalog = []serviceSpec{
	{
		ID: "nginx", Name: "Nginx",
		Bins: []string{"nginx"},
		KnownPaths: []string{
			"/usr/sbin/nginx", "/usr/local/sbin/nginx", "/usr/local/bin/nginx",
			"/opt/nginx/sbin/nginx",
		},
		Units:        []string{"nginx.service"},
		ConfigDirs:   []string{"/etc/nginx", "/usr/local/nginx/conf", "/usr/local/etc/nginx"},
		VersionArgs:  []string{"-v"},
		ServerProbe:  "nginx",
		ClientBins:   []string{"nginx"},
		Port:         80,
		ProcessNames: []string{"nginx"},
		InstallHint:  "Debian/Ubuntu: apt install nginx；RHEL: dnf install nginx",
	},
	{
		ID: "caddy", Name: "Caddy",
		Bins:         []string{"caddy"},
		KnownPaths:   []string{"/usr/bin/caddy", "/usr/local/bin/caddy"},
		Units:        []string{"caddy.service"},
		ConfigDirs:   []string{"/etc/caddy", "/usr/local/etc/caddy"},
		VersionArgs:  []string{"version"},
		Port:         80,
		ProcessNames: []string{"caddy"},
		InstallHint:  "Debian/Ubuntu: apt install caddy；RHEL: dnf install caddy（或从 caddyserver.com 下载）",
	},
	{
		ID: "apache2", Name: "Apache HTTP Server",
		Bins:         []string{"apache2", "apachectl"},
		KnownPaths:   []string{"/usr/sbin/apache2", "/usr/sbin/apachectl"},
		Units:        []string{"apache2.service"},
		ConfigDirs:   []string{"/etc/apache2"},
		VersionArgs:  []string{"-v"},
		Port:         80,
		ProcessNames: []string{"apache2"},
		InstallHint:  "Debian/Ubuntu: apt install apache2；RHEL: dnf install httpd",
	},
	{
		ID: "httpd", Name: "Apache httpd",
		Bins:         []string{"httpd"},
		KnownPaths:   []string{"/usr/sbin/httpd", "/usr/local/apache2/bin/httpd"},
		Units:        []string{"httpd.service"},
		ConfigDirs:   []string{"/etc/httpd", "/usr/local/apache2/conf"},
		VersionArgs:  []string{"-v"},
		Port:         80,
		ProcessNames: []string{"httpd"},
		InstallHint:  "RHEL: dnf install httpd；Debian/Ubuntu: apt install apache2",
	},
	{
		ID: "mysql", Name: "MySQL",
		Bins:         []string{"mysql", "mysqld"},
		KnownPaths:   []string{"/usr/bin/mysql", "/usr/sbin/mysqld", "/usr/local/mysql/bin/mysql"},
		Units:        []string{"mysql.service", "mysqld.service"},
		ConfigDirs:   []string{"/etc/mysql", "/etc/my.cnf.d"},
		ServerProbe:  "mysql",
		ClientBins:   []string{"mysql"},
		Port:         3306,
		ProcessNames: []string{"mysqld"},
		InstallHint:  "Debian/Ubuntu: apt install mysql-client；RHEL: dnf install mysql",
	},
	{
		ID: "mariadb", Name: "MariaDB",
		Bins:         []string{"mariadb", "mariadbd"},
		KnownPaths:   []string{"/usr/bin/mariadb", "/usr/sbin/mariadbd"},
		Units:        []string{"mariadb.service"},
		ConfigDirs:   []string{"/etc/mysql/mariadb.conf.d", "/etc/mysql"},
		ServerProbe:  "mysql",
		ClientBins:   []string{"mariadb", "mysql"},
		Port:         3306,
		ProcessNames: []string{"mariadbd"},
		InstallHint:  "Debian/Ubuntu: apt install mariadb-client；RHEL: dnf install mariadb",
	},
	{
		ID: "postgres", Name: "PostgreSQL",
		Bins: []string{"psql", "postgres", "pg_ctl"},
		KnownPaths: []string{
			"/usr/bin/psql", "/usr/lib/postgresql/*/bin/postgres",
			"/usr/local/pgsql/bin/postgres",
		},
		Units:        []string{"postgresql.service", "postgres.service"},
		ConfigDirs:   []string{"/etc/postgresql", "/var/lib/pgsql/data"},
		ServerProbe:  "postgres",
		ClientBins:   []string{"psql"},
		Port:         5432,
		ProcessNames: []string{"postgres"},
		InstallHint:  "Debian/Ubuntu: apt install postgresql-client；RHEL: dnf install postgresql",
	},
	{
		ID: "mongodb", Name: "MongoDB",
		Bins:         []string{"mongod", "mongosh", "mongo"},
		KnownPaths:   []string{"/usr/bin/mongod", "/usr/local/bin/mongod", "/usr/bin/mongosh", "/usr/local/bin/mongosh"},
		Units:        []string{"mongod.service", "mongodb.service"},
		ConfigDirs:   []string{"/etc/mongodb"},
		ServerProbe:  "mongodb",
		ClientBins:   []string{"mongosh", "mongo"},
		Port:         27017,
		ProcessNames: []string{"mongod"},
		InstallHint:  "Debian/Ubuntu: apt install mongodb-org（需 MongoDB 官方源）；RHEL: dnf install mongodb-org-server",
	},
	{
		ID: "redis", Name: "Redis",
		Bins:         []string{"redis-server", "redis-cli"},
		KnownPaths:   []string{"/usr/bin/redis-server", "/usr/local/bin/redis-server"},
		Units:        []string{"redis-server.service", "redis.service"},
		ConfigDirs:   []string{"/etc/redis", "/usr/local/etc/redis"},
		ServerProbe:  "redis",
		ClientBins:   []string{"redis-cli"},
		Port:         6379,
		ProcessNames: []string{"redis-server"},
		InstallHint:  "Debian/Ubuntu: apt install redis-server；RHEL: dnf install redis",
	},
	{
		ID: "memcached", Name: "Memcached",
		Bins:         []string{"memcached"},
		KnownPaths:   []string{"/usr/bin/memcached", "/usr/local/bin/memcached"},
		Units:        []string{"memcached.service"},
		ServerProbe:  "memcached",
		Port:         11211,
		ProcessNames: []string{"memcached"},
		InstallHint:  "Debian/Ubuntu: apt install memcached；RHEL: dnf install memcached",
	},
	{
		ID: "rabbitmq", Name: "RabbitMQ",
		Bins:         []string{"rabbitmqctl", "rabbitmq-server", "rabbitmq-diagnostics"},
		KnownPaths:   []string{"/usr/sbin/rabbitmqctl", "/usr/sbin/rabbitmq-server", "/usr/lib/rabbitmq/bin/rabbitmqctl"},
		Units:        []string{"rabbitmq-server.service"},
		ConfigDirs:   []string{"/etc/rabbitmq"},
		ServerProbe:  "rabbitmq",
		ClientBins:   []string{"rabbitmqctl", "rabbitmq-diagnostics"},
		Port:         5672,
		ProcessNames: []string{"rabbitmq-server"},
		InstallHint:  "Debian/Ubuntu: apt install rabbitmq-server；RHEL: dnf install rabbitmq-server",
	},
	{
		ID: "elasticsearch", Name: "Elasticsearch",
		Bins:        []string{"elasticsearch", "elasticsearch-cli"},
		KnownPaths:  []string{"/usr/share/elasticsearch/bin/elasticsearch", "/usr/bin/elasticsearch"},
		Units:       []string{"elasticsearch.service"},
		ConfigDirs:  []string{"/etc/elasticsearch"},
		ServerProbe: "elasticsearch",
		Port:        9200,
		InstallHint: "Debian/Ubuntu: apt install elasticsearch（需 Elastic 官方源）；RHEL: dnf install elasticsearch",
	},
	{
		ID: "php", Name: "PHP",
		Bins:         []string{"php"},
		KnownPaths:   []string{"/usr/bin/php", "/usr/local/bin/php"},
		ConfigDirs:   []string{"/etc/php", "/usr/local/etc/php"},
		ProcessNames: []string{"php-fpm"},
		InstallHint:  "Debian/Ubuntu: apt install php-cli；RHEL: dnf install php-cli",
	},
	{
		ID: "node", Name: "Node.js",
		Bins:         []string{"node", "nodejs"},
		KnownPaths:   []string{"/usr/bin/node", "/usr/local/bin/node", "/usr/bin/nodejs"},
		ConfigDirs:   []string{"/etc/node"},
		ProcessNames: []string{"node"},
		InstallHint:  "Debian/Ubuntu: apt install nodejs；RHEL: dnf install nodejs",
	},
	{
		ID: "python3", Name: "Python 3",
		Bins:         []string{"python3"},
		KnownPaths:   []string{"/usr/bin/python3", "/usr/local/bin/python3"},
		ConfigDirs:   []string{"/etc/python3"},
		ProcessNames: []string{"python3"},
		InstallHint:  "Debian/Ubuntu: apt install python3；RHEL: dnf install python3",
	},
	{
		ID: "docker", Name: "Docker",
		Bins:         []string{"docker"},
		KnownPaths:   []string{"/usr/bin/docker", "/usr/local/bin/docker", "/snap/bin/docker"},
		Units:        []string{"docker.service"},
		ConfigDirs:   []string{"/etc/docker"},
		Port:         2375,
		ProcessNames: []string{"dockerd"},
		RunningPath:  "/var/run/docker.sock",
		InstallHint:  "Debian/Ubuntu: apt install docker.io；RHEL: dnf install docker（或安装 docker-ce）",
	},
	{
		ID: "podman", Name: "Podman",
		Bins:         []string{"podman"},
		KnownPaths:   []string{"/usr/bin/podman", "/usr/local/bin/podman"},
		Units:        []string{"podman.service", "podman.socket"},
		ConfigDirs:   []string{"/etc/containers"},
		Port:         2375,
		ProcessNames: []string{"podman"},
		RunningPath:  "/run/podman/podman.sock",
		InstallHint:  "Debian/Ubuntu: apt install podman；RHEL: dnf install podman",
	},
	{
		ID: "crontab", Name: "Cron",
		Bins:         []string{"crontab"},
		KnownPaths:   []string{"/usr/bin/crontab", "/usr/sbin/crontab"},
		Units:        []string{"cron.service", "crond.service"},
		ConfigDirs:   []string{"/etc/cron.d", "/etc/crontabs"},
		ProcessNames: []string{"cron", "crond"},
		InstallHint:  "Debian/Ubuntu: apt install cron；RHEL: dnf install cronie",
	},
	{
		ID: "systemctl", Name: "systemd",
		Bins:         []string{"systemctl"},
		KnownPaths:   []string{"/usr/bin/systemctl", "/bin/systemctl"},
		ConfigDirs:   []string{"/etc/systemd/system"},
		ProcessNames: []string{"systemd"},
		RunningPath:  "/run/systemd/system",
		InstallHint:  "systemd 通常随系统自带；容器/Alpine 环境可能不可用（Alpine 使用 OpenRC）",
	},
	{
		ID: "ufw", Name: "UFW",
		Bins:        []string{"ufw"},
		KnownPaths:  []string{"/usr/sbin/ufw", "/usr/bin/ufw"},
		ConfigDirs:  []string{"/etc/ufw"},
		EnabledConf: "/etc/ufw/ufw.conf",
		InstallHint: "Debian/Ubuntu: apt install ufw；RHEL 用 firewalld: dnf install firewalld",
	},
	{
		ID: "iptables", Name: "iptables",
		Bins:        []string{"iptables"},
		KnownPaths:  []string{"/usr/sbin/iptables", "/sbin/iptables"},
		ConfigDirs:  []string{"/etc/iptables"},
		InstallHint: "Debian/Ubuntu: apt install iptables；RHEL: dnf install iptables",
	},
	{
		ID: "firewall-cmd", Name: "firewalld",
		Bins:         []string{"firewall-cmd"},
		KnownPaths:   []string{"/usr/bin/firewall-cmd", "/usr/sbin/firewall-cmd"},
		Units:        []string{"firewalld.service"},
		ConfigDirs:   []string{"/etc/firewalld"},
		ProcessNames: []string{"firewalld"},
		InstallHint:  "RHEL: dnf install firewalld；Debian/Ubuntu 用 ufw: apt install ufw",
	},
}

// systemdUnitDirs 是 systemd unit 文件的只读检查目录。
var systemdUnitDirs = []string{
	"/etc/systemd/system",
	"/lib/systemd/system",
	"/usr/lib/systemd/system",
}
