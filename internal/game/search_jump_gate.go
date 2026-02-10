package game

func filterJumpsByFlag(b *Board, side CellState, moves []Move, allowJump bool) []Move {
	if allowJump {
		return moves
	}
	n := 0
	originalCount := len(moves)
	for i := 0; i < originalCount; i++ {
		m := moves[i]
		if m.IsClone() {
			moves[n] = m
			n++
		}
	}
	if n > 0 {
		return moves[:n]
	}
	// 极端局面只有跳越可走时，兜底不拦（否则会卡死）
	return moves[:originalCount]
}
