package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/cuixudong74/app-shell-routing-go/loader"
	"github.com/cuixudong74/app-shell-routing-go/nav"
	"github.com/cuixudong74/app-shell-routing-go/render"
	"github.com/cuixudong74/app-shell-routing-go/router"
)

// startLoopback 在本地回环上启动真实服务器。
func startLoopback(t *testing.T, block map[string]chan struct{}) (*http.Client, string, func()) {
	t.Helper()
	reg := loader.NewRegistry()
	reg.Register("fast", func(ctx context.Context, lc loader.LoadContext) (any, error) {
		return "fast-data", nil
	})
	reg.Register("slow", func(ctx context.Context, lc loader.LoadContext) (any, error) {
		select {
		case <-block["slow"]:
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
	srv := New(nav.NewNavigator(tree, render.NewRenderer(reg)))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpSrv := &http.Server{Handler: srv.Handler()}
	go httpSrv.Serve(ln)
	return http.DefaultClient, fmt.Sprintf("http://%s", ln.Addr()), func() { httpSrv.Close() }
}

func readEvents(t *testing.T, resp *http.Response) []render.Event {
	t.Helper()
	defer resp.Body.Close()
	var events []render.Event
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var e render.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("事件解码失败: %v", err)
		}
		events = append(events, e)
	}
	return events
}

func TestLoopbackRenderStreamsInOrder(t *testing.T) {
	client, base, stop := startLoopback(t, map[string]chan struct{}{})
	defer stop()
	resp, err := client.Get(base + "/render?path=/fast")
	if err != nil {
		t.Fatal(err)
	}
	events := readEvents(t, resp)
	if len(events) != 3 {
		t.Fatalf("应收到 3 个事件，得到 %d", len(events))
	}
	if events[0].Kind != render.EventShell || events[1].Kind != render.EventData || events[2].Kind != render.EventDone {
		t.Fatalf("回环请求的事件序列错误: %+v", events)
	}
}

func TestLoopbackUnmatchedFallback(t *testing.T) {
	client, base, stop := startLoopback(t, map[string]chan struct{}{})
	defer stop()
	resp, err := client.Get(base + "/render?path=/nope")
	if err != nil {
		t.Fatal(err)
	}
	events := readEvents(t, resp)
	if len(events) != 2 || events[0].Kind != render.EventError || events[1].Kind != render.EventDone {
		t.Fatalf("未命中应返回明确兜底事件: %+v", events)
	}
}

func TestLoopbackNavigationCancelsPrevious(t *testing.T) {
	block := map[string]chan struct{}{"slow": make(chan struct{})}
	client, base, stop := startLoopback(t, block)
	defer stop()

	// 发起慢导航，读到外壳后发起新导航。
	slowResp, err := client.Get(base + "/render?path=/slow")
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(slowResp.Body)
	if !sc.Scan() {
		t.Fatal("慢导航应先输出外壳")
	}
	var shell render.Event
	json.Unmarshal(sc.Bytes(), &shell)
	if shell.Kind != render.EventShell {
		t.Fatalf("首事件应为外壳: %v", shell.Kind)
	}

	fastResp, err := client.Get(base + "/render?path=/fast")
	if err != nil {
		t.Fatal(err)
	}
	fastEvents := readEvents(t, fastResp)
	if fastEvents[len(fastEvents)-1].Kind != render.EventDone {
		t.Fatal("新导航应完整完成")
	}

	// 旧导航流应终止且没有 done；放行慢加载器后也不会复活。
	deadline := time.After(2 * time.Second)
	gotDone := false
	for sc.Scan() {
		var e render.Event
		json.Unmarshal(sc.Bytes(), &e)
		if e.Kind == render.EventDone || e.Kind == render.EventData {
			gotDone = true
		}
		select {
		case <-deadline:
			t.Fatal("旧导航流未终止")
		default:
		}
	}
	slowResp.Body.Close()
	close(block["slow"])
	if gotDone {
		t.Fatal("被取消的导航不得输出 data/done")
	}

	// 代际号应已推进。
	resp, err := client.Get(base + "/generation")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Generation int64 `json:"generation"`
	}
	json.NewDecoder(resp.Body).Decode(&g)
	resp.Body.Close()
	if g.Generation < 2 {
		t.Fatalf("代际号应至少为 2，得到 %d", g.Generation)
	}
}
