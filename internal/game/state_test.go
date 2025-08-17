// 文件：game/state_test.go
package game

import (
	"testing"
)

// TestClaimAllEmpty 测试 claimAllEmpty 方法能否把所有空格都赋给指定玩家，并且 updateScores 之后分数符合预期。
func TestClaimAllEmpty(t *testing.T) {
	// 1) 构造一个半径为 2 的新游戏状态
	gs := NewGameState(2)

	// 2) 先把整张棋盘都清成 Empty
	for _, coord := range gs.Board.AllCoords() {
		_ = gs.Board.Set(coord, Empty)
	}

	// 3) 放一个 A 子和一个 B 子在任意两个不同坐标上
	allCoords := gs.Board.AllCoords()
	if len(allCoords) < 2 {
		t.Fatal("棋盘坐标数不够")
	}
	coordA := allCoords[0]
	coordB := allCoords[1]
	_ = gs.Board.Set(coordA, PlayerA)
	_ = gs.Board.Set(coordB, PlayerB)

	// 4) 此时棋盘上除了这两个子，其他所有格子都是空
	//    调用 claimAllEmpty 把剩余空格都赋给 A
	gs.claimAllEmpty(PlayerA)

	// 5) updateScores 并检查：空格应该全部消失，A 的子数 = 总可用格子数 - 1（B 的子数）
	gs.updateScores()

	totalCells := len(gs.Board.AllCoords()) // 包括带 Blocked 的位置以及 Empty 位置
	bCount := 0
	for _, coord := range gs.Board.AllCoords() {
		if gs.Board.Get(coord) == PlayerB {
			bCount++
		}
	}

	// 统计空格
	emptyCnt := 0
	for _, coord := range gs.Board.AllCoords() {
		if gs.Board.Get(coord) == Empty {
			emptyCnt++
		}
	}
	if emptyCnt != 0 {
		t.Errorf("执行 claimAllEmpty 后，发现 %d 个格子仍然是 Empty，期望为 0", emptyCnt)
	}

	// A 的实际分数
	aCount := gs.ScoreA
	// 期望 A 得到：除了 B（1 个）和 Blocked（代码里 radius=2 只放了 3 个 Blocked），
	// 所有 Empty 都应算给 A。我们这里只比较 A = totalCells - bCount - blockedCnt。
	//  blockedCnt 也包含在 Board.AllCoords() 里，需要先统计。
	blockedCnt := 0
	for _, coord := range gs.Board.AllCoords() {
		if gs.Board.Get(coord) == Blocked {
			blockedCnt++
		}
	}

	expectedA := totalCells - blockedCnt - bCount
	if aCount != expectedA {
		t.Errorf("期望 A 的分数是 %d，但实际 gs.ScoreA=%d", expectedA, aCount)
	}
	if gs.ScoreB != bCount {
		t.Errorf("期望 B 的分数是 %d，但实际 gs.ScoreB=%d", bCount, gs.ScoreB)
	}
}

// TestMakeMoveOpponentNoMoves 演示在“对手无合法走法且还有空格”的情况下，直接调用 claimAllEmpty 并检查最终结果。
// 由于要触发 MakeMove 中的“下一玩家无路可走且 emptyCnt > 0”分支，我们这里手动构造棋盘，
// 让 B 从一开始就无法走；然后由 A 调用 claimAllEmpty。
func TestMakeMoveOpponentNoMoves(t *testing.T) {
	// 1) 先创建一个新棋局，radius=2
	gs := NewGameState(2)

	// 2) 把棋盘全部清成 Empty
	for _, coord := range gs.Board.AllCoords() {
		_ = gs.Board.Set(coord, Empty)
	}

	// 3) 只放一个 A 子和一个 B 子。且它们“相距较远”，让 B 无法在剩余空格里走
	//
	//    例如：我们在 (0, 0) 放 A；在 (2, -2) 放 B，其他都 Empty。如此一来，
	//    B 的邻居不在任何 Empty 上（假设 GenerateMoves 规则要求相邻或跳跃到空格）。
	//    如果你的 GenerateMoves 实现不一样，请替换成合适的“B 确实没法走”的坐标。
	aCoord := HexCoord{Q: 0, R: 0}
	bCoord := HexCoord{Q: 2, R: -2}
	_ = gs.Board.Set(aCoord, PlayerA)
	_ = gs.Board.Set(bCoord, PlayerB)

	// 确保当前回合是 A
	gs.CurrentPlayer = PlayerA
	gs.updateScores() // 这样 gs.ScoreA=1, gs.ScoreB=1

	// 4) 验证一下：B 的可行动作应当为 0
	nextMoves := GenerateMoves(gs.Board, PlayerB)
	if len(nextMoves) != 0 {
		t.Fatalf("预期 B 无法走，实际 GenerateMoves 返回 %d 种可走动作，请调整测试坐标", len(nextMoves))
	}

	// 5) 此时棋盘肯定还有空格，因为除了 (0,0)、(2,-2) 两个位置外，其余都 Empty
	emptyCnt := 0
	for _, coord := range gs.Board.AllCoords() {
		if gs.Board.Get(coord) == Empty {
			emptyCnt++
		}
	}
	if emptyCnt == 0 {
		t.Fatal("预期棋盘上有空格，但发现 emptyCnt=0，请检查测试环境")
	}

	// 6) 直接模拟 MakeMove 内部“B 无路可走且空格 >0”那个分支：
	//    把空格都判给 A，然后重新统计分数，标记游戏结束，设置 Winner。
	gs.claimAllEmpty(PlayerA)
	gs.updateScores()
	gs.GameOver = true
	gs.Winner = PlayerA

	// 7) 最后检查：B 只有 1 个子，A 的分数 = 总格子数 - Blocked 数 - B 的子数
	totalCells := len(gs.Board.AllCoords())
	blockedCnt := 0
	for _, coord := range gs.Board.AllCoords() {
		if gs.Board.Get(coord) == Blocked {
			blockedCnt++
		}
	}
	expectedA := totalCells - blockedCnt - 1 // B 只有 1 个子
	if gs.ScoreA != expectedA {
		t.Errorf("期望 A 的分数是 %d，但实际 gs.ScoreA=%d", expectedA, gs.ScoreA)
	}
	if gs.ScoreB != 1 {
		t.Errorf("期望 B 的分数是 1，但实际 gs.ScoreB=%d", gs.ScoreB)
	}
	if gs.Winner != PlayerA {
		t.Errorf("期望胜者为 PlayerA，但实际 gs.Winner=%v", gs.Winner)
	}
}
