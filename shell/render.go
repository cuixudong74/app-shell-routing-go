package shell

import "fmt"

// RenderFunc 渲染一层布局，Child 为下层已渲染内容。
type RenderFunc func(rc *RenderContext) (string, error)

// BoundaryFunc 是错误边界：捕获下层加载或渲染错误并渲染兜底。
type BoundaryFunc func(rc *RenderContext, err *RouteError) (string, error)

// RenderContext 向渲染逻辑暴露本层数据与子层输出。
type RenderContext struct {
	RouteID string
	Data    any
	Child   string
	// Ready 为 false 表示本层数据尚未就绪（流式外壳阶段），
	// 渲染逻辑必须输出占位而非把缺失数据当作空值完成渲染。
	Ready bool
}

// RouteError 携带出错路由与阶段信息，可定位。
type RouteError struct {
	RouteID string
	Phase   string // "load" 或 "render"
	Err     error
}

func (e *RouteError) Error() string {
	return fmt.Sprintf("route %q %s error: %v", e.RouteID, e.Phase, e.Err)
}

func (e *RouteError) Unwrap() error { return e.Err }

// 错误冒泡规则（确定）：
//   - 某层加载失败：由该层自身的 Boundary 兜底；没有则向上冒泡到最近的有 Boundary 的祖先；
//   - 某层渲染失败：Boundary 不捕获自身渲染错误，向上冒泡到最近的祖先 Boundary；
//   - 边界渲染自身出错：继续向上冒泡；
//   - 一路冒泡到根仍无人捕获：RenderChain 返回该 *RouteError，由调用方给出明确状态。
func RenderChain(chain []*Match, results []LoadResult) (string, *RouteError) {
	if len(chain) == 0 {
		return "", &RouteError{RouteID: "", Phase: "render", Err: fmt.Errorf("empty route chain")}
	}
	return renderLevel(chain, results, 0, nil)
}

func dataAt(results []LoadResult, i int) any {
	if i < len(results) {
		return results[i].Data
	}
	return nil
}

func loadErrAt(results []LoadResult, i int) *RouteError {
	if i < len(results) && results[i].Err != nil {
		if re, ok := results[i].Err.(*RouteError); ok {
			return re
		}
		routeID := ""
		if i < len(results) {
			routeID = results[i].RouteID
		}
		return &RouteError{RouteID: routeID, Phase: "load", Err: results[i].Err}
	}
	return nil
}

// renderLevel 渲染 chain[i:] 子树。pending 非空时，其中的路由视为数据未就绪：
// Data 置为 nil 且 Ready=false，用于流式外壳阶段。
func renderLevel(chain []*Match, results []LoadResult, i int, pending map[string]bool) (string, *RouteError) {
	rt := chain[i].Route
	rc := &RenderContext{RouteID: rt.ID, Data: dataAt(results, i), Ready: true}
	if pending != nil && pending[rt.ID] {
		rc.Data = nil
		rc.Ready = false
	}

	// 本层加载失败：优先用本层 Boundary 兜底，否则向上冒泡。
	if lerr := loadErrAt(results, i); lerr != nil {
		if rt.Boundary != nil {
			html, err := rt.Boundary(rc, lerr)
			if err != nil {
				return "", &RouteError{RouteID: rt.ID, Phase: "render", Err: err}
			}
			return html, nil
		}
		return "", lerr
	}

	// 渲染下层。
	child := ""
	if i+1 < len(chain) {
		c, rerr := renderLevel(chain, results, i+1, pending)
		if rerr != nil {
			// 下层错误冒泡到本层：本层 Boundary 兜底，否则继续向上。
			if rt.Boundary != nil {
				html, err := rt.Boundary(rc, rerr)
				if err != nil {
					return "", &RouteError{RouteID: rt.ID, Phase: "render", Err: err}
				}
				return html, nil
			}
			return "", rerr
		}
		child = c
	}
	rc.Child = child

	if rt.Render == nil {
		return child, nil
	}
	html, err := rt.Render(rc)
	if err != nil {
		// 自身渲染错误不被自身 Boundary 捕获，直接向上冒泡。
		return "", &RouteError{RouteID: rt.ID, Phase: "render", Err: err}
	}
	return html, nil
}
