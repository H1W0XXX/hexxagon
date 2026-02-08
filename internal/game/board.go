// File game/board.go
package game

import (
	"sync"
)

// CellState represents the state of a cell on the board.
// It can be Empty, Blocked, or occupied by PlayerA or PlayerB.
type CellState int

const (
	Empty CellState = iota
	Blocked
	PlayerA
	PlayerB
)

// HexCoord represents an axial hex coordinate (q, r).
type HexCoord struct {
	Q, R int
}

// Directions defines the 6 neighbor offsets in axial coordinates.
var Directions = []HexCoord{
	{1, 0}, {1, -1}, {0, -1},
	{-1, 0}, {-1, 1}, {0, 1},
}

const boardRadius = 4
const BoardN = 1 + 3*boardRadius*(boardRadius+1) // 预先按 AllCoords(3) 的顺序编号
// Board represents a hexagonal board of a given radius.
// Coordinates satisfying |q| <= radius, |r| <= radius, |q+r| <= radius are valid.
type Board struct {
	radius     int
	Cells      [BoardN]CellState // 定长数组
	hash       uint64
	LastMove   Move
	LastMover  CellState
	LastInfect int
}

var (
	CoordOf [BoardN]HexCoord // index -> 坐标
	IndexOf map[HexCoord]int // 坐标 -> index（仅入口/出口处用）
	NeighI  [BoardN][]int    // 每个格子的 6 邻居下标
	JumpI   [BoardN][]int    // 每个格子的跳跃可达下标（两格）
	Coords  [BoardN]HexCoord
)

var boardPool = sync.Pool{
	New: func() any {
		return &Board{}
	},
}

var coordsCache = map[int][]HexCoord{} // 支持多半径
var isOuterI [BoardN]bool

func init() {
	IndexOf = make(map[HexCoord]int, BoardN)
	i := 0
	for q := -boardRadius; q <= boardRadius; q++ {
		for r := -boardRadius; r <= boardRadius; r++ {
			if abs(q)+abs(r)+abs(-q-r) <= 2*boardRadius {
				c := HexCoord{q, r}
				Coords[i] = c
				IndexOf[c] = i
				i++
			}
		}
	}
}
func initBoardTables() {
	coords := AllCoords(boardRadius)
	if len(coords) != BoardN {
		// 保险：避免坐标枚举顺序变化导致 out-of-range
		panic("AllCoords(boardRadius) size mismatch")
	}
	IndexOf = make(map[HexCoord]int, BoardN)
	for i, c := range coords {
		CoordOf[i] = c
		IndexOf[c] = i
	}
	// 预计算邻居表
	for i, c := range coords {

		CoordOf[i] = c
		IndexOf[c] = i
		// 半径边界上的点就是外圈
		if abs(c.Q) == boardRadius || abs(c.R) == boardRadius || abs(-c.Q-c.R) == boardRadius {
			isOuterI[i] = true
		}

		for _, d := range Directions {
			n := HexCoord{c.Q + d.Q, c.R + d.R}
			if j, ok := IndexOf[n]; ok {
				NeighI[i] = append(NeighI[i], j)
			}
		}
		// 预计算跳跃：12 个方向（= 两步）
		for _, d := range jumpDirs { // 你已有 jumpDirs
			j := HexCoord{c.Q + d.Q, c.R + d.R}
			if k, ok := IndexOf[j]; ok {
				JumpI[i] = append(JumpI[i], k)
			}
		}
	}
}
func AllCoords(radius int) []HexCoord {
	if radius != boardRadius {
		panic("unsupported radius")
	}
	return Coords[:]
}

func acquireBoard(radius int) *Board {
	b := boardPool.Get().(*Board)
	b.radius = radius
	// 清空棋盘 & hash
	for i := 0; i < BoardN; i++ {
		b.Cells[i] = Empty
	}
	b.hash = 0
	b.LastMove = Move{}
	b.LastMover = Empty
	b.LastInfect = 0
	return b
}
func releaseBoard(b *Board) {
	boardPool.Put(b)
}

func (b *Board) set(c HexCoord, s CellState) {
	i, ok := IndexOf[c]
	if !ok {
		return
	}
	b.setI(i, s)
}

// NewBoard creates and initializes a new board with the given radius.
func NewBoard(radius int) *Board {
	if radius != boardRadius {
		panic("NewBoard: radius must match boardRadius (4)")
	}
	b := &Board{radius: radius}
	for i := 0; i < BoardN; i++ {
		b.Cells[i] = Empty
	}
	return b
}

// InBounds returns true if coord c is within the board's radius.
func (b *Board) InBounds(c HexCoord) bool {
	if abs(c.Q) > b.radius || abs(c.R) > b.radius || abs(-c.Q-c.R) > b.radius {
		return false
	}
	return true
}

// Get returns the cell state at coord c. If out of bounds, returns Blocked.
func (b *Board) GetI(i int) CellState { return b.Cells[i] }

func (b *Board) setI(i int, s CellState) {
	prev := b.Cells[i]
	if prev == s {
		return
	}
	b.hash ^= zobKeyI(i, prev)
	b.Cells[i] = s
	b.hash ^= zobKeyI(i, s)
}

// Neighbors returns all in-bounds neighbor coordinates of c.
func (b *Board) Neighbors(c HexCoord) []HexCoord {
	var result []HexCoord
	for _, d := range Directions {
		n := HexCoord{c.Q + d.Q, c.R + d.R}
		if b.InBounds(n) {
			result = append(result, n)
		}
	}
	return result
}

func (b *Board) AllCoords() []HexCoord {
	return AllCoords(b.radius)
}

// AllCoords returns a slice of all coordinates on the board.
//func (b *Board) AllCoords() []HexCoord {
//	coords := make([]HexCoord, 0, len(b.cells))
//	for c := range b.cells {
//		coords = append(coords, c)
//	}
//	return coords
//}

// abs returns the absolute value of x.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (b *Board) Clone() *Board {
	nb := acquireBoard(b.radius)
	for coord, state := range b.Cells {
		nb.Cells[coord] = state
	}
	nb.hash = b.hash
	nb.LastMove = b.LastMove

	nb.LastMover = b.LastMover
	nb.LastInfect = b.LastInfect
	return nb
}

func (b *Board) applyMove(m Move, player CellState) (infected int, undo func()) {
	opp := Opponent(player)

	// 将坐标映射为下标（board 初始化时已填好 indexOf）
	from, okFrom := IndexOf[m.From]
	to, okTo := IndexOf[m.To]
	if !okTo {
		// 非法坐标；对于已合法化的走法生成器，这里理论上不会触发
		return 0, func() {}
	}

	// 记录被修改过的格子（用于回溯）；容量估计：跳跃最多改 1(from) + 1(to) + 6(邻居) ≈ 8
	type change struct {
		i    int
		prev CellState
	}
	changed := make([]change, 0, 8)

	// 带记录的 set（维护 hash）
	setI := func(i int, s CellState) {
		prev := b.Cells[i]
		if prev == s {
			return
		}
		// 增量维护 zobrist
		b.hash ^= zobKeyI(i, prev)
		b.Cells[i] = s
		b.hash ^= zobKeyI(i, s)

		changed = append(changed, change{i: i, prev: prev})
	}

	// —— 执行克隆 / 跳跃 —— //
	if m.IsClone() {
		// 克隆：仅在 to 放一个自己的子
		setI(to, player)
	} else {
		// 跳跃：from 清空、to 放子
		if okFrom {
			setI(from, Empty)
		}
		setI(to, player)
	}

	// —— 邻居感染：把 to 的 6 邻居中属于对手的翻为我方 —— //
	for _, j := range NeighI[to] {
		if b.Cells[j] == opp {
			setI(j, player)
			infected++
		}
	}

	// 撤销函数：按相反顺序恢复所有被改格
	undo = func() {
		for k := len(changed) - 1; k >= 0; k-- {
			c := changed[k]
			// 通过 setI 还原可以再次维护 hash；但 setI 会再次记录 change。
			// 因此这里直接手动还原更省：反异或 + 赋值 + 再异或。
			cur := b.Cells[c.i]
			if cur != c.prev {
				b.hash ^= zobKeyI(c.i, cur)
				b.Cells[c.i] = c.prev
				b.hash ^= zobKeyI(c.i, c.prev)
			}
		}
	}

	return infected, undo
}

// Hash 返回当前局面的 Zobrist 哈希（供置换表/外部工具读取）
func (b *Board) Hash() uint64 {
	return b.hash
}

// CountPieces 统计棋盘上 pl 方棋子数量
func (b *Board) CountPieces(pl CellState) int {
	n := 0
	for i := 0; i < BoardN; i++ { 
		if b.Cells[i] == pl {
			n++
		}
	}
	return n
}

func (b *Board) ToFeatureInto(side CellState, dst []float32) []float32 {
	if cap(dst) < BoardN {
		dst = make([]float32, BoardN)
	} else {
		dst = dst[:BoardN]
	}
	// 可选：不必先清零，因为下面会逐项覆盖
	opp := Opponent(side)
	for i := 0; i < BoardN; i++ {
		switch b.Cells[i] {
		case side:
			dst[i] = 1
		case opp:
			dst[i] = -1
		default:
			dst[i] = 0
		}
	}
	return dst
}

func (b *Board) ApplyMove(m Move, player CellState) {
	infected, _ := b.applyMove(m, player)
	b.LastMove = m
	b.LastMover = player    // 新增
	b.LastInfect = infected // 新增
}
