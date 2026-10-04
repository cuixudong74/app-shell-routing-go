package shell

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ManifestVersion 是当前清单版本。
const ManifestVersion = 2

// 合法的路由类型。
const (
	RouteTypeLayout = "layout"
	RouteTypePage   = "page"
)

// RouteSpec 是清单中的路由声明（当前版本格式）。
type RouteSpec struct {
	ID       string      `json:"id"`
	Path     string      `json:"path"`
	Type     string      `json:"type"` // "layout" | "page"
	Loader   string      `json:"loader,omitempty"`
	Assets   []string    `json:"assets,omitempty"`
	Children []RouteSpec `json:"children,omitempty"`
}

// Manifest 是路由清单。
type Manifest struct {
	Version int         `json:"version"`
	Routes  []RouteSpec `json:"routes"`
}

// ---- v1 旧格式 ----

// routeSpecV1 是 v1 清单的路由格式：
// 类型字段名为 kind，资源为逗号分隔的单个字符串。
type routeSpecV1 struct {
	ID       string        `json:"id"`
	Path     string        `json:"path"`
	Kind     string        `json:"kind"`
	Loader   string        `json:"loader,omitempty"`
	Assets   string        `json:"assets,omitempty"`
	Children []routeSpecV1 `json:"children,omitempty"`
}

type manifestV1 struct {
	Version int           `json:"version"`
	Routes  []routeSpecV1 `json:"routes"`
}

// migrateV1toV2 按声明的迁移规则升级：
//   - kind 原样映射为 type（layout/page 之外一律拒绝）；
//   - 逗号分隔的 assets 字符串拆分为数组，逐项去空白，空项丢弃。
func migrateV1toV2(old manifestV1) (*Manifest, error) {
	var conv func(rs []routeSpecV1) []RouteSpec
	conv = func(rs []routeSpecV1) []RouteSpec {
		out := make([]RouteSpec, 0, len(rs))
		for _, r := range rs {
			spec := RouteSpec{
				ID:     r.ID,
				Path:   r.Path,
				Type:   r.Kind,
				Loader: r.Loader,
			}
			if r.Assets != "" {
				for _, a := range strings.Split(r.Assets, ",") {
					if a = strings.TrimSpace(a); a != "" {
						spec.Assets = append(spec.Assets, a)
					}
				}
			}
			spec.Children = conv(r.Children)
			out = append(out, spec)
		}
		return out
	}
	return &Manifest{Version: ManifestVersion, Routes: conv(old.Routes)}, nil
}

// ParseManifest 解析清单并按声明的迁移规则升级旧版本。
// 无法识别的版本或路由类型明确拒绝并说明原因，不静默跳过、不按默认值处理。
func ParseManifest(data []byte) (*Manifest, error) {
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("invalid manifest json: %w", err)
	}
	var m *Manifest
	switch head.Version {
	case 1:
		var old manifestV1
		if err := json.Unmarshal(data, &old); err != nil {
			return nil, fmt.Errorf("invalid v1 manifest: %w", err)
		}
		up, err := migrateV1toV2(old)
		if err != nil {
			return nil, fmt.Errorf("migrate v1->v2: %w", err)
		}
		m = up
	case ManifestVersion:
		var cur Manifest
		if err := json.Unmarshal(data, &cur); err != nil {
			return nil, fmt.Errorf("invalid v%d manifest: %w", ManifestVersion, err)
		}
		m = &cur
	default:
		return nil, fmt.Errorf("unsupported manifest version %d (supported: 1, %d)", head.Version, ManifestVersion)
	}
	if err := validateManifest(m); err != nil {
		return nil, err
	}
	return m, nil
}

// validateManifest 校验路由类型与必填字段，定位到具体路由。
func validateManifest(m *Manifest) error {
	var walk func(rs []RouteSpec, parent string) error
	walk = func(rs []RouteSpec, parent string) error {
		for _, r := range rs {
			loc := r.ID
			if parent != "" {
				loc = parent + "/" + r.ID
			}
			if r.ID == "" {
				return fmt.Errorf("route under %q: empty id", parent)
			}
			switch r.Type {
			case RouteTypeLayout, RouteTypePage:
			default:
				return fmt.Errorf("route %q: unknown route type %q (want %q or %q)", loc, r.Type, RouteTypeLayout, RouteTypePage)
			}
			if err := walk(r.Children, loc); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(m.Routes, "")
}

// AssetManifest 是一条路由的静态资源清单。
type AssetManifest struct {
	RouteID string   `json:"routeId"`
	Assets  []string `json:"assets"`
}

// BuildAssetManifests 为每条路由产出稳定的资源清单：
//   - 每条路由的清单包含自身及全部祖先声明的资源（渲染该路由所需）；
//   - 路由按 ID 排序，资源排序并去重；
//   - 同样输入下输出逐字节一致；局部改动只影响该路由及其后代的条目。
func BuildAssetManifests(m *Manifest) ([]byte, error) {
	if m == nil {
		return nil, fmt.Errorf("nil manifest")
	}
	var entries []AssetManifest
	var walk func(rs []RouteSpec, inherited []string)
	walk = func(rs []RouteSpec, inherited []string) {
		for _, r := range rs {
			assets := mergeAssets(inherited, r.Assets)
			entries = append(entries, AssetManifest{RouteID: r.ID, Assets: assets})
			walk(r.Children, assets)
		}
	}
	walk(m.Routes, nil)
	sort.Slice(entries, func(i, j int) bool { return entries[i].RouteID < entries[j].RouteID })
	out, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// mergeAssets 合并并规范化：排序、去重，保证确定性。
func mergeAssets(base, extra []string) []string {
	set := map[string]struct{}{}
	for _, a := range base {
		set[a] = struct{}{}
	}
	for _, a := range extra {
		set[a] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
