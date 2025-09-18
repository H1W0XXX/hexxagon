// cmd/bench_eval/main.go
package main

import (
	"fmt"
	"math/rand"
	"time"

	"hexxagon_go/internal/game"
)

func benchEval(
	name string,
	f func(b *game.Board, side game.CellState) int,
	positions []*game.Board,
	repeats int,
) (nsPerPos float64, mposPerSec float64, sum int64) {

	startAll := time.Now()
	totalRuns := 0
	var acc int64

	for _, b := range positions {
		for r := 0; r < repeats; r++ {
			acc += int64(f(b, game.PlayerA)) // 固定同一视角做纯耗时对比
			totalRuns++
		}
	}

	elapsed := time.Since(startAll)
	n := float64(totalRuns)
	nsPerPos = float64(elapsed.Nanoseconds()) / n
	if elapsed.Seconds() > 0 {
		mposPerSec = (n / elapsed.Seconds()) / 1e6
	}
	return nsPerPos, mposPerSec, acc // acc 只是防止编译器过度优化
}

func main() {
	rand.Seed(time.Now().UnixNano())

	// 1) 随机生成一批合法局面
	numPositions := 10000
	const radius = 4
	positions := make([]*game.Board, numPositions)
	for i := 0; i < numPositions; i++ {
		st := game.NewGameState(radius)
		nMoves := rand.Intn(35) + 5 // 覆盖开中后期
		pl := game.PlayerA
		for j := 0; j < nMoves; j++ {
			mvs := game.GenerateMoves(st.Board, pl)
			if len(mvs) == 0 {
				break
			}
			st.MakeMove(mvs[rand.Intn(len(mvs))])
			pl = game.Opponent(pl)
		}
		positions[i] = st.Board.Clone()
	}

	// 2) 一致性检查（可选但强烈建议）
	mismatch := 0
	const showFirstN = 5
	for _, b := range positions {
		got := game.EvaluateBitBoard(b, game.PlayerA)
		want := game.EvaluateStatic(b, game.PlayerA)
		if got != want {
			if mismatch < showFirstN {
				fmt.Printf("[Mismatch %d] bitboard=%d static=%d\n", mismatch+1, got, want)
			}
			mismatch++
		}
	}
	if mismatch > 0 {
		fmt.Printf("总计不一致：%d / %d 局面\n", mismatch, len(positions))
	} else {
		fmt.Println("一致性检查通过：两个评估在样本上完全一致。")
	}

	// 3) 纯耗时基准（重复次数可调大以降低抖动）
	repeats := 50

	bbNs, bbMpos, bbAcc := benchEval("BitBoard", game.EvaluateBitBoard, positions, repeats)
	stNs, stMpos, stAcc := benchEval("Static", game.EvaluateStatic, positions, repeats)

	fmt.Println("=== Evaluate Benchmark ===")
	fmt.Printf("[BitBoard] 平均耗时 = %.0f ns/pos | 吞吐 = %.3f Mpos/s | acc=%d\n", bbNs, bbMpos, bbAcc)
	fmt.Printf("[Static  ] 平均耗时 = %.0f ns/pos | 吞吐 = %.3f Mpos/s | acc=%d\n", stNs, stMpos, stAcc)
}
