package render

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cuixudong74/app-shell-routing-go/loader"
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

// collectWriter 把每次写入记录为一个事件，模拟客户端观察。
type collectWriter struct {
	events []Event
	onData func()
}

func (w *collectWriter) Write(p []byte) (int, error) {
	var e Event
	if err := jsonUnmarshal(p, &e); err != nil {
		return 0, err
	}
	w.events = append(w.events, e)
	if e.Kind == EventShell && w.onData != nil {
		w.onData()
	}
	return len(p), nil
}

func TestStreamShellBeforeSlowData(t *testing.T) {
	reg := loader.NewRegistry()
	slow := make(chan struct{})
	reg.Register("slow", func(ctx context.Context, lc loader.LoadContext) (any, error) {
		select {
		case <-slow:
			return "slow-data", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	routes := []*router.Route{{ID: "page", Path: "p", LoaderID: "slow"}}
	m := buildMatch(t, routes, "/p")
	w := &collectWriter{onData: func() {
		// 外壳已输出时慢数据尚未就绪。
		close(slow)
	}}
	r := NewRenderer(reg)
	if err := r.Stream(context.Background(), w, m); err != nil {
		t.Fatal(err)
	}
	if len(w.events) != 3 {
		t.Fatalf("事件数应为 3（shell/data/done），得到 %d", len(w.events))
	}
	if w.events[0].Kind != EventShell || w.events[1].Kind != EventData || w.events[2].Kind != EventDone {
		t.Fatalf("事件顺序错误: %v", kinds(w.events))
	}
	if !strings.Contains(w.events[0].Payload, `data-route="page"`) {
		t.Fatalf("外壳应包含路由占位: %s", w.events[0].Payload)
	}
	if !strings.Contains(w.events[1].Payload, "slow-data") {
		t.Fatalf("补齐事件应携带数据: %s", w.events[1].Payload)
	}
}

func TestStreamEventOrderStableAndNoDuplicate(t *testing.T) {
	reg := loader.NewRegistry()
	for _, id := range []string{"l1", "l2", "l3"} {
		id := id
		reg.Register(id, func(ctx context.Context, lc loader.LoadContext) (any, error) {
			return id + "-data", nil
		})
	}
	routes := []*router.Route{
		{ID: "a", Path: "a", LoaderID: "l1", Children: []*router.Route{
			{ID: "b", Path: "b", LoaderID: "l2", Children: []*router.Route{
				{ID: "c", Path: "c", LoaderID: "l3"},
			}},
		}},
	}
	m := buildMatch(t, routes, "/a/b/c")
	w := &collectWriter{}
	r := NewRenderer(reg)
	if err := r.Stream(context.Background(), w, m); err != nil {
		t.Fatal(err)
	}
	// shell + 3 data + done，每个路由恰好一次。
	if len(w.events) != 5 {
		t.Fatalf("事件数应为 5，得到 %d", len(w.events))
	}
	seen := map[string]int{}
	doneCount := 0
	for i, e := range w.events {
		if e.Kind == EventDone {
			doneCount++
			if i != len(w.events)-1 {
				t.Fatal("done 必须是最后一个事件")
			}
		}
		if e.Kind == EventData {
			seen[e.RouteID]++
		}
	}
	if doneCount != 1 {
		t.Fatalf("done 应恰好一次，得到 %d", doneCount)
	}
	for _, id := range []string{"a", "b", "c"} {
		if seen[id] != 1 {
			t.Fatalf("路由 %s 的数据事件应恰好一次，得到 %d", id, seen[id])
		}
	}
	// 数据事件必须按链顺序（上层先于下层）。
	if w.events[1].RouteID != "a" || w.events[2].RouteID != "b" || w.events[3].RouteID != "c" {
		t.Fatalf("数据事件顺序错误: %v", kinds(w.events))
	}
}

func TestStreamErrorBoundary(t *testing.T) {
	reg := loader.NewRegistry()
	reg.Register("ok", func(ctx context.Context, lc loader.LoadContext) (any, error) {
		return "ok-data", nil
	})
	reg.Register("bad", func(ctx context.Context, lc loader.LoadContext) (any, error) {
		return nil, errors.New("渲染数据爆炸")
	})
	reg.Register("never", func(ctx context.Context, lc loader.LoadContext) (any, error) {
		t.Error("错误边界下游的加载器不应被调用")
		return "x", nil
	})
	routes := []*router.Route{
		{ID: "top", Path: "t", LoaderID: "ok", Children: []*router.Route{
			{ID: "mid", Path: "m", LoaderID: "bad", Children: []*router.Route{
				{ID: "leaf", Path: "l", LoaderID: "never"},
			}},
		}},
	}
	m := buildMatch(t, routes, "/t/m/l")
	w := &collectWriter{}
	r := NewRenderer(reg)
	if err := r.Stream(context.Background(), w, m); err != nil {
		t.Fatal(err)
	}
	// shell + data(top) + error(mid) + done：上层保留，下层不渲染，结构有确定终止。
	if len(w.events) != 4 {
		t.Fatalf("事件数应为 4，得到 %d: %v", len(w.events), kinds(w.events))
	}
	if w.events[1].Kind != EventData || w.events[1].RouteID != "top" {
		t.Fatalf("上层数据应保留: %v", kinds(w.events))
	}
	if w.events[2].Kind != EventError || w.events[2].RouteID != "mid" {
		t.Fatalf("错误应定位到最近边界 mid: %v", kinds(w.events))
	}
	if !strings.Contains(w.events[2].Payload, "渲染数据爆炸") {
		t.Fatalf("错误事件应携带原因: %s", w.events[2].Payload)
	}
	if w.events[3].Kind != EventDone {
		t.Fatal("错误后仍应以 done 收尾，不得输出半截结构")
	}
}

func TestStreamCancelStopsEvents(t *testing.T) {
	reg := loader.NewRegistry()
	block := make(chan struct{})
	reg.Register("block", func(ctx context.Context, lc loader.LoadContext) (any, error) {
		select {
		case <-block:
			return "x", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	routes := []*router.Route{{ID: "p", Path: "p", LoaderID: "block"}}
	m := buildMatch(t, routes, "/p")
	ctx, cancel := context.WithCancel(context.Background())
	w := &collectWriter{onData: cancel} // 外壳输出后立即取消
	r := NewRenderer(reg)
	err := r.Stream(ctx, w, m)
	if err == nil {
		t.Fatal("取消后 Stream 应返回错误")
	}
	for _, e := range w.events {
		if e.Kind == EventDone {
			t.Fatal("取消后不得输出 done（半完成状态）")
		}
		if e.Kind == EventData {
			t.Fatal("取消后不得补齐数据")
		}
	}
	close(block)
	_ = time.Now
}

func kinds(events []Event) []string {
	var out []string
	names := map[EventKind]string{EventShell: "shell", EventData: "data", EventError: "error", EventDone: "done"}
	for _, e := range events {
		out = append(out, names[e.Kind]+":"+e.RouteID)
	}
	return out
}
