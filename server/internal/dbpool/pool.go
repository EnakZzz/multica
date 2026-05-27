package dbpool

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	DefaultMaxConns int32 = 25
	DefaultMinConns int32 = 5
)

func New(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}

	urlParams := PoolParamsFromURL(dbURL)

	maxFallback := DefaultMaxConns
	if urlParams["pool_max_conns"] {
		maxFallback = cfg.MaxConns
	}
	cfg.MaxConns = EnvInt32("DATABASE_MAX_CONNS", maxFallback)

	minFallback := DefaultMinConns
	if urlParams["pool_min_conns"] {
		minFallback = cfg.MinConns
	}
	cfg.MinConns = EnvInt32("DATABASE_MIN_CONNS", minFallback)

	if cfg.MinConns > cfg.MaxConns {
		cfg.MinConns = cfg.MaxConns
	}

	return pgxpool.NewWithConfig(ctx, cfg)
}

func PoolParamsFromURL(dbURL string) map[string]bool {
	out := map[string]bool{}
	u, err := url.Parse(dbURL)
	if err != nil {
		return out
	}
	for k := range u.Query() {
		out[k] = true
	}
	return out
}

func EnvInt32(name string, def int32) int32 {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || v <= 0 {
		slog.Warn("invalid env var, using default",
			"name", name, "value", raw, "default", def, "error", err)
		return def
	}
	return int32(v)
}

func LogConfig(pool *pgxpool.Pool) {
	cfg := pool.Config()
	slog.Info("db pool config",
		"max_conns", cfg.MaxConns,
		"min_conns", cfg.MinConns,
		"max_conn_lifetime", cfg.MaxConnLifetime.String(),
		"max_conn_idle_time", cfg.MaxConnIdleTime.String(),
		"health_check_period", cfg.HealthCheckPeriod.String(),
	)
}
