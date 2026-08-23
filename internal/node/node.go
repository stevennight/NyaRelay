package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"nyarelay/internal/node/relay"
	sharedcrypto "nyarelay/internal/shared/crypto"
	"nyarelay/internal/shared/logging"
	"nyarelay/internal/shared/model"
	sharedprotocol "nyarelay/internal/shared/protocol"
	sharedversion "nyarelay/internal/shared/version"
)

var Version = sharedversion.Version

// Keep control messages bounded while allowing node-scoped configs with many relays.
const (
	controlWebSocketReadLimit int64 = 256 * 1024
	maxSignedConfigLifetime         = 30 * time.Minute
	maxSignedConfigClockSkew        = 5 * time.Minute
)

type controlLoopOptions struct {
	heartbeatInterval     time.Duration
	configRefreshInterval time.Duration
	writeTimeout          time.Duration
	pingTimeout           time.Duration
}

var defaultControlLoopOptions = controlLoopOptions{
	heartbeatInterval:     20 * time.Second,
	configRefreshInterval: 5 * time.Minute,
	writeTimeout:          10 * time.Second,
	pingTimeout:           10 * time.Second,
}

type configProvider interface {
	config(context.Context) (model.SignedConfig, error)
}

func Run(ctx context.Context, args []string) error {
	if sharedversion.IsVersionCommand(args) {
		fmt.Println(sharedversion.Print("nyarelay-node"))
		return nil
	}
	if len(args) > 0 && args[0] == "update" {
		cfg := parseConfig(nil)
		opts := parseUpdateOptions(args[1:], cfg)
		if err := runUpdate(ctx, cfg, opts.requestPath, opts.statusPath, opts.binaryPath); err != nil {
			return err
		}
		if opts.skipRestart || os.Getenv("NYARELAY_SKIP_RESTART") == "1" {
			return nil
		}
		return restartNodeService()
	}
	cfg := parseConfig(args)
	if cfg.NodeID == "" || cfg.NodeToken == "" || strings.TrimSpace(cfg.SigningKey) == "" {
		return errors.New("node id, token, and signing key are required")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(cfg.DataDir, 0o700); err != nil {
		return err
	}

	log := logging.New(cfg.LogLevel)
	client := newClient(cfg)
	relayService := relay.New(log, cfg.NodeID)

	var currentRevision atomic.Int64
	log.Info("config signature verification enabled")

	applySignedConfig := func(ctx context.Context, signed model.SignedConfig, source string) error {
		if err := verifySignedConfig(cfg.NodeID, cfg.SigningKey, signed); err != nil {
			return err
		}
		if signed.Config.Revision < currentRevision.Load() {
			return nil
		}
		if err := relayService.Apply(ctx, signed.Config); err != nil {
			return err
		}
		if err := saveConfig(cfg.CachePath, signed); err != nil {
			log.Warn("cache config failed", "error", err)
		}
		currentRevision.Store(signed.Config.Revision)
		log.Info("config applied", "source", source, "revision", signed.Config.Revision, "forwards", len(signed.Config.Forwards), "tunnels", len(signed.Config.Tunnels))
		return nil
	}

	bootstrapConfig(ctx, client, cfg.CachePath, &currentRevision, applySignedConfig, log)

	go func() {
		ticker := time.NewTicker(cfg.MetricsEvery)
		defer ticker.Stop()
		start := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				report := model.MetricsReport{
					NodeID:       cfg.NodeID,
					ObservedAt:   time.Now().UTC(),
					ForwardStats: relayService.ForwardStats(),
					TunnelStats:  relayService.TunnelStats(),
					Runtime: model.RuntimeStat{
						UptimeSeconds: int64(time.Since(start).Seconds()),
						Goroutines:    runtime.NumGoroutine(),
					},
				}
				if err := client.metrics(ctx, report); err != nil {
					log.Debug("metrics report failed", "error", err)
				}
			}
		}
	}()

	go controlLoop(ctx, client, cfg, log, applySignedConfig)

	<-ctx.Done()
	return nil
}

func bootstrapConfig(
	ctx context.Context,
	client configProvider,
	cachePath string,
	currentRevision *atomic.Int64,
	apply func(context.Context, model.SignedConfig, string) error,
	log *slog.Logger,
) {
	if signed, err := client.config(ctx); err == nil {
		if err := apply(ctx, signed, "http"); err != nil {
			log.Warn("initial config apply failed", "error", err)
		}
	} else {
		log.Warn("initial config pull failed", "error", err)
	}

	if signed, err := loadConfig(cachePath); err == nil {
		if signed.Config.Revision > currentRevision.Load() {
			if err := apply(ctx, signed, "cache"); err != nil {
				log.Warn("cached config apply failed", "error", err)
			}
		}
	}
}

func controlLoop(ctx context.Context, client *client, cfg Config, log *slog.Logger, apply func(context.Context, model.SignedConfig, string) error) {
	controlLoopWithOptions(ctx, client, cfg, log, apply, defaultControlLoopOptions)
}

func controlLoopWithOptions(ctx context.Context, client *client, cfg Config, log *slog.Logger, apply func(context.Context, model.SignedConfig, string) error, options controlLoopOptions) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}

		conn, err := client.connectWS(ctx)
		if err != nil {
			logControlFailure(log, ctx, "control websocket connect failed", "error", err)
			if !sleep(ctx, backoff) {
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		log.Info("control websocket connected", "controller", controllerLogTarget(cfg.ControllerURL))
		conn.SetReadLimit(controlWebSocketReadLimit)
		connCtx, cancelConn := context.WithCancel(ctx)

		var writeMu sync.Mutex
		write := func(msg sharedprotocol.ControlMessage) error {
			writeMu.Lock()
			defer writeMu.Unlock()
			writeCtx, cancelWrite := context.WithTimeout(connCtx, options.writeTimeout)
			defer cancelWrite()
			return wsjson.Write(writeCtx, conn, msg)
		}

		hello := sharedprotocol.ControlMessage{
			Type:    "hello",
			NodeID:  cfg.NodeID,
			Version: Version,
			System:  nodeSystem(),
		}
		if err := write(hello); err != nil {
			logControlFailure(log, connCtx, "control websocket hello failed", "error", err)
			cancelConn()
			_ = conn.CloseNow()
			continue
		}
		reportUpdateStatus(ctx, cfg, write, log)

		hbDone := make(chan struct{})
		go func() {
			ticker := time.NewTicker(options.heartbeatInterval)
			refreshInterval := options.configRefreshInterval
			if refreshInterval <= 0 {
				refreshInterval = defaultControlLoopOptions.configRefreshInterval
			}
			refreshTicker := time.NewTicker(refreshInterval)
			defer ticker.Stop()
			defer refreshTicker.Stop()
			defer close(hbDone)
			for {
				select {
				case <-connCtx.Done():
					return
				case <-ticker.C:
					msg := sharedprotocol.ControlMessage{
						Type:    "heartbeat",
						NodeID:  cfg.NodeID,
						Version: Version,
						System:  nodeSystem(),
					}
					if report, err := loadUpdateStatus(cfg.UpdateStatusPath); err == nil {
						msg.UpdateReport = &report
					}
					if err := write(msg); err != nil {
						logControlFailure(log, connCtx, "control websocket heartbeat write failed", "error", err)
						cancelConn()
						_ = conn.CloseNow()
						return
					}
					pingCtx, cancelPing := context.WithTimeout(connCtx, options.pingTimeout)
					if err := conn.Ping(pingCtx); err != nil {
						cancelPing()
						logControlFailure(log, connCtx, "control websocket heartbeat ping failed", "error", err)
						cancelConn()
						_ = conn.CloseNow()
						return
					}
					cancelPing()
				case <-refreshTicker.C:
					if err := write(sharedprotocol.ControlMessage{
						Type:   "pull_config",
						NodeID: cfg.NodeID,
					}); err != nil {
						logControlFailure(log, connCtx, "control websocket config refresh failed", "error", err)
						cancelConn()
						_ = conn.CloseNow()
						return
					}
				}
			}
		}()

		for {
			var msg sharedprotocol.ControlMessage
			if err := wsjson.Read(connCtx, conn, &msg); err != nil {
				logControlReadFailure(log, connCtx, err)
				cancelConn()
				_ = conn.CloseNow()
				<-hbDone
				break
			}
			switch msg.Type {
			case "config":
				if msg.Config != nil {
					if err := apply(ctx, *msg.Config, "ws"); err != nil {
						logControlFailure(log, ctx, "config apply failed", "error", err)
					}
				}
			case "update":
				if msg.Update != nil {
					if err := handleUpdateCommand(cfg, *msg.Update); err != nil {
						log.Warn("update request failed", "error", err)
						if writeErr := write(sharedprotocol.ControlMessage{
							Type: "update_status",
							UpdateReport: &model.NodeUpdateReport{
								Status:  model.NodeUpdateFailed,
								Version: msg.Update.Version,
								Error:   err.Error(),
							},
						}); writeErr != nil {
							logControlFailure(log, connCtx, "update status write failed", "error", writeErr)
						}
						continue
					}
					if writeErr := write(sharedprotocol.ControlMessage{
						Type: "update_status",
						UpdateReport: &model.NodeUpdateReport{
							Status:  model.NodeUpdateRequested,
							Version: msg.Update.Version,
						},
					}); writeErr != nil {
						logControlFailure(log, connCtx, "update status write failed", "error", writeErr)
					}
				}
			case "error":
				if msg.Error != "" {
					log.Warn("controller error", "error", msg.Error)
				}
			}
		}
		cancelConn()

		if !sleep(ctx, backoff) {
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func reportUpdateStatus(ctx context.Context, cfg Config, write func(sharedprotocol.ControlMessage) error, log *slog.Logger) {
	report, err := loadUpdateStatus(cfg.UpdateStatusPath)
	if err != nil {
		return
	}
	if err := write(sharedprotocol.ControlMessage{Type: "update_status", UpdateReport: &report}); err != nil {
		logControlFailure(log, ctx, "update status report failed", "error", err)
	}
}

func logControlFailure(log *slog.Logger, ctx context.Context, message string, args ...any) {
	if ctx.Err() != nil {
		log.Debug(message, args...)
		return
	}
	log.Warn(message, args...)
}

func logControlReadFailure(log *slog.Logger, ctx context.Context, err error) {
	status := websocket.CloseStatus(err)
	if ctx.Err() != nil || status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway {
		log.Debug("control websocket read failed", "error", err)
		return
	}
	log.Warn("control websocket read failed", "error", err)
}

func restartNodeService() error {
	return exec.Command("systemctl", "restart", "nyarelay-node").Run()
}

func verifySignedConfig(nodeID string, signingKey string, signed model.SignedConfig) error {
	if strings.TrimSpace(signingKey) == "" {
		return errors.New("config signing key is required")
	}
	if err := sharedcrypto.VerifyJSON(signingKey, signed.Config, signed.Signature); err != nil {
		return fmt.Errorf("verify config: %w", err)
	}
	if signed.Config.NodeID != nodeID {
		return fmt.Errorf("config belongs to %s, not %s", signed.Config.NodeID, nodeID)
	}
	if signed.Config.IssuedAt.IsZero() {
		return errors.New("config issue time is missing")
	}
	if signed.Config.ExpiresAt.IsZero() {
		return errors.New("config expiration is missing")
	}
	if signed.Config.ExpiresAt.Before(signed.Config.IssuedAt) {
		return errors.New("config expiration is before issue time")
	}
	if signed.Config.ExpiresAt.Sub(signed.Config.IssuedAt) > maxSignedConfigLifetime {
		return errors.New("config lease is too long")
	}
	now := time.Now()
	if signed.Config.IssuedAt.After(now.Add(maxSignedConfigClockSkew)) {
		return errors.New("config issue time is too far in the future")
	}
	if signed.Config.ExpiresAt.After(now.Add(maxSignedConfigLifetime + maxSignedConfigClockSkew)) {
		return errors.New("config expiration is too far in the future")
	}
	if !now.Before(signed.Config.ExpiresAt) {
		return errors.New("config has expired")
	}
	return nil
}

func controllerLogTarget(raw string) string {
	u, err := validateControllerURL(raw)
	if err != nil {
		return "invalid"
	}
	return u.Scheme + "://" + u.Host
}

func saveConfig(path string, signed model.SignedConfig) error {
	payload, err := json.MarshalIndent(signed, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFileAtomic(path, payload)
}

func loadConfig(path string) (model.SignedConfig, error) {
	payload, err := readFileLimited(path, 8<<20)
	if err != nil {
		return model.SignedConfig{}, err
	}
	var signed model.SignedConfig
	if err := json.Unmarshal(payload, &signed); err != nil {
		return model.SignedConfig{}, err
	}
	return signed, nil
}

func readFileLimited(path string, maxBytes int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("file %s is too large", path)
	}
	return os.ReadFile(path)
}

func writePrivateFileAtomic(path string, payload []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nyarelay-private-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}
	// Windows cannot replace an existing file with Rename. The temporary file
	// still prevents readers from observing a partially written payload.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmpPath, path)
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
