// Package config 读部署给出的启动参数。
//
// 用户设置不在这里：它们只活在 Postgres 里，且没有第二条覆盖路径（ADR-0002）。
// 这里只有 bootstrap 参数，每一项都带默认值，所以开发机零配置就能起。
package config

import (
	"fmt"
	"net/url"
	"os"
)

const (
	// DefaultDatabaseURL 指向开发机 compose.yaml 里那套 Postgres（make dev 零配置的凭据）。
	DefaultDatabaseURL = "postgres://mdc:mdc@localhost:5432/mdc?sslmode=disable"
	// DefaultListen 是服务默认监听地址；生产由 home-ops 覆盖为容器端口。
	DefaultListen = ":8080"
)

// Config 是进程启动所需的全部外部输入。
type Config struct {
	// DatabaseURL 是 Postgres DSN，已含 MDC_DB_PASSWORD（若给出）。
	DatabaseURL string
	// Listen 是 HTTP 监听地址。
	Listen string
}

// Load 从环境变量读取配置，未设置或为空即取默认值。
//
// 认识的变量只有三个：MDC_DATABASE_URL、MDC_LISTEN、MDC_DB_PASSWORD（进程内拼进 DSN）。
func Load() (Config, error) {
	dsn := getenv("MDC_DATABASE_URL", DefaultDatabaseURL)

	u, err := url.Parse(dsn)
	if err != nil {
		return Config{}, fmt.Errorf("MDC_DATABASE_URL 无法解析: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return Config{}, fmt.Errorf("MDC_DATABASE_URL 不是 postgres:// DSN: %q", dsn)
	}

	if password := os.Getenv("MDC_DB_PASSWORD"); password != "" {
		u.User = url.UserPassword(u.User.Username(), password)
		dsn = u.String()
	}

	return Config{
		DatabaseURL: dsn,
		Listen:      getenv("MDC_LISTEN", DefaultListen),
	}, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
