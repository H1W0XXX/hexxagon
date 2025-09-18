// file: internal/game/evaluate_bitboard.go
package game

import (
	"math/bits"
	"sync"
)

// ---- 位板预计算缓存 ----

type BitBoardCache struct {
	edgeMask      uint64         // 外圈位掩码
	neighMask     [BoardN]uint64 // 每格 6 邻居的汇总掩码
	indexBit      [BoardN]uint64 // 1<<i 快速表
	tightTriMasks []uint64       // 所有“紧三角”三元组的掩码（去重后）
}

var (
	bbCache  BitBoardCache
	initOnce sync.Once
)

func ensurePrecomp() {
	initOnce.Do(func() {
		if BoardN > 64 {
			panic("bitboard impl assumes BoardN <= 64 (R=4 -> 61)")
		}

		// indexBit
		for i := 0; i < BoardN; i++ {
			bbCache.indexBit[i] = 1 << uint(i)
		}

		// 外圈掩码
		for i := 0; i < BoardN; i++ {
			if isOuterI[i] {
				bbCache.edgeMask |= bbCache.indexBit[i]
			}
		}

		// 邻居掩码
		for i := 0; i < BoardN; i++ {
			var m uint64
			for _, nb := range NeighI[i] {
				m |= bbCache.indexBit[nb]
			}
			bbCache.neighMask[i] = m
		}

		// 紧三角：任意三点两两相邻（去重）
		seen := make(map[uint64]struct{}, 256)
		for a := 0; a < BoardN; a++ {
			for _, b := range NeighI[a] {
				if b <= a {
					continue
				}
				for _, c := range NeighI[a] {
					if c <= b || c == a {
						continue
					}
					if isNeighborI(b, c) {
						mask := bbCache.indexBit[a] | bbCache.indexBit[b] | bbCache.indexBit[c]
						if _, ok := seen[mask]; !ok {
							seen[mask] = struct{}{}
							bbCache.tightTriMasks = append(bbCache.tightTriMasks, mask)
						}
					}
				}
			}
		}
	})
}

// ---- 位板工具 ----

func boardMasks(b *Board, player CellState) (my, op uint64) {
	opp := Opponent(player)
	for i := 0; i < BoardN; i++ {
		switch b.Cells[i] {
		case player:
			my |= bbCache.indexBit[i]
		case opp:
			op |= bbCache.indexBit[i]
		}
	}
	return
}

func floodComponent(seed, mask uint64) uint64 {
	comp := seed
	frontier := seed

	for frontier != 0 {
		// 批处理 frontier 的所有邻居
		var nbAll uint64
		f := frontier
		for f != 0 {
			lsb := f & -f
			idx := bits.TrailingZeros64(lsb)
			nbAll |= bbCache.neighMask[idx]
			f &= f - 1 // 清最低位
		}

		next := (nbAll & mask) &^ comp
		if next == 0 {
			break
		}
		comp |= next
		frontier = next
	}
	return comp
}

func componentHasTightTriangle(comp uint64) bool {
	// 快速剪枝：少于 3 个子不可能
	if bits.OnesCount64(comp) < 3 {
		return false
	}
	for _, tri := range bbCache.tightTriMasks {
		if comp&tri == tri {
			return true
		}
	}
	return false
}

func countTriangleBlocksBB(mask uint64) int {
	count := 0
	remain := mask
	for remain != 0 {
		seed := remain & -remain
		comp := floodComponent(seed, mask)
		if componentHasTightTriangle(comp) {
			count++
		}
		remain &^= comp // 去掉已处理分量
	}
	return count
}

// ---- 对外评估（位板实现）----

func EvaluateBitBoard(b *Board, player CellState) int {
	ensurePrecomp()

	my, op := boardMasks(b, player)

	pieceScore := (bits.OnesCount64(my) - bits.OnesCount64(op)) * pieceW
	edgeScore := (bits.OnesCount64(my&bbCache.edgeMask) - bits.OnesCount64(op&bbCache.edgeMask)) * edgeW

	myTri := countTriangleBlocksBB(my)
	opTri := countTriangleBlocksBB(op)
	triangleScore := (myTri - opTri) * triW

	return pieceScore + edgeScore + triangleScore
}

// ---- 兼容旧入口：直接走位板版 ----

func Evaluate(b *Board, player CellState) int {
	return EvaluateBitBoard(b, player)
}
