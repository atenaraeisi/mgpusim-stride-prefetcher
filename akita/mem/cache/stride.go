package cache

import (
	"github.com/sarchlab/akita/v5/mem/memprotocol"
	"github.com/sarchlab/akita/v5/mem/vm"
)

type StridePrefetcherConfig struct {
	// Maximum cache lines to prefetch per detected stride.
	Degree int

	// Matching strides required before prefetching.
	ConfidenceThreshold int

	// Maximum demand addresses stored per process.
	HistorySize int

	// Cache-line size in bytes.
	BlockSize uint64

	// Page size used for boundary checks.
	PageSize uint64

	// Prevents prefetching across page boundaries.
	PreventPageCrossing bool
}

// strideStreamState tracks per-process stride prediction state.
// PID is used because the prefetcher does not expose the program counter.
type strideStreamState struct {
	history     []uint64
	lastAddress uint64
	hasLast     bool
	stride      int64
	confidence  int
}

// StridePrefetcher detects a constant difference between consecutive demand
// cache-line addresses and predicts future addresses using that difference.
type StridePrefetcher struct {
	config  StridePrefetcherConfig
	streams map[vm.PID]*strideStreamState
	pending []uint64
}

// Verify at compile time that StridePrefetcher implements the common cache
// prefetcher interface.
var _ Prefetcher = (*StridePrefetcher)(nil)

// DefaultStridePrefetcherConfig returns conservative defaults suitable for a
// 64-byte cache line and a 4-KiB page.
func DefaultStridePrefetcherConfig() StridePrefetcherConfig {
	return StridePrefetcherConfig{
		Degree:              1,
		ConfidenceThreshold: 2,
		HistorySize:         8,
		BlockSize:           64,
		PageSize:            4096,
		PreventPageCrossing: true,
	}
}

// NewStridePrefetcher creates a configurable stride prefetcher. Invalid zero or
// negative numeric parameters are replaced with safe defaults.
func NewStridePrefetcher(config StridePrefetcherConfig) *StridePrefetcher {
	defaults := DefaultStridePrefetcherConfig()

	if config.Degree <= 0 {
		config.Degree = defaults.Degree
	}
	if config.ConfidenceThreshold <= 0 {
		config.ConfidenceThreshold = defaults.ConfidenceThreshold
	}
	if config.HistorySize < 2 {
		config.HistorySize = defaults.HistorySize
	}
	if config.BlockSize == 0 {
		config.BlockSize = defaults.BlockSize
	}
	if config.PreventPageCrossing && config.PageSize == 0 {
		config.PageSize = defaults.PageSize
	}

	return &StridePrefetcher{
		config:  config,
		streams: make(map[vm.PID]*strideStreamState),
	}
}

// Inspect observes one demand read request and updates the predictor. The list
// returned by GetPrefetchAddresses always corresponds to the most recent valid
// Inspect call.
func (p *StridePrefetcher) Inspect(req *memprotocol.ReadReq) {
	// Never expose stale predictions from an earlier demand request.
	p.pending = p.pending[:0]

	if req == nil || req.AccessByteSize == 0 {
		return
	}

	address := alignDown(req.Address, p.config.BlockSize)
	state := p.streamFor(req.PID)
	state.appendHistory(address, p.config.HistorySize)

	if !state.hasLast {
		state.lastAddress = address
		state.hasLast = true
		return
	}

	candidateStride, ok := calculateStride(state.lastAddress, address)
	if !ok {
		state.restartAt(address)
		return
	}

	state.lastAddress = address

	// Repeated accesses to the same cache line do not form a useful stride.
	// Ignore them without destroying an otherwise stable pattern.
	if candidateStride == 0 {
		return
	}

	if candidateStride != state.stride {
		// A changed stride immediately stops prefetching. This observation is
		// the first confirmation candidate for a new pattern.
		state.stride = candidateStride
		state.confidence = 1
	} else if state.confidence < p.config.ConfidenceThreshold {
		state.confidence++
	}

	if state.confidence < p.config.ConfidenceThreshold {
		return
	}

	p.generateAddresses(address, state.stride)
}

// GetPrefetchAddresses returns a copy of the predictions generated for the most
// recent demand request.
func (p *StridePrefetcher) GetPrefetchAddresses() []uint64 {
	return append([]uint64(nil), p.pending...)
}

// Reset clears all histories, confidence values, strides, and pending
// predictions while preserving the configured parameters.
func (p *StridePrefetcher) Reset() {
	p.streams = make(map[vm.PID]*strideStreamState)
	p.pending = nil
}

func (p *StridePrefetcher) streamFor(pid vm.PID) *strideStreamState {
	state, found := p.streams[pid]
	if found {
		return state
	}

	state = &strideStreamState{
		history: make([]uint64, 0, p.config.HistorySize),
	}
	p.streams[pid] = state
	return state
}

func (p *StridePrefetcher) generateAddresses(current uint64, stride int64) {
	next := current
	for i := 0; i < p.config.Degree; i++ {
		predicted, ok := addStride(next, stride)
		if !ok {
			break
		}

		if p.config.PreventPageCrossing &&
			p.config.PageSize > 0 &&
			current/p.config.PageSize != predicted/p.config.PageSize {
			break
		}

		p.pending = append(p.pending, predicted)
		next = predicted
	}
}

func (s *strideStreamState) appendHistory(address uint64, limit int) {
	if len(s.history) < limit {
		s.history = append(s.history, address)
		return
	}

	copy(s.history, s.history[1:])
	s.history[len(s.history)-1] = address
}

func (s *strideStreamState) restartAt(address uint64) {
	s.lastAddress = address
	s.hasLast = true
	s.stride = 0
	s.confidence = 0
}

func alignDown(address, blockSize uint64) uint64 {
	return address / blockSize * blockSize
}

func calculateStride(previous, current uint64) (int64, bool) {
	const maxInt64AsUint64 = uint64(1<<63 - 1)

	if current >= previous {
		difference := current - previous
		if difference > maxInt64AsUint64 {
			return 0, false
		}
		return int64(difference), true
	}

	difference := previous - current
	if difference > maxInt64AsUint64 {
		return 0, false
	}
	return -int64(difference), true
}

func addStride(address uint64, stride int64) (uint64, bool) {
	if stride > 0 {
		step := uint64(stride)
		if address > ^uint64(0)-step {
			return 0, false
		}
		return address + step, true
	}

	// calculateStride never returns math.MinInt64, so negation is safe here.
	step := uint64(-stride)
	if address < step {
		return 0, false
	}
	return address - step, true
}
