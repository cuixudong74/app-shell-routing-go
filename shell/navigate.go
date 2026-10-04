package shell

import (
	"context"
	"sync"
	"time"
)

// Navigator 管理并发导航：新导航到来即取消旧导航，
// 旧导航的事件流随之终止，过期结果不会覆盖较新的结果。
// 取消或超时后不会输出 Done，不会留下半渲染状态。
type Navigator struct {
	router *Router

	mu     sync.Mutex
	gen    int
	cancel context.CancelFunc
}

// NewNavigator 创建导航管理器。
func NewNavigator(r *Router) *Navigator {
	return &Navigator{router: r}
}

// Navigate 开始一次导航并取消上一次未完成的导航。
// 返回单调递增的导航代次与该次导航的事件流。
// 路径不匹配时本次导航立即失败并返回错误（上一次导航仍已被取消）。
func (n *Navigator) Navigate(path string) (int, <-chan Event, error) {
	return n.navigate(context.Background(), path, 0)
}

// NavigateTimeout 同 Navigate，但为本次导航设置超时；
// 超时等价于取消：事件流终止且不输出 Done。
func (n *Navigator) NavigateTimeout(path string, timeout time.Duration) (int, <-chan Event, error) {
	return n.navigate(context.Background(), path, timeout)
}

func (n *Navigator) navigate(parent context.Context, path string, timeout time.Duration) (int, <-chan Event, error) {
	n.mu.Lock()
	if n.cancel != nil {
		n.cancel() // 取消旧导航，其事件流立即终止
	}
	ctx := parent
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, timeout)
	} else {
		ctx, cancel = context.WithCancel(parent)
	}
	n.cancel = cancel
	n.gen++
	gen := n.gen
	n.mu.Unlock()

	ch, err := Stream(ctx, n.router, path)
	if err != nil {
		cancel()
		return gen, nil, err
	}
	return gen, ch, nil
}

// Current 返回最新导航代次。
func (n *Navigator) Current() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.gen
}

// Cancel 取消当前导航（若有）。
func (n *Navigator) Cancel() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.cancel != nil {
		n.cancel()
		n.cancel = nil
	}
}
