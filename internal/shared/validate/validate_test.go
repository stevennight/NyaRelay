package validate

import (
	"testing"

	"nyarelay/internal/shared/model"
)

func TestRouteAllowsSingleNodeDirectOut(t *testing.T) {
	err := Route(model.Route{
		ID:        "route_1",
		Name:      "premium-vless-entry",
		Protocol:  model.ProtocolTCP,
		EntryNode: "node_1",
		Listen:    "127.0.0.1:8443",
		Hops:      nil,
		Target:    "127.0.0.1:443",
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLinkRejectsUnknownType(t *testing.T) {
	err := Link(model.Link{
		ID:         "link_1",
		Name:       "bad",
		Type:       "shell",
		FromNode:   "a",
		ToNode:     "b",
		BindAddr:   "127.0.0.1:9000",
		PublicAddr: "127.0.0.1:9000",
		Enabled:    true,
	})
	if err == nil {
		t.Fatal("expected unknown link type to fail")
	}
}
