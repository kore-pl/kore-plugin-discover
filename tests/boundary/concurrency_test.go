// 边界类别：并发（concurrent / parallel / race）。
// 断言执行面：同一探测批次被真并发调用时无数据竞争、结果一致；探测与缓存刷新并发安全。
package boundary_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pengtang1024/kore-plugin-discover/internal/probe"
)

// countingSource 是线程安全的可控执行面：记录调用次数，供缓存/并发断言使用。
type countingSource struct {
	mu            sync.Mutex
	servicesCalls int
	portsCalls    int
	services      []probe.Service
	ports         []probe.ListeningPort
}

// Services 在锁内累加计数并返回预置清单。
func (s *countingSource) Services(context.Context) ([]probe.Service, error) {
	s.mu.Lock()
	s.servicesCalls++
	s.mu.Unlock()
	return s.services, nil
}

// Listeners 在锁内累加计数并返回预置端口。
func (s *countingSource) Listeners(context.Context) ([]probe.ListeningPort, error) {
	s.mu.Lock()
	s.portsCalls++
	s.mu.Unlock()
	return s.ports, nil
}

// calls 返回当前探测次数快照。
func (s *countingSource) calls() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.servicesCalls, s.portsCalls
}

// TestConcurrent_ProbeSameBatchNoRace 并发探测同一批服务（真实只读执行面 + fixture），
// 断言所有 goroutine 拿到的输出完全一致；数据竞争由 -race 检测。
func TestConcurrent_ProbeSameBatchNoRace(t *testing.T) {
	t.Parallel()
	root := newFixtureRoot(t)
	binDir := t.TempDir()
	newFixtureExecutable(t, binDir, "node")
	rt := probe.NewRuntime(probe.Options{
		GOOS: "linux", RootFS: root, PathDirs: []string{binDir},
		Runner: fixedRunner("v20.11.1\n", nil),
	}, nil)

	const workers = 24
	const rounds = 6
	type sample struct{ services, ports, summary string }
	var mu sync.Mutex
	samples := make([]sample, 0, workers*rounds)
	errCh := make(chan string, workers*rounds)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for round := 0; round < rounds; round++ {
				params := json.RawMessage(nil)
				if (worker+round)%3 == 0 {
					params = json.RawMessage(`{"refresh":true}`)
				}
				services, err := rt.Call("discover.services", params)
				if err != nil {
					errCh <- "services: " + err.Message
					continue
				}
				ports, err := rt.Call("discover.ports", params)
				if err != nil {
					errCh <- "ports: " + err.Message
					continue
				}
				summary, err := rt.Call("discover.summary", params)
				if err != nil {
					errCh <- "summary: " + err.Message
					continue
				}
				mu.Lock()
				samples = append(samples, sample{string(services), string(ports), string(summary)})
				mu.Unlock()
			}
		}(worker)
	}
	wg.Wait()
	close(errCh)
	for message := range errCh {
		t.Fatalf("并发探测返回错误: %s", message)
	}
	if len(samples) != workers*rounds {
		t.Fatalf("有效样本数 = %d，期望 %d", len(samples), workers*rounds)
	}
	baseline := samples[0]
	for index, got := range samples[1:] {
		if got != baseline {
			t.Fatalf("并发探测结果不一致（样本 %d）: services_equal=%v ports_equal=%v summary_equal=%v",
				index+1, got.services == baseline.services, got.ports == baseline.ports, got.summary == baseline.summary)
		}
	}

	var services []probe.Service
	if err := json.Unmarshal([]byte(baseline.services), &services); err != nil {
		t.Fatalf("services 输出不是数组: %v", err)
	}
	var node *probe.Service
	for i := range services {
		if services[i].ID == "node" {
			node = &services[i]
		}
	}
	if node == nil || !node.Found || node.ClientVersion != "20.11.1" {
		t.Fatalf("并发探测未得到预期的 node 结果: %+v", node)
	}
}

// TestConcurrent_ProbeAndCacheRefresh 并发执行「普通调用」与「refresh=true 强制重探」，
// 断言缓存读写无竞态、结果一致、强制刷新确实触发了执行面探测。
func TestConcurrent_ProbeAndCacheRefresh(t *testing.T) {
	t.Parallel()
	src := &countingSource{
		services: []probe.Service{{
			ID: "nginx", Name: "Nginx", Found: true, Bin: "/usr/sbin/nginx",
			ClientVersion: "1.24.0", VersionSource: probe.VersionSourceClient, Port: 80, Source: "which",
		}},
		ports: []probe.ListeningPort{{Port: 80, Addr: "0.0.0.0", Family: "ipv4", Listening: true}},
	}
	rt := probe.NewRuntime(probe.Options{GOOS: "linux", PathDirs: []string{}, TTL: time.Minute}, src)

	const workers = 32
	const rounds = 8
	var mu sync.Mutex
	outputs := make([]string, 0, workers*rounds)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for round := 0; round < rounds; round++ {
				params := json.RawMessage(nil)
				if (worker+round)%2 == 0 {
					params = json.RawMessage(`{"refresh":true}`)
				}
				raw, err := rt.Call("discover.summary", params)
				if err != nil {
					t.Errorf("summary 并发调用失败: %+v", err)
					return
				}
				mu.Lock()
				outputs = append(outputs, string(raw))
				mu.Unlock()
			}
		}(worker)
	}
	wg.Wait()
	if len(outputs) != workers*rounds {
		t.Fatalf("有效样本数 = %d，期望 %d", len(outputs), workers*rounds)
	}
	for index, got := range outputs[1:] {
		if got != outputs[0] {
			t.Fatalf("并发刷新结果不一致（样本 %d）", index+1)
		}
	}
	servicesCalls, portsCalls := src.calls()
	if servicesCalls == 0 || portsCalls == 0 {
		t.Fatalf("并发刷新未触发执行面探测: services=%d ports=%d", servicesCalls, portsCalls)
	}
	if servicesCalls > workers*rounds || portsCalls > workers*rounds {
		t.Fatalf("探测次数超过调用次数上界: services=%d ports=%d", servicesCalls, portsCalls)
	}
}
