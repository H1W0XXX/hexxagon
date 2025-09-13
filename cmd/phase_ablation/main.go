package main

import (
	"flag"
	"fmt"
	"math/rand"
	"time"

	game "hexxagon_go/internal/game"
)

var (
	radius     = flag.Int("radius", 4, "棋盘半径")
	depthEval  = flag.Int("depth", 2, "搜索深度")
	samples    = flag.Int("n", 100, "每阶段采样局面数量")
	randomOpen = flag.Int("random_open", 2, "开局随机回合数")
	seed       = flag.Int64("seed", time.Now().UnixNano(), "随机种子")
)

// --- 工具函数 ---
func emptiesCount(b *game.Board) int {
	empties := 0
	for i := 0; i < game.BoardN; i++ {
		if b.Cells[i] == game.Empty {
			empties++
		}
	}
	return empties
}
func emptyRatio(b *game.Board) float64 {
	total := len(b.AllCoords())
	return float64(emptiesCount(b)) / float64(total)
}
func pieceDiff(b *game.Board) int {
	return b.CountPieces(game.PlayerA) - b.CountPieces(game.PlayerB)
}

// 从某阶段采样起始局面
func sampleStateForPhase(rng *rand.Rand, phase string) *game.GameState {
	st := game.NewGameState(*radius)
	// 随机开局若干手，打破对称
	for i := 0; i < *randomOpen; i++ {
		for _, pl := range []game.CellState{game.PlayerA, game.PlayerB} {
			moves := game.GenerateMoves(st.Board, pl)
			if len(moves) == 0 {
				continue
			}
			mv := moves[rng.Intn(len(moves))]
			st.MakeMove(mv)
		}
	}
	// 用静态搜索推进，直到到达目标阶段
	cur := game.PlayerA
	for step := 0; step < 200 && !st.GameOver; step++ {
		r := emptyRatio(st.Board)
		switch phase {
		case "opening":
			if r >= 0.75 {
				return st
			}
		case "endgame":
			if r <= 0.25 {
				return st
			}
		case "midgame":
			if r < 0.75 && r > 0.25 {
				return st
			}
		}
		mv, ok := game.FindBestMoveAtDepth(st.Board, cur, 2, true) // 用 base 搜索推进
		if !ok {
			break
		}
		st.MakeMove(mv)
		cur = game.Opponent(cur)
	}
	return st
}

// 整盘对战：一方=全静态，一方=只在某阶段用 NN
func duel(st0 *game.GameState, depth int64, phase string) int {
	// A=全静态；B=PhaseSelect
	st := *st0
	b := *st0.Board
	st.Board = &b
	cur := game.PlayerA
	ply := 0

	for {
		ply++
		var mv game.Move
		var ok bool

		if cur == game.PlayerA {
			// 全静态
			mv, ok = game.FindBestMoveAtDepth(st.Board, cur, depth, true)
			game.SetPhaseSwitch(game.PhaseSwitch{ // 全静态
				UseNNOpening: false, UseNNMidgame: false, UseNNEndgame: false,
				ROpen: 0.75, REnd: 0.25,
			})
		} else {
			// 只在目标阶段启用 NN
			ps := game.PhaseSwitch{
				UseNNOpening: false,
				UseNNMidgame: false,
				UseNNEndgame: false,
				ROpen:        0.75,
				REnd:         0.25,
			}
			switch phase {
			case "opening":
				ps.UseNNOpening = true
			case "midgame":
				ps.UseNNMidgame = true
			case "endgame":
				ps.UseNNEndgame = true
			}
			game.SetPhaseSwitch(ps)
			mv, ok = game.FindBestMoveAtDepthHybrid(st.Board, cur, depth, true)
		}

		if !ok {
			break
		}
		st.MakeMove(mv)
		if st.GameOver || emptiesCount(st.Board) == 0 || ply > 1024 {
			break
		}
		cur = game.Opponent(cur)
	}
	d := pieceDiff(st.Board)
	switch {
	case d > 0:
		return +1 // 静态赢
	case d < 0:
		return -1 // NN赢
	default:
		return 0
	}
}

func main() {
	flag.Parse()
	rng := rand.New(rand.NewSource(*seed))

	phases := []string{"opening", "midgame", "endgame"}
	for _, ph := range phases {
		w, l, d := 0, 0, 0
		for i := 0; i < *samples; i++ {
			st := sampleStateForPhase(rng, ph)
			res := duel(st, int64(*depthEval), ph)
			switch res {
			case +1:
				w++
			case -1:
				l++
			default:
				d++
			}
		}
		fmt.Printf("[%s] 静态胜=%d NN胜=%d 平=%d | NN胜率=%.1f%%\n",
			ph, w, l, d, 100*float64(l)/float64(w+l+d))
	}
}

// go build -o phase_ablation.exe .\cmd\phase_ablation\main.go
