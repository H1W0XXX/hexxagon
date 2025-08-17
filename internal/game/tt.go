// internal/game/tt.go
package game

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// ------------------------------------------------------------
//  Zobrist 随机键（预生成 + 零锁查询）
// ------------------------------------------------------------

// 如果你的棋盘最大半径固定，填在这里；否则可暴露为变量或在 initZobrist 里计算。
const maxRadius = 3 // ★根据实际棋盘大小调整

var (
	zobristCell     [][4]uint64      // 下标 → 4 个状态随机数
	hexCoordToIndex map[HexCoord]int // 坐标 → 下标
	onceZobristInit sync.Once
)

// side-to-move Zobrist keys: index 0 = PlayerA, index 1 = PlayerB
var zobristSide [2]uint64

// init 在程序启动时执行一次，生成所有随机键。
func init() {
	initZobrist()
}

// initZobrist 预生成 maxRadius 棋盘内所有格子的 Zobrist 键。
func initZobrist() {
	onceZobristInit.Do(func() {
		// 1) Seed the RNG for reproducible randomness
		rand.Seed(time.Now().UnixNano())

		// 2) Build per-cell Zobrist keys
		coords := AllCoords(maxRadius)
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

// zobristKey 直接数组查表，0 锁、0 原子操作。
func zobristKey(c HexCoord, s CellState) uint64 {
	return zobristCell[hexCoordToIndex[c]][s]
}

// hashBoard 计算整盘哈希（全盘 XOR）。
func hashBoard(b *Board) uint64 {
	var h uint64
	for coord, state := range b.cells {
		if state != Empty {
			h ^= zobristKey(coord, state)
		}
	}
	return h
}

// ------------------------------------------------------------
//  置换表（Transposition Table）
// ------------------------------------------------------------

const ttSize = 1 << 23 // 4 M entries ≈ 200 MB
const ttMask = ttSize - 1

type ttFlag uint8

const (
	ttExact ttFlag = iota
	ttLower
	ttUpper
)

type ttEntry struct {
	key     uint64 // 哈希
	score   int32  // αβ 分值
	depth   int16  // 深度
	flag    ttFlag // 界类型
	bestIdx uint8  // 根节点最佳着（可选）
}

var (
	ttProbeCount uint64 // 总 probe 次数
	ttHitCount   uint64 // 命中次数
)

var (
	ttTable = make([]ttEntry, ttSize) // 切片比 map 更快
	ttMu    [256]sync.Mutex           // 分片锁（若需并发写更安全，可选）
)

func lockFor(hash uint64) *sync.Mutex { return &ttMu[hash&255] }

// probeTT 只做累加，不打印
func probeTT(hash uint64, depth int) (bool, int, ttFlag) {
	ttProbeCount++
	e := ttTable[hash&ttMask]
	if e.key == hash && int(e.depth) >= depth {
		ttHitCount++
		return true, int(e.score), e.flag
	}
	return false, 0, 0
}

// storeTT - 写回置换表；以“深度更深者优先”策略覆盖。
func storeTT(hash uint64, depth, score int, flag ttFlag) {
	idx := hash & ttMask
	if int(ttTable[idx].depth) <= depth {
		ttTable[idx] = ttEntry{
			key:   hash,
			score: int32(score),
			depth: int16(depth),
			flag:  flag,
		}
	}
}

func probeBestIdx(hash uint64) (bool, uint8) {
	e := ttTable[hash&ttMask]
	if e.key == hash {
		return true, e.bestIdx
	}
	return false, 0
}

func storeBestIdx(hash uint64, idx uint8) {
	e := &ttTable[hash&ttMask]
	if e.key == hash { // 仅写同槽
		e.bestIdx = idx
	}
}

// 调用结束后，打印或获取命中率
func GetTTStats() (probes, hits uint64, hitRate float64) {
	probes = atomic.LoadUint64(&ttProbeCount)
	hits = atomic.LoadUint64(&ttHitCount)
	if probes == 0 {
		hitRate = 0
	} else {
		hitRate = float64(hits) / float64(probes) * 100
	}
	return
}

// 例如在搜索结束后调用：
func PrintTTStats() {
	probes, hits, rate := GetTTStats()
	fmt.Printf("TT probes: %d, hits: %d, hit rate: %.2f%%\n", probes, hits, rate)
}

// RunSearch 是你最外层启动搜索的函数（改成你自己的名字）
func RunSearch(b *Board, player CellState, depth int) int {
	// 重置计数
	ttProbeCount = 0
	ttHitCount = 0

	// 调用已有的 DeepSearch（你原来是：alphaBeta(b, b.hash, side, side,...)）
	score := DeepSearch(b, b.hash, player, depth)

	// 只在这里打印一次
	probes, hits, rate := GetTTStats()
	fmt.Printf("TT probes: %d, hits: %d, hit rate: %.2f%%\n",
		probes, hits, rate)

	return score
}

func sideIdx(p CellState) int {
	if p == PlayerB {
		return 1
	}
	return 0
}
