// cmd/bench_perf/main.go
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime/pprof"
	"time"

	"hexxagon_go/internal/game"
)

func main() {
	rand.Seed(time.Now().UnixNano())

	// 开启 CPU Profile
	f, err := os.Create("cpu_onnx.prof")
	if err != nil {
		fmt.Println("could not create CPU profile: ", err)
		return
	}
	defer f.Close()
	if err := pprof.StartCPUProfile(f); err != nil {
		fmt.Println("could not start CPU profile: ", err)
		return
	}
	defer pprof.StopCPUProfile()

	fmt.Println("Starting Full Game AI Benchmarking (ONNX Enabled)...")

	// 1) 初始化游戏
	radius := 4
	st := game.NewGameState(radius)
	
	// 确保启用 ONNX
	game.UseONNXForPlayerA = true
	game.UseONNXForPlayerB = true

	// 2) 模拟一个完整的对局（直到结束或达到步数上限）
	depth := 2 // 深度设为 2，兼顾速度与真实负载
	maxMoves := 100
	
	start := time.Now()
	for i := 0; i < maxMoves; i++ {
		if st.GameOver {
			fmt.Printf("Game over at move %d\n", i)
			break
		}
		
		fmt.Printf("Move %d, Player %v searching (depth %d)...\n", i+1, st.CurrentPlayer, depth)
		mv, ok := game.FindBestMoveAtDepth(st.Board, st.CurrentPlayer, int64(depth), true)
		if !ok {
			fmt.Println("No legal moves, skipping...")
			// 这里根据游戏逻辑处理跳过或结束
			break 
		}
		
		// 执行移动
		st.MakeMove(mv)
	}
	elapsed := time.Since(start)

	fmt.Printf("Total time for full game: %v\n", elapsed)
	fmt.Println("Profile saved to cpu_onnx.prof. Run 'go tool pprof -http=:8080 cpu_onnx.prof' to view the heatmap.")
}
