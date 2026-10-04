package shell

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func e2eServer(t *testing.T) *httptest.Server {
	t.Helper()
	slow := &Route{ID: "slow", Path: "slow",
		Loader: Defer(func(ctx context.Context, lc *LoaderContext) (any, error) {
			select {
			case <-time.After(20 * time.Millisecond):
				return "slow-data", nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}),
		Render: func(rc *RenderContext) (string, error) {
			if !rc.Ready {
				return "SLOW-LOADING", nil
			}
			return "SLOW:" + rc.Data.(string), nil
		}}
	root := &Route{ID: "root", Path: "",
		Loader: func(ctx context.Context, lc *LoaderContext) (any, error) { return "root-data", nil },
		Render: func(rc *RenderContext) (string, error) {
			return "<root>" + rc.Child + "</root>", nil
		},
		Children: []*Route{
			slow,
			{ID: "user", Path: "user/:id",
				Loader: func(ctx context.Context, lc *LoaderContext) (any, error) {
					if lc.ParentData != "root-data" {
						t.Errorf("parent data not ready before child load")
					}
					return "user-" + lc.Params["id"], nil
				},
				Render: func(rc *RenderContext) (string, error) {
					return "USER:" + rc.Data.(string), nil
				}},
		}}
	r, err := Compile([]*Route{root})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	manifest := &Manifest{Version: 2, Routes: []RouteSpec{
		{ID: "root", Path: "", Type: "layout", Assets: []string{"app.css"}, Children: []RouteSpec{
			{ID: "slow", Path: "slow", Type: "page", Assets: []string{"slow.js"}},
		}},
	}}
	srv := NewServer(NewNavigator(r), manifest)
	return httptest.NewServer(srv.Handler())
}

func readNDJSON(t *testing.T, resp *http.Response) []eventJSON {
	t.Helper()
	defer resp.Body.Close()
	var out []eventJSON
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var ev eventJSON
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("bad ndjson line %q: %v", sc.Text(), err)
		}
		out = append(out, ev)
	}
	return out
}

func TestServerStreamOverLoopback(t *testing.T) {
	ts := e2eServer(t)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/navigate?path=/slow")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	evs := readNDJSON(t, resp)
	if len(evs) != 3 {
		t.Fatalf("events = %d: %+v", len(evs), evs)
	}
	if evs[0].Type != "shell" || !strings.Contains(evs[0].HTML, "SLOW-LOADING") {
		t.Errorf("shell = %+v", evs[0])
	}
	if evs[1].Type != "patch" || !strings.Contains(evs[1].HTML, "SLOW:slow-data") {
		t.Errorf("patch = %+v", evs[1])
	}
	if evs[2].Type != "done" {
		t.Errorf("done = %+v", evs[2])
	}
}

func TestServerNestedParamsOverLoopback(t *testing.T) {
	ts := e2eServer(t)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/navigate?path=/user/42")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	evs := readNDJSON(t, resp)
	if len(evs) != 2 || evs[0].Type != "shell" || evs[1].Type != "done" {
		t.Fatalf("events = %+v", evs)
	}
	if evs[0].HTML != "<root>USER:user-42</root>" {
		t.Errorf("html = %q", evs[0].HTML)
	}
}

func TestServerNoMatch404(t *testing.T) {
	ts := e2eServer(t)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/navigate?path=/nope")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if !strings.Contains(body["error"], "/nope") {
		t.Errorf("body = %v", body)
	}
}

func TestServerManifestDeterministicOverLoopback(t *testing.T) {
	ts := e2eServer(t)
	defer ts.Close()
	var bodies []string
	for i := 0; i < 3; i++ {
		resp, err := http.Get(ts.URL + "/manifest")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		buf := make([]byte, 4096)
		n, _ := resp.Body.Read(buf)
		resp.Body.Close()
		bodies = append(bodies, string(buf[:n]))
	}
	for i := 1; i < len(bodies); i++ {
		if bodies[i] != bodies[0] {
			t.Fatalf("manifest not byte-identical: %q vs %q", bodies[0], bodies[i])
		}
	}
	want := `[{"routeId":"root","assets":["app.css"]},{"routeId":"slow","assets":["app.css","slow.js"]}]`
	if bodies[0] != want {
		t.Errorf("manifest = %s, want %s", bodies[0], want)
	}
}
