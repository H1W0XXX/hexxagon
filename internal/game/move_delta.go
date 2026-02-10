package game

// 1) 记录被改动的格子 (最多 7: 起点/终点 + 感染 6)
type undoCell struct {
	coord HexCoord
	prev  CellState
	idx   int
}
type undoInfo struct {
	changed        []undoCell
	prevLastMove   Move
	prevLastMover  CellState
	prevLastInfect int
}

// MakeMove 在原盘执行走子，返回 (感染数, undoInfo)
func (m Move) MakeMove(b *Board, player CellState) (infectedCoords []HexCoord, undo undoInfo) {
	b.LastMove = m

	// 预分配
	infectedCoords = make([]HexCoord, 0, 6)
	undo.changed = make([]undoCell, 0, 8)

	// 坐标→下标
	from, okF := IndexOf[m.From]
	to, okT := IndexOf[m.To]
	if !okT || !okF {
		// 非法坐标（对正式走法一般不会发生）
		return infectedCoords, undo
	}

	// 带回溯记录的 setI（维护 zobrist + bitmask）
	setI := func(i int, s CellState) {
		prev := b.Cells[i]
		if prev == s {
			return
		}
		// 记录
		undo.changed = append(undo.changed, undoCell{idx: i, prev: prev})
		// 增量更新 hash
		b.hash ^= zobKeyI(i, prev)
		// 增量更新 bitmask
		mask := uint64(1) << uint(i)
		if prev == PlayerA {
			b.bitA &= ^mask
		} else if prev == PlayerB {
			b.bitB &= ^mask
		}

		b.Cells[i] = s
		b.hash ^= zobKeyI(i, s)
		if s == PlayerA {
			b.bitA |= mask
		} else if s == PlayerB {
			b.bitB |= mask
		}
	}

	// 1) 跳跃则清起点
	if m.IsJump() {
		setI(from, Empty)
	}
	// 2) 落子
	setI(to, player)

	// 3) 感染：把落点的对方相邻翻为我方
	opp := Opponent(player)
	for _, nb := range NeighI[to] {
		if b.Cells[nb] == opp {
			setI(nb, player)
			infectedCoords = append(infectedCoords, CoordOf[nb])
		}
	}

	return infectedCoords, undo
}

// UnmakeMove 按相反顺序恢复格子 & hash & bitmask
func (b *Board) UnmakeMove(u undoInfo) {
	// 先恢复最近一步元信息
	b.LastMove = u.prevLastMove
	b.LastMover = u.prevLastMover
	b.LastInfect = u.prevLastInfect

	// 再倒序回滚所有格子 & hash & bitmask
	for i := len(u.changed) - 1; i >= 0; i-- {
		ch := u.changed[i]
		cur := b.Cells[ch.idx]
		if cur == ch.prev {
			continue
		}
		// 恢复 hash
		b.hash ^= zobKeyI(ch.idx, cur)
		// 恢复 bitmask
		mask := uint64(1) << uint(ch.idx)
		if cur == PlayerA {
			b.bitA &= ^mask
		} else if cur == PlayerB {
			b.bitB &= ^mask
		}

		b.Cells[ch.idx] = ch.prev
		b.hash ^= zobKeyI(ch.idx, ch.prev)
		if ch.prev == PlayerA {
			b.bitA |= mask
		} else if ch.prev == PlayerB {
			b.bitB |= mask
		}
	}
}
