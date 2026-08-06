package relay

import (
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"nyarelay/internal/shared/model"
)

const (
	defaultCandidateMaxFails    = 1
	defaultCandidateFailTimeout = 60 * time.Second
)

type candidateSelector struct {
	mu          sync.Mutex
	rr          map[string]int
	states      map[string]*candidateState
	rnd         *rand.Rand
	maxFails    int
	failTimeout time.Duration
}

type targetSelector struct {
	mu          sync.Mutex
	rr          map[string]int
	states      map[string]*candidateState
	rnd         *rand.Rand
	maxFails    int
	failTimeout time.Duration
}

type candidateState struct {
	consecutiveFailures int
	lastFailureAt       time.Time
	disabledUntil       time.Time
}

func newCandidateSelector() *candidateSelector {
	return &candidateSelector{
		rr:          make(map[string]int),
		states:      make(map[string]*candidateState),
		rnd:         rand.New(rand.NewSource(time.Now().UnixNano())),
		maxFails:    defaultCandidateMaxFails,
		failTimeout: defaultCandidateFailTimeout,
	}
}

func newTargetSelector() *targetSelector {
	return &targetSelector{
		rr:          make(map[string]int),
		states:      make(map[string]*candidateState),
		rnd:         rand.New(rand.NewSource(time.Now().UnixNano())),
		maxFails:    defaultCandidateMaxFails,
		failTimeout: defaultCandidateFailTimeout,
	}
}

func (s *targetSelector) order(forward model.ForwardRuntime, network string) []int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	candidates := forwardTargetsForNetwork(forward.Targets, forward.Target, network)
	if len(candidates) == 0 {
		return nil
	}
	strategy := forwardStrategyForNetwork(forward, network)
	order := make([]int, 0, len(candidates))
	switch strategy {
	case "round_robin":
		pool := weightedTargetPool(candidates)
		key := targetSelectorKey(forward.ID, network)
		start := s.rr[key] % len(pool)
		s.rr[key] = (start + 1) % len(pool)
		for _, idx := range dedupeCandidatePool(pool[start:], pool[:start]) {
			target := candidates[idx].target
			if s.targetAvailableLocked(forward.ID, target.ID, network, now) {
				order = append(order, candidates[idx].index)
			}
		}
	case "random":
		pool := weightedTargetPool(candidates)
		s.rnd.Shuffle(len(pool), func(i, j int) {
			pool[i], pool[j] = pool[j], pool[i]
		})
		for _, idx := range dedupeCandidatePool(pool) {
			target := candidates[idx].target
			if s.targetAvailableLocked(forward.ID, target.ID, network, now) {
				order = append(order, candidates[idx].index)
			}
		}
	case "failover":
		for _, candidate := range candidates {
			if s.targetAvailableLocked(forward.ID, candidate.target.ID, network, now) {
				order = append(order, candidate.index)
			}
		}
	case "single":
		candidate := candidates[0]
		if s.targetAvailableLocked(forward.ID, candidate.target.ID, network, now) {
			order = append(order, candidate.index)
		}
	default:
		candidate := candidates[0]
		if s.targetAvailableLocked(forward.ID, candidate.target.ID, network, now) {
			order = append(order, candidate.index)
		}
	}
	return order
}

func (s *targetSelector) recordSuccess(forwardID, targetID, network string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[targetSelectorStateKey(forwardID, targetID, network)] = &candidateState{}
}

func (s *targetSelector) recordFailure(forwardID, targetID, network string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	key := targetSelectorStateKey(forwardID, targetID, network)
	state := s.states[key]
	if state == nil {
		state = &candidateState{}
		s.states[key] = state
	}
	state.consecutiveFailures++
	state.lastFailureAt = now
	if s.maxFails <= 0 {
		s.maxFails = defaultCandidateMaxFails
	}
	if state.consecutiveFailures >= s.maxFails {
		state.disabledUntil = now.Add(s.failTimeout)
	}
}

func (s *targetSelector) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rr = make(map[string]int)
	s.states = make(map[string]*candidateState)
}

func (s *targetSelector) targetAvailableLocked(forwardID, targetID, network string, now time.Time) bool {
	state := s.states[targetSelectorStateKey(forwardID, targetID, network)]
	if state == nil {
		return true
	}
	if !state.disabledUntil.IsZero() && !now.Before(state.disabledUntil) {
		state.consecutiveFailures = 0
		state.disabledUntil = time.Time{}
		return true
	}
	return state.disabledUntil.IsZero()
}

func (s *candidateSelector) order(tunnelID string, stage model.TunnelRuntimeStage, network string) []int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	candidates := stageCandidatesForNetwork(stage, network)
	total := len(candidates)
	if total == 0 {
		return nil
	}

	strategy := strategyForNetwork(stage, network)
	order := make([]int, 0, total)
	switch strategy {
	case "round_robin":
		pool := weightedCandidatePool(candidates)
		if len(pool) == 0 {
			break
		}
		key := stageSelectorKey(tunnelID, stage.Index, network)
		start := s.rr[key] % len(pool)
		s.rr[key] = (start + 1) % len(pool)
		for _, idx := range dedupeCandidatePool(pool[start:], pool[:start]) {
			candidate := candidates[idx]
			if s.candidateAvailableLocked(tunnelID, stage.Index, candidate.node.NodeID, network, now) {
				order = append(order, candidate.index)
			}
		}
	case "random":
		pool := weightedCandidatePool(candidates)
		if len(pool) == 0 {
			break
		}
		s.rnd.Shuffle(len(pool), func(i, j int) {
			pool[i], pool[j] = pool[j], pool[i]
		})
		for _, idx := range dedupeCandidatePool(pool) {
			candidate := candidates[idx]
			if s.candidateAvailableLocked(tunnelID, stage.Index, candidate.node.NodeID, network, now) {
				order = append(order, candidate.index)
			}
		}
	case "failover":
		for _, candidate := range candidates {
			if s.candidateAvailableLocked(tunnelID, stage.Index, candidate.node.NodeID, network, now) {
				order = append(order, candidate.index)
			}
		}
	case "single":
		candidate := candidates[0]
		if s.candidateAvailableLocked(tunnelID, stage.Index, candidate.node.NodeID, network, now) {
			order = append(order, candidate.index)
		}
	default:
		candidate := candidates[0]
		if len(order) == 0 && s.candidateAvailableLocked(tunnelID, stage.Index, candidate.node.NodeID, network, now) {
			order = append(order, candidate.index)
		}
	}
	return order
}

func (s *candidateSelector) recordSuccess(tunnelID string, stageIndex int, nodeID, network string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := candidateSelectorKey(tunnelID, stageIndex, nodeID, network)
	s.states[key] = &candidateState{}
}

func (s *candidateSelector) recordFailure(tunnelID string, stageIndex int, nodeID, network string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	key := candidateSelectorKey(tunnelID, stageIndex, nodeID, network)
	state := s.states[key]
	if state == nil {
		state = &candidateState{}
		s.states[key] = state
	}
	state.consecutiveFailures++
	state.lastFailureAt = now
	if s.maxFails <= 0 {
		s.maxFails = defaultCandidateMaxFails
	}
	if state.consecutiveFailures >= s.maxFails {
		state.disabledUntil = now.Add(s.failTimeout)
	}
}

func (s *candidateSelector) candidateAvailableLocked(tunnelID string, stageIndex int, nodeID, network string, now time.Time) bool {
	key := candidateSelectorKey(tunnelID, stageIndex, nodeID, network)
	state := s.states[key]
	if state == nil {
		return true
	}
	if !state.disabledUntil.IsZero() && !now.Before(state.disabledUntil) {
		state.consecutiveFailures = 0
		state.disabledUntil = time.Time{}
		return true
	}
	return state.disabledUntil.IsZero()
}

func stageSelectorKey(tunnelID string, stageIndex int, network string) string {
	return tunnelID + ":" + strconv.Itoa(stageIndex) + ":" + strings.ToLower(strings.TrimSpace(network))
}

func candidateSelectorKey(tunnelID string, stageIndex int, nodeID, network string) string {
	return tunnelID + ":" + strconv.Itoa(stageIndex) + ":" + strings.ToLower(strings.TrimSpace(network)) + ":" + nodeID
}

func targetSelectorKey(forwardID, network string) string {
	return forwardID + ":" + strings.ToLower(strings.TrimSpace(network))
}

func targetSelectorStateKey(forwardID, targetID, network string) string {
	return targetSelectorKey(forwardID, network) + ":" + targetID
}

func normalizeStageStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "", "single":
		return "single"
	case "round_robin":
		return "round_robin"
	case "random":
		return "random"
	case "failover":
		return "failover"
	default:
		return "single"
	}
}

func strategyForNetwork(stage model.TunnelRuntimeStage, network string) string {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "tcp":
		if normalized := normalizeStageStrategy(stage.TCPStrategy); normalized != "single" || strings.TrimSpace(stage.TCPStrategy) != "" {
			return normalized
		}
	case "udp":
		if normalized := normalizeStageStrategy(stage.UDPStrategy); normalized != "single" || strings.TrimSpace(stage.UDPStrategy) != "" {
			return normalized
		}
	}
	return normalizeStageStrategy(stage.Strategy)
}

type stageCandidate struct {
	index int
	node  model.TunnelRuntimeNode
}

func stageCandidatesForNetwork(stage model.TunnelRuntimeStage, network string) []stageCandidate {
	out := make([]stageCandidate, 0, len(stage.Nodes))
	protocol := model.ForwardProtocol(strings.ToLower(strings.TrimSpace(network)))
	for idx, node := range stage.Nodes {
		if runtimeNodeSupportsProtocol(node, protocol) {
			out = append(out, stageCandidate{index: idx, node: node})
		}
	}
	return out
}

func runtimeNodeSupportsProtocol(node model.TunnelRuntimeNode, protocol model.ForwardProtocol) bool {
	if protocol != model.ForwardProtocolTCP && protocol != model.ForwardProtocolUDP {
		return false
	}
	if len(node.Protocols) == 0 {
		return true
	}
	for _, candidateProtocol := range node.Protocols {
		if candidateProtocol == protocol {
			return true
		}
	}
	return false
}

func weightedCandidatePool(candidates []stageCandidate) []int {
	pool := make([]int, 0, len(candidates))
	for idx, candidate := range candidates {
		weight := candidate.node.Weight
		if weight <= 0 {
			weight = 1
		}
		for i := 0; i < weight; i++ {
			pool = append(pool, idx)
		}
	}
	return pool
}

type forwardTargetCandidate struct {
	index  int
	target model.ForwardTarget
}

func forwardTargetsForNetwork(targets []model.ForwardTarget, legacyTarget, network string) []forwardTargetCandidate {
	if len(targets) == 0 && strings.TrimSpace(legacyTarget) != "" {
		targets = []model.ForwardTarget{{
			ID:      "legacy-target",
			Address: strings.TrimSpace(legacyTarget),
			Weight:  1,
			Enabled: true,
		}}
	}
	protocol := model.ForwardProtocol(strings.ToLower(strings.TrimSpace(network)))
	out := make([]forwardTargetCandidate, 0, len(targets))
	for idx, target := range targets {
		if !target.Enabled || strings.TrimSpace(target.Address) == "" || !targetSupportsProtocol(target.Protocols, protocol) {
			continue
		}
		if target.ID == "" {
			target.ID = target.Address
		}
		out = append(out, forwardTargetCandidate{index: idx, target: target})
	}
	return out
}

func forwardTargetAt(forward model.ForwardRuntime, index int) (model.ForwardTarget, bool) {
	if index >= 0 && index < len(forward.Targets) {
		target := forward.Targets[index]
		if target.ID == "" {
			target.ID = target.Address
		}
		return target, true
	}
	if len(forward.Targets) == 0 && index == 0 && strings.TrimSpace(forward.Target) != "" {
		return model.ForwardTarget{
			ID:      "legacy-target",
			Address: strings.TrimSpace(forward.Target),
			Weight:  1,
			Enabled: true,
		}, true
	}
	return model.ForwardTarget{}, false
}

func targetSupportsProtocol(protocols []model.ForwardProtocol, protocol model.ForwardProtocol) bool {
	if protocol != model.ForwardProtocolTCP && protocol != model.ForwardProtocolUDP {
		return false
	}
	if len(protocols) == 0 {
		return true
	}
	for _, candidate := range protocols {
		if candidate == protocol {
			return true
		}
	}
	return false
}

func weightedTargetPool(candidates []forwardTargetCandidate) []int {
	pool := make([]int, 0, len(candidates))
	for idx, candidate := range candidates {
		weight := candidate.target.Weight
		if weight <= 0 {
			weight = 1
		}
		for i := 0; i < weight; i++ {
			pool = append(pool, idx)
		}
	}
	return pool
}

func normalizeForwardStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "", "single":
		return "single"
	case "round_robin":
		return "round_robin"
	case "random":
		return "random"
	case "failover":
		return "failover"
	default:
		return "single"
	}
}

func forwardStrategyForNetwork(forward model.ForwardRuntime, network string) string {
	strategy := forward.Strategy
	if strings.EqualFold(strings.TrimSpace(network), "tcp") && strings.TrimSpace(forward.TCPStrategy) != "" {
		strategy = forward.TCPStrategy
	}
	if strings.EqualFold(strings.TrimSpace(network), "udp") && strings.TrimSpace(forward.UDPStrategy) != "" {
		strategy = forward.UDPStrategy
	}
	return normalizeForwardStrategy(strategy)
}

func dedupeCandidatePool(indices ...[]int) []int {
	seen := make(map[int]bool)
	out := make([]int, 0)
	for _, seq := range indices {
		for _, idx := range seq {
			if seen[idx] {
				continue
			}
			seen[idx] = true
			out = append(out, idx)
		}
	}
	return out
}
