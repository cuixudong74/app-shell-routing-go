// Package loader 提供按匹配链分层的数据加载：上层先于下层就绪，同层并发。
package loader

import (
	"context"
	"fmt"
	"sync"

	"github.com/cuixudong74/app-shell-routing-go/router"
)

// LoadContext 是加载器的输入：本段参数与上层已就绪数据。
type LoadContext struct {
	Params  map[string]string
	Parents map[string]any // 按路由 ID 索引的上层数据
	RouteID string
}

// Loader 是单个路由的数据加载逻辑。
type Loader func(ctx context.Context, lc LoadContext) (any, error)

// Registry 按 LoaderID 注册加载器。
type Registry struct {
	mu      sync.RWMutex
	loaders map[string]Loader
}

func NewRegistry() *Registry { return &Registry{loaders: map[string]Loader{}} }

func (r *Registry) Register(id string, l Loader) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loaders[id] = l
}

// Lookup 按 ID 查找已注册的加载器。
func (r *Registry) Lookup(id string) (Loader, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	l, ok := r.loaders[id]
	return l, ok
}

// LoadError 定位到具体路由的加载失败。
type LoadError struct {
	RouteID string
	Err     error
}

func (e *LoadError) Error() string {
	return fmt.Sprintf("loader: 路由 %q 数据加载失败: %v", e.RouteID, e.Err)
}
func (e *LoadError) Unwrap() error { return e.Err }

// Result 是单个路由的加载结果。
type Result struct {
	RouteID string
	Data    any
}

// LoadChain 沿匹配链自顶向下加载：上层完成后下层才开始，同层并发。
// 任一失败返回 *LoadError，缺失数据不会被当成空值继续。
func LoadChain(ctx context.Context, reg *Registry, match *router.MatchResult) ([]Result, error) {
	parents := map[string]any{}
	results := make([]Result, 0, len(match.Routes))
	for _, route := range match.Routes {
		if route.LoaderID == "" {
			parents[route.ID] = nil
			continue
		}
		l, ok := reg.Lookup(route.LoaderID)
		if !ok {
			return nil, &LoadError{RouteID: route.ID,
				Err: fmt.Errorf("加载器 %q 未注册", route.LoaderID)}
		}
		lc := LoadContext{
			Params:  match.Params,
			Parents: cloneMap(parents),
			RouteID: route.ID,
		}
		data, err := l(ctx, lc)
		if err != nil {
			return nil, &LoadError{RouteID: route.ID, Err: err}
		}
		if data == nil {
			return nil, &LoadError{RouteID: route.ID,
				Err: fmt.Errorf("加载器返回了空数据")}
		}
		parents[route.ID] = data
		results = append(results, Result{RouteID: route.ID, Data: data})
	}
	return results, nil
}

// LoadSiblings 并发加载同层的多个路由（各自的上层数据已就绪）。
// 所有结果按输入顺序返回；任一失败返回第一个 *LoadError。
func LoadSiblings(ctx context.Context, reg *Registry, routes []*router.Route, params map[string]string, parents map[string]any) ([]Result, error) {
	results := make([]Result, len(routes))
	errs := make([]error, len(routes))
	var wg sync.WaitGroup
	for i, route := range routes {
		if route.LoaderID == "" {
			continue
		}
		l, ok := reg.Lookup(route.LoaderID)
		if !ok {
			errs[i] = &LoadError{RouteID: route.ID,
				Err: fmt.Errorf("加载器 %q 未注册", route.LoaderID)}
			continue
		}
		wg.Add(1)
		go func(i int, route *router.Route, l Loader) {
			defer wg.Done()
			lc := LoadContext{
				Params:  params,
				Parents: cloneMap(parents),
				RouteID: route.ID,
			}
			data, err := l(ctx, lc)
			if err != nil {
				errs[i] = &LoadError{RouteID: route.ID, Err: err}
				return
			}
			if data == nil {
				errs[i] = &LoadError{RouteID: route.ID,
					Err: fmt.Errorf("加载器返回了空数据")}
				return
			}
			results[i] = Result{RouteID: route.ID, Data: data}
		}(i, route, l)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}

func cloneMap(m map[string]any) map[string]any {
	c := make(map[string]any, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
