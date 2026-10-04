# app-shell-routing-go

服务端渲染的应用外壳（App Shell）与嵌套路由库。页面由多层路由与布局组合而成，
每个路由可声明数据加载逻辑，慢数据通过流式补齐，导航支持并发与取消。
整条链路只在本进程内完成，通过本地回环 HTTP 即可复现全部行为。

## 快速开始

```go
router, err := shell.Compile([]*shell.Route{ ... })   // 装配期校验冲突
nav := shell.NewNavigator(router)
srv := shell.NewServer(nav, manifest)
http.ListenAndServe("127.0.0.1:8080", srv.Handler())
```

- `GET /navigate?path=/user/42` —— 流式输出导航事件（NDJSON，每行一个事件）
- `GET /manifest` —— 全部路由的静态资源清单（确定性字节）

## 路由语义

### 路径段

| 形式   | 类型   | 说明                                   |
|--------|--------|----------------------------------------|
| `user` | 静态段 | 逐字节匹配                             |
| `:id`  | 参数段 | 匹配任意单段，值写入 `Match.Params`    |
| `*rest`| 通配段 | 匹配剩余全部段，**只能出现在末尾**     |
| `""`   | 空模式 | 无路径布局，不消耗段，每层至多一条     |

### 匹配优先级

每一层的尝试次序固定为 **静态 > 参数 > 通配 > 空模式**。
候选的下层匹配失败时按同一次序回溯到下一个候选，因此结果确定且可复现，
与路由声明顺序无关。

### 装配期校验（`Compile`）

出现以下情况时**整体拒绝**，错误信息定位到具体路由规则：

- 路由 ID 重复（全局唯一）；
- 同层静态段字面量重复；
- 同层出现两个参数段（如 `:id` 与 `:name`，互相冲突）；
- 同层出现两个通配段，或两条空模式路由；
- 通配段不在末尾、参数/通配名为空、段为空。

### 缺失匹配

匹配失败返回 `*NoMatchError`（HTTP 层映射为 404），不会静默失败或渲染空页面。

## 数据加载契约

每个路由可声明 `Loader LoaderFunc`：

```go
type LoaderFunc func(ctx context.Context, lc *LoaderContext) (any, error)

type LoaderContext struct {
    RouteID    string            // 当前路由 ID
    Params     map[string]string // 本段路径参数
    ParentData any               // 上层已就绪数据（根层为 nil）
}
```

- **上层先于下层**：下层的 `ParentData` 保证是上层 Loader 的返回值；
- **同层并发**：`LoadTree` 下同一层的兄弟节点并发加载；
- **失败可定位**：加载错误包装为 `*RouteError{RouteID, Phase: "load"}`，
  缺失数据不会被当作空值继续渲染，下层加载直接跳过；
- **慢数据**：用 `shell.Defer(fn)` 包装慢加载，Loader 立即返回 `*Deferred`，
  页面先输出外壳，数据就绪后再补齐。

## 流式输出

`shell.Stream(ctx, router, path)` 返回事件流，同一请求内保证：

1. 事件 `Seq` 从 0 严格递增，顺序稳定（按路由链自顶向下）；
2. 外壳（`shell`）中未就绪数据以 `RenderContext.Ready=false` 渲染，
   渲染逻辑必须输出占位，不得把缺失数据当作空值标记完成；
3. 每条慢数据至多补齐（`patch`）一次，不会重复输出；
4. `done` 是最后一个事件，之后不会再有任何输出；
5. `ctx` 取消或超时后事件流立即终止，不输出 `done`，不留半渲染状态。

事件类型：`shell` → `patch`* → `done`；未被边界捕获的错误以 `error` 事件终止。

## 错误边界

- 路由可声明 `Boundary BoundaryFunc` 作为就近错误边界；
- 某层**加载失败**：优先由该层自身 Boundary 兜底，否则向上冒泡到最近的祖先 Boundary；
- 某层**渲染失败**：Boundary 不捕获自身渲染错误，直接向上冒泡；
- 子树出错不影响树上其余部分（兄弟分支照常渲染）；
- 一路冒泡到根仍无人捕获：`RenderChain` 返回 `*RouteError`（含路由 ID 与阶段），
  流式输出以 `error` 事件终止，HTTP 层返回明确错误状态，绝不输出空白页或半截结构。

## 并发导航与取消

`Navigator` 保证：

- 新导航到来即取消上一次未完成的导航，旧事件流立即终止；
- 导航代次单调递增（`Navigator.Current()`），过期结果不会覆盖较新结果；
- `NavigateTimeout` 支持单次导航超时，超时等价于取消；
- 路径不匹配的导航立即返回 `*NoMatchError`（上一次导航仍已被取消）。

## 路由清单与版本演进

清单为 JSON，当前版本 `2`：

```json
{
  "version": 2,
  "routes": [
    {"id": "root", "path": "", "type": "layout", "assets": ["app.css"],
     "children": [
       {"id": "home", "path": "", "type": "page", "loader": "home", "assets": ["home.js"]}
     ]}
  ]
}
```

- `type` 仅接受 `layout` / `page`，其他值明确拒绝并定位到路由；
- **v1 → v2 迁移规则**：`kind` 字段映射为 `type`；逗号分隔的 `assets`
  字符串拆分为数组（逐项去空白、丢弃空项）；
- 无法识别的版本（非 1/2）明确拒绝并说明支持的版本，不静默跳过、不按默认值处理。

## 静态资源清单

`BuildAssetManifests(manifest)` 为每条路由产出资源清单：

- 每条路由的条目包含自身及全部祖先声明的资源（渲染该路由所需的完整集合）；
- 路由按 ID 排序，资源排序并去重，**同样输入下输出逐字节一致**；
- 局部改动只影响该路由及其后代的条目，其余条目逐字节保持原样。

格式示例：

```json
[{"routeId":"root","assets":["app.css"]},{"routeId":"home","assets":["app.css","home.js"]}]
```

## 测试

```sh
go test ./... -race
```

覆盖：路由冲突与匹配优先级、嵌套加载顺序（上层先于下层、同层并发）、
流式补齐（顺序、去重、取消）、错误边界（就近捕获、冒泡、未捕获定位）、
导航取消与超时、清单版本迁移与确定性输出、本地回环端到端。
