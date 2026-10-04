package manifest

import (
	"strings"
	"testing"

	"github.com/cuixudong74/app-shell-routing-go/router"
)

func TestParseV1MigratesToCurrent(t *testing.T) {
	data := []byte(`{
		"version": 1,
		"routes": [
			{"id": "root", "path": "", "children": [
				{"id": "users", "path": "users", "loader": "usersLoader"},
				{"id": "user", "path": ":id", "loader": "userLoader"},
				{"id": "rest", "path": "*rest"}
			]}
		]
	}`)
	m, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != CurrentVersion {
		t.Fatalf("迁移后版本应为 %d，得到 %d", CurrentVersion, m.Version)
	}
	users := m.Routes[0].Children[0]
	if users.LoaderID != "usersLoader" {
		t.Fatalf("v1 的 loader 字段应迁移为 loaderId: %+v", users)
	}
	if users.Type != "static" {
		t.Fatalf("应推断为 static: %s", users.Type)
	}
	if m.Routes[0].Children[1].Type != "param" {
		t.Fatal("应推断为 param")
	}
	if m.Routes[0].Children[2].Type != "wildcard" {
		t.Fatal("应推断为 wildcard")
	}
	routes, err := m.ToRoutes()
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || len(routes[0].Children) != 3 {
		t.Fatalf("转换后的路由树结构错误: %+v", routes)
	}
}

func TestParseRejectsUnknownVersion(t *testing.T) {
	_, err := Parse([]byte(`{"version": 99, "routes": []}`))
	if err == nil || !strings.Contains(err.Error(), "99") {
		t.Fatalf("未知版本应明确拒绝并说明版本号: %v", err)
	}
	_, err = Parse([]byte(`{"routes": []}`))
	if err == nil {
		t.Fatal("缺失版本应被拒绝")
	}
}

func TestParseRejectsUnknownType(t *testing.T) {
	data := []byte(`{
		"version": 2,
		"routes": [{"id": "x", "path": "x", "type": "magic"}]
	}`)
	_, err := Parse(data)
	if err == nil || !strings.Contains(err.Error(), `"x"`) || !strings.Contains(err.Error(), "magic") {
		t.Fatalf("未知类型应拒绝并定位路由: %v", err)
	}
}

func TestParseRejectsTypePathMismatch(t *testing.T) {
	data := []byte(`{
		"version": 2,
		"routes": [{"id": "x", "path": ":id", "type": "static"}]
	}`)
	_, err := Parse(data)
	if err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("类型与路径不一致应被拒绝: %v", err)
	}
}

func TestParseRejectsInvalidPath(t *testing.T) {
	data := []byte(`{
		"version": 2,
		"routes": [{"id": "x", "path": "*a/b", "type": "wildcard"}]
	}`)
	if _, err := Parse(data); err == nil {
		t.Fatal("非法路径模式应被拒绝")
	}
}

func TestToRoutesRejectsUnmigrated(t *testing.T) {
	m := &Manifest{Version: 1}
	if _, err := m.ToRoutes(); err == nil {
		t.Fatal("未迁移到当前版本的清单不得转换")
	}
}

func TestAssetManifestDeterministic(t *testing.T) {
	routes := []*router.Route{
		{ID: "b", Path: "b"},
		{ID: "a", Path: "a", Children: []*router.Route{{ID: "c", Path: "c"}}},
	}
	assets := map[string][]string{
		"a": {"z.js", "a.js"},
		"b": {"b.css"},
	}
	m1, err := BuildAssets(routes, assets)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := BuildAssets(routes, assets)
	if err != nil {
		t.Fatal(err)
	}
	if string(m1.Bytes()) != string(m2.Bytes()) {
		t.Fatal("相同输入的清单必须逐字节一致")
	}
	// 条目按路由 ID 排序，资源按字典序排序。
	want := `{"entries":[{"routeId":"a","assets":["a.js","z.js"]},{"routeId":"b","assets":["b.css"]},{"routeId":"c","assets":[]}]}`
	if string(m1.Bytes()) != want {
		t.Fatalf("清单内容不符合规范:\n%s", m1.Bytes())
	}
}

func TestAssetManifestLocalChangeIsolation(t *testing.T) {
	routes := []*router.Route{
		{ID: "a", Path: "a"},
		{ID: "b", Path: "b"},
		{ID: "c", Path: "c"},
	}
	base, _ := BuildAssets(routes, map[string][]string{"a": {"1.js"}, "b": {"2.js"}, "c": {"3.js"}})
	changed, _ := BuildAssets(routes, map[string][]string{"a": {"1.js"}, "b": {"2.js", "extra.js"}, "c": {"3.js"}})
	if len(base.Entries) != len(changed.Entries) {
		t.Fatal("条目数量不应变化")
	}
	for i := range base.Entries {
		same := string(mustJSON(base.Entries[i])) == string(mustJSON(changed.Entries[i]))
		if changed.Entries[i].RouteID == "b" {
			if same {
				t.Fatal("改动路由的条目应变化")
			}
		} else if !same {
			t.Fatalf("未受影响的路由 %s 条目应保持原样", base.Entries[i].RouteID)
		}
	}
}

func TestAssetManifestRejectsUnknownRoute(t *testing.T) {
	routes := []*router.Route{{ID: "a", Path: "a"}}
	_, err := BuildAssets(routes, map[string][]string{"ghost": {"g.js"}})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("指向不存在路由的资源应被拒绝: %v", err)
	}
}

func mustJSON(v any) []byte {
	b, _ := jsonMarshal(v)
	return b
}
