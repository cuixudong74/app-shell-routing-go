package router

import (
	"strings"
	"testing"
)

func TestBuildRejectsDuplicateRoutes(t *testing.T) {
	_, err := Build([]*Route{
		{ID: "a", Path: "users"},
		{ID: "b", Path: "users"},
	})
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("期望 ConflictError，得到 %v", err)
	}
	if ce.RouteA != "a" || ce.RouteB != "b" {
		t.Fatalf("冲突未定位到具体路由: %+v", ce)
	}
}

func TestBuildRejectsAmbiguousParams(t *testing.T) {
	// 同层 ":id" 与 ":name" 不可区分，必须被拒绝。
	_, err := Build([]*Route{
		{ID: "a", Path: ":id"},
		{ID: "b", Path: ":name"},
	})
	if err == nil {
		t.Fatal("期望同层参数段冲突被拒绝")
	}
	if !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
		t.Fatalf("错误应定位到具体路由: %v", err)
	}
}

func TestBuildRejectsDuplicateID(t *testing.T) {
	_, err := Build([]*Route{
		{ID: "a", Path: "x"},
		{ID: "a", Path: "y"},
	})
	if err == nil {
		t.Fatal("期望重复 ID 被拒绝")
	}
}

func TestBuildRejectsInvalidPattern(t *testing.T) {
	if _, err := Build([]*Route{{ID: "a", Path: "*rest/more"}}); err == nil {
		t.Fatal("通配段不在末尾应被拒绝")
	}
	if _, err := Build([]*Route{{ID: "a", Path: ":"}}); err == nil {
		t.Fatal("空参数名应被拒绝")
	}
}

func TestMatchPriorityDeterministic(t *testing.T) {
	// 静态 > 参数 > 通配，与声明顺序无关。
	routes := []*Route{
		{ID: "wild", Path: "*rest"},
		{ID: "param", Path: ":id"},
		{ID: "static", Path: "users"},
	}
	tree, err := Build(routes)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path string
		want string
	}{
		{"/users", "static"},
		{"/other", "param"},
		{"/a/b/c", "wild"},
	}
	for _, c := range cases {
		m, ok := tree.Match(c.path)
		if !ok {
			t.Fatalf("路径 %s 未匹配", c.path)
		}
		if got := m.Routes[len(m.Routes)-1].ID; got != c.want {
			t.Fatalf("路径 %s 命中 %s，期望 %s", c.path, got, c.want)
		}
	}
}

func TestMatchNestedParams(t *testing.T) {
	tree, err := Build([]*Route{
		{ID: "root", Path: "", Children: []*Route{
			{ID: "users", Path: "users", Children: []*Route{
				{ID: "user", Path: ":id", Children: []*Route{
					{ID: "posts", Path: "posts/:postId"},
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := tree.Match("/users/42/posts/7")
	if !ok {
		t.Fatal("嵌套路径未匹配")
	}
	var ids []string
	for _, r := range m.Routes {
		ids = append(ids, r.ID)
	}
	want := []string{"root", "users", "user", "posts"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("匹配链 %v，期望 %v", ids, want)
	}
	if m.Params["id"] != "42" || m.Params["postId"] != "7" {
		t.Fatalf("参数提取错误: %v", m.Params)
	}
}

func TestMatchMissReturnsFalse(t *testing.T) {
	tree, _ := Build([]*Route{{ID: "a", Path: "users"}})
	if _, ok := tree.Match("/nope"); ok {
		t.Fatal("未命中应返回 false 以便上层兜底")
	}
}

func TestMatchBacktracksDeterministically(t *testing.T) {
	// 参数段子树失败时应回退到通配段，且结果确定。
	tree, err := Build([]*Route{
		{ID: "param", Path: ":id", Children: []*Route{
			{ID: "edit", Path: "edit"},
		}},
		{ID: "wild", Path: "*rest"},
	})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := tree.Match("/a/b")
	if !ok || m.Routes[0].ID != "wild" {
		t.Fatalf("应回退到通配路由: %+v", m)
	}
	m, ok = tree.Match("/a/edit")
	if !ok || m.Routes[len(m.Routes)-1].ID != "edit" {
		t.Fatalf("应命中参数子树: %+v", m)
	}
}
