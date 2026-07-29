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

	// Admission milestone on the incoming-buffer task: the message left the Top
	// buffer because the directory stage buffer had room. The buffer task is
	// keyed by the peeked message and ends at RetrieveIncoming below.
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
			p.issuePrefetches(pf.GetPrefetchAddresses(), readReq.PID, readReq.AccessByteSize)
		}
	}

	return true
}

func (p *topParser) issuePrefetches(addrs []uint64, pid vm.PID, accessSize uint64) {
	next := &p.cache.comp.State

	for _, addr := range addrs {
		if !next.DirStageBuf.CanPush() {
			break
		}

		trans := transactionState{
			ID:                 timing.GetIDGenerator().Generate(),
			HasRead:            true,
			ReadAddress:        addr,
			ReadAccessByteSize: accessSize,
			ReadPID:            pid,
			IsPrefetch:         true,
		}

		idx := next.allocTransaction(trans)
		next.DirStageBuf.PushTyped(idx)
		next.StatPrefetchRequests++
	}
}
