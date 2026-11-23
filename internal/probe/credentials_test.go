package probe

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// envLookup 构造假的环境变量读取面：只有 secrets 里的键有值。
func envLookup(secrets map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := secrets[key]
		return value, ok
	}
}

// TestBuildConfigGetAllMissing 全部凭据缺失：三条声明齐全、status=missing、hint 给补齐路径。
func TestBuildConfigGetAllMissing(t *testing.T) {
	result := buildConfigGet(envLookup(nil))
	if result.Plugin != "discover" || result.Persisted {
		t.Fatalf("config.get 固定字段不符: %+v", result)
	}
	if len(result.CredentialRequirements) != 3 {
		t.Fatalf("凭据声明应有 3 条，实际 %d", len(result.CredentialRequirements))
	}
	wantEnv := []string{"MYSQL_PWD", "PGPASSWORD", "REDISCLI_AUTH"}
	for index, requirement := range result.CredentialRequirements {
		if requirement.EnvVar != wantEnv[index] {
			t.Fatalf("第 %d 条 env_var=%q，期望 %q", index, requirement.EnvVar, wantEnv[index])
		}
		if requirement.Status != "missing" || requirement.Required {
			t.Fatalf("%s 状态不符: %+v", requirement.EnvVar, requirement)
		}
		if requirement.Purpose == "" || requirement.FillHint == "" || len(requirement.Services) == 0 {
			t.Fatalf("%s 声明字段不完整: %+v", requirement.EnvVar, requirement)
		}
	}
	if !strings.Contains(result.Hint, "0/3") || !strings.Contains(result.Hint, "§3.5 #8") {
		t.Fatalf("hint 应含计数与内核注入指引: %q", result.Hint)
	}
	if !strings.Contains(result.Transport, "环境变量") {
		t.Fatalf("transport 应说明只从环境变量读取: %q", result.Transport)
	}
}

// TestBuildConfigGetPartial 部分凭据已配置：status=configured，且结果里绝不含凭据值。
func TestBuildConfigGetPartial(t *testing.T) {
	result := buildConfigGet(envLookup(map[string]string{"MYSQL_PWD": "s3cr3t-value"}))
	if result.CredentialRequirements[0].Status != "configured" {
		t.Fatalf("MYSQL_PWD 应为 configured: %+v", result.CredentialRequirements[0])
	}
	if result.CredentialRequirements[1].Status != "missing" || result.CredentialRequirements[2].Status != "missing" {
		t.Fatalf("其余凭据应 missing: %+v", result.CredentialRequirements)
	}
	if !strings.Contains(result.Hint, "1/3") {
		t.Fatalf("hint 计数不符: %q", result.Hint)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if strings.Contains(string(raw), "s3cr3t-value") {
		t.Fatalf("config.get 结果泄漏了凭据值: %s", raw)
	}
}

// TestCallConfigGet 校验方法级入口：config.get 平台无关、不执行探测、结构可解析。
func TestCallConfigGet(t *testing.T) {
	runtime := NewRuntime(Options{
		GOOS:      "linux",
		LookupEnv: envLookup(map[string]string{"REDISCLI_AUTH": "redis-secret"}),
	}, nil)
	raw, perr := runtime.Call("discover.config.get", nil)
	if perr != nil {
		t.Fatalf("discover.config.get 失败: %+v", perr)
	}
	var result ConfigGetResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("结果不是对象: %v (%s)", err, raw)
	}
	if len(result.CredentialRequirements) != 3 || result.CredentialRequirements[2].Status != "configured" {
		t.Fatalf("config.get 结果不符: %+v", result)
	}
	// 非 Linux 也应能读凭据声明（纯声明 + 环境变量，不依赖 /proc）。
	nonLinux := NewRuntime(Options{GOOS: "darwin", LookupEnv: envLookup(nil)}, nil)
	if _, perr := nonLinux.Call("discover.config.get", nil); perr != nil {
		t.Fatalf("非 Linux 的 config.get 不应报错: %+v", perr)
	}
	if _, perr := nonLinux.Call("discover.services", nil); perr == nil || perr.Code != "E_UNSUPPORTED" {
		t.Fatalf("非 Linux 的 services 应 E_UNSUPPORTED，实际 %v", perr)
	}
}

// TestVersionHintCredentialMissing 缺凭据时，services 条目的 version_hint 必须
// 点名环境变量与去哪填（§3.5 #8），且 version_source 如实降级为 client。
func TestVersionHintCredentialMissing(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	writeFakeScript(t, filepath.Join(binDir, "mysql"),
		"#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'mysql  Ver 8.0.34 for Linux'; exit 0; fi\nexit 1\n")
	source := newFakeSource(t, root, []string{binDir}, nil) // LookupEnv 缺省（无凭据）
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("探测服务失败: %v", err)
	}
	mysql, _ := findService(services, "mysql")
	if mysql.VersionSource != VersionSourceClient || mysql.ServerVersion != "" {
		t.Fatalf("版本来源应降级为 client: %+v", mysql)
	}
	if !strings.Contains(mysql.VersionHint, "MYSQL_PWD") || !strings.Contains(mysql.VersionHint, "db-mysql") ||
		!strings.Contains(mysql.VersionHint, "§3.5 #8") {
		t.Fatalf("version_hint 缺可操作信息: %q", mysql.VersionHint)
	}
	// 未安装项只给 install_hint，不给 version_hint。
	caddy, _ := findService(services, "caddy")
	if caddy.Found || caddy.VersionHint != "" || caddy.InstallHint == "" {
		t.Fatalf("未安装项提示不符: %+v", caddy)
	}
}

// TestVersionHintCredentialConfigured 已配置凭据但服务端不可达 → 提示说明是探测失败，
// 不再让用户去填凭据。
func TestVersionHintCredentialConfigured(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	writeFakeScript(t, filepath.Join(binDir, "mysql"),
		"#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'mysql  Ver 8.0.34 for Linux'; exit 0; fi\nexit 1\n")
	source := newFakeSource(t, root, []string{binDir}, func(opts *Options) {
		opts.LookupEnv = envLookup(map[string]string{"MYSQL_PWD": "configured-secret"})
	})
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("探测服务失败: %v", err)
	}
	mysql, _ := findService(services, "mysql")
	if !strings.Contains(mysql.VersionHint, "已配置 MYSQL_PWD") || !strings.Contains(mysql.VersionHint, "探测失败") {
		t.Fatalf("version_hint 应说明已配置但探测失败: %q", mysql.VersionHint)
	}
	if strings.Contains(mysql.VersionHint, "未配置") {
		t.Fatalf("已配置时不应提示去配置: %q", mysql.VersionHint)
	}
}

// TestVersionHintClearedWhenServerVersion 探到服务端版本时不产生提示。
func TestVersionHintClearedWhenServerVersion(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "fakebin")
	writeFakeScript(t, filepath.Join(binDir, "mysql"),
		"#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'mysql  Ver 8.0.34 for Linux'; exit 0; fi\necho '9.7.1'\n")
	source := newFakeSource(t, root, []string{binDir}, func(opts *Options) {
		opts.LookupEnv = envLookup(map[string]string{"MYSQL_PWD": "configured-secret"})
	})
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("探测服务失败: %v", err)
	}
	mysql, _ := findService(services, "mysql")
	if mysql.ServerVersion != "9.7.1" || mysql.VersionHint != "" {
		t.Fatalf("探到服务端版本后不应有 version_hint: %+v", mysql)
	}
}

// TestCatalogNewFingerprints 新增的四个服务指纹必须出现在清单里，未装时带安装提示。
func TestCatalogNewFingerprints(t *testing.T) {
	source := newFakeSource(t, t.TempDir(), []string{}, nil)
	services, err := source.Services(context.Background())
	if err != nil {
		t.Fatalf("探测服务失败: %v", err)
	}
	wantPorts := map[string]int{"memcached": 11211, "mongodb": 27017, "rabbitmq": 5672, "elasticsearch": 9200}
	for id, port := range wantPorts {
		service, ok := findService(services, id)
		if !ok {
			t.Fatalf("服务清单缺少新指纹 %s", id)
		}
		if service.Found || service.InstallHint == "" || service.VersionSource != VersionSourceUnknown {
			t.Fatalf("%s 未安装时字段不符: %+v", id, service)
		}
		if service.Port != port {
			t.Fatalf("%s port=%d，期望 %d", id, service.Port, port)
		}
	}
}
