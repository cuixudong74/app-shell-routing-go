package shell

import (
	"context"
	"sync"
)

// LoaderFunc 是路由的数据加载逻辑。
type LoaderFunc func(ctx context.Context, lc *LoaderContext) (any, error)

// LoaderContext 向加载逻辑暴露本段参数与上层已就绪数据。
type LoaderContext struct {
	RouteID    string
	Params     map[string]string
	ParentData any // 上层加载结果；根层为 nil
}

// Deferred 表示一个慢数据承诺：先返回外壳，就绪后再补齐。
type Deferred struct {
	C <-chan DeferredResult
}

// DeferredResult 是慢数据的最终结果。
type DeferredResult struct {
	Data any
	Err  error
}

// Defer 把慢加载包装为 Deferred：立即返回，真正的加载在后台进行，
// 其结果可通过返回值的 Deferred.C 订阅。渲染器据此先输出外壳再补齐。
func Defer(fn LoaderFunc) LoaderFunc {
	return func(ctx context.Context, lc *LoaderContext) (any, error) {
		ch := make(chan DeferredResult, 1)
		go func() {
			data, err := fn(ctx, lc)
			ch <- DeferredResult{Data: data, Err: err}
			close(ch)
		}()
		return &Deferred{C: ch}, nil
	}
}

// LoadResult 是单条路由的加载结果。
type LoadResult struct {
	RouteID string
	Data    any   // 可能是 *Deferred（慢数据）
	Err     error // 非 nil 时 Data 无效，*RouteError 可定位
}

// MatchNode 把匹配链/匹配森林表达为树，以支持同层并发加载。
type MatchNode struct {
	Match    *Match
	Children []*MatchNode
}

// LoadNode 是与 MatchNode 同构的加载结果树。
type LoadNode struct {
	Result   LoadResult
	Children []*LoadNode
}

// ChainToTree 把匹配链转为单支 MatchNode 树。
func ChainToTree(chain []*Match) *MatchNode {
	if len(chain) == 0 {
		return nil
	}
	nodes := make([]*MatchNode, len(chain))
	for i, m := range chain {
		nodes[i] = &MatchNode{Match: m}
		if i > 0 {
			nodes[i-1].Children = []*MatchNode{nodes[i]}
		}
	}
	return nodes[0]
}

// LoadTree 加载整棵匹配树：上层先于下层就绪（下层可读到上层数据），
// 同层兄弟节点并发加载。上层失败时下层不再加载。
func LoadTree(ctx context.Context, node *MatchNode) *LoadNode {
	return loadNode(ctx, node, nil)
}

func loadNode(ctx context.Context, node *MatchNode, parentData any) *LoadNode {
	res := LoadResult{RouteID: node.Match.Route.ID}
	if node.Match.Route.Loader != nil {
		data, err := node.Match.Route.Loader(ctx, &LoaderContext{
			RouteID:    node.Match.Route.ID,
			Params:     node.Match.Params,
			ParentData: parentData,
		})
		if err != nil {
			res.Err = &RouteError{RouteID: node.Match.Route.ID, Phase: "load", Err: err}
		} else {
			res.Data = data
		}
	}
	out := &LoadNode{Result: res}
	if res.Err != nil {
		return out // 上层失败，下层不再加载
	}
	if len(node.Children) > 0 {
		out.Children = make([]*LoadNode, len(node.Children))
		var wg sync.WaitGroup
		for i, child := range node.Children {
			wg.Add(1)
			go func(i int, child *MatchNode) {
				defer wg.Done()
				out.Children[i] = loadNode(ctx, child, res.Data)
			}(i, child)
		}
		wg.Wait()
	}
	return out
}

// LoadChain 沿匹配链加载：上层先于下层就绪，逐层传递数据。
func LoadChain(ctx context.Context, chain []*Match) []LoadResult {
	root := ChainToTree(chain)
	if root == nil {
		return nil
	}
	loaded := LoadTree(ctx, root)
	var out []LoadResult
	for cur := loaded; cur != nil; {
		out = append(out, cur.Result)
		if len(cur.Children) == 0 {
			break
		}
		cur = cur.Children[0]
	}
	return out
}
