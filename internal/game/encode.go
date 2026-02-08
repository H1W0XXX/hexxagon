// internal/game/encode.go
package game

const (
	GridSize  = 9 // 把 (-4..4, -4..4) 映射到 9×9
	PlaneCnt  = 3 // [我方, 对方, Blocked]
	TensorLen = PlaneCnt * GridSize * GridSize
)

var (
	// 预计算表
	boardIndexToGrid [BoardN]int               // 37 -> 0..80
	gridInBoard      [GridSize * GridSize]bool // 81 -> in radius-3?
	gridAxial        [GridSize * GridSize]HexCoord
	encodeTablesInit bool
)

// 在 initBoardTables() 之后调用一次
func initEncodeTables() {
	// 1) 9×9 网格 → 轴坐标，并标注是否在半径4棋盘内
	idx := 0
	for y := 0; y < GridSize; y++ {
		for x := 0; x < GridSize; x++ {
			q := x - 4
			r := y - 4
			c := HexCoord{Q: q, R: r}
			gridAxial[idx] = c
			// 边长为5，意味着半径为4。判断标准：|q|<=4, |r|<=4, |q+r|<=4
			in := abs(q) <= 4 && abs(r) <= 4 && abs(-q-r) <= 4
			gridInBoard[idx] = in
			idx++
		}
	}
	// 2) 棋盘下标 -> 网格下标
	for i := 0; i < BoardN; i++ {
		c := CoordOf[i] 
		x := c.Q + 4
		r := c.R + 4
		g := r*GridSize + x
		boardIndexToGrid[i] = g
	}
	encodeTablesInit = true
}

// EncodeBoardTensor 把棋盘即时编码成 [243]float32 张量
// plane 0: 我方, plane 1: 对方, plane 2: Blocked(非棋盘区域)
func EncodeBoardTensor(b *Board, me CellState) [TensorLen]float32 {
	if !encodeTablesInit {
		// 防御：确保预表已初始化（正常应在程序启动时就调用 initEncodeTables）
		initEncodeTables()
	}

	var t [TensorLen]float32
	const plane = GridSize * GridSize

	// 先把非棋盘区域标记到 Blocked 平面
	for g := 0; g < GridSize*GridSize; g++ {
		if !gridInBoard[g] {
			t[2*plane+g] = 1
		}
	}

	opp := Opponent(me)

	// 遍历棋盘 37 格：把棋子映射到对应网格位
	for i := 0; i < BoardN; i++ {
		s := b.Cells[i]
		if s == Empty {
			continue // 空格子：三平面都 0
		}
		g := boardIndexToGrid[i]

		// 在棋盘内的格子不是 Blocked
		// 非棋盘的 Blocked 已在上面设置；棋盘内默认是 0
		// 只需根据棋子设置我方/对方平面：
		switch s {
		case me:
			t[g] = 1 // plane 0
		case opp:
			t[plane+g] = 1 // plane 1
		case Blocked:
			// 如果你的棋盘内部不会出现 Blocked，可忽略
			t[2*plane+g] = 1
		}
	}
	return t
}

// AxialToIndex 把落子坐标映射到 0..80 的 move 索引
// 仍然保留直接计算，或用 gridAxial 反查也行
func AxialToIndex(c HexCoord) int {
	return (c.R+4)*GridSize + (c.Q + 4)
}
