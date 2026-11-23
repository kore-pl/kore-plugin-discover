package main

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// declaredInfo 是 info.json 里本仓关心的声明字段（显式定形，不用 map[string]any：G-24）。
type declaredInfo struct {
	ID                 string            `json:"id"`
	Version            string            `json:"version"`
	Protocol           string            `json:"protocol"`
	RequiresKore       string            `json:"requires_kore"`
	Capabilities       []string          `json:"capabilities"`
	MethodCapabilities map[string]string `json:"method_capabilities"`
}

// privilegedCapabilities 是协议 §3.1.2 的 6 项宿主级能力（与内核 registry 白名单逐字一致）。
var privilegedCapabilities = map[string]bool{
	"system:read": true, "system:admin": true,
	"net:read": true, "net:write": true,
	"proc:read": true, "proc:write": true,
}

// methodKeyPattern 是协议 §3.1.2.1 的方法级 capability 键语法（<id>.<action>，action 可点分）。
var methodKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*\.[a-z][a-zA-Z0-9]*(\.[a-z][a-zA-Z0-9]*)*$`)

// loadDeclaredInfo 读取仓根 info.json。
func loadDeclaredInfo(t *testing.T) declaredInfo {
	t.Helper()
	raw, err := os.ReadFile("info.json")
	if err != nil {
		t.Fatalf("读取 info.json: %v", err)
	}
	var info declaredInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatalf("解析 info.json: %v", err)
	}
	return info
}

// TestMethodCapabilitiesConsistency 锁定方法级能力声明与实现一致（§3.5 #12）：
//   - 每个实现的方法都有声明，且声明集合 == 实现集合（声明了却不实现 / 实现了却不声明都失败）；
//   - 键语法/插件域/能力白名单符合协议；本插件全只读，不得出现 write/admin；
//   - 没有 config.set，因此也不得声明 config.set 的 admin 映射（不接受明文凭据）。
func TestMethodCapabilitiesConsistency(t *testing.T) {
	info := loadDeclaredInfo(t)
	// 本包实际暴露的业务方法（与 probe.Runtime.Call 的 switch 一一对应）。
	implemented := []string{"discover.services", "discover.ports", "discover.summary", "discover.config.get"}

	if len(info.MethodCapabilities) != len(implemented) {
		t.Fatalf("method_capabilities 声明 %d 条，实现 %d 条（必须一一对应）",
			len(info.MethodCapabilities), len(implemented))
	}
	for _, method := range implemented {
		capability, ok := info.MethodCapabilities[method]
		if !ok {
			t.Fatalf("方法 %q 已实现但未声明 method_capabilities", method)
		}
		if capability != "system:read" {
			t.Fatalf("%s 的能力=%q，本插件只读探测应声明 system:read", method, capability)
		}
		// 键必须能被插件自身的方法归一化逻辑接受（防止声明了根本不存在的方法名）。
		verb := strings.TrimPrefix(method, info.ID+".")
		if got, ok := normalizeMethod(verb, info.ID); !ok || got != method {
			t.Fatalf("声明的方法 %q 未被实现侧接受（normalizeMethod(%q)=%q,%v）", method, verb, got, ok)
		}
	}
	for key, capability := range info.MethodCapabilities {
		if !methodKeyPattern.MatchString(key) {
			t.Fatalf("方法级能力键 %q 不符合协议语法", key)
		}
		if !strings.HasPrefix(key, info.ID+".") {
			t.Fatalf("方法级能力键 %q 的域必须等于插件 id %q", key, info.ID)
		}
		if !privilegedCapabilities[capability] {
			t.Fatalf("%s 的能力 %q 不是 6 项宿主能力之一", key, capability)
		}
		if strings.HasSuffix(capability, ":write") || strings.HasSuffix(capability, ":admin") {
			t.Fatalf("%s 声明了写/管理能力 %q，但 discover 是只读插件", key, capability)
		}
	}
	// 只读插件不提供 config.set：既不得声明，也不得被方法归一化接受。
	if _, ok := info.MethodCapabilities["discover.config.set"]; ok {
		t.Fatal("不得声明 discover.config.set（本插件不接受明文凭据，避免密码进入插件 API）")
	}
	if _, ok := normalizeMethod("config.set", info.ID); ok {
		t.Fatal("normalizeMethod 不应接受 config.set（无该方法，也不应接受凭据写入）")
	}
	// 插件级能力仍必须非空，且覆盖所有方法级能力（方法级只是收窄，不是替代声明）。
	if len(info.Capabilities) == 0 {
		t.Fatal("插件级 capabilities 不能为空")
	}
	pluginLevel := make(map[string]bool, len(info.Capabilities))
	for _, capability := range info.Capabilities {
		if !privilegedCapabilities[capability] {
			t.Fatalf("插件级能力 %q 不是 6 项宿主能力之一", capability)
		}
		pluginLevel[capability] = true
	}
	for method, capability := range info.MethodCapabilities {
		if !pluginLevel[capability] {
			t.Fatalf("%s 的方法级能力 %q 未在插件级 capabilities 中声明", method, capability)
		}
	}
}

// TestManifestMatchesInfoJSON 防版本 bump 漂移：manifest.json（随二进制内嵌、参与握手）
// 与 info.json 的握手字段必须一致（make dist 也校验，但单测先拦住）。
func TestManifestMatchesInfoJSON(t *testing.T) {
	info := loadDeclaredInfo(t)
	raw, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatalf("读取 manifest.json: %v", err)
	}
	var manifest declaredInfo
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("解析 manifest.json: %v", err)
	}
	if manifest.ID != info.ID || manifest.Version != info.Version ||
		manifest.Protocol != info.Protocol || manifest.RequiresKore != info.RequiresKore {
		t.Fatalf("manifest 与 info.json 不一致：manifest=%+v info=%+v", manifest, info)
	}
	if len(manifest.Capabilities) != len(info.Capabilities) {
		t.Fatalf("capabilities 数量不一致：%v vs %v", manifest.Capabilities, info.Capabilities)
	}
	for index := range info.Capabilities {
		if manifest.Capabilities[index] != info.Capabilities[index] {
			t.Fatalf("capabilities[%d]=%q 与 info.json %q 不一致",
				index, manifest.Capabilities[index], info.Capabilities[index])
		}
	}
}
