package node

import (
	"flag"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	ControllerURL     string
	NodeID            string
	NodeToken         string
	SigningKey        string
	DataDir           string
	CachePath         string
	UpdateRequestPath string
	UpdateStatusPath  string
	LogLevel          string
	PollInterval      time.Duration
	MetricsEvery      time.Duration
}

func parseConfig(args []string) Config {
	cfg := Config{
		ControllerURL: env("NYARELAY_CONTROLLER", "http://127.0.0.1:8080"),
		NodeID:        env("NYARELAY_NODE_ID", ""),
		NodeToken:     env("NYARELAY_NODE_TOKEN", ""),
		SigningKey:    env("NYARELAY_SIGNING_KEY", ""),
		DataDir:       env("NYARELAY_DATA", "./node-data"),
		LogLevel:      env("NYARELAY_LOG_LEVEL", "info"),
		PollInterval:  25 * time.Second,
		MetricsEvery:  15 * time.Second,
	}
	fs := flag.NewFlagSet("node", flag.ExitOnError)
	fs.StringVar(&cfg.ControllerURL, "controller", cfg.ControllerURL, "controller URL")
	fs.StringVar(&cfg.NodeID, "id", cfg.NodeID, "node id")
	fs.StringVar(&cfg.NodeToken, "token", cfg.NodeToken, "node token")
	fs.StringVar(&cfg.SigningKey, "signing-key", cfg.SigningKey, "controller Ed25519 public signing key")
	fs.StringVar(&cfg.DataDir, "data", cfg.DataDir, "node data directory")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "log level")
	_ = fs.Parse(args)
	cfg.DataDir = filepath.Clean(cfg.DataDir)
	cfg.CachePath = filepath.Join(cfg.DataDir, "last-config.json")
	cfg.UpdateRequestPath = filepath.Join(cfg.DataDir, "update", "request.json")
	cfg.UpdateStatusPath = filepath.Join(cfg.DataDir, "update", "status.json")
	return cfg
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
