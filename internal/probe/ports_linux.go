//go:build linux

package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// readListeners 读取 /proc/net/tcp 与 tcp6，返回本机 TCP 监听套接字；
// 再通过 /proc/<pid>/fd 的 socket inode 反查进程名（非 root 只能看到同 uid 进程，
// 查不到时 Process 为 nil，不影响端口本身）。
func readListeners(fsRoot string) ([]ListeningPort, error) {
	procRoot := filepath.Join(fsRoot, "proc")
	raw := make([]rawListener, 0, 64)
	wanted := make(map[uint64]bool)
	for index, name := range []string{"tcp", "tcp6"} {
		data, err := os.ReadFile(filepath.Join(procRoot, "net", name))
		if err != nil {
			if index == 0 {
				return nil, fmt.Errorf("读取 /proc/net/tcp 失败: %w", err)
			}
			// tcp6 可能被内核模块关闭；缺失不影响 IPv4 结果。
			continue
		}
		for _, listener := range parseTCPFile(data) {
			raw = append(raw, listener)
			if listener.Inode != 0 {
				wanted[listener.Inode] = true
			}
		}
	}

	processes := resolveInodes(procRoot, wanted)
	ports := make([]ListeningPort, 0, len(raw))
	for _, listener := range raw {
		var process *string
		if name, ok := processes[listener.Inode]; ok && name != "" {
			processName := name
			process = &processName
		}
		ports = append(ports, ListeningPort{
			Port:      listener.Port,
			Addr:      listener.Addr,
			Family:    listener.Family,
			Listening: true,
			Process:   process,
		})
	}
	sort.Slice(ports, func(i, j int) bool {
		if ports[i].Port != ports[j].Port {
			return ports[i].Port < ports[j].Port
		}
		if ports[i].Family != ports[j].Family {
			return ports[i].Family < ports[j].Family
		}
		return ports[i].Addr < ports[j].Addr
	})
	return ports, nil
}

// resolveInodes 扫描 /proc/<pid>/fd，把监听 socket 的 inode 映射到进程名（comm）。
// 读不到的进程目录（非 root 的其它 uid）直接跳过，不报错、不伪造进程名。
func resolveInodes(procRoot string, wanted map[uint64]bool) map[uint64]string {
	resolved := make(map[uint64]string)
	if len(wanted) == 0 {
		return resolved
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return resolved
	}
	for _, entry := range entries {
		if _, ok := parsePID(entry.Name()); !ok {
			continue
		}
		fdDir := filepath.Join(procRoot, entry.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		var comm string
		commLoaded := false
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			inode, ok := socketInode(target)
			if !ok || !wanted[inode] {
				continue
			}
			if _, exists := resolved[inode]; exists {
				continue
			}
			if !commLoaded {
				data, readErr := os.ReadFile(filepath.Join(procRoot, entry.Name(), "comm"))
				if readErr != nil {
					break
				}
				comm = strings.TrimSpace(string(data))
				commLoaded = true
			}
			if comm != "" {
				resolved[inode] = comm
			}
		}
	}
	return resolved
}
