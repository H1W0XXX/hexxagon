package game

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// -------- 参数：按需调大 --------
const ttBuckets = 1 << 21 // 桶数量（2M 桶）
const ttWays = 4          // 组相联路数：2 或 4
const ttMask = ttBuckets - 1

type ttFlag uint8

const (
	ttExact ttFlag = iota
	ttLower
	ttUpper
)

type ttEntry struct {
	// seqlock：偶数=稳定，奇数=写入中
	version uint32  // 原子读写
	score   int32   // 分值
	depth   int32   // 搜索深度
	flag    ttFlag  // 类型
	bestIdx uint8   // 走法索引（可选）
	key     uint64  // 原子发布（最后写）
	_       [8]byte // 简单填充，减小伪共享（可按需调到 64B）
}

var zobristSide [2]uint64
var (
	ttTable         = make([][ttWays]ttEntry, ttBuckets)
	ttProbeCount    uint64
	ttHitCount      uint64
	onceZobristInit sync.Once
)
var (
	zobristCell     [][4]uint64
	hexCoordToIndex map[HexCoord]int
)

var zobCell [BoardN][4]uint64           // [index][state]
func zobKeyI(i int, s CellState) uint64 { return zobristCell[i][s] }

var ttSalt uint64 // 与 zobrist/side xor 组成最终 key
// init 在程序启动时执行一次，生成所有随机键。
func init() {
	initBoardTables()
	initZobrist()
	initEncodeTables()
	// 初始化一个随机盐，避免进程内碰撞
	atomic.StoreUint64(&ttSalt, rand.Uint64()|1) // 确保非零
}
func initZobrist() {
	onceZobristInit.Do(func() {
		// 1) Seed the RNG for reproducible randomness
		rand.Seed(time.Now().UnixNano())

		// 2) Build per-cell Zobrist keys
		coords := AllCoords(boardRadius)
		zobristCell = make([][4]uint64, len(coords))
		hexCoordToIndex = make(map[HexCoord]int, len(coords))
		for i, c := range coords {
			hexCoordToIndex[c] = i
			zobristCell[i] = [4]uint64{
				rand.Uint64(), // Empty
				0,             // Blocked (never participates)
				rand.Uint64(), // PlayerA
				rand.Uint64(), // PlayerB
			}
		}

		// 3) Build side-to-move Zobrist keys
		zobristSide[0] = rand.Uint64() // PlayerA to move
		zobristSide[1] = rand.Uint64() // PlayerB to move
	})
}

func ttKeyFor(b *Board, current CellState) uint64 {
	return b.hash ^ zobristSide[sideIdx(current)] ^ atomic.LoadUint64(&ttSalt)
}

func ClearTT() {
	// 换个盐：让所有旧 key 立刻无法命中
	atomic.AddUint64(&ttSalt, 1)
	// 统计计数也一起清零
	atomic.StoreUint64(&ttProbeCount, 0)
	atomic.StoreUint64(&ttHitCount, 0)
}

// 读：循环直到拿到稳定快照（version 偶数且前后一致）
func probeTT(key uint64, needDepth int) (bool, int, ttFlag) {
	atomic.AddUint64(&ttProbeCount, 1)
	b := &ttTable[key&ttMask]

	for w := 0; w < ttWays; w++ {
		e := &b[w]
		for {
			v1 := atomic.LoadUint32(&e.version)
			if v1&1 == 1 { // 正在写
				// 退一步读其他路
				break
			}
			k := atomic.LoadUint64(&e.key)
			if k != key {
				break
			}
			// 快照字段
			score := atomic.LoadInt32(&e.score)
			depth := atomic.LoadInt32(&e.depth)
			flag := e.flag // 非原子也行

			v2 := atomic.LoadUint32(&e.version)
			if v1 == v2 && v2&1 == 0 { // 稳定
				if int(depth) >= needDepth {
					atomic.AddUint64(&ttHitCount, 1)
					return true, int(score), flag
				}
				break
			}
			// 版本变化，重试这一路
		}
	}
	return false, 0, 0
}

// 写：优先覆盖同 key；否则覆盖“更浅深度”的槽；再不行覆盖 0 号
func storeTT(key uint64, depth, score int, flag ttFlag) {
	b := &ttTable[key&ttMask]

	// 1) 找到要写的路
	slot := 0
	bestDepth := int(^uint(0) >> 1) // +Inf
	for w := 0; w < ttWays; w++ {
		e := &b[w]
		if atomic.LoadUint64(&e.key) == key {
			slot = w
			break
		}
		d := int(atomic.LoadInt32(&e.depth))
		if d < bestDepth {
			bestDepth = d
			slot = w
		}
	}

	e := &b[slot]
	// 2) seqlock: version++(odd) → 写字段 → 写 key → version++(even)
	v := atomic.AddUint32(&e.version, 1) // 变奇数
	_ = v

	atomic.StoreInt32(&e.score, int32(score))
	atomic.StoreInt32(&e.depth, int32(depth))
	e.flag = flag // 非原子 OK
	// bestIdx 留给 storeBestIdx 来写或置 0
	atomic.StoreUint64(&e.key, key)

	atomic.AddUint32(&e.version, 1) // 变回偶数，发布完成
}

func probeBestIdx(key uint64) (bool, uint8) {
	b := &ttTable[key&ttMask]
	for w := 0; w < ttWays; w++ {
		e := &b[w]
		for {
			v1 := atomic.LoadUint32(&e.version)
			if v1&1 == 1 {
				break
			}
			if atomic.LoadUint64(&e.key) != key {
				break
			}
			idx := e.bestIdx
			v2 := atomic.LoadUint32(&e.version)
			if v1 == v2 && v2&1 == 0 {
				return true, idx
			}
		}
	}
	return false, 0
}

func storeBestIdx(key uint64, idxBest uint8) {
	b := &ttTable[key&ttMask]
	for w := 0; w < ttWays; w++ {
		e := &b[w]
		if atomic.LoadUint64(&e.key) == key {
			// 小字段非原子写即可；读侧有 seqlock 保护
			e.bestIdx = idxBest
			return
		}
	}
}

func GetTTStats() (probes, hits uint64, rate float64) {
	probes = atomic.LoadUint64(&ttProbeCount)
	hits = atomic.LoadUint64(&ttHitCount)
	if probes > 0 {
		rate = float64(hits) / float64(probes) * 100
	}
	return
}

func sideIdx(p CellState) int {
	if p == PlayerB {
		return 1
	}
	return 0
}

func zobristKey(c HexCoord, s CellState) uint64 {
	return zobristCell[hexCoordToIndex[c]][s]
}
