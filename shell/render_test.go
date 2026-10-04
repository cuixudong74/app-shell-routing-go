package shell

import (
	"errors"
	"strings"
	"testing"
)

func layout(id string) *Route {
	return &Route{ID: id, Render: func(rc *RenderContext) (string, error) {
		return "<" + rc.RouteID + ">" + rc.Child + "</" + rc.RouteID + ">", nil
	}}
}

func TestRenderChainNestsLayouts(t *testing.T) {
	chain := []*Match{
		{Route: layout("root"), Params: map[string]string{}},
		{Route: layout("dash"), Params: map[string]string{}},
		{Route: &Route{ID: "leaf", Render: func(rc *RenderContext) (string, error) {
			return "leaf-content", nil
		}}, Params: map[string]string{}},
	}
	html, rerr := RenderChain(chain, []LoadResult{{}, {}, {}})
	if rerr != nil {
		t.Fatalf("render: %v", rerr)
	}
	if html != "<root><dash>leaf-content</dash></root>" {
		t.Errorf("html = %q", html)
	}
}

func TestErrorBoundaryCatchesChildLoadError(t *testing.T) {
	boom := errors.New("db down")
	root := layout("root")
	root.Boundary = func(rc *RenderContext, err *RouteError) (string, error) {
		return "<root-boundary>" + err.RouteID + ":" + err.Phase + "</root-boundary>", nil
	}
	chain := []*Match{
		{Route: root, Params: map[string]string{}},
		{Route: layout("dash"), Params: map[string]string{}},
		{Route: &Route{ID: "leaf"}, Params: map[string]string{}},
	}
	results := []LoadResult{
		{RouteID: "root"},
		{RouteID: "dash"},
		{RouteID: "leaf", Err: &RouteError{RouteID: "leaf", Phase: "load", Err: boom}},
	}
	html, rerr := RenderChain(chain, results)
	if rerr != nil {
		t.Fatalf("render: %v", rerr)
	}
	if html != "<root-boundary>leaf:load</root-boundary>" {
		t.Errorf("html = %q", html)
	}
}

func TestNearestBoundaryWins(t *testing.T) {
	root := layout("root")
	root.Boundary = func(rc *RenderContext, err *RouteError) (string, error) {
		return "ROOT-BOUNDARY", nil
	}
	dash := layout("dash")
	dash.Boundary = func(rc *RenderContext, err *RouteError) (string, error) {
		return "<dash-boundary>" + err.RouteID + "</dash-boundary>", nil
	}
	chain := []*Match{
		{Route: root, Params: map[string]string{}},
		{Route: dash, Params: map[string]string{}},
		{Route: &Route{ID: "leaf", Render: func(rc *RenderContext) (string, error) {
			return "", errors.New("render boom")
		}}, Params: map[string]string{}},
	}
	html, rerr := RenderChain(chain, []LoadResult{{}, {}, {}})
	if rerr != nil {
		t.Fatalf("render: %v", rerr)
	}
	// 就近边界 dash 捕获，且 root 正常包裹
	if html != "<root><dash-boundary>leaf</dash-boundary></root>" {
		t.Errorf("html = %q", html)
	}
}

func TestUncaughtErrorReturnsRouteError(t *testing.T) {
	chain := []*Match{
		{Route: layout("root"), Params: map[string]string{}},
		{Route: &Route{ID: "leaf", Render: func(rc *RenderContext) (string, error) {
			return "", errors.New("kaboom")
		}}, Params: map[string]string{}},
	}
	html, rerr := RenderChain(chain, []LoadResult{{}, {}})
	if rerr == nil {
		t.Fatal("expected RouteError, got nil")
	}
	if html != "" {
		t.Errorf("html should be empty on uncaught error, got %q", html)
	}
	if rerr.RouteID != "leaf" || rerr.Phase != "render" {
		t.Errorf("RouteError = %+v", rerr)
	}
	if !strings.Contains(rerr.Error(), "leaf") {
		t.Errorf("error message not locatable: %v", rerr)
	}
}

func TestOwnBoundaryCatchesOwnLoadError(t *testing.T) {
	leaf := &Route{ID: "leaf"}
	leaf.Boundary = func(rc *RenderContext, err *RouteError) (string, error) {
		return "leaf-fallback", nil
	}
	chain := []*Match{
		{Route: layout("root"), Params: map[string]string{}},
		{Route: leaf, Params: map[string]string{}},
	}
	results := []LoadResult{
		{RouteID: "root"},
		{RouteID: "leaf", Err: &RouteError{RouteID: "leaf", Phase: "load", Err: errors.New("x")}},
	}
	html, rerr := RenderChain(chain, results)
	if rerr != nil {
		t.Fatalf("render: %v", rerr)
	}
	if html != "<root>leaf-fallback</root>" {
		t.Errorf("html = %q", html)
	}
}
