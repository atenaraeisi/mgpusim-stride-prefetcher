package cache

import (
	"testing"

	"github.com/sarchlab/akita/v5/mem/memprotocol"
	"github.com/sarchlab/akita/v5/mem/vm"
)

func TestStridePrefetcherPositiveStride(t *testing.T) {
	p := newTestStridePrefetcher(1, 2, true)

	inspectAddress(p, 1, 0x1000)
	assertAddresses(t, p.GetPrefetchAddresses())

	inspectAddress(p, 1, 0x1040)
	assertAddresses(t, p.GetPrefetchAddresses())

	inspectAddress(p, 1, 0x1080)
	assertAddresses(t, p.GetPrefetchAddresses(), 0x10c0)
}

func TestStridePrefetcherDegree(t *testing.T) {
	p := newTestStridePrefetcher(3, 2, true)

	inspectAddress(p, 1, 0x2000)
	inspectAddress(p, 1, 0x2040)
	inspectAddress(p, 1, 0x2080)

	assertAddresses(t, p.GetPrefetchAddresses(), 0x20c0, 0x2100, 0x2140)
}

func TestStridePrefetcherNegativeStride(t *testing.T) {
	p := newTestStridePrefetcher(2, 2, true)

	inspectAddress(p, 1, 0x1180)
	inspectAddress(p, 1, 0x1140)
	inspectAddress(p, 1, 0x1100)

	assertAddresses(t, p.GetPrefetchAddresses(), 0x10c0, 0x1080)
}

func TestStridePrefetcherStopsWhenPatternChanges(t *testing.T) {
	p := newTestStridePrefetcher(1, 2, true)

	inspectAddress(p, 1, 0x1000)
	inspectAddress(p, 1, 0x1040)
	inspectAddress(p, 1, 0x1080)
	assertAddresses(t, p.GetPrefetchAddresses(), 0x10c0)

	inspectAddress(p, 1, 0x1200)
	assertAddresses(t, p.GetPrefetchAddresses())

	inspectAddress(p, 1, 0x1240)
	assertAddresses(t, p.GetPrefetchAddresses())

	inspectAddress(p, 1, 0x1280)
	assertAddresses(t, p.GetPrefetchAddresses(), 0x12c0)
}

func TestStridePrefetcherIgnoresZeroStride(t *testing.T) {
	p := newTestStridePrefetcher(1, 2, true)

	inspectAddress(p, 1, 0x1000)
	inspectAddress(p, 1, 0x1004) // Same 64-byte cache line.
	assertAddresses(t, p.GetPrefetchAddresses())

	inspectAddress(p, 1, 0x1040)
	assertAddresses(t, p.GetPrefetchAddresses())

	inspectAddress(p, 1, 0x1080)
	assertAddresses(t, p.GetPrefetchAddresses(), 0x10c0)
}

func TestStridePrefetcherStopsAtPageBoundary(t *testing.T) {
	p := newTestStridePrefetcher(4, 2, true)

	inspectAddress(p, 1, 0x0f00)
	inspectAddress(p, 1, 0x0f40)
	inspectAddress(p, 1, 0x0f80)

	assertAddresses(t, p.GetPrefetchAddresses(), 0x0fc0)
}

func TestStridePrefetcherCanCrossPageWhenConfigured(t *testing.T) {
	p := newTestStridePrefetcher(3, 2, false)

	inspectAddress(p, 1, 0x0f00)
	inspectAddress(p, 1, 0x0f40)
	inspectAddress(p, 1, 0x0f80)

	assertAddresses(t, p.GetPrefetchAddresses(), 0x0fc0, 0x1000, 0x1040)
}

func TestStridePrefetcherStopsNegativeStrideAtPageBoundary(t *testing.T) {
	p := newTestStridePrefetcher(2, 2, true)

	inspectAddress(p, 1, 0x1080)
	inspectAddress(p, 1, 0x1040)
	inspectAddress(p, 1, 0x1000)

	assertAddresses(t, p.GetPrefetchAddresses())
}

func TestStridePrefetcherAvoidsAddressOverflow(t *testing.T) {
	config := DefaultStridePrefetcherConfig()
	config.BlockSize = 1
	config.PageSize = 0
	config.PreventPageCrossing = false
	config.Degree = 2
	config.ConfidenceThreshold = 1
	p := NewStridePrefetcher(config)

	inspectAddress(p, 1, ^uint64(0)-2)
	inspectAddress(p, 1, ^uint64(0)-1)

	assertAddresses(t, p.GetPrefetchAddresses(), ^uint64(0))
}

func TestStridePrefetcherAvoidsAddressUnderflow(t *testing.T) {
	config := DefaultStridePrefetcherConfig()
	config.BlockSize = 1
	config.PageSize = 0
	config.PreventPageCrossing = false
	config.Degree = 2
	config.ConfidenceThreshold = 1
	p := NewStridePrefetcher(config)

	inspectAddress(p, 1, 2)
	inspectAddress(p, 1, 1)

	assertAddresses(t, p.GetPrefetchAddresses(), 0)
}

func TestStridePrefetcherKeepsIndependentPIDState(t *testing.T) {
	p := newTestStridePrefetcher(1, 2, true)

	inspectAddress(p, 1, 0x1000)
	inspectAddress(p, 2, 0x8000)
	inspectAddress(p, 1, 0x1040)
	inspectAddress(p, 2, 0x8080)
	inspectAddress(p, 1, 0x1080)
	assertAddresses(t, p.GetPrefetchAddresses(), 0x10c0)

	inspectAddress(p, 2, 0x8100)
	assertAddresses(t, p.GetPrefetchAddresses(), 0x8180)
}

func TestStridePrefetcherBoundsHistory(t *testing.T) {
	config := DefaultStridePrefetcherConfig()
	config.HistorySize = 3
	config.BlockSize = 1
	config.PreventPageCrossing = false
	p := NewStridePrefetcher(config)

	inspectAddress(p, 7, 1)
	inspectAddress(p, 7, 2)
	inspectAddress(p, 7, 3)
	inspectAddress(p, 7, 4)

	state := p.streams[vm.PID(7)]
	assertAddresses(t, state.history, 2, 3, 4)
}

func TestStridePrefetcherRejectsInvalidRequestsAndClearsStaleOutput(t *testing.T) {
	p := newTestStridePrefetcher(1, 2, true)

	inspectAddress(p, 1, 0x1000)
	inspectAddress(p, 1, 0x1040)
	inspectAddress(p, 1, 0x1080)
	assertAddresses(t, p.GetPrefetchAddresses(), 0x10c0)

	p.Inspect(nil)
	assertAddresses(t, p.GetPrefetchAddresses())

	p.Inspect(&memprotocol.ReadReq{Address: 0x10c0, AccessByteSize: 0, PID: 1})
	assertAddresses(t, p.GetPrefetchAddresses())
}

func TestStridePrefetcherReset(t *testing.T) {
	p := newTestStridePrefetcher(1, 2, true)

	inspectAddress(p, 1, 0x1000)
	inspectAddress(p, 1, 0x1040)
	inspectAddress(p, 1, 0x1080)
	p.Reset()

	if len(p.streams) != 0 {
		t.Fatalf("expected all stream states to be cleared, got %d", len(p.streams))
	}
	assertAddresses(t, p.GetPrefetchAddresses())

	inspectAddress(p, 1, 0x10c0)
	assertAddresses(t, p.GetPrefetchAddresses())
}

func TestGetPrefetchAddressesReturnsCopy(t *testing.T) {
	p := newTestStridePrefetcher(1, 2, true)

	inspectAddress(p, 1, 0x1000)
	inspectAddress(p, 1, 0x1040)
	inspectAddress(p, 1, 0x1080)

	addresses := p.GetPrefetchAddresses()
	addresses[0] = 0

	assertAddresses(t, p.GetPrefetchAddresses(), 0x10c0)
}

func newTestStridePrefetcher(
	degree int,
	confidenceThreshold int,
	preventPageCrossing bool,
) *StridePrefetcher {
	config := DefaultStridePrefetcherConfig()
	config.Degree = degree
	config.ConfidenceThreshold = confidenceThreshold
	config.PreventPageCrossing = preventPageCrossing
	return NewStridePrefetcher(config)
}

func inspectAddress(p *StridePrefetcher, pid vm.PID, address uint64) {
	p.Inspect(&memprotocol.ReadReq{
		Address:        address,
		AccessByteSize: 4,
		PID:            pid,
	})
}

func assertAddresses(t *testing.T, actual []uint64, expected ...uint64) {
	t.Helper()

	if len(actual) != len(expected) {
		t.Fatalf("expected %d addresses %v, got %d addresses %v",
			len(expected), expected, len(actual), actual)
	}

	for i := range expected {
		if actual[i] != expected[i] {
			t.Fatalf("address %d: expected %#x, got %#x", i, expected[i], actual[i])
		}
	}
}
