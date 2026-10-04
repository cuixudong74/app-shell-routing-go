// Package manifest 提供路由清单的版本演进与静态资源清单产出。
package manifest

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cuixudong74/app-shell-routing-go/router"
)

// CurrentVersion 是当前支持的清单版本。
const CurrentVersion = 2

// Manifest 是版本化的路由清单。
type Manifest struct {
	Version int         `json:"version"`
	Routes  []RouteSpec `json:"routes"`
}

// RouteSpec 是清单中的一条路由声明。
type RouteSpec struct {
	ID       string      `json:"id"`
	Path     string      `json:"path"`
	LoaderID string      `json:"loaderId,omitempty"`
	Type     string      `json:"type"` // "static" | "param" | "wildcard"
	Children []RouteSpec `json:"children,omitempty"`
}

// 合法的路由类型集合。
var knownTypes = map[string]bool{"static": true, "param": true, "wildcard": true}

// specV1 是版本 1 的路由声明：没有 type 字段，加载器字段名为 loader。
type specV1 struct {
	ID       string   `json:"id"`
	Path     string   `json:"path"`
	Loader   string   `json:"loader,omitempty"`
	Children []specV1 `json:"children,omitempty"`
}

type manifestV1 struct {
	Version int      `json:"version"`
	Routes  []specV1 `json:"routes"`
}

// Parse 解析清单并按迁移规则升级到当前版本；
// 无法识别的版本或路由类型明确拒绝并说明原因。
func Parse(data []byte) (*Manifest, error) {
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("manifest: 清单不是合法的 JSON: %w", err)
	}
	switch {
	case head.Version <= 0:
		return nil, fmt.Errorf("manifest: 清单缺少有效的 version 字段（得到 %d）", head.Version)
	case head.Version > CurrentVersion:
		return nil, fmt.Errorf("manifest: 无法识别的清单版本 %d（当前支持到 %d）",
			head.Version, CurrentVersion)
	}

	var m *Manifest
	var err error
	switch head.Version {
	case 1:
		m, err = parseV1(data)
	case 2:
		m, err = parseV2(data)
	}
	if err != nil {
		return nil, err
	}
	// 逐级迁移到当前版本（当前仅 1→2 一步，后续版本在此追加）。
	for m.Version < CurrentVersion {
		m, err = migrate(m)
		if err != nil {
			return nil, err
		}
	}
	if err := validate(m.Routes, ""); err != nil {
		return nil, err
	}
	return m, nil
}

func parseV1(data []byte) (*Manifest, error) {
	var v1 manifestV1
	if err := json.Unmarshal(data, &v1); err != nil {
		return nil, fmt.Errorf("manifest: 版本 1 清单解析失败: %w", err)
	}
	return &Manifest{Version: 1, Routes: convertV1(v1.Routes)}, nil
}

func parseV2(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest: 版本 2 清单解析失败: %w", err)
	}
	return &m, nil
}

// migrate 把清单从版本 n 升级到 n+1。
func migrate(m *Manifest) (*Manifest, error) {
	switch m.Version {
	case 1:
		// 1→2：字段已在解析期映射，这里只提升版本号。
		return &Manifest{Version: 2, Routes: m.Routes}, nil
	default:
		return nil, fmt.Errorf("manifest: 没有从版本 %d 的迁移规则", m.Version)
	}
}

// convertV1 把 v1 声明映射为 v2：loader→loaderId，并按路径推断路由类型。
func convertV1(specs []specV1) []RouteSpec {
	out := make([]RouteSpec, 0, len(specs))
	for _, s := range specs {
		out = append(out, RouteSpec{
			ID:       s.ID,
			Path:     s.Path,
			LoaderID: s.Loader,
			Type:     inferType(s.Path),
			Children: convertV1(s.Children),
		})
	}
	return out
}

// inferType 按路径模式推断类型：含 "*" 为通配，含 ":" 为参数，否则静态。
func inferType(path string) string {
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		if strings.HasPrefix(seg, "*") {
			return "wildcard"
		}
	}
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		if strings.HasPrefix(seg, ":") {
			return "param"
		}
	}
	return "static"
}

// validate 校验每条声明：类型必须已知且与路径模式一致，路径必须可解析。
func validate(specs []RouteSpec, parentPath string) error {
	for _, s := range specs {
		loc := parentPath + "/" + s.Path
		if s.ID == "" {
			return fmt.Errorf("manifest: 路由（路径 %q）缺少 id", loc)
		}
		if !knownTypes[s.Type] {
			return fmt.Errorf("manifest: 路由 %q 的类型 %q 无法识别（支持 static|param|wildcard）",
				s.ID, s.Type)
		}
		if got := inferType(s.Path); got != s.Type {
			return fmt.Errorf("manifest: 路由 %q 声明类型 %q 与路径 %q 推断类型 %q 不一致",
				s.ID, s.Type, s.Path, got)
		}
		if _, err := router.ParsePattern(s.Path); err != nil {
			return fmt.Errorf("manifest: 路由 %q 路径非法: %w", s.ID, err)
		}
		if err := validate(s.Children, loc); err != nil {
			return err
		}
	}
	return nil
}

// ToRoutes 把清单转换为路由树输入。
func (m *Manifest) ToRoutes() ([]*router.Route, error) {
	if m.Version != CurrentVersion {
		return nil, fmt.Errorf("manifest: 只能转换版本 %d 的清单（当前为 %d）",
			CurrentVersion, m.Version)
	}
	return toRoutes(m.Routes), nil
}

func toRoutes(specs []RouteSpec) []*router.Route {
	out := make([]*router.Route, 0, len(specs))
	for _, s := range specs {
		out = append(out, &router.Route{
			ID:       s.ID,
			Path:     s.Path,
			LoaderID: s.LoaderID,
			Children: toRoutes(s.Children),
		})
	}
	return out
}
