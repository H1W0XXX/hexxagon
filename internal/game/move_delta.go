package game

// 1) 记录被改动的格子 (最多 7: 起点/终点 + 感染 6)
type undoCell struct {
	coord HexCoord
	prev  CellState
}
type undoInfo struct {
	changed []undoCell
}

// MakeMove 在原盘执行走子，返回 (感染数, undoInfo)
func (m Move) MakeMove(b *Board, player CellState) (infectedCoords []HexCoord, undo undoInfo) {

	b.LastMove = m
	// 预分配一个足够装下所有可能被感染的 slice
	infectedCoords = make([]HexCoord, 0, 6)
	undo.changed = make([]undoCell, 0, 8)

	// 内部 helper：写格子并记 prev
	set := func(c HexCoord, s CellState) {
		prev := b.cells[c]
		if prev == s {
			return
		}
		undo.changed = append(undo.changed, undoCell{c, prev})
		b.set(c, s)
	}

	// --- 1. 起点 => 空（跳跃需要，克隆不变） ---
	if m.IsJump() {
		set(m.From, Empty)
	}

	// --- 2. 终点放我方 ---
	set(m.To, player)

	// --- 3. 感染邻格，并记录坐标 ---
	for _, n := range b.Neighbors(m.To) {
		if b.Get(n) == Opponent(player) {
			set(n, player)
			infectedCoords = append(infectedCoords, n)
		}
	}

	return infectedCoords, undo
}

// UnmakeMove 按相反顺序恢复格子 & hash
func (b *Board) UnmakeMove(u undoInfo) {
	// 逆序回滚
	for i := len(u.changed) - 1; i >= 0; i-- {
		c := u.changed[i]
		b.set(c.coord, c.prev)
	}
}
