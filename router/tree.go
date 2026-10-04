package router

import (
	"fmt"
	"sort"
	"strings"
)

// Route 描述一条路由规则。
type Route struct {
	ID       string
	Path     string // 相对父级的路径模式，如 "users"、":id"、"*rest"
	LoaderID string // 数据加载器标识，可为空
	Children []*Route
}

// ConflictError 描述装配阶段发现的路由冲突。
type ConflictError struct {
	RouteA  string
	RouteB  string
	Pattern string
	Reason  string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("router: 路由冲突 %q 与 %q（模式 %q）：%s",
		e.RouteA, e.RouteB, e.Pattern, e.Reason)
}

// Tree 是装配完成的路由树。
type Tree struct {
	Roots []*node
}

// MatchResult 是一次匹配的结果：从根到叶的匹配链与参数。
type MatchResult struct {
	Routes []*Route
	Params map[string]string
}

// Build 装配路由树，重复或冲突的路由在此阶段被整体拒绝。
func Build(routes []*Route) (*Tree, error) {
	seen := map[string]string{}
	nodes, err := buildLevel(routes, seen)
	if err != nil {
		return nil, err
	}
	return &Tree{Roots: nodes}, nil
}

func buildLevel(routes []*Route, seen map[string]string) ([]*node, error) {
	var nodes []*node
	for _, r := range routes {
		if r.ID == "" {
			return nil, fmt.Errorf("router: 存在缺少 ID 的路由（路径 %q）", r.Path)
		}
		if prev, dup := seen[r.ID]; dup {
			return nil, &ConflictError{RouteA: prev, RouteB: r.ID, Pattern: r.Path,
				Reason: "路由 ID 重复定义"}
		}
		seen[r.ID] = r.ID
		pat, err := ParsePattern(r.Path)
		if err != nil {
			return nil, err
		}
		children, err := buildLevel(r.Children, seen)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, &node{route: r, pattern: pat, children: children})
	}
	// 同层两两检查不可区分模式（互相冲突）。
	for i := 0; i < len(nodes); i++ {
		for j := i + 1; j < len(nodes); j++ {
			if ambiguous(nodes[i].pattern, nodes[j].pattern) {
				return nil, &ConflictError{
					RouteA:  nodes[i].route.ID,
					RouteB:  nodes[j].route.ID,
					Pattern: nodes[i].pattern.Raw + " ~ " + nodes[j].pattern.Raw,
					Reason:  "同层路由模式不可区分，匹配结果将依赖声明顺序",
				}
			}
		}
	}
	// 按确定优先级排序，保证匹配顺序可复现。
	sort.SliceStable(nodes, func(a, b int) bool {
		return nodes[a].pattern.Less(nodes[b].pattern)
	})
	return nodes, nil
}

// ambiguous 判断两个模式是否不可区分：逐段比较，静态段字面量相同、
// 参数段对参数段、通配段对通配段，则任意路径对两者的匹配结果一致。
func ambiguous(a, b Pattern) bool {
	if len(a.Segments) != len(b.Segments) {
		return false
	}
	for i := range a.Segments {
		sa, sb := a.Segments[i], b.Segments[i]
		if sa.Kind != sb.Kind {
			return false
		}
		if sa.Kind == SegmentStatic && sa.Value != sb.Value {
			return false
		}
	}
	return len(a.Segments) > 0 || a.Raw == b.Raw
}

// Match 在树中匹配路径，未命中返回 nil 与 false（由上层走兜底逻辑）。
func (t *Tree) Match(path string) (*MatchResult, bool) {
	segs := splitPath(path)
	params := map[string]string{}
	var chain []*Route
	if matchLevel(t.Roots, segs, params, &chain) {
		return &MatchResult{Routes: chain, Params: params}, true
	}
	return nil, false
}

func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// matchLevel 按优先级顺序尝试每个节点；子树失败时回退到下一个候选，
// 顺序固定因此结果确定。
func matchLevel(nodes []*node, segs []string, params map[string]string, chain *[]*Route) bool {
	for _, n := range nodes {
		p, consumed, ok := n.pattern.Match(segs)
		if !ok {
			continue
		}
		rest := segs[consumed:]
		snapshot := cloneParams(params)
		for k, v := range p {
			params[k] = v
		}
		*chain = append(*chain, n.route)
		if len(rest) == 0 && len(n.children) == 0 {
			return true
		}
		if len(rest) > 0 && matchLevel(n.children, rest, params, chain) {
			return true
		}
		if len(rest) == 0 && len(n.children) > 0 {
			// 路径耗尽但存在子路由：尝试子路由中的空模式（索引路由）。
			if matchLevel(n.children, rest, params, chain) {
				return true
			}
		}
		// 回退：恢复参数与链。
		copyParams(params, snapshot)
		*chain = (*chain)[:len(*chain)-1]
	}
	return false
}

func cloneParams(m map[string]string) map[string]string {
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func copyParams(dst, src map[string]string) {
	for k := range dst {
		delete(dst, k)
	}
	for k, v := range src {
		dst[k] = v
	}
}

type node struct {
	route    *Route
	pattern  Pattern
	children []*node
}
