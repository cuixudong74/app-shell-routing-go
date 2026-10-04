package shell

import (
	"fmt"
	"sort"
	"strings"
)

// SegmentKind 描述路径段的类型。
type SegmentKind int

const (
	SegStatic   SegmentKind = iota // 静态段，逐字节匹配
	SegParam                       // 参数段，形如 :id
	SegWildcard                    // 通配段，形如 *rest，只能出现在末尾
)

// Segment 是一个已解析的路径段。
type Segment struct {
	Kind SegmentKind
	Name string // 静态段为字面量；参数/通配段为参数名
}

// Route 是一条路由规则，可嵌套。
type Route struct {
	ID       string
	Path     string // 本段路径模式，如 "users" / ":id" / "*rest"
	Segments []Segment
	Loader   LoaderFunc
	Render   RenderFunc
	Boundary BoundaryFunc // 就近错误边界，nil 表示不捕获
	Children []*Route
}

// Match 表示链路上一层路由的匹配结果。
type Match struct {
	Route  *Route
	Params map[string]string
}

// Router 是装配完成、可执行匹配的路由表。
type Router struct {
	roots []*Route
}

// NoMatchError 表示缺失匹配，进入明确兜底。
type NoMatchError struct {
	Path string
}

func (e *NoMatchError) Error() string {
	return fmt.Sprintf("no route matches path %q", e.Path)
}

// parsePattern 把路径模式解析为段序列。
func parsePattern(pattern string) ([]Segment, error) {
	pattern = strings.Trim(pattern, "/")
	if pattern == "" {
		return nil, nil
	}
	parts := strings.Split(pattern, "/")
	segs := make([]Segment, 0, len(parts))
	for i, p := range parts {
		switch {
		case strings.HasPrefix(p, ":"):
			name := p[1:]
			if name == "" {
				return nil, fmt.Errorf("empty param name in pattern %q", pattern)
			}
			segs = append(segs, Segment{Kind: SegParam, Name: name})
		case strings.HasPrefix(p, "*"):
			name := p[1:]
			if name == "" {
				return nil, fmt.Errorf("empty wildcard name in pattern %q", pattern)
			}
			if i != len(parts)-1 {
				return nil, fmt.Errorf("wildcard %q must be the last segment in pattern %q", p, pattern)
			}
			segs = append(segs, Segment{Kind: SegWildcard, Name: name})
		case p == "":
			return nil, fmt.Errorf("empty segment in pattern %q", pattern)
		default:
			segs = append(segs, Segment{Kind: SegStatic, Name: p})
		}
	}
	return segs, nil
}

// Compile 校验并装配路由表；存在重复或冲突时整体拒绝。
func Compile(routes []*Route) (*Router, error) {
	ids := map[string]string{}
	if err := compileLevel(routes, ids, ""); err != nil {
		return nil, err
	}
	return &Router{roots: routes}, nil
}

// compileLevel 校验同一层级的兄弟路由，并递归校验子级。
// 冲突规则（同层内）：
//   - 路由 ID 全局唯一；
//   - 静态段字面量不得重复；
//   - 参数段至多一个（不同名的两个参数段互相冲突）；
//   - 通配段至多一个；
//   - 空模式（无路径布局）至多一个，且优先级最低；
//   - 首段相同（静态同字面量 / 同为参数 / 同为通配）视为冲突。
func compileLevel(routes []*Route, ids map[string]string, parentID string) error {
	statics := map[string]string{} // 字面量 -> 路由 ID
	var paramID, wildcardID, emptyID string
	for _, r := range routes {
		loc := r.ID
		if parentID != "" {
			loc = parentID + "/" + r.ID
		}
		if r.ID == "" {
			return fmt.Errorf("route under %q: empty route id", parentID)
		}
		if prev, dup := ids[r.ID]; dup {
			return fmt.Errorf("duplicate route id %q (at %q and %q)", r.ID, prev, loc)
		}
		ids[r.ID] = loc

		segs, err := parsePattern(r.Path)
		if err != nil {
			return fmt.Errorf("route %q: %w", loc, err)
		}
		r.Segments = segs

		if len(segs) == 0 {
			if emptyID != "" {
				return fmt.Errorf("conflicting routes %q and %q: multiple pathless routes at same level", emptyID, loc)
			}
			emptyID = loc
		} else {
			switch first := segs[0]; first.Kind {
			case SegStatic:
				if prev, dup := statics[first.Name]; dup {
					return fmt.Errorf("conflicting routes %q and %q: duplicate static segment %q", prev, loc, first.Name)
				}
				statics[first.Name] = loc
			case SegParam:
				if paramID != "" {
					return fmt.Errorf("conflicting routes %q and %q: ambiguous param segments %q and %q at same level", paramID, loc, paramID, r.ID)
				}
				paramID = loc
			case SegWildcard:
				if wildcardID != "" {
					return fmt.Errorf("conflicting routes %q and %q: multiple wildcards at same level", wildcardID, loc)
				}
				wildcardID = loc
			}
		}
		if err := compileLevel(r.Children, ids, loc); err != nil {
			return err
		}
	}
	return nil
}

// Match 按确定性优先级匹配 path，返回从根到叶的匹配链。
// 每层优先级：静态 > 参数 > 通配 > 空模式；下层失败时按同一次序回溯，
// 因此结果确定且可复现。全部不匹配返回 *NoMatchError。
func (r *Router) Match(path string) ([]*Match, error) {
	clean := strings.Trim(path, "/")
	var parts []string
	if clean != "" {
		parts = strings.Split(clean, "/")
	}
	chain, ok := matchChain(r.roots, parts)
	if !ok {
		return nil, &NoMatchError{Path: path}
	}
	return chain, nil
}

// matchChain 在一层兄弟路由中按优先级逐个尝试候选，
// 候选的下层匹配失败时回溯到下一个候选，保证确定性。
func matchChain(routes []*Route, parts []string) ([]*Match, bool) {
	try := func(rt *Route, params map[string]string, consumed int) ([]*Match, bool) {
		m := &Match{Route: rt, Params: params}
		rest := parts[consumed:]
		if len(rest) == 0 {
			// 路径已耗尽：若有子路由则尝试匹配空路径（索引/无路径子路由），
			// 否则链路允许在本层结束。
			if len(rt.Children) > 0 {
				if sub, ok := matchChain(rt.Children, rest); ok {
					return append([]*Match{m}, sub...), true
				}
			}
			return []*Match{m}, true
		}
		if len(rt.Children) == 0 {
			return nil, false // 还有剩余段但无子路由，候选失败
		}
		if sub, ok := matchChain(rt.Children, rest); ok {
			return append([]*Match{m}, sub...), true
		}
		return nil, false
	}

	for _, kind := range []SegmentKind{SegStatic, SegParam, SegWildcard} {
		for _, rt := range routes {
			if len(rt.Segments) == 0 || rt.Segments[0].Kind != kind {
				continue
			}
			if params, consumed, ok := matchSegments(rt.Segments, parts); ok {
				if chain, ok := try(rt, params, consumed); ok {
					return chain, true
				}
			}
		}
	}
	// 空模式（无路径布局）最后尝试，不消耗任何段。
	for _, rt := range routes {
		if len(rt.Segments) == 0 {
			if chain, ok := try(rt, map[string]string{}, 0); ok {
				return chain, true
			}
		}
	}
	return nil, false
}

// matchSegments 用一条路由的全部段匹配路径头部。
func matchSegments(segs []Segment, parts []string) (map[string]string, int, bool) {
	params := map[string]string{}
	i := 0
	for _, s := range segs {
		switch s.Kind {
		case SegStatic:
			if i >= len(parts) || parts[i] != s.Name {
				return nil, 0, false
			}
			i++
		case SegParam:
			if i >= len(parts) {
				return nil, 0, false
			}
			params[s.Name] = parts[i]
			i++
		case SegWildcard:
			params[s.Name] = strings.Join(parts[i:], "/")
			return params, len(parts), true
		}
	}
	return params, i, true
}

// sortRoutes 返回按 ID 排序的路由副本（供清单等确定性输出使用）。
func sortRoutes(routes []*Route) []*Route {
	out := make([]*Route, len(routes))
	copy(out, routes)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
