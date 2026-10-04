package shell

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Server 通过本地回环 HTTP 暴露导航与清单能力。
type Server struct {
	nav      *Navigator
	manifest *Manifest // 可为 nil，此时 /manifest 返回 404
}

// NewServer 创建服务端。manifest 可为 nil。
func NewServer(nav *Navigator, manifest *Manifest) *Server {
	return &Server{nav: nav, manifest: manifest}
}

// eventJSON 是事件的线上格式。
type eventJSON struct {
	Seq     int    `json:"seq"`
	Type    string `json:"type"`
	RouteID string `json:"routeId,omitempty"`
	HTML    string `json:"html,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Handler 返回 HTTP 处理器：
//
//	GET /navigate?path=/...  流式输出导航事件（NDJSON，每行一个事件）
//	GET /manifest            全部路由的静态资源清单（确定性字节）
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/navigate", s.handleNavigate)
	mux.HandleFunc("/manifest", s.handleManifest)
	return mux
}

func (s *Server) handleNavigate(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, `{"error":"missing path parameter"}`, http.StatusBadRequest)
		return
	}
	_, events, err := s.nav.Navigate(path)
	if err != nil {
		var nm *NoMatchError
		if errors.As(err, &nm) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": nm.Error()})
			return
		}
		http.Error(w, `{"error":`+strconvQuote(err.Error())+`}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	flusher, _ := w.(http.Flusher)
	for ev := range events {
		ej := eventJSON{Seq: ev.Seq, Type: string(ev.Type), RouteID: ev.RouteID, HTML: ev.HTML}
		if ev.Err != nil {
			ej.Error = ev.Err.Error()
		}
		if err := enc.Encode(ej); err != nil {
			return // 客户端断开，停止写入
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	if s.manifest == nil {
		http.Error(w, `{"error":"no manifest configured"}`, http.StatusNotFound)
		return
	}
	out, err := BuildAssetManifests(s.manifest)
	if err != nil {
		http.Error(w, `{"error":`+strconvQuote(err.Error())+`}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
