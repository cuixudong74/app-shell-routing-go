package shell

import (
	"context"
	"fmt"
)

// EventType 是流式输出事件的类型。
type EventType string

const (
	EventShell EventType = "shell" // 页面外壳（慢数据位置为占位，Ready=false）
	EventPatch EventType = "patch" // 慢数据补齐，每路由至多一次
	EventError EventType = "error" // 未被边界捕获的错误，终止事件流
	EventDone  EventType = "done"  // 全部就绪，之后不再有事件
)

// Event 是一次流式输出，Seq 在同一请求内从 0 严格递增。
type Event struct {
	Seq     int
	Type    EventType
	RouteID string
	HTML    string
	Err     *RouteError
}

// Stream 执行一次导航的完整流水线：匹配、加载、渲染外壳、按链路顺序补齐慢数据。
// 保证：
//   - 事件 Seq 严格递增，输出顺序稳定（按路由链自顶向下）；
//   - 未就绪数据在外壳中以 Ready=false 渲染，不会被标记为完成；
//   - 每条慢数据至多补齐一次，Done 之后不再有事件；
//   - ctx 取消时立即停止，不再输出任何事件。
func Stream(ctx context.Context, r *Router, path string) (<-chan Event, error) {
	chain, err := r.Match(path)
	if err != nil {
		return nil, err
	}
	out := make(chan Event)
	go func() {
		defer close(out)
		seq := 0
		send := func(ev Event) bool {
			ev.Seq = seq
			seq++
			select {
			case out <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		}

		results := LoadChain(ctx, chain)
		if ctx.Err() != nil {
			return
		}

		// 标记慢数据为未就绪，渲染外壳。
		pending := map[string]bool{}
		deferredIdx := []int{} // 按链路顺序记录 Deferred 所在层
		for i, res := range results {
			if _, ok := res.Data.(*Deferred); ok {
				pending[res.RouteID] = true
				deferredIdx = append(deferredIdx, i)
			}
		}
		shell, rerr := renderLevel(chain, results, 0, pending)
		if rerr != nil {
			send(Event{Type: EventError, RouteID: rerr.RouteID, Err: rerr})
			return
		}
		if !send(Event{Type: EventShell, HTML: shell}) {
			return
		}

		// 按链路顺序逐个补齐，保证同一请求内输出顺序稳定。
		for _, i := range deferredIdx {
			d := results[i].Data.(*Deferred)
			var dr DeferredResult
			select {
			case dr = <-d.C:
			case <-ctx.Done():
				return
			}
			routeID := results[i].RouteID
			if dr.Err != nil {
				// 慢数据失败：就近边界兜底为一次 Patch；无边界则 Error 终止。
				re := &RouteError{RouteID: routeID, Phase: "load", Err: dr.Err}
				fallback, berr := renderBoundaryFor(chain, results, i, re)
				if berr != nil {
					send(Event{Type: EventError, RouteID: berr.RouteID, Err: berr})
					return
				}
				if !send(Event{Type: EventPatch, RouteID: routeID, HTML: fallback, Err: re}) {
					return
				}
				continue
			}
			// 用就绪数据重渲染该层片段作为补齐内容。
			results[i].Data = dr.Data
			frag, ferr := renderFragment(chain, results, i)
			if ferr != nil {
				send(Event{Type: EventError, RouteID: ferr.RouteID, Err: ferr})
				return
			}
			if !send(Event{Type: EventPatch, RouteID: routeID, HTML: frag}) {
				return
			}
		}
		send(Event{Type: EventDone})
	}()
	return out, nil
}

// renderFragment 渲染第 i 层片段：本层数据已就绪，子层沿用当前结果渲染。
func renderFragment(chain []*Match, results []LoadResult, i int) (string, *RouteError) {
	child := ""
	if i+1 < len(chain) {
		c, rerr := renderLevel(chain, results, i+1, nil)
		if rerr != nil {
			return "", rerr
		}
		child = c
	}
	rt := chain[i].Route
	if rt.Render == nil {
		return child, nil
	}
	html, err := rt.Render(&RenderContext{RouteID: rt.ID, Data: dataAt(results, i), Child: child, Ready: true})
	if err != nil {
		return "", &RouteError{RouteID: rt.ID, Phase: "render", Err: err}
	}
	return html, nil
}

// renderBoundaryFor 为第 i 层的加载错误寻找最近边界（含本层）并渲染兜底。
func renderBoundaryFor(chain []*Match, results []LoadResult, i int, re *RouteError) (string, *RouteError) {
	for j := i; j >= 0; j-- {
		rt := chain[j].Route
		if rt.Boundary == nil {
			continue
		}
		html, err := rt.Boundary(&RenderContext{RouteID: rt.ID, Data: dataAt(results, j), Ready: true}, re)
		if err != nil {
			return "", &RouteError{RouteID: rt.ID, Phase: "render", Err: err}
		}
		return html, nil
	}
	return "", &RouteError{RouteID: re.RouteID, Phase: re.Phase, Err: fmt.Errorf("uncaught: %w", re.Err)}
}
