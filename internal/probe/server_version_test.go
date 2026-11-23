package probe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeArgvScript 生成一个假客户端脚本：`--version` 返回 clientOutput，
// 其余调用把 argv 写入 $ARGV_FILE，并按 body 的尾部逻辑返回。
func scriptWithArgvDump(clientVersionLine, tail string) string {
	return "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then echo '" + clientVersionLine + "'; exit 0; fi\n" +
		": > \"$ARGV_FILE\"\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> \"$ARGV_FILE\"; done\n" +
		tail
}

// readArgv 读取假脚本记录的 argv。
func readArgv(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 argv 记录失败: %v", err)
	}
	return splitLines(string(data))
}

// assertArgvOmits 断言 argv 里没有凭据（凭据只允许走环境变量）。
func assertArgvOmits(t *testing.T, argv []string, secret string) {
	t.Helper()
	for _, arg := range argv {
		if strings.Contains(arg, secret) {
			t.Fatalf("凭据泄漏进 argv: %q（argv=%v）", arg, argv)
		}
	}
}

// assertArgvContains 断言 argv 依次包含给定参数（按子序列匹配，顺序必须一致）。
func assertArgvContains(t *testing.T, argv []string, want ...string) {
	t.Helper()
	index := 0
	for _, arg := range argv {
		if index < len(want) && arg == want[index] {
			index++
		}
	}
	if index != len(want) {
		t.Fatalf("argv 缺少参数序列 %v（实际 argv=%v）", want[index:], argv)
	}
}

// TestServerVersionMySQL 覆盖 MySQL 成功分支：SELECT VERSION() 走服务端，
// 密码只经 MYSQL_PWD 环境变量传递，argv 里不得出现。
func TestServerVersionMySQL(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	argvFile := filepath.Join(root, "mysql-argv.txt")
	t.Setenv("ARGV_FILE", argvFile)
	writeFakeScript(t, filepath.Join(binDir, "mysql"), scriptWithArgvDump(
		"mysql  Ver 11.8.6-MariaDB for Linux",
		"if [ \"$MYSQL_PWD\" != \"test-password\" ]; then echo 'missing MYSQL_PWD' >&2; exit 41; fi\n"+
			"echo '9.7.1'\n",
	))
	source := newFakeSource(t, root, []string{binDir}, func(opts *Options) {
		opts.LookupEnv = func(key string) (string, bool) {
			if key == "MYSQL_PWD" {
				return "test-password", true
			}
			return "", false
		}
	})
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("探测服务失败: %v", err)
	}
	mysql, ok := findService(services, "mysql")
	if !ok {
		t.Fatal("服务清单缺少 mysql")
	}
	if mysql.ClientVersion != "11.8.6-MariaDB" {
		t.Fatalf("mysql client_version=%q，期望 11.8.6-MariaDB", mysql.ClientVersion)
	}
	if mysql.ServerVersion != "9.7.1" || mysql.VersionSource != VersionSourceServer {
		t.Fatalf("mysql server_version=%q source=%q，期望 9.7.1/server", mysql.ServerVersion, mysql.VersionSource)
	}
	argv := readArgv(t, argvFile)
	assertArgvOmits(t, argv, "test-password")
	assertArgvContains(t, argv,
		"-h", "127.0.0.1", "-P", "3306", "--protocol=TCP", "--connect-timeout=3",
		"-u", "root", "-e", "SELECT VERSION()", "-N", "-B")
}

// TestServerVersionMySQLFailure 服务端不可达/密码错误时，server_version 留空、
// version_source 降级为 client，绝不把客户端版本冒充服务端。
func TestServerVersionMySQLFailure(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	t.Setenv("ARGV_FILE", filepath.Join(root, "argv.txt"))
	writeFakeScript(t, filepath.Join(binDir, "mysql"), scriptWithArgvDump(
		"mysql  Ver 11.8.6-MariaDB for Linux",
		"echo 'ERROR 1045 (28000): Access denied for user' >&2\nexit 1\n",
	))
	source := newFakeSource(t, root, []string{binDir}, nil)
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("servers 探测失败时不应让整表报错: %v", err)
	}
	mysql, _ := findService(services, "mysql")
	if mysql.ServerVersion != "" {
		t.Fatalf("服务端不可达时 server_version 必须为空，实际 %q", mysql.ServerVersion)
	}
	if mysql.VersionSource != VersionSourceClient || mysql.ClientVersion != "11.8.6-MariaDB" {
		t.Fatalf("降级结果不符: %+v", mysql)
	}
}

// TestServerVersionMySQLTimeout 服务端探测超时同样只降级、不报错。
func TestServerVersionMySQLTimeout(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	t.Setenv("ARGV_FILE", filepath.Join(root, "argv.txt"))
	writeFakeScript(t, filepath.Join(binDir, "mysql"), scriptWithArgvDump(
		"mysql  Ver 11.8.6-MariaDB for Linux",
		"sleep 5\necho '9.7.1'\n",
	))
	source := newFakeSource(t, root, []string{binDir}, func(opts *Options) {
		opts.CommandTimeout = 100 * time.Millisecond
	})
	start := time.Now()
	services, err := source.Services(context.Background())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("探测超时不应让整表报错: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("服务端探测未按 CommandTimeout 截断：耗时 %s", elapsed)
	}
	mysql, _ := findService(services, "mysql")
	if mysql.ServerVersion != "" || mysql.VersionSource != VersionSourceClient {
		t.Fatalf("超时后应返回空 server_version + client 来源: %+v", mysql)
	}
}

// TestServerVersionPostgres 覆盖 psql 分支：SHOW server_version 走服务端，
// 密码经 PGPASSWORD、超时经 PGCONNECT_TIMEOUT（psql 无 --connect-timeout 选项）。
func TestServerVersionPostgres(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	argvFile := filepath.Join(root, "psql-argv.txt")
	envFile := filepath.Join(root, "psql-env.txt")
	t.Setenv("ARGV_FILE", argvFile)
	t.Setenv("ENV_FILE", envFile)
	writeFakeScript(t, filepath.Join(binDir, "psql"), scriptWithArgvDump(
		"psql (PostgreSQL) 16.1",
		"printf '%s\\n' \"$PGCONNECT_TIMEOUT\" > \"$ENV_FILE\"\n"+
			"if [ \"$PGPASSWORD\" != \"pg-secret\" ]; then echo 'no PGPASSWORD' >&2; exit 42; fi\n"+
			"echo '16.4'\n",
	))
	source := newFakeSource(t, root, []string{binDir}, func(opts *Options) {
		opts.LookupEnv = func(key string) (string, bool) {
			if key == "PGPASSWORD" {
				return "pg-secret", true
			}
			return "", false
		}
	})
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("探测服务失败: %v", err)
	}
	postgres, ok := findService(services, "postgres")
	if !ok {
		t.Fatal("服务清单缺少 postgres")
	}
	if postgres.ClientVersion != "16.1" {
		t.Fatalf("postgres client_version=%q，期望 16.1", postgres.ClientVersion)
	}
	if postgres.ServerVersion != "16.4" || postgres.VersionSource != VersionSourceServer {
		t.Fatalf("postgres server_version=%q source=%q，期望 16.4/server", postgres.ServerVersion, postgres.VersionSource)
	}
	argv := readArgv(t, argvFile)
	assertArgvOmits(t, argv, "pg-secret")
	assertArgvContains(t, argv, "-h", "127.0.0.1", "-p", "5432", "-U", "postgres", "-tAc", "SHOW server_version")
	timeout, err := os.ReadFile(envFile)
	if err != nil || strings.TrimSpace(string(timeout)) != "3" {
		t.Fatalf("PGCONNECT_TIMEOUT 未设为 3：%q err=%v", string(timeout), err)
	}
}

// TestServerVersionRedis 覆盖 redis-cli 分支：INFO server 的 redis_version 才是服务端版本。
func TestServerVersionRedis(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	argvFile := filepath.Join(root, "redis-argv.txt")
	t.Setenv("ARGV_FILE", argvFile)
	writeFakeScript(t, filepath.Join(binDir, "redis-cli"), scriptWithArgvDump(
		"redis-cli 7.0.11",
		"if [ \"$REDISCLI_AUTH\" != \"redis-secret\" ]; then echo 'NOAUTH Authentication required.'; exit 1; fi\n"+
			"printf 'redis_version:7.4.12\\nredis_mode:standalone\\n'\n",
	))
	source := newFakeSource(t, root, []string{binDir}, func(opts *Options) {
		opts.LookupEnv = func(key string) (string, bool) {
			if key == "REDISCLI_AUTH" {
				return "redis-secret", true
			}
			return "", false
		}
	})
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("探测服务失败: %v", err)
	}
	redis, ok := findService(services, "redis")
	if !ok {
		t.Fatal("服务清单缺少 redis")
	}
	if redis.ClientVersion != "7.0.11" {
		t.Fatalf("redis client_version=%q，期望 7.0.11", redis.ClientVersion)
	}
	if redis.ServerVersion != "7.4.12" || redis.VersionSource != VersionSourceServer {
		t.Fatalf("redis server_version=%q source=%q，期望 7.4.12/server", redis.ServerVersion, redis.VersionSource)
	}
	argv := readArgv(t, argvFile)
	assertArgvOmits(t, argv, "redis-secret")
	assertArgvContains(t, argv, "-h", "127.0.0.1", "-p", "6379", "--no-auth-warning", "INFO", "server")
}

// TestServerVersionRedisContainer 宿主机没有 redis-cli（Redis 只在容器里）时，
// 回退到 `docker exec -e REDISCLI_AUTH <容器> redis-cli ...`；凭据仍只走环境变量。
func TestServerVersionRedisContainer(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	argvFile := filepath.Join(root, "docker-argv.txt")
	t.Setenv("ARGV_FILE", argvFile)
	writeFakeScript(t, filepath.Join(binDir, "redis-server"), "#!/bin/sh\necho 'Redis server v=7.0.11 sha=00000000:0 malloc=jemalloc bits=64'\n")
	writeFakeScript(t, filepath.Join(binDir, "docker"), "#!/bin/sh\n"+
		"if [ \"$1\" = \"--version\" ]; then echo 'Docker version 29.7.2, build abc'; exit 0; fi\n"+
		"if [ \"$1\" = \"ps\" ]; then echo 'abc123 common-redis redis:7-alpine'; exit 0; fi\n"+
		"if [ \"$1\" = \"exec\" ]; then\n"+
		"  : > \"$ARGV_FILE\"; for a in \"$@\"; do printf '%s\\n' \"$a\" >> \"$ARGV_FILE\"; done\n"+
		"  if [ \"$REDISCLI_AUTH\" != \"redis-secret\" ]; then echo 'no auth env' >&2; exit 43; fi\n"+
		"  printf 'redis_version:7.4.12\\n'; exit 0\n"+
		"fi\nexit 1\n")
	source := newFakeSource(t, root, []string{binDir}, func(opts *Options) {
		opts.LookupEnv = func(key string) (string, bool) {
			if key == "REDISCLI_AUTH" {
				return "redis-secret", true
			}
			return "", false
		}
	})
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("探测服务失败: %v", err)
	}
	redis, _ := findService(services, "redis")
	if redis.ServerVersion != "7.4.12" || redis.VersionSource != VersionSourceServer {
		t.Fatalf("容器回退结果不符: %+v", redis)
	}
	argv := readArgv(t, argvFile)
	assertArgvOmits(t, argv, "redis-secret")
	assertArgvContains(t, argv, "exec", "-e", "REDISCLI_AUTH", "abc123", "redis-cli", "INFO", "server")
}

// TestServerVersionMySQLFlavor 服务端类型按 SELECT VERSION() 归属：MariaDB 服务端
// 只挂到 mariadb 条目，mysql 条目降级为 client，反之亦然。
func TestServerVersionMySQLFlavor(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	t.Setenv("ARGV_FILE", filepath.Join(root, "argv.txt"))
	body := scriptWithArgvDump(
		"mysql  Ver 11.8.6-MariaDB for Linux",
		"echo '11.8.6-MariaDB-0ubuntu0.24.04.1'\n",
	)
	writeFakeScript(t, filepath.Join(binDir, "mysql"), body)
	writeFakeScript(t, filepath.Join(binDir, "mariadb"), body)
	source := newFakeSource(t, root, []string{binDir}, nil)
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("探测服务失败: %v", err)
	}
	mysql, _ := findService(services, "mysql")
	if mysql.ServerVersion != "" || mysql.VersionSource != VersionSourceClient {
		t.Fatalf("MySQL 条目不应认领 MariaDB 服务端: %+v", mysql)
	}
	mariadb, _ := findService(services, "mariadb")
	if mariadb.ServerVersion != "11.8.6-MariaDB-0ubuntu0.24.04.1" || mariadb.VersionSource != VersionSourceServer {
		t.Fatalf("MariaDB 条目应认领 MariaDB 服务端: %+v", mariadb)
	}
}

// TestServerVersionMongoDB 覆盖 mongosh 分支：db.version() 输出即真实服务端版本；
// 命令固定 argv（宿主/端口/脚本），不接受用户输入。
func TestServerVersionMongoDB(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	argvFile := filepath.Join(root, "mongosh-argv.txt")
	t.Setenv("ARGV_FILE", argvFile)
	writeFakeScript(t, filepath.Join(binDir, "mongosh"), "#!/bin/sh\n"+
		"if [ \"$1\" = \"--version\" ]; then echo '2.2.0'; exit 0; fi\n"+
		": > \"$ARGV_FILE\"; for a in \"$@\"; do printf '%s\\n' \"$a\" >> \"$ARGV_FILE\"; done\n"+
		"echo '7.0.5'\n")
	source := newFakeSource(t, root, []string{binDir}, nil)
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("探测服务失败: %v", err)
	}
	mongodb, ok := findService(services, "mongodb")
	if !ok {
		t.Fatal("服务清单缺少 mongodb")
	}
	if mongodb.ClientVersion != "2.2.0" {
		t.Fatalf("mongodb client_version=%q，期望 2.2.0", mongodb.ClientVersion)
	}
	if mongodb.ServerVersion != "7.0.5" || mongodb.VersionSource != VersionSourceServer {
		t.Fatalf("mongodb server_version=%q source=%q，期望 7.0.5/server", mongodb.ServerVersion, mongodb.VersionSource)
	}
	if mongodb.VersionHint != "" {
		t.Fatalf("探到服务端版本后不应有 version_hint: %q", mongodb.VersionHint)
	}
	argv := readArgv(t, argvFile)
	assertArgvContains(t, argv, "--quiet", "--host", "127.0.0.1", "--port", "27017", "--eval", "db.version()")
}

// TestServerVersionRabbitMQ 覆盖 rabbitmqctl version 分支；客户端 --version 失败只丢
// client_version，服务端版本仍可通过 rabbitmqctl 读到。
func TestServerVersionRabbitMQ(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	argvFile := filepath.Join(root, "rabbitmqctl-argv.txt")
	t.Setenv("ARGV_FILE", argvFile)
	writeFakeScript(t, filepath.Join(binDir, "rabbitmqctl"), "#!/bin/sh\n"+
		"if [ \"$1\" = \"--version\" ]; then echo 'unknown command --version' >&2; exit 64; fi\n"+
		": > \"$ARGV_FILE\"; for a in \"$@\"; do printf '%s\\n' \"$a\" >> \"$ARGV_FILE\"; done\n"+
		"echo '3.13.7'\n")
	source := newFakeSource(t, root, []string{binDir}, nil)
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("探测服务失败: %v", err)
	}
	rabbitmq, ok := findService(services, "rabbitmq")
	if !ok {
		t.Fatal("服务清单缺少 rabbitmq")
	}
	if rabbitmq.ServerVersion != "3.13.7" || rabbitmq.VersionSource != VersionSourceServer {
		t.Fatalf("rabbitmq server_version=%q source=%q，期望 3.13.7/server", rabbitmq.ServerVersion, rabbitmq.VersionSource)
	}
	argv := readArgv(t, argvFile)
	assertArgvContains(t, argv, "version")
}
