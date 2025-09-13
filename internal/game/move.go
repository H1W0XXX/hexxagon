package game

import "fmt"

// Move 表示一次从 From 到 To 的走子
type Move struct {
	From HexCoord
	To   HexCoord
}

// cloneDirs 定义了 6 个相邻偏移（Distance == 1 的方向）
var cloneDirs = []HexCoord{
	{+1, 0},  // 东
	{+1, -1}, // 东北
	{0, -1},  // 西北
	{-1, 0},  // 西
	{-1, +1}, // 西南
	{0, +1},  // 东南
}

// jumpDirs 定义了 12 个跳跃偏移（Distance == 2 的方向）
// 这些偏移就是所有满足 |dq|+|dr|+|ds|=4 (hex 距离=2) 的组合
var jumpDirs = []HexCoord{
	{+2, 0}, {+2, -1}, {+2, -2},
	{+1, -2}, {0, -2}, {-1, -1},
	{-2, 0}, {-2, +1}, {-2, +2},
	{-1, +2}, {0, +2}, {+1, +1},
}

func max3(a, b, c int) int {
	if a < b {
		a = b
	}
	if a < c {
		a = c
	}
	return a
}
func Opponent(player CellState) CellState {
	switch player {
	case PlayerA:
		return PlayerB
	case PlayerB:
		return PlayerA
	}
	return Empty
}

// ---- 判定函数 ----
//func (m Move) IsClone() bool { return hexDist(m.From, m.To) == 1 }
//func (m Move) IsJump() bool  { return hexDist(m.From, m.To) == 2 }

// IsClone 返回这步是否是复制（复制：落点是距离 1 的相邻格子）
func (m Move) IsClone() bool {
	dq := m.To.Q - m.From.Q
	dr := m.To.R - m.From.R
	for _, d := range cloneDirs {
		if d.Q == dq && d.R == dr {
			return true
		}
	}
	return false
}

// IsJump：只要 (To-From) 等于 jumpDirs 之一，就判定为跳跃
func (m Move) IsJump() bool {
	dq := m.To.Q - m.From.Q
	dr := m.To.R - m.From.R
	for _, d := range jumpDirs {
		if d.Q == dq && d.R == dr {
			return true
		}
	}
	return false
}

func (m Move) IsJumpOld() bool {
	for _, d := range jumpDirs {
		if m.From.Q+d.Q == m.To.Q && m.From.R+d.R == m.To.R {
			return true
		}
	}
	return false
}
func GenerateMoves(b *Board, player CellState) []Move {
	moves := make([]Move, 0, 64) // 预分配

	for i := 0; i < BoardN; i++ {
		if b.Cells[i] != player {
			continue
		}

		// 克隆（距离=1）
		for _, to := range NeighI[i] {
			if b.Cells[to] == Empty {
				moves = append(moves, Move{
					From: CoordOf[i],
					To:   CoordOf[to],
				})
			}
		}

		// 跳跃（距离=2）
		for _, to := range JumpI[i] {
			if b.Cells[to] == Empty {
				moves = append(moves, Move{
					From: CoordOf[i],
					To:   CoordOf[to],
				})
			}
		}
	}
	return moves
}

// GenerateMoves 枚举玩家 player 在棋盘 b 上所有合法走法
//func GenerateMovesOld(b *Board, player CellState) []Move {
//	var moves []Move
//	// 遍历所有格子
//	for _, c := range b.AllCoords() {
//		if b.Get(c) != player {
//			continue
//		}
//		// 1) 复制走法：6 个方向
//		for _, d := range cloneDirs {
//			to := HexCoord{c.Q + d.Q, c.R + d.R}
//			if b.Get(to) == Empty {
//				moves = append(moves, Move{From: c, To: to})
//			}
//		}
//		// 2) 跳跃走法：12 个方向
//		for _, d := range jumpDirs {
//			to := HexCoord{c.Q + d.Q, c.R + d.R}
//			if b.Get(to) == Empty {
//				moves = append(moves, Move{From: c, To: to})
//			}
//		}
//	}
//	return moves
//}

// 1) 把 Apply 改成返回被感染的坐标切片
// Move.Apply —— 在棋盘上执行一步棋：克隆或跳跃 + 邻居感染
// 返回：本步被感染的格子（HexCoord 列表），以及可能的错误（越界/占用/起点不对等）
//
// 依赖：indexOf、coordOf、neighI、b.setI、Opponent 等均已初始化
func (m Move) Apply(b *Board, player CellState) ([]HexCoord, error) {
	// —— 坐标 → 下标 —— //
	toIdx, okTo := IndexOf[m.To]
	fromIdx, okFrom := IndexOf[m.From]
	if !okTo || !okFrom {
		return nil, fmt.Errorf("apply: coord out of board (from=%v ok=%v, to=%v ok=%v)", m.From, okFrom, m.To, okTo)
	}

	// —— 基本合法性校验（按你原逻辑需要可增减）—— //
	if b.Cells[fromIdx] != player {
		return nil, fmt.Errorf("apply: from is not player's piece")
	}
	if b.Cells[toIdx] != Empty {
		return nil, fmt.Errorf("apply: destination not empty")
	}
	// 如需严格校验“是否真的是克隆/跳跃目的地”，可加：
	//   - 克隆：toIdx 必须在 neighI[fromIdx] 中
	//   - 跳跃：toIdx 必须在 jumpI[fromIdx] 中
	// 这里按“调用方保证合法走法”处理，省分支

	opp := Opponent(player)

	// —— 预先收集将被感染的邻居（以索引存一份，返回时也要 HexCoord）—— //
	infectedIdx := make([]int, 0, 6)
	infected := make([]HexCoord, 0, 6)
	for _, nb := range NeighI[toIdx] {
		if b.Cells[nb] == opp {
			infectedIdx = append(infectedIdx, nb)
			infected = append(infected, CoordOf[nb])
		}
	}

	// —— 执行跳跃/克隆 —— //
	if m.IsJump() {
		b.setI(fromIdx, Empty)
	}
	b.setI(toIdx, player)

	// —— 执行感染 —— //
	for _, nb := range infectedIdx {
		b.setI(nb, player)
	}

	return infected, nil
}

func HexDist(a, b HexCoord) int {
	dq, dr := a.Q-b.Q, a.R-b.R
	return max3(abs(dq), abs(dr), abs(dq+dr)) // ring distance
}

func IsLegalMove(from, to HexCoord) (clone, jump bool) {
	d := HexDist(from, to)
	return d == 1, d == 2
}
