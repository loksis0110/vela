package main

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/velaapp/vela/core"
)

//go:embed ui
var uiFiles embed.FS

const uiPort = 47390

type Server struct {
	app   *App
	token string
	addr  string
}

func NewServer(app *App) *Server {
	return &Server{app: app, token: randomID() + randomID(), addr: fmt.Sprintf("127.0.0.1:%d", uiPort)}
}

func (s *Server) URL() string { return fmt.Sprintf("http://%s/#t=%s", s.addr, s.token) }

func (s *Server) Listen() (net.Listener, error) { return net.Listen("tcp", s.addr) }

func (s *Server) Serve(l net.Listener) error {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(uiFiles, "ui")
	static := http.FileServer(http.FS(sub))
	mux.Handle("/", noCache(static))
	mux.HandleFunc("/show", func(w http.ResponseWriter, r *http.Request) {
		// another launch of Vela.exe asks us to bring the window back
		openWindow(s.URL(), s.app.dataDir)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/events", s.auth(s.events))
	mux.HandleFunc("/api/state", s.auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.app.Snapshot())
	}))
	mux.HandleFunc("/api/import", s.auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Text string `json:"text"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		msg, err := s.app.Import(r.Context(), req.Text)
		reply(w, msg, err)
	}))
	mux.HandleFunc("/api/profile/update", s.auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID string `json:"id"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		reply(w, "Подписка обновлена", s.app.UpdateProfile(r.Context(), req.ID))
	}))
	mux.HandleFunc("/api/profile/update-all", s.auth(func(w http.ResponseWriter, r *http.Request) {
		msg, err := s.app.UpdateAll(r.Context())
		reply(w, msg, err)
	}))
	mux.HandleFunc("/api/profile/rename", s.auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		reply(w, "Название сохранено", s.app.RenameProfile(req.ID, req.Name))
	}))
	mux.HandleFunc("/api/profile/delete", s.auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID string `json:"id"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		reply(w, "Удалено", s.app.DeleteProfile(req.ID))
	}))
	mux.HandleFunc("/api/node/delete", s.auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Profile string `json:"profile"`
			Node    string `json:"node"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		reply(w, "Сервер удалён", s.app.DeleteNode(req.Profile, req.Node))
	}))
	mux.HandleFunc("/api/select", s.auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Profile string `json:"profile"`
			Node    string `json:"node"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		reply(w, "", s.app.Select(req.Profile, req.Node))
	}))
	mux.HandleFunc("/api/connect", s.auth(func(w http.ResponseWriter, r *http.Request) {
		go s.app.Connect()
		reply(w, "", nil)
	}))
	mux.HandleFunc("/api/disconnect", s.auth(func(w http.ResponseWriter, r *http.Request) {
		go s.app.Disconnect()
		reply(w, "", nil)
	}))
	mux.HandleFunc("/api/ping", s.auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Profile string `json:"profile"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		go s.app.Ping(req.Profile)
		reply(w, "", nil)
	}))
	mux.HandleFunc("/api/settings", s.auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Settings        core.Settings `json:"settings"`
			ConnectOnLaunch bool          `json:"connectOnLaunch"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		s.app.SetSettings(req.Settings, req.ConnectOnLaunch)
		reply(w, "", nil)
	}))
	mux.HandleFunc("/api/onboarding/done", s.auth(func(w http.ResponseWriter, r *http.Request) {
		s.app.FinishOnboarding()
		reply(w, "", nil)
	}))
	mux.HandleFunc("/api/elevate", s.auth(func(w http.ResponseWriter, r *http.Request) {
		if err := relaunchElevated(); err != nil {
			reply(w, "", err)
			return
		}
		reply(w, "", nil)
		go func() {
			time.Sleep(300 * time.Millisecond)
			quit(s.app)
		}()
	}))
	mux.HandleFunc("/api/open", s.auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Target string `json:"target"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		openExternal(req.Target, s.app.dataDir)
		reply(w, "", nil)
	}))
	mux.HandleFunc("/api/favorite", s.auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Node string `json:"node"`
			On   bool   `json:"on"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		s.app.SetFavorite(req.Node, req.On)
		reply(w, "", nil)
	}))
	mux.HandleFunc("/api/theme", s.auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Theme string `json:"theme"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		s.app.SetTheme(req.Theme)
		reply(w, "", nil)
	}))
	mux.HandleFunc("/api/myip", s.auth(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		info, err := s.app.MyIP(ctx)
		if err != nil {
			reply(w, "", err)
			return
		}
		writeJSON(w, info)
	}))
	mux.HandleFunc("/api/speedtest", s.auth(func(w http.ResponseWriter, r *http.Request) {
		reply(w, "", s.app.SpeedTest())
	}))
	mux.HandleFunc("/api/qr", s.auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Text string `json:"text"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		url, err := s.app.QRDataURL(req.Text)
		if err != nil {
			reply(w, "", err)
			return
		}
		writeJSON(w, map[string]string{"image": url})
	}))
	mux.HandleFunc("/api/qr/decode", s.auth(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Image string `json:"image"` // base64 or data URL
		}
		if !readJSON(w, r, &req) {
			return
		}
		raw := req.Image
		if i := strings.Index(raw, ","); strings.HasPrefix(raw, "data:") && i > 0 {
			raw = raw[i+1:]
		}
		data, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			reply(w, "", errors.New("не удалось прочитать картинку"))
			return
		}
		text, err := core.DecodeQR(data)
		if err != nil {
			reply(w, "", err)
			return
		}
		writeJSON(w, map[string]string{"text": text})
	}))
	mux.HandleFunc("/api/export", s.auth(func(w http.ResponseWriter, r *http.Request) {
		text, err := s.app.Export()
		if err != nil {
			reply(w, "", err)
			return
		}
		out := map[string]string{"text": text}
		if img, err := s.app.QRDataURL(text); err == nil && len(text) < 2500 {
			out["image"] = img
		}
		writeJSON(w, out)
	}))
	mux.HandleFunc("/api/logs", s.auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"lines": s.app.Logs(400)})
	}))
	mux.HandleFunc("/api/processes", s.auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"processes": listProcesses()})
	}))
	mux.HandleFunc("/api/quit", s.auth(func(w http.ResponseWriter, r *http.Request) {
		reply(w, "", nil)
		go quit(s.app)
	}))
	srv := &http.Server{Handler: s.guard(mux), ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(l)
}

// guard blocks DNS-rebinding: only requests addressed to our loopback host
// are accepted.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != s.addr && r.Host != fmt.Sprintf("localhost:%d", uiPort) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// auth requires the per-launch token, so other local programs and web
// pages cannot control the VPN.
func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-Vela-Token")
		if tok == "" {
			tok = r.URL.Query().Get("t")
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch := s.app.subscribe()
	defer s.app.unsubscribe(ch)
	first, _ := json.Marshal(map[string]any{"type": "state", "data": s.app.Snapshot()})
	fmt.Fprintf(w, "data: %s\n\n", first)
	flusher.Flush()
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func reply(w http.ResponseWriter, msg string, err error) {
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"message": msg})
}
