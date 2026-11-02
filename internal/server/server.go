// Package server 是 HTTP 面：探针、版本、内嵌 SPA。
//
// 它只依赖一个 Ping 一次数据库的能力和一份前端资源——真实实现分别在 internal/db 与 internal/web。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/2017fighting/oh-my-mdc/internal/javdb"
	"github.com/2017fighting/oh-my-mdc/internal/loops"
	"github.com/2017fighting/oh-my-mdc/internal/qbittorrent"
	"github.com/2017fighting/oh-my-mdc/internal/settings"
	"github.com/2017fighting/oh-my-mdc/internal/sidefx"
	"github.com/2017fighting/oh-my-mdc/internal/task"
)

// defaultVersion 是没被 -ldflags -X 注入时的版本，与镜像构建前的本地运行一致。
const defaultVersion = "dev"

// pingTimeout 是一次就绪检查最多等多久；探针本身不该被慢数据库拖住。
const pingTimeout = 3 * time.Second

// Pinger 是就绪检查对数据库的唯一要求。
type Pinger interface {
	Ping(ctx context.Context) error
}

// Options 是构造 HTTP 面所需的全部输入。
type Options struct {
	// Version 由 -ldflags -X 注入，空则记作 dev。
	Version string
	// DB 供 /readyz 探测；nil 视为不可用。
	DB Pinger
	// Assets 是前端构建产物，根目录需含 index.html。
	Assets fs.FS
	// Settings 是设置域（/api/config、/api/overview）；nil 时这些端点回 503 db_unavailable。
	Settings *settings.Store
	// JavDB 是 JavDB 集成门面（向导探测/代建清单）；nil 时 /api/javdb/* 回 503 db_unavailable。
	JavDB *javdb.Integration
	// Qbt 是 qBittorrent 集成门面（测试连接、autorun 改写）；nil 时 /api/qbittorrent/* 回 503。
	Qbt *qbittorrent.Integration
	// Tasks 是任务域读模型（/api/tasks、/api/overview counts）；nil 时这些端点回 503。
	Tasks *task.Store
	// Sidefx 是副作用出箱（overview 的「需人工」要把驻留待办算进去）；nil 时按 0 计。
	Sidefx *sidefx.Outbox
	// Loops 是三条循环的进程内健康位；nil 时 overview 的 loops 全空。
	Loops *loops.Registry
	// Unready 是启动期留下的致命依赖问题（迁移失败、连接池建不起来）。
	// 非 nil 时 /readyz 恒 503 db_unavailable 并原样回出这句原因——启动不 abort。
	Unready error
}

// New 组装路由并返回可挂到 http.Server 的 handler。
func New(opts Options) (http.Handler, error) {
	if opts.Assets == nil {
		return nil, errors.New("server: 缺少前端资源")
	}

	version := opts.Version
	if version == "" {
		version = defaultVersion
	}

	s := &server{
		version:  version,
		db:       opts.DB,
		assets:   opts.Assets,
		unready:  opts.Unready,
		settings: opts.Settings,
		javdb:    opts.JavDB,
		qbt:      opts.Qbt,
		tasks:    opts.Tasks,
		sidefx:   opts.Sidefx,
		loops:    opts.Loops,
	}

	shell, err := fs.ReadFile(opts.Assets, "index.html")
	if err != nil {
		return nil, errors.New("server: 前端资源里没有 index.html")
	}
	s.shell = shell

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /version", s.versionHandler)
	mux.HandleFunc("GET /api/overview", s.overview)
	mux.HandleFunc("GET /api/config", s.configAll)
	mux.HandleFunc("GET /api/tasks", s.tasksList)
	mux.HandleFunc("GET /api/tasks/{movie_id}", s.taskDetail)
	mux.HandleFunc("GET /api/config/{section}", s.configSection)
	mux.HandleFunc("PATCH /api/config/{section}", s.patchSection)
	mux.HandleFunc("DELETE /api/config/{section}", s.deleteSection)
	mux.HandleFunc("POST /api/config/{section}/test", s.testSection)
	mux.HandleFunc("GET /api/javdb/lists", s.javdbLists)
	mux.HandleFunc("POST /api/javdb/lists", s.javdbCreateList)
	mux.HandleFunc("GET /api/qbittorrent/autorun", s.qbtAutorunPlan)
	mux.HandleFunc("POST /api/qbittorrent/autorun", s.qbtAutorunApply)
	mux.Handle("/", s.spa())
	return mux, nil
}

type server struct {
	version  string
	db       Pinger
	assets   fs.FS
	shell    []byte
	unready  error
	settings *settings.Store
	javdb    *javdb.Integration
	qbt      *qbittorrent.Integration
	tasks    *task.Store
	sidefx   *sidefx.Outbox
	loops    *loops.Registry
}

// healthz 只看进程活不活：数据库或上游挂了也不该让编排器重启容器。
func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// readyz 才看数据库：迁移失败或 ping 不通都回 503 db_unavailable，并带上原因。
func (s *server) readyz(w http.ResponseWriter, r *http.Request) {
	if s.unready != nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", s.unready.Error())
		return
	}
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "数据库未配置")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), pingTimeout)
	defer cancel()

	if err := s.db.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "db_unavailable", "Postgres 不可达: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) versionHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"version": s.version})
}

// spa 提供内嵌的 SPA：有对应文件就给文件；/assets/ 下缺的文件 404（指纹资源不许被壳页顶替）；
// 其余 URL 一律导向 index.html，前端路由自己认路。
func (s *server) spa() http.Handler {
	files := http.FileServer(http.FS(s.assets))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." {
			name = "index.html"
		}

		if f, err := s.assets.Open(name); err == nil {
			f.Close()
			w.Header().Set("Cache-Control", cacheControlFor(name))
			files.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(name, assetsPrefix) {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Cache-Control", noCache)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(s.shell)
	})
}

// assetsPrefix 是 Vite 的指纹资源目录：里面的文件要么命中，要么真的是缺了。
const assetsPrefix = "assets/"

// 壳页每次回源（它引用的指纹资源会变），指纹资源可长缓存。
const (
	noCache        = "no-cache"
	immutableCache = "public, max-age=31536000, immutable"
)

func cacheControlFor(name string) string {
	if strings.HasPrefix(name, assetsPrefix) {
		return immutableCache
	}
	return noCache
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError 输出规格 §8.4 的统一错误信封：{ok:false, error:{code, message}}。
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"ok": false,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}
