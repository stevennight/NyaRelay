package controller

import (
	"strings"
	"testing"
)

func TestInstallScriptRestartsNodeServiceAfterOverwrite(t *testing.T) {
	script := installScript()

	if !strings.Contains(script, "systemctl enable nyarelay-node\nsystemctl restart nyarelay-node") {
		t.Fatalf("install script should restart nyarelay-node after overwriting the binary:\n%s", script)
	}
	if strings.Contains(script, "systemctl enable --now nyarelay-node\n") {
		t.Fatalf("install script should not rely on enable --now for existing running node service")
	}
}
