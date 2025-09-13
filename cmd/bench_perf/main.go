package main

import (
	"fmt"
	"math/rand"
	"time"

	"hexxagon_go/internal/game"
)

func bench(
	name string,
	f func(b *game.Board, player game.CellState, depth int64, allowJump bool) (game.Move, bool),
	depth int64,
	positions []*game.Board,
	repeats int,
) (msPerPos float64, nodesPerPos float64, nps float64) {

	totalTime := time.Duration(0)
	totalNodes := int64(0)
	totalRuns := 0

	for _, b := range positions {
		for r := 0; r < repeats; r++ {
			game.ResetNodes()
			start := time.Now()
			_, _ = f(b, game.PlayerA, depth, true)
			elapsed := time.Since(start)

			totalTime += elapsed
			totalNodes += game.NodesSearched
			totalRuns++
		}
	}

	n := float64(totalRuns)
	msPerPos = float64(totalTime.Milliseconds()) / n
	nodesPerPos = float64(totalNodes) / n
	nps = float64(totalNodes) / totalTime.Seconds()
	return
}

func main() {
	numPositions := 1000
	const radius = 4
	positions := make([]*game.Board, numPositions)
	for i := 0; i < numPositions; i++ {
		st := game.NewGameState(radius)

		// 随机走几步，制造不同局面
		nMoves := rand.Intn(10) + 5
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

	// 对比 depth 2/3/4
	repeats := 1
	for _, d := range []int64{2, 3, 4} {
		sMs, sNodes, sNps := bench("Base", game.FindBestMoveAtDepth, d, positions, repeats)
		hMs, hNodes, hNps := bench("Hybrid", game.FindBestMoveAtDepthHybrid, d, positions, repeats)

		fmt.Printf("=== Depth %d ===\n", d)
		fmt.Printf("[Base]   平均耗时=%.2f ms | 节点=%.0f | NPS=%.0f\n", sMs, sNodes, sNps)
		fmt.Printf("[Hybrid] 平均耗时=%.2f ms | 节点=%.0f | NPS=%.0f\n", hMs, hNodes, hNps)
	}
}

// go build -o bench_perf.exe .\cmd\bench_perf\main.go
