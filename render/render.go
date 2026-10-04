// Package render 提供应用外壳的流式渲染与就近错误边界。
package render

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/cuixudong74/app-shell-routing-go/loader"
	"github.com/cuixudong74/app-shell-routing-go/router"
)

// EventKind 是流式输出事件的类型。
type EventKind int

const (
	EventShell  EventKind = iota // 页面外壳（含占位）
	EventData                    // 某路由数据补齐
	EventError                   // 某错误边界内失败
	EventDone                    // 全部就绪，仅出现一次且在最后
)

// Event 是流式输出的一个事件，同一请求内顺序稳定。
type Event struct {
	Kind    EventKind
	RouteID string
	Payload string
}

// Renderer 把匹配结果渲染为事件流。
type Renderer struct {
	Reg *loader.Registry
}

func NewRenderer(reg *loader.Registry) *Renderer { return &Renderer{Reg: reg} }

// Stream 渲染匹配链：先输出外壳，再按链顺序逐级加载并补齐数据。
// 某级失败时，错误被该级路由的就近边界捕获：输出 EventError 后停止下钻，
// 已就绪的上层结果保持有效；EventDone 恰好输出一次且在最后。
// ctx 取消后立即停止，不再写入任何事件，也不输出 EventDone。
func (r *Renderer) Stream(ctx context.Context, w io.Writer, match *router.MatchResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// 1. 先输出外壳：包含每一级路由的占位结构。
	if err := writeEvent(w, Event{Kind: EventShell, Payload: shellHTML(match)}); err != nil {
		return err
	}
	// 2. 逐级加载并补齐，保证上层数据先于下层可用。
	parents := map[string]any{}
	for _, route := range match.Routes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if route.LoaderID == "" {
			parents[route.ID] = nil
			continue
		}
		l, ok := r.Reg.Lookup(route.LoaderID)
		if !ok {
			err := &loader.LoadError{RouteID: route.ID,
				Err: fmt.Errorf("加载器 %q 未注册", route.LoaderID)}
			return emitBoundaryError(ctx, w, route, err)
		}
		data, err := l(ctx, loader.LoadContext{
			Params:  match.Params,
			Parents: cloneParents(parents),
			RouteID: route.ID,
		})
		if err != nil {
			return emitBoundaryError(ctx, w, route,
				&loader.LoadError{RouteID: route.ID, Err: err})
		}
		if data == nil {
			return emitBoundaryError(ctx, w, route, &loader.LoadError{
				RouteID: route.ID, Err: fmt.Errorf("加载器返回了空数据")})
		}
		parents[route.ID] = data
		payload, _ := json.Marshal(map[string]any{"routeId": route.ID, "data": data})
		if err := writeEvent(w, Event{Kind: EventData, RouteID: route.ID, Payload: string(payload)}); err != nil {
			return err
		}
	}
	// 3. 全部就绪，输出唯一的完成事件。
	return writeEvent(w, Event{Kind: EventDone})
}

// emitBoundaryError 在最近边界输出错误事件并以 EventDone 收尾，
// 保证不输出半截结构：客户端总能看到确定的终止。
func emitBoundaryError(ctx context.Context, w io.Writer, route *router.Route, lerr *loader.LoadError) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"routeId": lerr.RouteID,
		"error":   lerr.Error(),
	})
	if err := writeEvent(w, Event{Kind: EventError, RouteID: route.ID, Payload: string(payload)}); err != nil {
		return err
	}
	return writeEvent(w, Event{Kind: EventDone})
}

// shellHTML 生成应用外壳：每一级路由一个占位槽，结构完整闭合。
func shellHTML(match *router.MatchResult) string {
	var b strings.Builder
	for _, route := range match.Routes {
		fmt.Fprintf(&b, `<section data-route="%s">`, route.ID)
	}
	for range match.Routes {
		b.WriteString(`</section>`)
	}
	return b.String()
}

// writeEvent 以换行分隔的 JSON 写入单个事件，单次写入保证不交错。
func writeEvent(w io.Writer, e Event) error {
	buf, err := json.Marshal(e)
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	_, err = w.Write(buf)
	return err
}



func cloneParents(m map[string]any) map[string]any {
	c := make(map[string]any, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
