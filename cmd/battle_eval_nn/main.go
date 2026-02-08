// cmd/hybrid_vs_base/main.go
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	// TODO: 把这个路径改成你项目里 game 包的真实模块路径
	game "hexxagon_go/internal/game"
)

// 两个搜索函数的统一签名（与你现有的一致）
type searchFn func(b *game.Board, player game.CellState, depth int64, allowJump bool) (game.Move, bool)

func emptiesCount(b *game.Board) int {
	empties := 0
	for i := 0; i < game.BoardN; i++ {
		if b.Cells[i] == game.Empty {
			empties++
		}
	}
	return empties
}

func pieceDiff(b *game.Board) int {
	return b.CountPieces(game.PlayerA) - b.CountPieces(game.PlayerB)
}

type frameRow struct {
	game int
	ply  int
	emp  int
	diff int
	tag  string // "Hybrid" 或 "Base" 当前行动方标签（可用于后续分析）
}

// 一盘棋：aFirst 决定谁先手（奇数局让 Hybrid 先；偶数局 Base 先）
// A 使用 fnA，B 使用 fnB。为了对战公平，不做你那些额外过滤，完全按函数本身逻辑来。
// 用 GameState 初始化 & 推进，对战 Hybrid vs Base
func playOneGame(
	radius int,
	aFirst bool,
	depthA, depthB int64,
	allowJump bool,
	fnA, fnB searchFn,
) (winner int, frames []frameRow) {

	st := game.NewGameState(radius)

	cur := game.PlayerA
	ply := 0
	frames = make([]frameRow, 0, 128)

	for {
		ply++
		var mv game.Move
		var ok bool
		var tag string

		if aFirst {
			// A=Hybrid, B=Base
			if cur == game.PlayerA {
				mv, ok = fnA(st.Board, cur, depthA, allowJump)
				tag = "Hybrid"
			} else {
				mv, ok = fnB(st.Board, cur, depthB, allowJump)
				tag = "Base"
			}
		} else {
			// A=Base, B=Hybrid
			if cur == game.PlayerA {
				mv, ok = fnB(st.Board, cur, depthB, allowJump)
				tag = "Base"
			} else {
				mv, ok = fnA(st.Board, cur, depthA, allowJump)
				tag = "Hybrid"
			}
		}

		if !ok {
			// 当前方无合法着法 → 终局
			break
		}

		// 用 GameState 推进（会处理感染、LastMove/GameOver 等）
		st.MakeMove(mv)

		// 记录一帧（横轴=空位，纵轴=棋子差A-B）
		frames = append(frames, frameRow{
			game: 0,
			ply:  ply,
			emp:  emptiesCount(st.Board),
			diff: pieceDiff(st.Board),
			tag:  tag,
		})

		if st.GameOver || frames[len(frames)-1].emp == 0 {
			break
		}

		cur = game.Opponent(cur)
		if ply > 1024 {
			break
		}
	}

	// 判胜负（与你 selfplay 一样的规则）
	d := pieceDiff(st.Board) // A 子数 - B 子数
	switch {
	case d > 0:
		winner = +1 // A 胜
	case d < 0:
		winner = -1 // B 胜
	default:
		winner = 0
	}
	return
}
func writeCSV(path string, rows [][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	return w.WriteAll(rows)
}

func main() {
	// 信号监听，按下 Ctrl+C 强制退出
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Printf("\n[系统] 接收到退出信号，正在强制停止...\n")
		os.Exit(0)
	}()

	rand.Seed(time.Now().UnixNano())

	var (
		games     = flag.Int("games", 100, "对战总局数")
		radius    = flag.Int("radius", 4, "棋盘半径（4=9x9）")
		depthA    = flag.Int("depth_hybrid", 2, "Hybrid 搜索深度")
		depthB    = flag.Int("depth_base", 3, "Base 搜索深度")
		allowJump = flag.Bool("allow_jump", true, "是否允许跳跃（传给AI层的门控）")
		outCSV    = flag.String("out", "hybrid_vs_base_samples.csv", "采样CSV输出路径")
	)
	flag.Parse()

	// 绑定搜索：统一用当前 αβ 实现，区别在于 Evaluate 是否启用 ONNX。
	// 我们通过切换 UseONNXForPlayerA/B 来实现“ONNX vs 旧评估”。
	fnSearch := game.FindBestMoveAtDepth

	aWins, bWins, draws := 0, 0, 0
	rows := [][]string{{"game", "ply", "empties", "piece_diff", "mover_ai"}} // mover_ai: 执棋方标签（Hybrid/Base）

	for g := 1; g <= *games; g++ {
		aFirst := (g%2 == 1) // 奇数局 Hybrid 先，偶数局 Base 先

		// 根据先后手切换 ONNX 使用方：
		// aFirst=true  -> PlayerA(先手)=Hybrid(ONNX)，PlayerB=Base(旧评估)
		// aFirst=false -> PlayerA=Base，PlayerB=Hybrid(ONNX)
		if aFirst {
			game.UseONNXForPlayerA = true
			game.UseONNXForPlayerB = false
		} else {
			game.UseONNXForPlayerA = false
			game.UseONNXForPlayerB = true
		}

		w, frames := playOneGame(*radius, aFirst, int64(*depthA), int64(*depthB), *allowJump, fnSearch, fnSearch)

		switch w {
		case +1: // A 赢
			if aFirst { // Hybrid 先手
				aWins++
			} else {
				bWins++ // A=Base
			}
		case -1: // B 赢
			if aFirst { // B=Base
				bWins++
			} else { // B=Hybrid
				aWins++
			}
		default:
			draws++
		}

		// 写帧
		for _, fr := range frames {
			rows = append(rows, []string{
				fmt.Sprintf("%d", g),
				fmt.Sprintf("%d", fr.ply),
				fmt.Sprintf("%d", fr.emp),
				fmt.Sprintf("%d", fr.diff),
				fr.tag,
			})
		}

		if g%10 == 0 {
			log.Printf("进度 %d/%d | Hybrid胜:%d Base胜:%d 平:%d", g, *games, aWins, bWins, draws)
		}
	}

	fmt.Printf("\n===== FindBestMoveAtDepthHybrid vs FindBestMoveAtDepth =====\n")
	fmt.Printf("总局数: %d（轮流先手）\n", *games)
	fmt.Printf("Hybrid 胜: %d | Base 胜: %d | 平: %d\n", aWins, bWins, draws)

	if err := writeCSV(*outCSV, rows); err != nil {
		log.Fatalf("写CSV失败: %v", err)
	}
	fmt.Printf("采样已写入: %s（列: game, ply, empties, piece_diff, mover_ai）\n", *outCSV)
}