package relay

import (
	"math/rand"
	"reflect"
	"testing"
	"time"

	"nyarelay/internal/shared/model"
)

func TestCandidateSelectorRoundRobin(t *testing.T) {
	sel := newCandidateSelector()
	stage := model.TunnelRuntimeStage{
		Index:    1,
		Role:     model.TunnelStageMiddle,
		Strategy: "round_robin",
		Nodes: []model.TunnelRuntimeNode{
			{NodeID: "a", Weight: 1},
			{NodeID: "b", Weight: 1},
			{NodeID: "c", Weight: 1},
		},
	}

	if got := sel.order("tun", stage, "tcp"); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Fatalf("first round robin order = %v", got)
	}
	if got := sel.order("tun", stage, "tcp"); !reflect.DeepEqual(got, []int{1, 2, 0}) {
		t.Fatalf("second round robin order = %v", got)
	}
}

func TestCandidateSelectorRandom(t *testing.T) {
	sel := newCandidateSelector()
	sel.rnd = rand.New(rand.NewSource(1))
	stage := model.TunnelRuntimeStage{
		Index:    1,
		Role:     model.TunnelStageMiddle,
		Strategy: "random",
		Nodes: []model.TunnelRuntimeNode{
			{NodeID: "a", Weight: 1},
			{NodeID: "b", Weight: 1},
			{NodeID: "c", Weight: 1},
		},
	}

	got := sel.order("tun", stage, "tcp")
	if len(got) != 3 {
		t.Fatalf("random order length = %d, want 3", len(got))
	}
	seen := map[int]bool{}
	for _, idx := range got {
		seen[idx] = true
	}
	for _, idx := range []int{0, 1, 2} {
		if !seen[idx] {
			t.Fatalf("random order missing index %d in %v", idx, got)
		}
	}
}

func TestCandidateSelectorFailoverRecovers(t *testing.T) {
	sel := newCandidateSelector()
	sel.failTimeout = 10 * time.Millisecond
	stage := model.TunnelRuntimeStage{
		Index:    1,
		Role:     model.TunnelStageMiddle,
		Strategy: "failover",
		Nodes: []model.TunnelRuntimeNode{
			{NodeID: "a", Weight: 1},
			{NodeID: "b", Weight: 1},
		},
	}

	sel.recordFailure("tun", 1, "a", "tcp")
	if got := sel.order("tun", stage, "tcp"); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("failover order after failure = %v", got)
	}

	time.Sleep(20 * time.Millisecond)
	if got := sel.order("tun", stage, "tcp"); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("failover order after recovery = %v", got)
	}
}

func TestCandidateSelectorFailureStateIsPerNetwork(t *testing.T) {
	sel := newCandidateSelector()
	stage := model.TunnelRuntimeStage{
		Index:    1,
		Role:     model.TunnelStageMiddle,
		Strategy: "failover",
		Nodes: []model.TunnelRuntimeNode{
			{NodeID: "a", Weight: 1},
			{NodeID: "b", Weight: 1},
		},
	}

	sel.recordFailure("tun", 1, "a", "udp")
	if got := sel.order("tun", stage, "udp"); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("udp order after udp failure = %v", got)
	}
	if got := sel.order("tun", stage, "tcp"); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("tcp order should ignore udp failure = %v", got)
	}
}

func TestStrategyForNetworkFallsBackToStageStrategy(t *testing.T) {
	stage := model.TunnelRuntimeStage{
		Strategy:    "failover",
		TCPStrategy: "round_robin",
	}
	if got := strategyForNetwork(stage, "tcp"); got != "round_robin" {
		t.Fatalf("tcp strategy = %q, want round_robin", got)
	}
	if got := strategyForNetwork(stage, "udp"); got != "failover" {
		t.Fatalf("udp strategy = %q, want failover fallback", got)
	}
}

func TestCandidateSelectorFiltersByProtocolMask(t *testing.T) {
	sel := newCandidateSelector()
	stage := model.TunnelRuntimeStage{
		Index:    1,
		Role:     model.TunnelStageMiddle,
		Strategy: "failover",
		Nodes: []model.TunnelRuntimeNode{
			{NodeID: "both-old-config", Weight: 1},
			{NodeID: "tcp-only", Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP}, Weight: 1},
			{NodeID: "udp-only", Protocols: []model.ForwardProtocol{model.ForwardProtocolUDP}, Weight: 1},
		},
	}

	if got := sel.order("tun", stage, "tcp"); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("tcp order = %v, want old-config and tcp-only", got)
	}
	if got := sel.order("tun", stage, "udp"); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Fatalf("udp order = %v, want old-config and udp-only", got)
	}
}

func TestCandidateSelectorRoundRobinCursorsArePerNetwork(t *testing.T) {
	sel := newCandidateSelector()
	stage := model.TunnelRuntimeStage{
		Index:       1,
		Role:        model.TunnelStageMiddle,
		Strategy:    "failover",
		TCPStrategy: "round_robin",
		UDPStrategy: "round_robin",
		Nodes: []model.TunnelRuntimeNode{
			{NodeID: "a", Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP, model.ForwardProtocolUDP}, Weight: 1},
			{NodeID: "b", Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP, model.ForwardProtocolUDP}, Weight: 1},
		},
	}

	if got := sel.order("tun", stage, "tcp"); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("first tcp order = %v", got)
	}
	if got := sel.order("tun", stage, "udp"); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("first udp order = %v, want independent cursor", got)
	}
	if got := sel.order("tun", stage, "tcp"); !reflect.DeepEqual(got, []int{1, 0}) {
		t.Fatalf("second tcp order = %v", got)
	}
	if got := sel.order("tun", stage, "udp"); !reflect.DeepEqual(got, []int{1, 0}) {
		t.Fatalf("second udp order = %v", got)
	}
}

func TestTargetSelectorRoundRobinAndProtocolMask(t *testing.T) {
	sel := newTargetSelector()
	forward := model.ForwardRuntime{
		ID:          "fwd",
		Strategy:    "failover",
		TCPStrategy: "round_robin",
		Targets: []model.ForwardTarget{
			{ID: "tcp-a", Address: "127.0.0.1:1", Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP}, Weight: 1, Enabled: true},
			{ID: "udp-only", Address: "127.0.0.1:2", Protocols: []model.ForwardProtocol{model.ForwardProtocolUDP}, Weight: 1, Enabled: true},
			{ID: "tcp-b", Address: "127.0.0.1:3", Protocols: []model.ForwardProtocol{model.ForwardProtocolTCP}, Weight: 1, Enabled: true},
		},
	}

	if got := sel.order(forward, "tcp"); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Fatalf("first target order = %v, want [0 2]", got)
	}
	if got := sel.order(forward, "tcp"); !reflect.DeepEqual(got, []int{2, 0}) {
		t.Fatalf("second target order = %v, want [2 0]", got)
	}
	if got := sel.order(forward, "udp"); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("udp target order = %v, want [1]", got)
	}
}

func TestTargetSelectorFailoverRecovers(t *testing.T) {
	sel := newTargetSelector()
	sel.failTimeout = 10 * time.Millisecond
	forward := model.ForwardRuntime{
		ID:       "fwd",
		Strategy: "failover",
		Targets: []model.ForwardTarget{
			{ID: "primary", Address: "127.0.0.1:1", Weight: 1, Enabled: true},
			{ID: "backup", Address: "127.0.0.1:2", Weight: 1, Enabled: true},
		},
	}

	sel.recordFailure("fwd", "primary", "tcp")
	if got := sel.order(forward, "tcp"); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("target failover order = %v, want [1]", got)
	}
	time.Sleep(20 * time.Millisecond)
	if got := sel.order(forward, "tcp"); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("target recovery order = %v, want [0 1]", got)
	}
}

func TestSelectorsSingleRetryAfterFailureWithoutFallback(t *testing.T) {
	candidateSel := newCandidateSelector()
	candidateSel.failTimeout = time.Hour
	stage := model.TunnelRuntimeStage{
		Index:    1,
		Strategy: "single",
		Nodes: []model.TunnelRuntimeNode{
			{NodeID: "primary"},
			{NodeID: "backup"},
		},
	}
	candidateSel.recordFailure("tun", 1, "primary", "tcp")
	if got := candidateSel.order("tun", stage, "tcp"); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("single candidate order after failure = %v, want [0]", got)
	}

	targetSel := newTargetSelector()
	targetSel.failTimeout = time.Hour
	forward := model.ForwardRuntime{
		ID:       "fwd",
		Strategy: "single",
		Targets: []model.ForwardTarget{
			{ID: "primary", Address: "127.0.0.1:1", Enabled: true},
			{ID: "backup", Address: "127.0.0.1:2", Enabled: true},
		},
	}
	targetSel.recordFailure("fwd", "primary", "tcp")
	if got := targetSel.order(forward, "tcp"); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("single target order after failure = %v, want [0]", got)
	}
}
