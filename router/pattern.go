// Package router 提供路由模式解析、路由树装配与匹配能力。
package router

import (
	"fmt"
	"strings"
)

// SegmentKind 表示路径段的类型。
type SegmentKind int

const (
	// SegmentStatic 静态段，逐字节相等匹配。
	SegmentStatic SegmentKind = iota
	// SegmentParam 参数段，形如 ":id"，匹配任意单段。
	SegmentParam
	// SegmentWildcard 通配段，形如 "*rest"，匹配剩余全部段，只能出现在末尾。
	SegmentWildcard
)

// Segment 是路径模式中的一个段。
type Segment struct {
	Kind  SegmentKind
	Value string // 静态段的字面量，或参数/通配段的名字
}

// Pattern 是一个解析后的路径模式。
type Pattern struct {
	Raw      string
	Segments []Segment
}

// ParsePattern 解析路径模式，非法模式返回错误。
// 规则：以 "/" 开头与结尾的斜杠被忽略；":name" 为参数段；"*name" 为通配段，
// 通配段只能是最后一段；参数与通配段的名字不能为空。
func ParsePattern(raw string) (Pattern, error) {
	p := Pattern{Raw: raw}
	trimmed := strings.Trim(raw, "/")
	if trimmed == "" {
		return p, nil
	}
	parts := strings.Split(trimmed, "/")
	for i, part := range parts {
		switch {
		case strings.HasPrefix(part, ":"):
			name := part[1:]
			if name == "" {
				return p, fmt.Errorf("router: 模式 %q 第 %d 段参数名为空", raw, i)
			}
			if strings.ContainsAny(name, ":*") {
				return p, fmt.Errorf("router: 模式 %q 第 %d 段参数名 %q 含非法字符", raw, i, name)
			}
			p.Segments = append(p.Segments, Segment{Kind: SegmentParam, Value: name})
		case strings.HasPrefix(part, "*"):
			name := part[1:]
			if name == "" {
				return p, fmt.Errorf("router: 模式 %q 第 %d 段通配名为空", raw, i)
			}
			if i != len(parts)-1 {
				return p, fmt.Errorf("router: 模式 %q 的通配段只能出现在末尾", raw)
			}
			p.Segments = append(p.Segments, Segment{Kind: SegmentWildcard, Value: name})
		case strings.ContainsAny(part, ":*"):
			return p, fmt.Errorf("router: 模式 %q 第 %d 段 %q 非法", raw, i, part)
		default:
			p.Segments = append(p.Segments, Segment{Kind: SegmentStatic, Value: part})
		}
	}
	return p, nil
}

// Match 尝试用该模式匹配给定路径段，成功时返回提取的参数与消耗的段数。
func (p Pattern) Match(segs []string) (params map[string]string, consumed int, ok bool) {
	params = map[string]string{}
	for i, seg := range p.Segments {
		if seg.Kind == SegmentWildcard {
			params[seg.Value] = strings.Join(segs[i:], "/")
			return params, len(segs), true
		}
		if i >= len(segs) {
			return nil, 0, false
		}
		switch seg.Kind {
		case SegmentStatic:
			if segs[i] != seg.Value {
				return nil, 0, false
			}
		case SegmentParam:
			if segs[i] == "" {
				return nil, 0, false
			}
			params[seg.Value] = segs[i]
		}
	}
	if len(p.Segments) > 0 && p.Segments[len(p.Segments)-1].Kind == SegmentWildcard {
		return params, len(segs), true
	}
	return params, len(p.Segments), true
}

// rank 返回段的优先级，数值越小优先级越高。
func (k SegmentKind) rank() int {
	switch k {
	case SegmentStatic:
		return 0
	case SegmentParam:
		return 1
	default:
		return 2
	}
}

// Less 定义模式间的确定优先级：逐段比较，静态段优先于参数段，
// 参数段优先于通配段；前缀相同则段数少者优先；再相同按字面量字典序，
// 保证比较结果确定且可复现。
func (p Pattern) Less(other Pattern) bool {
	n := len(p.Segments)
	if len(other.Segments) < n {
		n = len(other.Segments)
	}
	for i := 0; i < n; i++ {
		a, b := p.Segments[i], other.Segments[i]
		if a.Kind != b.Kind {
			return a.Kind.rank() < b.Kind.rank()
		}
		if a.Value != b.Value {
			return a.Value < b.Value
		}
	}
	if len(p.Segments) != len(other.Segments) {
		return len(p.Segments) < len(other.Segments)
	}
	return p.Raw < other.Raw
}
