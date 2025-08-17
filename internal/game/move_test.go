package game

import "testing"

func TestJumpOverObstacle(t *testing.T) {
	// 1) 新建半径 2 的小棋盘（足够测试）
	b := NewBoard(2)

	// 2) 在 (0,0) 放一颗 A 子，在 (1,0) 放障碍
	b.Set(HexCoord{0, 0}, PlayerA)
	b.Set(HexCoord{1, 0}, Blocked)

	// 3) 列出所有 A 方走法
	moves := GenerateMoves(b, PlayerA)
	want := Move{From: HexCoord{0, 0}, To: HexCoord{2, 0}}

	found := false
	for _, m := range moves {
		if m == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("期望能跳过障碍：%v 应该在 moves 里，但没找到", want)
	}
}
