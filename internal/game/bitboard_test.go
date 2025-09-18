package game

import (
	"math/rand"
	"testing"
	"time"
)

func TestEvalConsistency(t *testing.T) {
	positions := RandomBoards(500, 4) // 生成 500 个局面，半径=4

	for _, b := range positions {
		for _, side := range []CellState{PlayerA, PlayerB} {
			got := EvaluateBitBoard(b, side) // 位板版
			want := EvaluateStatic(b, side)  // 原版
			if got != want {
				t.Fatalf("mismatch: got=%d want=%d\nb=%v", got, want, b.Cells)
			}
		}
	}
}

func RandomBoards(numPositions int, radius int) []*Board {
	rand.Seed(time.Now().UnixNano())

	positions := make([]*Board, numPositions)
	for i := 0; i < numPositions; i++ {
		st := NewGameState(radius)

		// 随机走 5~40 步，制造不同阶段的局面
		nMoves := rand.Intn(35) + 5
		pl := PlayerA

		for j := 0; j < nMoves; j++ {
			mvs := GenerateMoves(st.Board, pl)
			if len(mvs) == 0 {
				break
			}
			st.MakeMove(mvs[rand.Intn(len(mvs))])
			pl = Opponent(pl)
		}

		positions[i] = st.Board.Clone()
	}
	return positions
}
