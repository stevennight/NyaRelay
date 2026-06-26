package controller

import (
	"testing"

	"nyarelay/internal/shared/model"
)

func TestScopeConfigForNodeLimitsLinkSecrets(t *testing.T) {
	link := model.Link{
		ID:       "link_1",
		Type:     model.LinkMTLS,
		FromNode: "entry",
		ToNode:   "exit",
		Settings: map[string]string{
			"secret":      "relay-secret",
			"ca_cert":     "ca",
			"server_cert": "server-cert",
			"server_key":  "server-key",
			"client_cert": "client-cert",
			"client_key":  "client-key",
		},
	}
	route := model.Route{
		ID:        "route_1",
		EntryNode: "entry",
		Hops:      []model.RouteHop{{LinkID: "link_1"}},
		Enabled:   true,
	}

	_, entryLinks := scopeConfigForNode("entry", []model.Route{route}, []model.Link{link})
	if len(entryLinks) != 1 {
		t.Fatalf("expected entry link")
	}
	if entryLinks[0].Settings["server_key"] != "" {
		t.Fatal("entry node must not receive server key")
	}
	if entryLinks[0].Settings["client_key"] == "" {
		t.Fatal("entry node must receive client key")
	}

	_, exitLinks := scopeConfigForNode("exit", []model.Route{route}, []model.Link{link})
	if len(exitLinks) != 1 {
		t.Fatalf("expected exit link")
	}
	if exitLinks[0].Settings["client_key"] != "" {
		t.Fatal("exit node must not receive client key")
	}
	if exitLinks[0].Settings["server_key"] == "" {
		t.Fatal("exit node must receive server key")
	}
}
