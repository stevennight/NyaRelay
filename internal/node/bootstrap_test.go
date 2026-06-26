package node

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"

	sharedcrypto "nyarelay/internal/shared/crypto"
	"nyarelay/internal/shared/model"
)

type fakeConfigClient struct {
	signed model.SignedConfig
	err    error
}

func (f fakeConfigClient) config(context.Context) (model.SignedConfig, error) {
	return f.signed, f.err
}

func TestBootstrapConfigPrefersNewerCache(t *testing.T) {
	ctx := context.Background()
	pub, priv, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}

	httpCfg := signedConfig(t, priv, 1, "node-1")
	cacheCfg := signedConfig(t, priv, 2, "node-1")

	cacheDir := t.TempDir()
	if err := saveConfig(cacheDir+"/cached.json", cacheCfg); err != nil {
		t.Fatal(err)
	}

	client := fakeConfigClient{signed: httpCfg}
	var currentRevision atomic.Int64
	var applied []int64
	apply := func(ctx context.Context, signed model.SignedConfig, source string) error {
		if err := verifySignedConfig("node-1", pub, signed); err != nil {
			return err
		}
		applied = append(applied, signed.Config.Revision)
		currentRevision.Store(signed.Config.Revision)
		return nil
	}

	bootstrapConfig(ctx, client, cacheDir+"/cached.json", &currentRevision, apply, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if len(applied) != 2 || applied[0] != 1 || applied[1] != 2 {
		t.Fatalf("unexpected apply order: %v", applied)
	}
}

func TestBootstrapConfigFallsBackToCache(t *testing.T) {
	ctx := context.Background()
	pub, priv, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}

	cacheCfg := signedConfig(t, priv, 3, "node-1")
	cacheDir := t.TempDir()
	if err := saveConfig(cacheDir+"/cached.json", cacheCfg); err != nil {
		t.Fatal(err)
	}

	client := fakeConfigClient{err: context.DeadlineExceeded}
	var currentRevision atomic.Int64
	var applied []int64
	apply := func(ctx context.Context, signed model.SignedConfig, source string) error {
		if err := verifySignedConfig("node-1", pub, signed); err != nil {
			return err
		}
		applied = append(applied, signed.Config.Revision)
		currentRevision.Store(signed.Config.Revision)
		return nil
	}

	bootstrapConfig(ctx, client, cacheDir+"/cached.json", &currentRevision, apply, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if len(applied) != 1 || applied[0] != 3 {
		t.Fatalf("unexpected apply order: %v", applied)
	}
}

func signedConfig(t *testing.T, priv string, revision int64, nodeID string) model.SignedConfig {
	t.Helper()
	cfg := model.RelayConfig{
		Revision: revision,
		NodeID:   nodeID,
	}
	sig, err := sharedcrypto.SignJSON(priv, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return model.SignedConfig{
		Config:    cfg,
		Signature: sig,
	}
}
