package writeback

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v5/mem/cache"
	"github.com/sarchlab/akita/v5/mem/vm"
	"github.com/sarchlab/akita/v5/modeling"
	"github.com/sarchlab/akita/v5/queueing"
	"github.com/sarchlab/akita/v5/timing"
)

var _ = Describe("Prefetch Integration", func() {
	var (
		ds *directoryStage
		m  *pipelineMW
	)

	BeforeEach(func() {
		initialState := State{
			CacheState:   int(cacheStateRunning),
			EvictingList: make(map[uint64]bool),
			DirStageBuf:  queueing.NewBuffer[int]("Cache.DirStageBuf", 4),
			DirToBankBufs: []queueing.Buffer[int]{
				queueing.NewBuffer[int]("Cache.DirToBankBuf", 4),
			},
			WriteBufferToBankBufs: []queueing.Buffer[int]{
				queueing.NewBuffer[int]("Cache.WBToBankBuf", 4),
			},
			MSHRStageBuf:       queueing.NewBuffer[int]("Cache.MSHRStageBuf", 4),
			WriteBufferBuf:     queueing.NewBuffer[int]("Cache.WriteBufferBuf", 4),
			DirPipeline:        queueing.NewPipeline[int](4, 0),
			DirPostPipelineBuf: queueing.NewBuffer[int]("Cache.DirPostBuf", 4),
			BankPipelines: []queueing.Pipeline[int]{
				queueing.NewPipeline[int](4, 10),
			},
			BankPostPipelineBufs: []postPipelineBuf{
				newPostPipelineBuf(4),
			},
			BankInflightTransCounts:         []int{0},
			BankDownwardInflightTransCounts: []int{0},
		}

		m = &pipelineMW{}
		m.comp = modeling.NewBuilder[Spec, State, Resources]().
			WithEngine(nil).
			WithFreq(1 * timing.GHz).
			WithSpec(Spec{
				Log2BlockSize:    6,
				NumReqPerCycle:   4,
				WayAssociativity: 4,
				NumMSHREntry:     16,
				NumSets:          64,
				NumBanks:         1,
			}).
			Build("Cache")

		m.comp.State = initialState
		next := &m.comp.State
		cache.DirectoryReset(&next.DirectoryState, 64, 4, 64)

		ds = &directoryStage{cache: m}
		m.dirStage = ds
	})

	// --- مورد ۳: Demand بعد از این‌که Prefetch بلاک را نصب کرد ---
	Context("demand read after a prefetch already installed the block", func() {
		BeforeEach(func() {
			next := &m.comp.State
			setID := cache.DirectorySetID(0x100, 64, 64)
			block := &next.DirectoryState.Sets[setID].Blocks[0]
			block.Tag = 0x100
			block.PID = 0
			block.IsValid = true

			trans := transactionState{
				HasRead:            true,
				ReadAddress:        0x100,
				ReadAccessByteSize: 4,
				ReadPID:            0,
				IsPrefetch:         false,
			}
			next.Transactions = []transactionState{trans}
			next.DirPostPipelineBuf.Clear()
			next.DirPostPipelineBuf.PushTyped(0)
		})

		It("should hit and NOT create a new MSHR entry", func() {
			ret := ds.Tick()

			Expect(ret).To(BeTrue())
			next := &m.comp.State
			Expect(next.Transactions[0].Action).To(Equal(bankReadHit))

			Expect(next.MSHRState.Entries).To(HaveLen(0))
		})
	})

	// --- مورد ۴: Demand هنگامی که Prefetch هنوز در MSHR است ---
	Context("demand read while a prefetch for the same block is in-flight", func() {
		BeforeEach(func() {
			prefetchTrans := transactionState{
				HasRead:            true,
				ReadAddress:        0x100,
				ReadAccessByteSize: 4,
				ReadPID:            0,
				IsPrefetch:         true,
			}
			next := &m.comp.State
			next.Transactions = []transactionState{prefetchTrans}
			next.DirPostPipelineBuf.Clear()
			next.DirPostPipelineBuf.PushTyped(0)
		})

		It("should create exactly one MSHR entry for the prefetch's fetch", func() {
			ret := ds.Tick()

			Expect(ret).To(BeTrue())
			next := &m.comp.State
			Expect(next.MSHRState.Entries).To(HaveLen(1))
			Expect(next.MSHRState.Entries[0].TransactionIndices).To(HaveLen(1))
		})

		It("should coalesce a later demand for the same block onto that entry", func() {

			ds.Tick()

			next := &m.comp.State
			demandTrans := transactionState{
				HasRead:            true,
				ReadAddress:        0x100,
				ReadAccessByteSize: 4,
				ReadPID:            0,
				IsPrefetch:         false,
			}
			next.Transactions = append(next.Transactions, demandTrans)
			next.DirPostPipelineBuf.Clear()
			next.DirPostPipelineBuf.PushTyped(1)

			ret := ds.Tick()

			Expect(ret).To(BeTrue())
			next = &m.comp.State

			Expect(next.MSHRState.Entries).To(HaveLen(1))

			Expect(next.MSHRState.Entries[0].TransactionIndices).To(HaveLen(2))
		})
	})
})

// --- مورد ۵: صف کم‌اولویت وقتی DirStageBuf پر است ---
var _ = Describe("Prefetch Queue Backpressure", func() {
	var (
		m      *pipelineMW
		parser *topParser
	)

	BeforeEach(func() {
		initialState := State{
			CacheState:   int(cacheStateRunning),
			EvictingList: make(map[uint64]bool),

			DirStageBuf: queueing.NewBuffer[int]("Cache.DirStageBuf", 1),
		}

		m = &pipelineMW{}
		m.comp = modeling.NewBuilder[Spec, State, Resources]().
			WithEngine(nil).
			WithFreq(1 * timing.GHz).
			WithSpec(Spec{
				NumReqPerCycle:        1,
				PrefetchQueueCapacity: 4,
			}).
			Build("Cache")

		m.comp.State = initialState
		parser = &topParser{cache: m}
	})

	It("should queue prefetches instead of dropping them when the buffer is full", func() {
		next := &m.comp.State

		dummyTrans := transactionState{HasRead: true}
		idx := next.allocTransaction(dummyTrans)
		next.DirStageBuf.PushTyped(idx)
		Expect(next.DirStageBuf.CanPush()).To(BeFalse())

		parser.enqueuePrefetches([]uint64{0x200, 0x300, 0x400}, vm.PID(0), 4)

		next = &m.comp.State
		Expect(next.PendingPrefetches).To(HaveLen(3))

		next.DirStageBuf.Pop()
		Expect(next.DirStageBuf.CanPush()).To(BeTrue())

		progress := parser.drainPendingPrefetches()

		Expect(progress).To(BeTrue())
		next = &m.comp.State

		Expect(next.PendingPrefetches).To(HaveLen(2))
		Expect(next.StatPrefetchRequests).To(Equal(uint64(1)))
	})
})
