package probe

import (
	"regexp"
	"strings"
)

// versionPattern 匹配版本号主干：至少一个点（1.24 / 1.24.0），可选 -/~/+ 后缀，
// 兼容 mysql 的 "8.0.34-0ubuntu0.22.04.3" 形式。
var versionPattern = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?(?:[-~+][0-9A-Za-z.~+-]+)?`)

// parseVersion 从命令输出里提取第一个版本号；没有匹配时返回空串（不编造版本）。
func parseVersion(output string) string {
	match := versionPattern.FindString(output)
	if match == "" {
		return ""
	}
	return strings.TrimRight(match, ".,;:")
}

// splitLines 按 \n 切行并去掉行尾 \r，供配置文件解析复用。
func splitLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.Split(text, "\n")
}

// equalFoldTrim 比较忽略首尾空白与大小写的整行。
func equalFoldTrim(line, want string) bool {
	return strings.EqualFold(strings.TrimSpace(line), want)
}
