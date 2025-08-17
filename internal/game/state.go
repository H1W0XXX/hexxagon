package game

import (
	"errors"
	"fmt"
)

// GameState 包含了整个游戏的状态，包括棋盘、当前玩家、分数和胜负状态
type GameState struct {
	Board         *Board    // 棋盘
	CurrentPlayer CellState // 当前玩家 (PlayerA 或 PlayerB)
	ScoreA        int       // 玩家 A 的分数
	ScoreB        int       // 玩家 B 的分数
	GameOver      bool      // 游戏是否结束
	Winner        CellState // 胜者 (PlayerA、PlayerB 或 Empty 表示平局)

}

// NewGameState 创建并初始化一个新的游戏状态，radius 是棋盘半径
// 默认在六边形的三个角放置玩家 A 的棋子，在相对三个角放置玩家 B 的棋子
func NewGameState(radius int) *GameState {
	// 创建空棋盘
	b := NewBoard(radius)
	// 角落坐标 (A 方)
	cornersA := []HexCoord{
		{radius, 0},
		{0, -radius},
		{-radius, radius},
	}
	// 对立角坐标 (B 方)
	cornersB := []HexCoord{
		{-radius, 0},
		{0, radius},
		{radius, -radius},
	}
	// 放置初始棋子
	for _, c := range cornersA {
		_ = b.Set(c, PlayerA)
	}
	for _, c := range cornersB {
		_ = b.Set(c, PlayerB)
	}

	// 放置障碍物
	centerBlocks := []HexCoord{
		{1, 0},
		{-1, 1},
		{0, -1},
	}
	for _, c := range centerBlocks {
		_ = b.Set(c, Blocked)
	}

	// 构造 GameState
	gs := &GameState{
		Board:         b,
		CurrentPlayer: PlayerA,
	}

	// 把“行棋方随机键” XOR 进棋盘哈希
	b.hash ^= zobristSide[sideIdx(gs.CurrentPlayer)]

	gs.updateScores() // 计算初始分数
	return gs
}

//func NewGameState(radius int) *GameState {
//	// 创建空棋盘
//	b := NewBoard(radius)
//	// 角落坐标 (A 方)
//	cornersA := []HexCoord{
//		{radius, 0},
//		{0, -radius},
//		{-radius, radius},
//	}
//	// 对立角坐标 (B 方)
//	cornersB := []HexCoord{
//		{-radius, 0},
//		{0, radius},
//		{radius, -radius},
//	}
//	// 放置初始棋子
//	for _, c := range cornersA {
//		_ = b.Set(c, PlayerA)
//	}
//	for _, c := range cornersB {
//		_ = b.Set(c, PlayerB)
//	}
//
//	// 放置障碍物
//	centerBlocks := []HexCoord{
//		{1, 0},
//		{-1, 1},
//		{0, -1},
//	}
//	for _, c := range centerBlocks {
//		_ = b.Set(c, Blocked)
//	}
//
//	// 构造 GameState
//	gs := &GameState{
//		Board:         b,
//		CurrentPlayer: PlayerA,
//	}
//
//	// 把“行棋方随机键” XOR 进棋盘哈希
//	b.hash ^= zobristSide[sideIdx(gs.CurrentPlayer)]
//
//	gs.updateScores() // 计算初始分数
//	return gs
//}

// updateScores 重新统计棋子数量，更新 ScoreA 和 ScoreB
func (gs *GameState) updateScores() {
	a, b := 0, 0
	for _, coord := range gs.Board.AllCoords() {
		switch gs.Board.Get(coord) {
		case PlayerA:
			a++
		case PlayerB:
			b++
		}
	}
	gs.ScoreA = a
	gs.ScoreB = b
}

// MakeMove 尝试执行一次玩家移动，并自动处理翻转、分数更新、切换回合和结束判定
func (gs *GameState) MakeMove(m Move) ([]HexCoord, undoInfo, error) {

	if gs.GameOver {
		return nil, undoInfo{}, errors.New("游戏已结束")
	}

	// ★ 先记住这一步是谁在走
	mover := gs.CurrentPlayer

	// 1) 执行克隆/跳跃并感染
	infected, undo := m.MakeMove(gs.Board, mover)

	// ★ 立刻记录“上一手是谁 + 感染了多少”，供 UI/MCTS 使用
	gs.Board.LastMover = mover
	gs.Board.LastInfect = len(infected)
	// 2) 更新子数 & 统计空格
	gs.updateScores()
	emptyCnt := 0
	for _, c := range gs.Board.AllCoords() {
		if gs.Board.Get(c) == Empty {
			emptyCnt++
		}
	}

	// 3) 计算“下一执子方”并检查他／她有没有合法走法
	next := Opponent(gs.CurrentPlayer)
	nextMoves := GenerateMoves(gs.Board, next)

	// —— 新增：对手无子可走，且棋盘还有空格 ——
	if len(nextMoves) == 0 && emptyCnt > 0 {
		// ① 把所有空格判给当前玩家
		gs.claimAllEmpty(gs.CurrentPlayer)
		// ② 重新统计分数
		gs.updateScores()

		// ③ 设置结束标记并决定赢家
		gs.GameOver = true
		if gs.ScoreA > gs.ScoreB {
			gs.Winner = PlayerA
		} else if gs.ScoreB > gs.ScoreA {
			gs.Winner = PlayerB
		} else {
			gs.Winner = Empty // 平局
		}

		// —— 在这里打印胜负结果 & 棋子数量 ——
		switch gs.Winner {
		case PlayerA:
			fmt.Printf("玩家 A: %d 个棋子，玩家 B: %d 个棋子\n", gs.ScoreA, gs.ScoreB)
			fmt.Println("玩家 A 获胜！")
		case PlayerB:
			fmt.Printf("玩家 A: %d 个棋子，玩家 B: %d 个棋子\n", gs.ScoreA, gs.ScoreB)
			fmt.Println("玩家 B 获胜！")
		default:
			fmt.Printf("玩家 A: %d 个棋子，玩家 B: %d 个棋子\n", gs.ScoreA, gs.ScoreB)
			fmt.Println("平局！")
		}

		return infected, undo, nil
	}

	// 4) 是否满足任一终局条件？（原有逻辑：一方无子、棋盘已满或下一方无走法）
	gameEnds :=
		gs.ScoreA == 0 || // 一方无子
			gs.ScoreB == 0 ||
			emptyCnt == 0 || // 棋盘已满
			(len(nextMoves) == 0) // 当前玩家走完后，下一方无合法着

	if gameEnds {
		// 4.1 处理游戏结束时的分数
		if gs.ScoreA == 0 || gs.ScoreB == 0 || emptyCnt == 0 {
			// 如果是因为一方无子或棋盘已满，正常填充封闭区域并计算分数
			gs.fillEnclosedRegions()
			gs.updateScores()
		} else if len(nextMoves) == 0 {
			// 如果是因为下一玩家无合法走法，将所有空格分配给当前玩家
			totalCells := len(gs.Board.AllCoords())
			blockedCnt := 0
			for _, c := range gs.Board.AllCoords() {
				if gs.Board.Get(c) == Blocked {
					blockedCnt++
				}
			}
			// 注意：这里假设当前走子方是 A，且是 A 在这一步之后检查到 B 无法走
			// 所以直接把剩余空格算到 A。你如果想兼容两种走子方，都要判断一下 gs.CurrentPlayer：
			if gs.CurrentPlayer == PlayerA {
				gs.ScoreA = totalCells - blockedCnt - gs.ScoreB
			} else {
				gs.ScoreB = totalCells - blockedCnt - gs.ScoreA
			}
		}

		// 4.2 标记 GameOver & Winner，并打印结果
		gs.GameOver = true
		switch {
		case gs.ScoreA > gs.ScoreB:
			gs.Winner = PlayerA
			fmt.Printf("玩家 A: %d 个棋子，玩家 B: %d 个棋子\n", gs.ScoreA, gs.ScoreB)
			fmt.Printf("Player A: %d pieces, Player B: %d pieces\n", gs.ScoreA, gs.ScoreB)
			fmt.Println("玩家 A 获胜！ / Player A wins!")
		case gs.ScoreB > gs.ScoreA:
			gs.Winner = PlayerB
			fmt.Printf("玩家 A: %d 个棋子，玩家 B: %d 个棋子\n", gs.ScoreA, gs.ScoreB)
			fmt.Printf("Player A: %d pieces, Player B: %d pieces\n", gs.ScoreA, gs.ScoreB)
			fmt.Println("玩家 B 获胜！ / Player B wins!")
		default:
			gs.Winner = Empty // 平局
			fmt.Printf("玩家 A: %d 个棋子，玩家 B: %d 个棋子\n", gs.ScoreA, gs.ScoreB)
			fmt.Printf("Player A: %d pieces, Player B: %d pieces\n", gs.ScoreA, gs.ScoreB)
			fmt.Println("平局！ / It's a tie!")
		}
		return infected, undo, nil
	}

	// 5) 还没结束，正常换手
	gs.CurrentPlayer = next
	return infected, undo, nil
}

// GetScores 返回当前双方的分数 (A, B)
func (gs *GameState) GetScores() (int, int) {
	return gs.ScoreA, gs.ScoreB
}

// Reset 重置游戏到初始状态，保留相同半径
func (gs *GameState) Reset() {
	radius := gs.Board.radius
	newGs := NewGameState(radius)
	*gs = *newGs
}

// fillEnclosedRegions 会把那些既不连通到棋盘最外圈、
// 也只被单一方棋子（不含 Blocked）包围的空格区域填充给该包围方。
func (gs *GameState) fillEnclosedRegions() {
	radius := gs.Board.radius
	visited := make(map[HexCoord]bool)

	for _, start := range gs.Board.AllCoords() {
		// 只对未访问过且是空的格子做 BFS
		if gs.Board.Get(start) != Empty || visited[start] {
			continue
		}

		// 准备做 BFS，把从 start 出发的这片“空格连通区域”都搜出来
		queue := []HexCoord{start}
		region := []HexCoord{start}
		visited[start] = true

		touchesBorder := false
		borderStates := make(map[CellState]bool)

		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]

			// 如果 cur 已经在最外圈（|Q|==radius 或 |R|==radius 或 |Q+R|==radius），
			// 那么整个 region 就不算封闭区域了
			if abs(cur.Q) == radius || abs(cur.R) == radius || abs(cur.Q+cur.R) == radius {
				touchesBorder = true
			}

			// 枚举 6 个邻居
			for _, nb := range gs.Board.Neighbors(cur) {
				s := gs.Board.Get(nb)
				switch s {
				case Empty:
					// 相邻还是空格，就继续把它加入到 BFS
					if !visited[nb] {
						visited[nb] = true
						queue = append(queue, nb)
						region = append(region, nb)
					}
				case Blocked:
					// 如果遇到障碍，不标记 touchesBorder，也不记在 borderStates 里
					continue
				case PlayerA, PlayerB:
					// 遇到的是棋子，就把它记到 borderStates
					borderStates[s] = true
				}
			}
		}

		// BFS 结束后，region 保存了 start 所在的整个空洞连通块
		// 如果这片区域 没有连到外圈 (touchesBorder==false)
		// 并且 只有一种棋子包围 (len(borderStates)==1)，那么就把 region 全部填给这一侧
		if !touchesBorder && len(borderStates) == 1 {
			var owner CellState
			for p := range borderStates {
				owner = p
			}
			for _, c := range region {
				// 把每个空格都设成 owner，并忽略错误
				_ = gs.Board.Set(c, owner)
			}
		}
	}
}

// claimAllEmpty 把棋盘上所有空格判给指定玩家。
func (gs *GameState) claimAllEmpty(to CellState) {
	for _, c := range gs.Board.AllCoords() {
		if gs.Board.Get(c) == Empty {
			_ = gs.Board.Set(c, to) // 忽略 error
		}
	}
}
