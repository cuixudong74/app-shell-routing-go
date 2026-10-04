package shell

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func collect(ch <-chan Event) []Event {
	var out []Event
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func shellRouter(slowDelay time.Duration, slowErr error) *Router {
	r, err := Compile([]*Route{
		{ID: "root", Path: "", Render: func(rc *RenderContext) (string, error) {
			return "<root>" + rc.Child + "</root>", nil
		}, Children: []*Route{
			{ID: "page", Path: "page",
				Loader: Defer(func(ctx context.Context, lc *LoaderContext) (any, error) {
					select {
					case <-time.After(slowDelay):
						if slowErr != nil {
							return nil, slowErr
						}
						return "slow-data", nil
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}),
				Render: func(rc *RenderContext) (string, error) {
					if !rc.Ready {
						return `<div id="page">LOADING</div>`, nil
					}
					return `<div id="page">` + rc.Data.(string) + `</div>`, nil
				}},
		}},
	})
	if err != nil {
		panic(err)
	}
	return r
}

func TestStreamShellThenPatchThenDone(t *testing.T) {
	r := shellRouter(30*time.Millisecond, nil)
	ch, err := Stream(context.Background(), r, "/page")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	evs := collect(ch)
	if len(evs) != 3 {
		t.Fatalf("events = %d, want 3: %+v", len(evs), evs)
	}
	if evs[0].Type != EventShell || evs[1].Type != EventPatch || evs[2].Type != EventDone {
		t.Fatalf("types = %v,%v,%v", evs[0].Type, evs[1].Type, evs[2].Type)
	}
	for i, ev := range evs {
		if ev.Seq != i {
			t.Errorf("ev[%d].Seq = %d", i, ev.Seq)
		}
	}
	// 外壳不含慢数据，且带占位
	if strings.Contains(evs[0].HTML, "slow-data") || !strings.Contains(evs[0].HTML, "LOADING") {
		t.Errorf("shell html = %q", evs[0].HTML)
	}
	// 补丁携带就绪数据，且只出现一次
	if !strings.Contains(evs[1].HTML, "slow-data") {
		t.Errorf("patch html = %q", evs[1].HTML)
	}
	count := 0
	for _, ev := range evs {
		if strings.Contains(ev.HTML, "slow-data") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("slow-data appeared %d times, want exactly 1", count)
	}
}

func TestStreamDeferredErrorUsesBoundary(t *testing.T) {
	r := shellRouter(10*time.Millisecond, errors.New("slow failed"))
	// 给 page 加边界
	r.roots[0].Children[0].Boundary = func(rc *RenderContext, err *RouteError) (string, error) {
		return "PAGE-FALLBACK(" + err.RouteID + ")", nil
	}
	ch, err := Stream(context.Background(), r, "/page")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	evs := collect(ch)
	if len(evs) != 3 {
		t.Fatalf("events = %d: %+v", len(evs), evs)
	}
	if evs[1].Type != EventPatch || !strings.Contains(evs[1].HTML, "PAGE-FALLBACK(page)") {
		t.Errorf("patch = %+v", evs[1])
	}
	if evs[1].Err == nil || evs[1].Err.RouteID != "page" {
		t.Errorf("patch should carry locatable error: %+v", evs[1].Err)
	}
	if evs[2].Type != EventDone {
		t.Errorf("last = %v", evs[2].Type)
	}
}

func TestStreamUncaughtDeferredErrorTerminates(t *testing.T) {
	r := shellRouter(10*time.Millisecond, errors.New("slow failed"))
	ch, err := Stream(context.Background(), r, "/page")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	evs := collect(ch)
	if len(evs) != 2 {
		t.Fatalf("events = %d: %+v", len(evs), evs)
	}
	if evs[1].Type != EventError || evs[1].Err.RouteID != "page" {
		t.Errorf("terminal = %+v", evs[1])
	}
}

func TestStreamCancelStopsEvents(t *testing.T) {
	r := shellRouter(200*time.Millisecond, nil)
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := Stream(ctx, r, "/page")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	first := <-ch // shell
	if first.Type != EventShell {
		t.Fatalf("first = %v", first.Type)
	}
	cancel()
	rest := collect(ch)
	for _, ev := range rest {
		if ev.Type == EventDone {
			t.Errorf("got Done after cancel")
		}
	}
}

func TestStreamNoMatch(t *testing.T) {
	r := shellRouter(0, nil)
	if _, err := Stream(context.Background(), r, "/nope"); err == nil {
		t.Fatal("expected NoMatchError")
	} else if _, ok := err.(*NoMatchError); !ok {
		t.Fatalf("err type = %T", err)
	}
}
