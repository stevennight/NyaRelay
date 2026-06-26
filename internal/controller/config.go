package controller

import (
	"flag"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	ListenAddr      string
	DataDir         string
	DBPath          string
	LogLevel        string
	SessionLifetime time.Duration
	PublicURL       string
}

func parseConfig(args []string) Config {
	cfg := Config{
		ListenAddr:      env("NYARELAY_LISTEN", ":8080"),
		DataDir:         env("NYARELAY_DATA", "./data"),
		LogLevel:        env("NYARELAY_LOG_LEVEL", "info"),
		SessionLifetime: 24 * time.Hour,
		PublicURL:       env("NYARELAY_PUBLIC_URL", ""),
	}
	fs := flag.NewFlagSet("controller", flag.ExitOnError)
	fs.StringVar(&cfg.ListenAddr, "listen", cfg.ListenAddr, "controller listen address")
	fs.StringVar(&cfg.DataDir, "data", cfg.DataDir, "controller data directory")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "log level")
	fs.StringVar(&cfg.PublicURL, "public-url", cfg.PublicURL, "public URL shown in setup docs")
	_ = fs.Parse(args)
	cfg.DBPath = filepath.Join(cfg.DataDir, "nyarelay.db")
	return cfg
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
