package node

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

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

func TestBootstrapConfigUsesLongerCacheForSameRevision(t *testing.T) {
	ctx := context.Background()
	pub, priv, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}

	httpCfg := signedConfig(t, priv, 4, "node-1")
	cacheCfg := signedConfig(t, priv, 4, "node-1")
	cacheCfg.Config.ExpiresAt = cacheCfg.Config.IssuedAt.Add(model.RelayConfigLease)
	cacheCfg.Signature, err = sharedcrypto.SignJSON(priv, cacheCfg.Config)
	if err != nil {
		t.Fatal(err)
	}

	cacheDir := t.TempDir()
	if err := saveConfig(cacheDir+"/cached.json", cacheCfg); err != nil {
		t.Fatal(err)
	}

	client := fakeConfigClient{signed: httpCfg}
	var currentRevision atomic.Int64
	var applied []string
	apply := func(ctx context.Context, signed model.SignedConfig, source string) error {
		if err := verifySignedConfig("node-1", pub, signed); err != nil {
			return err
		}
		applied = append(applied, source)
		currentRevision.Store(signed.Config.Revision)
		return nil
	}

	bootstrapConfig(ctx, client, cacheDir+"/cached.json", &currentRevision, apply, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if len(applied) != 2 || applied[0] != "http" || applied[1] != "cache" {
		t.Fatalf("unexpected apply order: %v", applied)
	}
}

func TestVerifySignedConfigRequiresSigningKeyAndFreshExpiry(t *testing.T) {
	pub, priv, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	valid := signedConfig(t, priv, 1, "node-1")
	if err := verifySignedConfig("node-1", "", valid); err == nil {
		t.Fatal("missing signing key must be rejected")
	}
	expired := valid
	expired.Config.ExpiresAt = time.Now().Add(-time.Minute)
	expired.Signature, err = sharedcrypto.SignJSON(priv, expired.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySignedConfig("node-1", pub, expired); err == nil {
		t.Fatal("expired config must be rejected")
	}
	longLease := valid
	longLease.Config.ExpiresAt = longLease.Config.IssuedAt.Add(maxSignedConfigLifetime)
	longLease.Signature, err = sharedcrypto.SignJSON(priv, longLease.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySignedConfig("node-1", pub, longLease); err != nil {
		t.Fatalf("maximum supported config lease must be accepted: %v", err)
	}
	tooLong := valid
	tooLong.Config.ExpiresAt = tooLong.Config.IssuedAt.Add(maxSignedConfigLifetime + time.Minute)
	tooLong.Signature, err = sharedcrypto.SignJSON(priv, tooLong.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySignedConfig("node-1", pub, tooLong); err == nil {
		t.Fatal("overlong config lease must be rejected")
	}

	future := valid
	future.Config.IssuedAt = time.Now().Add(maxSignedConfigClockSkew + time.Minute)
	future.Config.ExpiresAt = future.Config.IssuedAt.Add(10 * time.Minute)
	future.Signature, err = sharedcrypto.SignJSON(priv, future.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySignedConfig("node-1", pub, future); err == nil {
		t.Fatal("config issued too far in the future must be rejected")
	}

	missingIssuedAt := valid
	missingIssuedAt.Config.IssuedAt = time.Time{}
	missingIssuedAt.Signature, err = sharedcrypto.SignJSON(priv, missingIssuedAt.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySignedConfig("node-1", pub, missingIssuedAt); err == nil {
		t.Fatal("config without issue time must be rejected")
	}
}

func signedConfig(t *testing.T, priv string, revision int64, nodeID string) model.SignedConfig {
	t.Helper()
	cfg := model.RelayConfig{
		Revision:  revision,
		IssuedAt:  time.Now().UTC(),
		NodeID:    nodeID,
		ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
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
