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
