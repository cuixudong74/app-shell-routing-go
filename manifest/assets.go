package manifest

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/cuixudong74/app-shell-routing-go/router"
)

// AssetEntry 是单个路由的静态资源条目。
type AssetEntry struct {
	RouteID string   `json:"routeId"`
	Assets  []string `json:"assets"`
}

// AssetManifest 是按路由组织的资源清单，序列化结果逐字节稳定。
type AssetManifest struct {
	Entries []AssetEntry `json:"entries"`
}

// BuildAssets 为路由树与资源输入产出确定性清单：
// 条目按路由 ID 排序、资源按字典序排序，因此相同输入逐字节一致；
// 每个路由独立成条目，局部改动只影响相关路由的条目。
func BuildAssets(routes []*router.Route, assets map[string][]string) (*AssetManifest, error) {
	known := map[string]bool{}
	var entries []AssetEntry
	collect(routes, assets, known, &entries)
	for id := range assets {
		if !known[id] {
			return nil, fmt.Errorf("manifest: 资源指向了不存在的路由 %q", id)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].RouteID < entries[j].RouteID })
	return &AssetManifest{Entries: entries}, nil
}

func collect(routes []*router.Route, assets map[string][]string, known map[string]bool, entries *[]AssetEntry) {
	for _, r := range routes {
		known[r.ID] = true
		list := append([]string(nil), assets[r.ID]...)
		if list == nil {
			list = []string{}
		}
		sort.Strings(list)
		*entries = append(*entries, AssetEntry{RouteID: r.ID, Assets: list})
		collect(r.Children, assets, known, entries)
	}
}

// Bytes 返回清单的规范化字节表示：结构体字段顺序固定、
// 无 map 迭代，保证逐字节稳定。
func (a *AssetManifest) Bytes() []byte {
	buf, _ := json.Marshal(a)
	return buf
}
