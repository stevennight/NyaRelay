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

func (s *candidateSelector) order(tunnelID string, stage model.TunnelRuntimeStage, network string) []int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	total := len(stage.Nodes)
	if total == 0 {
		return nil
	}

	strategy := normalizeStageStrategy(stage.Strategy)
	order := make([]int, 0, total)
	switch strategy {
	case "round_robin":
		pool := weightedCandidatePool(stage.Nodes)
		if len(pool) == 0 {
			break
		}
		key := stageSelectorKey(tunnelID, stage.Index, network)
		start := s.rr[key] % len(pool)
		s.rr[key] = (start + 1) % len(pool)
		for _, idx := range dedupeCandidatePool(pool[start:], pool[:start]) {
			if s.candidateAvailableLocked(tunnelID, stage.Index, stage.Nodes[idx].NodeID, now) {
				order = append(order, idx)
			}
		}
	case "random":
		pool := weightedCandidatePool(stage.Nodes)
		if len(pool) == 0 {
			break
		}
		s.rnd.Shuffle(len(pool), func(i, j int) {
			pool[i], pool[j] = pool[j], pool[i]
		})
		for _, idx := range dedupeCandidatePool(pool) {
			if s.candidateAvailableLocked(tunnelID, stage.Index, stage.Nodes[idx].NodeID, now) {
				order = append(order, idx)
			}
		}
	case "failover":
		for idx := range stage.Nodes {
			if s.candidateAvailableLocked(tunnelID, stage.Index, stage.Nodes[idx].NodeID, now) {
				order = append(order, idx)
			}
		}
	case "single":
		if s.candidateAvailableLocked(tunnelID, stage.Index, stage.Nodes[0].NodeID, now) {
			order = append(order, 0)
		}
	default:
		if len(order) == 0 && s.candidateAvailableLocked(tunnelID, stage.Index, stage.Nodes[0].NodeID, now) {
			order = append(order, 0)
		}
	}
	return order
}

func (s *candidateSelector) recordSuccess(tunnelID string, stageIndex int, nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := candidateSelectorKey(tunnelID, stageIndex, nodeID)
	s.states[key] = &candidateState{}
}

func (s *candidateSelector) recordFailure(tunnelID string, stageIndex int, nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	key := candidateSelectorKey(tunnelID, stageIndex, nodeID)
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

func (s *candidateSelector) candidateAvailableLocked(tunnelID string, stageIndex int, nodeID string, now time.Time) bool {
	key := candidateSelectorKey(tunnelID, stageIndex, nodeID)
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

func candidateSelectorKey(tunnelID string, stageIndex int, nodeID string) string {
	return tunnelID + ":" + strconv.Itoa(stageIndex) + ":" + nodeID
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

func weightedCandidatePool(nodes []model.TunnelRuntimeNode) []int {
	pool := make([]int, 0, len(nodes))
	for idx, node := range nodes {
		weight := node.Weight
		if weight <= 0 {
			weight = 1
		}
		for i := 0; i < weight; i++ {
			pool = append(pool, idx)
		}
	}
	return pool
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
