package loader

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cuixudong74/app-shell-routing-go/router"
)

func buildMatch(t *testing.T, routes []*router.Route, path string) *router.MatchResult {
	t.Helper()
	tree, err := router.Build(routes)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := tree.Match(path)
	if !ok {
		t.Fatalf("路径 %s 未匹配", path)
	}
	return m
}

func TestLoadChainParentBeforeChild(t *testing.T) {
	reg := NewRegistry()
	var mu sync.Mutex
	var order []string
	reg.Register("rootL", func(ctx context.Context, lc LoadContext) (any, error) {
		mu.Lock()
		order = append(order, "root")
		mu.Unlock()
		return "root-data", nil
	})
	reg.Register("childL", func(ctx context.Context, lc LoadContext) (any, error) {
		// 下层必须能读到上层已就绪的数据。
		if lc.Parents["root"] != "root-data" {
			t.Errorf("下层未看到上层数据: %v", lc.Parents)
		}
		if lc.Params["id"] != "42" {
			t.Errorf("下层未看到本段参数: %v", lc.Params)
		}
		mu.Lock()
		order = append(order, "child")
		mu.Unlock()
		return "child-data", nil
	})
	routes := []*router.Route{
		{ID: "root", Path: "users", LoaderID: "rootL", Children: []*router.Route{
			{ID: "child", Path: ":id", LoaderID: "childL"},
		}},
	}
	m := buildMatch(t, routes, "/users/42")
	results, err := LoadChain(context.Background(), reg, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "root" || order[1] != "child" {
		t.Fatalf("加载顺序错误: %v", order)
	}
	if len(results) != 2 {
		t.Fatalf("结果数量错误: %v", results)
	}
}

func TestLoadSiblingsConcurrent(t *testing.T) {
	reg := NewRegistry()
	started := make(chan string, 2)
	release := make(chan struct{})
	for _, id := range []string{"la", "lb"} {
		id := id
		reg.Register(id, func(ctx context.Context, lc LoadContext) (any, error) {
			started <- id
			<-release // 两个加载器都启动后才放行，以此证明并发
			return id + "-data", nil
		})
	}
	routes := []*router.Route{
		{ID: "a", Path: "a", LoaderID: "la"},
		{ID: "b", Path: "b", LoaderID: "lb"},
	}
	done := make(chan []Result, 1)
	go func() {
		r, err := LoadSiblings(context.Background(), reg, routes, nil, nil)
		if err != nil {
			t.Error(err)
		}
		done <- r
	}()
	// 若同层不并发，这里只会收到一个启动信号。
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case id := <-started:
			got[id] = true
		case <-time.After(2 * time.Second):
			t.Fatal("同层加载未并发执行")
		}
	}
	close(release)
	results := <-done
	if len(results) != 2 || results[0].RouteID != "a" || results[1].RouteID != "b" {
		t.Fatalf("结果顺序应与输入一致: %v", results)
	}
}

func TestLoadChainErrorLocatesRoute(t *testing.T) {
	reg := NewRegistry()
	reg.Register("ok", func(ctx context.Context, lc LoadContext) (any, error) {
		return "fine", nil
	})
	reg.Register("bad", func(ctx context.Context, lc LoadContext) (any, error) {
		return nil, errors.New("数据库连接超时")
	})
	routes := []*router.Route{
		{ID: "root", Path: "x", LoaderID: "ok", Children: []*router.Route{
			{ID: "leaf", Path: "y", LoaderID: "bad"},
		}},
	}
	m := buildMatch(t, routes, "/x/y")
	_, err := LoadChain(context.Background(), reg, m)
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("期望 LoadError，得到 %v", err)
	}
	if le.RouteID != "leaf" {
		t.Fatalf("错误未定位到失败路由: %v", le.RouteID)
	}
	if !errors.Is(le.Unwrap(), le.Err) || le.Error() == "" {
		t.Fatal("错误应携带原因")
	}
}

func TestLoadChainRejectsNilData(t *testing.T) {
	reg := NewRegistry()
	reg.Register("nil", func(ctx context.Context, lc LoadContext) (any, error) {
		return nil, nil // 缺失数据不得当成空值继续
	})
	routes := []*router.Route{{ID: "r", Path: "r", LoaderID: "nil"}}
	m := buildMatch(t, routes, "/r")
	if _, err := LoadChain(context.Background(), reg, m); err == nil {
		t.Fatal("空数据应报错而不是继续渲染")
	}
}

func TestLoadChainUnregisteredLoader(t *testing.T) {
	reg := NewRegistry()
	routes := []*router.Route{{ID: "r", Path: "r", LoaderID: "ghost"}}
	m := buildMatch(t, routes, "/r")
	_, err := LoadChain(context.Background(), reg, m)
	var le *LoadError
	if !errors.As(err, &le) || le.RouteID != "r" {
		t.Fatalf("未注册加载器应定位到路由: %v", err)
	}
}
