package writeback

import (
	"github.com/sarchlab/akita/v5/mem/memprotocol"
	"github.com/sarchlab/akita/v5/mem/vm"
	"github.com/sarchlab/akita/v5/timing"
	"github.com/sarchlab/akita/v5/tracing"
)

type topParser struct {
	cache *pipelineMW
}

func (p *topParser) Tick() bool {
	next := &p.cache.comp.State

	if cacheState(next.CacheState) != cacheStateRunning {
		return false
	}

	demandProgress := p.tickDemand()

	prefetchProgress := p.drainPendingPrefetches()

	return demandProgress || prefetchProgress
}

// tickDemand admits one processor-issued request into the pipeline, if the
// top port has one waiting and DirStageBuf has room.
func (p *topParser) tickDemand() bool {
	next := &p.cache.comp.State

	msg := p.cache.topPort().PeekIncoming()
	if msg == nil {
		return false
	}

	if !next.DirStageBuf.CanPush() {
		return false
	}

	trans := transactionState{
		ID: timing.GetIDGenerator().Generate(),
	}

	switch m := msg.(type) {
	case memprotocol.ReadReq:
		trans.HasRead = true
		trans.ReadMeta = m.MsgMeta
		trans.ReadAddress = m.Address
		trans.ReadAccessByteSize = m.AccessByteSize
		trans.ReadPID = m.PID

	case memprotocol.WriteReq:
		trans.HasWrite = true
		trans.WriteMeta = m.MsgMeta
		trans.WriteAddress = m.Address
		trans.WriteData = m.Data
		trans.WriteDirtyMask = m.DirtyMask
		trans.WritePID = m.PID
	}

	idx := next.allocTransaction(trans)
	next.DirStageBuf.PushTyped(idx)

	tracing.AddMilestone(p.cache.comp, tracing.Milestone{
		TaskID: tracing.MsgIDAtIncomingBuffer(msg, p.cache.comp),
		Kind:   tracing.MilestoneKindHardwareResource,
		What:   p.cache.comp.Name() + ".dir_stage_buf",
	})

	tracing.TraceReqReceive(p.cache.comp, msg)

	p.cache.topPort().RetrieveIncoming()

	if readReq, ok := msg.(memprotocol.ReadReq); ok &&
		p.cache.comp.Spec().PrefetcherEnabled {
		if pf := p.cache.comp.Resources().PrefetchUnit; pf != nil {
			pf.Inspect(&readReq)
			p.enqueuePrefetches(
				pf.GetPrefetchAddresses(), readReq.PID, readReq.AccessByteSize)
		}
	}

	return true
}

// enqueuePrefetches appends newly predicted addresses to the low-priority
// queue. It never pushes into DirStageBuf directly — only
// drainPendingPrefetches does that — so a full buffer never causes a
// predicted address to be silently lost. If the queue is already at
// capacity, new addresses are dropped (not the older, already-queued ones,
// since those represent work already committed to and closer to being
// useful).
func (p *topParser) enqueuePrefetches(addrs []uint64, pid vm.PID, accessSize uint64) {
	next := &p.cache.comp.State
	spec := p.cache.comp.Spec()

	capacity := spec.PrefetchQueueCapacity
	if capacity <= 0 {
		capacity = 8
	}

	for _, addr := range addrs {
		if len(next.PendingPrefetches) >= capacity {
			break
		}
		next.PendingPrefetches = append(next.PendingPrefetches, pendingPrefetch{
			Addr:       addr,
			PID:        pid,
			AccessSize: accessSize,
		})
	}
}

// drainPendingPrefetches pushes as many queued prefetches as DirStageBuf has
// room for, oldest first. It runs after tickDemand in the same Tick, so it
// only ever consumes capacity a demand request did not need this cycle.
func (p *topParser) drainPendingPrefetches() bool {
	next := &p.cache.comp.State
	progress := false

	for len(next.PendingPrefetches) > 0 {
		if !next.DirStageBuf.CanPush() {
			break
		}

		pf := next.PendingPrefetches[0]
		next.PendingPrefetches = next.PendingPrefetches[1:]

		trans := transactionState{
			ID:                 timing.GetIDGenerator().Generate(),
			HasRead:            true,
			ReadAddress:        pf.Addr,
			ReadAccessByteSize: pf.AccessSize,
			ReadPID:            pf.PID,
			IsPrefetch:         true,
		}

		idx := next.allocTransaction(trans)
		next.DirStageBuf.PushTyped(idx)
		next.StatPrefetchRequests++

		progress = true
	}

	return progress
}
