package probe

import (
	"os"
	"path/filepath"
	"sort"
)

// hostFS 是只读文件系统视图：真实运行 root="/"；单元测试可指向 t.TempDir()。
// 它只提供 stat/read/glob，不写任何文件。
type hostFS struct {
	root string
}

// abs 把绝对路径映射到 root 之下；root 为 "/" 时原样返回。
func (f hostFS) abs(path string) string {
	if f.root == "" || f.root == "/" {
		return path
	}
	return filepath.Join(f.root, path)
}

// isExecutable 判断路径是普通文件且任一执行位已置位（跟随符号链接）。
func (f hostFS) isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

// isDir 判断路径是否存在且是目录。
func (f hostFS) isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// exists 判断路径是否存在（任意类型）。
func (f hostFS) exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// readFile 读取 root 下的文件。
func (f hostFS) readFile(path string) ([]byte, error) {
	return os.ReadFile(f.abs(path))
}

// glob 展开 root 下的通配路径，结果排序保证输出确定。
func (f hostFS) glob(pattern string) []string {
	matches, err := filepath.Glob(f.abs(pattern))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	return matches
}

// findExecutable 按 PATH 目录 → 常见路径的顺序定位命令：
// PATH 命中返回 source="which"，常见路径命中返回 source="known_path"。
func findExecutable(fs hostFS, pathDirs []string, spec serviceSpec) (string, string) {
	for _, name := range spec.Bins {
		for _, dir := range pathDirs {
			if dir == "" {
				continue
			}
			candidate := filepath.Join(dir, name)
			if fs.isExecutable(candidate) {
				return candidate, "which"
			}
		}
	}
	for _, pattern := range spec.KnownPaths {
		for _, candidate := range fs.glob(pattern) {
			if fs.isExecutable(candidate) {
				return candidate, "known_path"
			}
		}
	}
	return "", ""
}

// findUnit 在 systemd unit 目录里查找服务单元；命中返回单元文件路径。
func findUnit(fs hostFS, spec serviceSpec) string {
	for _, unit := range spec.Units {
		for _, dir := range systemdUnitDirs {
			candidate := fs.abs(filepath.Join(dir, unit))
			info, err := os.Stat(candidate)
			if err == nil && info.Mode().IsRegular() {
				return candidate
			}
		}
	}
	return ""
}

// findConfigDir 返回第一个存在的配置目录（保持绝对路径）。
func findConfigDir(fs hostFS, spec serviceSpec) string {
	for _, dir := range spec.ConfigDirs {
		if fs.isDir(fs.abs(dir)) {
			return fs.abs(dir)
		}
	}
	return ""
}

// confEnabled 判断配置文件里是否有 ENABLED=yes（大小写不敏感），用于 ufw 这类
// "进程不在但防火墙已启用"的判定；读取失败按未启用处理。
func confEnabled(fs hostFS, path string) bool {
	data, err := fs.readFile(path)
	if err != nil {
		return false
	}
	for _, line := range splitLines(string(data)) {
		if equalFoldTrim(line, "ENABLED=yes") {
			return true
		}
	}
	return false
}
