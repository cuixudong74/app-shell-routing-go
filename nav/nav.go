// Package nav 管理并发导航：新导航取消旧导航，过期结果不得覆盖新结果。
package nav

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/cuixudong74/app-shell-routing-go/render"
	"github.com/cuixudong74/app-shell-routing-go/router"
)

// Navigator 串行化导航代际，保证只有最新一代的结果生效。
type Navigator struct {
	tree *router.Tree
	rend *render.Renderer

	mu     sync.Mutex
	gen    int64
	cancel context.CancelFunc
}

func NewNavigator(tree *router.Tree, rend *render.Renderer) *Navigator {
	return &Navigator{tree: tree, rend: rend}
}

// Navigate 发起一次导航，返回该代际的事件通道；到来更新的导航时，
// 本代际被取消，通道关闭且不再产出事件。每次导航独占一个通道，
// 因此过期代际的结果永远不会混入较新的代际。
func (n *Navigator) Navigate(path string) (<-chan render.Event, context.CancelFunc) {
	n.mu.Lock()
	if n.cancel != nil {
		n.cancel() // 取消上一代导航
	}
	n.gen++
	ctx, cancel := context.WithCancel(context.Background())
	n.cancel = cancel
	n.mu.Unlock()

	ch := make(chan render.Event, 64)
	go n.run(ctx, ch, path)
	return ch, cancel
}

func (n *Navigator) run(ctx context.Context, ch chan<- render.Event, path string) {
	defer close(ch)
	match, ok := n.tree.Match(path)
	if !ok {
		// 明确的兜底：未命中路由输出可定位的错误事件而非静默失败。
		payload, _ := json.Marshal(map[string]any{
			"error": fmt.Sprintf("nav: 没有路由匹配路径 %q", path),
		})
		emit(ctx, ch, render.Event{Kind: render.EventError, Payload: string(payload)})
		emit(ctx, ch, render.Event{Kind: render.EventDone})
		return
	}
	w := &eventWriter{ctx: ctx, ch: ch}
	_ = n.rend.Stream(ctx, w, match) // 取消时 Stream 返回错误，直接收尾关闭通道
}

// eventWriter 把渲染器的每次写入转换为一个事件投递到通道；
// 取消后拒绝写入，保证不会留下半写入的状态。
type eventWriter struct {
	ctx context.Context
	ch  chan<- render.Event
}

func (w *eventWriter) Write(p []byte) (int, error) {
	var e render.Event
	if err := json.Unmarshal(p, &e); err != nil {
		return 0, fmt.Errorf("nav: 无法解码渲染事件: %w", err)
	}
	select {
	case w.ch <- e:
		return len(p), nil
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	}
}

func emit(ctx context.Context, ch chan<- render.Event, e render.Event) {
	select {
	case ch <- e:
	case <-ctx.Done():
	}
}

// Current 返回当前生效的导航代际号，单调递增。
func (n *Navigator) Current() int64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.gen
}
