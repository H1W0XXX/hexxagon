package main

import (
	"flag"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"hexxagon_go/internal/game"
	"hexxagon_go/internal/ui"
	"log"
)

//import _ "net/http/pprof"

//func init() {
//	if runtime.GOOS == "windows" {
//		h := windows.CurrentProcess()
//
//		// BELOW_NORMAL_PRIORITY_CLASS = 0x00004000
//		if err := windows.SetPriorityClass(h, windows.BELOW_NORMAL_PRIORITY_CLASS); err != nil {
//			log.Printf("设置进程优先级失败: %v", err)
//		} else {
//			log.Println("已将进程优先级设置为 BELOW_NORMAL")
//		}
//	}
//}

//	func init() {
//		go func() {
//			addr := "127.0.0.1:6060" // 只监听本机，避免暴露外网
//			log.Println("[pprof] listening on", addr, "/debug/pprof/")
//			if err := http.ListenAndServe(addr, nil); err != nil {
//				log.Println("pprof server error:", err)
//			}
//		}()
//
//		runtime.SetBlockProfileRate(1)     // 1：采样全部阻塞事件
//		runtime.SetMutexProfileFraction(1) // 1：采样全部互斥锁事件
//	}
func main() {
	const (
		screenW     = 800
		screenH     = 600
		sampleRate  = 44100
		ScreenScale = 1
	)

	// —— 新增：启动参数 —— //
	modeFlag := flag.String("mode", "pve", "游戏模式: pve(人机) 或 pvp(人人)")
	depthFlag := flag.Int("depth", 1, "人机搜索深度 (ONNX 建议 1 或 2)")
	// 支持 -tip / -tips 两个别名
	showScoresFlag := flag.Bool("tip", false, "是否展示玩家棋子评分")
	flag.BoolVar(showScoresFlag, "tips", false, "是否展示玩家棋子评分 (同 -tip)")
	flag.Parse()
	aiEnabled := (*modeFlag == "pve") // pve=启用 AI，pvp=禁用 AI
	aiDepth := *depthFlag
	showScores := *showScoresFlag

	// 在后台立即开始初始化 ONNX/TensorRT 编译
	game.PreloadModels()

	ctx := audio.NewContext(sampleRate)
	if ctx == nil {
		log.Fatal("audio context not initialized")
	}

	screen, err := ui.NewGameScreen(ctx, aiEnabled, aiDepth, showScores) // 传入 AI 开关和深度
	if err != nil {
		log.Fatal(err)
	}
	//ebiten.SetFPSMode(ebiten.FPSModeVsyncOffMinimum)
	ebiten.SetVsyncEnabled(true)
	ebiten.SetTPS(60)
	ebiten.SetWindowSize(screenW*ScreenScale, screenH*ScreenScale)
	ebiten.SetWindowTitle("Hexxagon")

	if err := ebiten.RunGame(screen); err != nil {
		log.Fatal(err)
	}
}

// $dll = "D:\go\ddddocr_go\gpu\onnxruntime.dll"
// $env:ONNXRUNTIME_SHARED_LIBRARY_PATH = $dll
// $env:Path = "D:\go\ddddocr_go\gpu;" + $env:Path
// go build -ldflags="-s -w" -gcflags="all=-trimpath=${PWD}" -asmflags="all=-trimpath=${PWD}" -o hexxagon.exe .\cmd\hexxagon\main.go
