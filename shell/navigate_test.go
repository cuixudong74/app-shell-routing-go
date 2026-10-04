package shell

import (
	"context"
	"testing"
	"time"
)

func navRouter() *Router {
	mk := func(id, path string, delay time.Duration) *Route {
		return &Route{ID: id, Path: path,
			Loader: Defer(func(ctx context.Context, lc *LoaderContext) (any, error) {
				select {
				case <-time.After(delay):
					return id + "-data", nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}),
			Render: func(rc *RenderContext) (string, error) {
				if !rc.Ready {
					return id + ":loading", nil
				}
				return id + ":" + rc.Data.(string), nil
			}}
	}
	r, err := Compile([]*Route{
		mk("slow", "slow", 150*time.Millisecond),
		mk("fast", "fast", 5*time.Millisecond),
	})
	if err != nil {
		panic(err)
	}
	return r
}

func TestNavigateCancelsPrevious(t *testing.T) {
	nav := NewNavigator(navRouter())
	gen1, ch1, err := nav.Navigate("/slow")
	if err != nil {
		t.Fatalf("nav1: %v", err)
	}
	ev1 := <-ch1 // 旧导航外壳已输出
	if ev1.Type != EventShell {
		t.Fatalf("ev1 = %v", ev1.Type)
	}
	gen2, ch2, err := nav.Navigate("/fast")
	if err != nil {
		t.Fatalf("nav2: %v", err)
	}
	if gen2 <= gen1 {
		t.Errorf("gen not increasing: %d -> %d", gen1, gen2)
	}
	if nav.Current() != gen2 {
		t.Errorf("Current = %d, want %d", nav.Current(), gen2)
	}
	// 旧流必须终止且没有 Done、没有 patch（过期结果不得覆盖）
	for ev := range ch1 {
		if ev.Type == EventDone || ev.Type == EventPatch {
			t.Errorf("stale stream emitted %v after being superseded", ev.Type)
		}
	}
	// 新流完整到达 Done
	evs := collect(ch2)
	if len(evs) == 0 || evs[len(evs)-1].Type != EventDone {
		t.Fatalf("new stream did not complete: %+v", evs)
	}
	found := false
	for _, ev := range evs {
		if ev.Type == EventPatch && ev.HTML == "fast:fast-data" {
			found = true
		}
	}
	if !found {
		t.Errorf("new stream missing patch: %+v", evs)
	}
}

func TestNavigateTimeout(t *testing.T) {
	nav := NewNavigator(navRouter())
	_, ch, err := nav.NavigateTimeout("/slow", 30*time.Millisecond)
	if err != nil {
		t.Fatalf("nav: %v", err)
	}
	var evs []Event
	for ev := range ch {
		evs = append(evs, ev)
	}
	if len(evs) == 0 || evs[0].Type != EventShell {
		t.Fatalf("expected shell before timeout, got %+v", evs)
	}
	for _, ev := range evs {
		if ev.Type == EventDone {
			t.Errorf("got Done despite timeout")
		}
	}
}

func TestNavigateNoMatchStillCancelsPrevious(t *testing.T) {
	nav := NewNavigator(navRouter())
	_, ch1, err := nav.Navigate("/slow")
	if err != nil {
		t.Fatalf("nav1: %v", err)
	}
	<-ch1 // shell
	if _, _, err := nav.Navigate("/nowhere"); err == nil {
		t.Fatal("expected NoMatchError")
	} else if _, ok := err.(*NoMatchError); !ok {
		t.Fatalf("err = %T", err)
	}
	for ev := range ch1 {
		if ev.Type == EventDone || ev.Type == EventPatch {
			t.Errorf("stale stream emitted %v after failed navigation", ev.Type)
		}
	}
}
