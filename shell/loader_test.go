package shell

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadChainParentBeforeChild(t *testing.T) {
	var order []string
	record := func(name string) func(context.Context, *LoaderContext) (any, error) {
		return func(ctx context.Context, lc *LoaderContext) (any, error) {
			order = append(order, name)
			return name + "-data", nil
		}
	}
	chain := []*Match{
		{Route: &Route{ID: "a", Loader: record("a")}, Params: map[string]string{}},
		{Route: &Route{ID: "b", Loader: record("b")}, Params: map[string]string{}},
		{Route: &Route{ID: "c", Loader: record("c")}, Params: map[string]string{}},
	}
	results := LoadChain(context.Background(), chain)
	if len(results) != 3 {
		t.Fatalf("results len = %d", len(results))
	}
	for i, want := range []string{"a", "b", "c"} {
		if order[i] != want {
			t.Errorf("order[%d] = %q, want %q", i, order[i], want)
		}
		if results[i].Data != want+"-data" {
			t.Errorf("results[%d].Data = %v", i, results[i].Data)
		}
	}
}

func TestLoadChainChildSeesParentData(t *testing.T) {
	chain := []*Match{
		{Route: &Route{ID: "p", Loader: func(ctx context.Context, lc *LoaderContext) (any, error) {
			return "parent-data", nil
		}}, Params: map[string]string{}},
		{Route: &Route{ID: "c", Loader: func(ctx context.Context, lc *LoaderContext) (any, error) {
			if lc.ParentData != "parent-data" {
				return nil, errors.New("parent data not ready")
			}
			return lc.ParentData.(string) + "+child", nil
		}}, Params: map[string]string{"id": "7"}},
	}
	results := LoadChain(context.Background(), chain)
	if results[1].Err != nil {
		t.Fatalf("child err: %v", results[1].Err)
	}
	if results[1].Data != "parent-data+child" {
		t.Errorf("child data = %v", results[1].Data)
	}
}

func TestLoadTreeSiblingsConcurrent(t *testing.T) {
	var inFlight, maxInFlight int32
	slow := func(ctx context.Context, lc *LoaderContext) (any, error) {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			old := atomic.LoadInt32(&maxInFlight)
			if n <= old || atomic.CompareAndSwapInt32(&maxInFlight, old, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		return lc.RouteID, nil
	}
	tree := &MatchNode{
		Match: &Match{Route: &Route{ID: "root"}, Params: map[string]string{}},
		Children: []*MatchNode{
			{Match: &Match{Route: &Route{ID: "s1", Loader: slow}, Params: map[string]string{}}},
			{Match: &Match{Route: &Route{ID: "s2", Loader: slow}, Params: map[string]string{}}},
			{Match: &Match{Route: &Route{ID: "s3", Loader: slow}, Params: map[string]string{}}},
		},
	}
	start := time.Now()
	out := LoadTree(context.Background(), tree)
	elapsed := time.Since(start)
	if len(out.Children) != 3 {
		t.Fatalf("children = %d", len(out.Children))
	}
	if atomic.LoadInt32(&maxInFlight) < 2 {
		t.Errorf("siblings did not run concurrently, maxInFlight=%d", maxInFlight)
	}
	if elapsed > 140*time.Millisecond {
		t.Errorf("took %v, siblings appear serialized", elapsed)
	}
}

func TestLoadFailureLocatedAndStopsChildren(t *testing.T) {
	boom := errors.New("db down")
	childRan := false
	chain := []*Match{
		{Route: &Route{ID: "ok"}, Params: map[string]string{}},
		{Route: &Route{ID: "bad", Loader: func(ctx context.Context, lc *LoaderContext) (any, error) {
			return nil, boom
		}}, Params: map[string]string{}},
		{Route: &Route{ID: "child", Loader: func(ctx context.Context, lc *LoaderContext) (any, error) {
			childRan = true
			return nil, nil
		}}, Params: map[string]string{}},
	}
	results := LoadChain(context.Background(), chain)
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2 (child must not load)", len(results))
	}
	re, ok := results[1].Err.(*RouteError)
	if !ok {
		t.Fatalf("err type = %T", results[1].Err)
	}
	if re.RouteID != "bad" || re.Phase != "load" || !errors.Is(re.Err, boom) {
		t.Errorf("RouteError = %+v", re)
	}
	if childRan {
		t.Error("child loader ran despite parent failure")
	}
}
