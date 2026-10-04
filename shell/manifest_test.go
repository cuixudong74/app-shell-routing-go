package shell

import (
	"strings"
	"testing"
)

func TestParseManifestV2(t *testing.T) {
	data := []byte(`{"version":2,"routes":[{"id":"root","path":"","type":"layout","assets":["app.css"],"children":[{"id":"home","path":"","type":"page","loader":"home","assets":["home.js"]}]}]}`)
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Version != 2 || len(m.Routes) != 1 || m.Routes[0].Children[0].ID != "home" {
		t.Errorf("manifest = %+v", m)
	}
}

func TestParseManifestV1Migrates(t *testing.T) {
	data := []byte(`{"version":1,"routes":[{"id":"root","path":"","kind":"layout","assets":"app.css, reset.css ,,","children":[{"id":"home","path":"","kind":"page","loader":"home","assets":"home.js"}]}]}`)
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Version != ManifestVersion {
		t.Errorf("version = %d, want %d", m.Version, ManifestVersion)
	}
	root := m.Routes[0]
	if root.Type != "layout" {
		t.Errorf("type = %q", root.Type)
	}
	if got := strings.Join(root.Assets, ","); got != "app.css,reset.css" {
		t.Errorf("assets = %q", got)
	}
	if root.Children[0].Assets[0] != "home.js" {
		t.Errorf("child assets = %v", root.Children[0].Assets)
	}
}

func TestParseManifestRejectsUnknownVersion(t *testing.T) {
	for _, v := range []string{`{"version":0,"routes":[]}`, `{"version":3,"routes":[]}`, `{"version":-1,"routes":[]}`} {
		_, err := ParseManifest([]byte(v))
		if err == nil {
			t.Errorf("%s: expected rejection", v)
		} else if !strings.Contains(err.Error(), "unsupported manifest version") {
			t.Errorf("%s: err = %v", v, err)
		}
	}
}

func TestParseManifestRejectsUnknownRouteType(t *testing.T) {
	data := []byte(`{"version":2,"routes":[{"id":"root","path":"","type":"layout","children":[{"id":"x","path":"x","type":"widget"}]}]}`)
	_, err := ParseManifest(data)
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !strings.Contains(err.Error(), `unknown route type "widget"`) || !strings.Contains(err.Error(), "root/x") {
		t.Errorf("err = %v", err)
	}
}

func TestAssetManifestDeterministic(t *testing.T) {
	data := []byte(`{"version":2,"routes":[
		{"id":"b","path":"b","type":"page","assets":["b.js","a.css"]},
		{"id":"a","path":"","type":"layout","assets":["app.css"],"children":[
			{"id":"a-child","path":"c","type":"page","assets":["c.js","app.css"]}
		]}
	]}`)
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	first, err := BuildAssetManifests(m)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for i := 0; i < 10; i++ {
		again, _ := BuildAssetManifests(m)
		if string(first) != string(again) {
			t.Fatalf("iter %d: not byte-identical:\n%s\n%s", i, first, again)
		}
	}
	// 排序与继承
	want := `[{"routeId":"a","assets":["app.css"]},{"routeId":"a-child","assets":["app.css","c.js"]},{"routeId":"b","assets":["a.css","b.js"]}]`
	if string(first) != want {
		t.Errorf("got  %s\nwant %s", first, want)
	}
}

func TestAssetManifestLocalChangeIsolates(t *testing.T) {
	base := `{"version":2,"routes":[
		{"id":"a","path":"","type":"layout","assets":["app.css"],"children":[
			{"id":"a-child","path":"c","type":"page","assets":["c.js"]}
		]},
		{"id":"b","path":"b","type":"page","assets":["b.js"]}
	]}`
	m1, _ := ParseManifest([]byte(base))
	out1, _ := BuildAssetManifests(m1)

	changed := strings.Replace(base, `"assets":["b.js"]`, `"assets":["b2.js"]`, 1)
	m2, _ := ParseManifest([]byte(changed))
	out2, _ := BuildAssetManifests(m2)

	if string(out1) == string(out2) {
		t.Fatal("expected output to change")
	}
	// 未受影响条目保持原样
	if !strings.Contains(string(out2), `{"routeId":"a","assets":["app.css"]}`) ||
		!strings.Contains(string(out2), `{"routeId":"a-child","assets":["app.css","c.js"]}`) {
		t.Errorf("unaffected entries changed: %s", out2)
	}
}
