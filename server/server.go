// Package server 在本地回环上暴露应用外壳渲染与导航能力。
package server

import (
	"encoding/json"
	"net/http"

	"github.com/cuixudong74/app-shell-routing-go/nav"
	"github.com/cuixudong74/app-shell-routing-go/render"
)

// Server 包装一个 Navigator，通过 HTTP 暴露流式渲染端点。
// 所有端点只监听本地回环即可复现，不依赖任何外部服务。
type Server struct {
	nav *nav.Navigator
	mux *http.ServeMux
}

func New(n *nav.Navigator) *Server {
	s := &Server{nav: n, mux: http.NewServeMux()}
	s.mux.HandleFunc("/render", s.handleRender)
	s.mux.HandleFunc("/generation", s.handleGeneration)
	return s
}

// Handler 返回根处理器。
func (s *Server) Handler() http.Handler { return s.mux }

// handleRender 以 NDJSON 流式输出一次导航的事件序列：
// 新的导航会取消上一次未完成的导航（单客户端应用外壳语义）。
func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, `{"error":"缺少 path 参数"}`, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	flusher, canFlush := w.(http.Flusher)

	events, cancel := s.nav.Navigate(path)
	defer cancel()
	for e := range events {
		select {
		case <-r.Context().Done():
			return // 客户端断开：取消并停止，不留半写入状态
		default:
		}
		buf, err := json.Marshal(e)
		if err != nil {
			continue
		}
		if _, err := w.Write(append(buf, '\n')); err != nil {
			return
		}
		if canFlush {
			flusher.Flush()
		}
	}
}

// handleGeneration 返回当前导航代际号，便于观察取消行为。
func (s *Server) handleGeneration(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]int64{"generation": s.nav.Current()})
}

// KindName 给事件类型一个稳定的名字，便于客户端展示。
func KindName(k render.EventKind) string {
	switch k {
	case render.EventShell:
		return "shell"
	case render.EventData:
		return "data"
	case render.EventError:
		return "error"
	default:
		return "done"
	}
}
