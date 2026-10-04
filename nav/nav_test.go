package nav

import (
	"context"
	"testing"
	"time"

	"github.com/cuixudong74/app-shell-routing-go/loader"
	"github.com/cuixudong74/app-shell-routing-go/render"
	"github.com/cuixudong74/app-shell-routing-go/router"
)

func setup(t *testing.T, block map[string]chan struct{}) *Navigator {
	t.Helper()
	reg := loader.NewRegistry()
	reg.Register("fast", func(ctx context.Context, lc loader.LoadContext) (any, error) {
		return "fast-data", nil
	})
	reg.Register("slow", func(ctx context.Context, lc loader.LoadContext) (any, error) {
		ch := block["slow"]
		select {
		case <-ch:
			return "slow-data", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	routes := []*router.Route{
		{ID: "fast", Path: "fast", LoaderID: "fast"},
		{ID: "slow", Path: "slow", LoaderID: "slow"},
	}
	tree, err := router.Build(routes)
	if err != nil {
		t.Fatal(err)
	}
	return NewNavigator(tree, render.NewRenderer(reg))
}

func drain(ch <-chan render.Event) []render.Event {
	var out []render.Event
	for e := range ch {
		out = append(out, e)
	}
	return out
}

func TestNewNavigationCancelsOld(t *testing.T) {
	block := map[string]chan struct{}{"slow": make(chan struct{})}
	n := setup(t, block)

	oldCh, _ := n.Navigate("/slow")
	// 等旧导航输出外壳（说明它正在等待慢数据）。
	e := <-oldCh
	if e.Kind != render.EventShell {
		t.Fatalf("旧导航应先输出外壳，得到 %v", e.Kind)
	}
	gen1 := n.Current()

	newCh, _ := n.Navigate("/fast")
	if n.Current() != gen1+1 {
		t.Fatal("新导航应推进代际号")
	}

	// 旧导航必须被取消：通道关闭，且不出现 data/done。
	for e := range oldCh {
		if e.Kind == render.EventData || e.Kind == render.EventDone {
			t.Fatalf("被取消的导航不得再产出事件: %v", e.Kind)
		}
	}
	// 放行慢加载器，验证旧结果也不会复活。
	close(block["slow"])
	time.Sleep(50 * time.Millisecond)

	events := drain(newCh)
	var kinds []render.EventKind
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	if len(events) != 3 || kinds[0] != render.EventShell || kinds[1] != render.EventData || kinds[2] != render.EventDone {
		t.Fatalf("新导航事件序列错误: %v", kinds)
	}
}

func TestStaleGenerationCannotOverwrite(t *testing.T) {
	block := map[string]chan struct{}{"slow": make(chan struct{})}
	n := setup(t, block)

	slowCh, _ := n.Navigate("/slow")
	<-slowCh // shell
	fastCh, _ := n.Navigate("/fast")
	fastEvents := drain(fastCh)

	// 慢导航被放行后，其结果不得混入已完成的新代际。
	close(block["slow"])
	time.Sleep(50 * time.Millisecond)
	select {
	case e, ok := <-slowCh:
		if ok && (e.Kind == render.EventData || e.Kind == render.EventDone) {
			t.Fatalf("过期代际不得产出完成事件: %v", e.Kind)
		}
	default:
	}
	// 新代际已完整结束，不受任何影响。
	if fastEvents[len(fastEvents)-1].Kind != render.EventDone {
		t.Fatal("新代际应正常完成")
	}
}

func TestUnmatchedPathFallback(t *testing.T) {
	n := setup(t, map[string]chan struct{}{})
	ch, _ := n.Navigate("/nowhere")
	events := drain(ch)
	if len(events) != 2 || events[0].Kind != render.EventError || events[1].Kind != render.EventDone {
		t.Fatalf("未命中应输出明确的兜底事件: %v", events)
	}
}

func TestManualCancel(t *testing.T) {
	block := map[string]chan struct{}{"slow": make(chan struct{})}
	n := setup(t, block)
	ch, cancel := n.Navigate("/slow")
	<-ch // shell
	cancel()
	for e := range ch {
		if e.Kind == render.EventDone {
			t.Fatal("手动取消后不得输出 done")
		}
	}
	close(block["slow"])
}
