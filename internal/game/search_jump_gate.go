package game

func filterJumpsByFlag(b *Board, side CellState, moves []Move, allowJump bool) []Move {
	if allowJump {
		return moves
	}
	keep := make([]Move, 0, len(moves))
	for _, m := range moves {
		if m.IsClone() {
			keep = append(keep, m)
		}
	}
	if len(keep) > 0 {
		return keep
	}
	// 极端局面只有跳越可走时，兜底不拦（否则会卡死）
	return moves
}
