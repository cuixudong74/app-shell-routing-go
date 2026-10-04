package shell

import (
	"strings"
	"testing"
)

func TestMatchPriorityStaticOverParamOverWildcard(t *testing.T) {
	routes := []*Route{
		{ID: "wild", Path: "*rest"},
		{ID: "param", Path: ":id"},
		{ID: "static", Path: "about"},
	}
	r, err := Compile(routes)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	cases := map[string]string{
		"/about":   "static",
		"/xyz":     "param",
		"/a/b/c":   "wild",
	}
	for path, wantID := range cases {
		chain, err := r.Match(path)
		if err != nil {
			t.Fatalf("match %s: %v", path, err)
		}
		if got := chain[len(chain)-1].Route.ID; got != wantID {
			t.Errorf("match %s: got %q want %q", path, got, wantID)
		}
	}
}

func TestMatchNestedAndParams(t *testing.T) {
	routes := []*Route{
		{ID: "root", Path: "", Children: []*Route{
			{ID: "users", Path: "users", Children: []*Route{
				{ID: "user", Path: ":id"},
			}},
		}},
	}
	r, err := Compile(routes)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	chain, err := r.Match("/users/42")
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(chain) != 3 {
		t.Fatalf("chain len = %d, want 3", len(chain))
	}
	if chain[2].Params["id"] != "42" {
		t.Errorf("param id = %q, want 42", chain[2].Params["id"])
	}
}

func TestNoMatchIsExplicit(t *testing.T) {
	r, err := Compile([]*Route{{ID: "a", Path: "a"}})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, p := range []string{"/b", "/a/extra"} {
		if _, err := r.Match(p); err == nil {
			t.Errorf("match %s: expected NoMatchError, got nil", p)
		} else if _, ok := err.(*NoMatchError); !ok {
			t.Errorf("match %s: expected *NoMatchError, got %T", p, err)
		}
	}
}

func TestCompileRejectsConflicts(t *testing.T) {
	cases := []struct {
		name   string
		routes []*Route
		want   string
	}{
		{"dup static", []*Route{{ID: "a", Path: "x"}, {ID: "b", Path: "x"}}, "conflict"},
		{"dup param", []*Route{{ID: "a", Path: ":id"}, {ID: "b", Path: ":name"}}, "conflict"},
		{"dup wildcard", []*Route{{ID: "a", Path: "*a"}, {ID: "b", Path: "*b"}}, "conflict"},
		{"dup id", []*Route{{ID: "a", Path: "x"}, {ID: "a", Path: "y"}}, "duplicate route id"},
		{"wildcard not last", []*Route{{ID: "a", Path: "*r/x"}}, "last segment"},
		{"nested dup", []*Route{{ID: "p", Path: "p", Children: []*Route{{ID: "a", Path: "x"}, {ID: "b", Path: "x"}}}}, "conflict"},
	}
	for _, tc := range cases {
		_, err := Compile(tc.routes)
		if err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not contain %q", tc.name, err, tc.want)
		}
	}
}

func TestMatchDeterministicRepeated(t *testing.T) {
	build := func() *Router {
		r, err := Compile([]*Route{
			{ID: "wild", Path: "*rest"},
			{ID: "param", Path: ":id"},
			{ID: "static", Path: "a"},
		})
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		return r
	}
	for i := 0; i < 20; i++ {
		chain, err := build().Match("/zzz")
		if err != nil || chain[0].Route.ID != "param" {
			t.Fatalf("iter %d: got %v %v", i, chain, err)
		}
	}
}
