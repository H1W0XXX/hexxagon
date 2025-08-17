package main

import (
	"flag"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"hexxagon_go/internal/ui"
	"log"
	"strconv"
)

// import _ "net/http/pprof"

// import "golang.org/x/sys/windows"
// import "runtime"
//
//	func init() {
//		if runtime.GOOS == "windows" {
//			h := windows.CurrentProcess()
//
//			// BELOW_NORMAL_PRIORITY_CLASS = 0x00004000
//			if err := windows.SetPriorityClass(h, windows.BELOW_NORMAL_PRIORITY_CLASS); err != nil {
//				log.Printf("设置进程优先级失败: %v", err)
//			} else {
//				log.Println("已将进程优先级设置为 BELOW_NORMAL")
//			}
//		}
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
	scoreTipFlag := flag.String("tip", "false", "是否展示玩家棋子评分(true/false)")
	flag.Parse()
	aiEnabled := (*modeFlag == "pve") // pve=启用 AI，pvp=禁用 AI
	// 把 string 转成 bool
	showScores, err := strconv.ParseBool(*scoreTipFlag)
	if err != nil {
		log.Fatalf("无效的 -tip 参数 %q: %v", *scoreTipFlag, err)
	}

	ctx := audio.NewContext(sampleRate)
	if ctx == nil {
		log.Fatal("audio context not initialized")
	}

	screen, err := ui.NewGameScreen(ctx, aiEnabled, showScores) // 传入 AI 开关
	if err != nil {
		log.Fatal(err)
	}
	//ebiten.SetFPSMode(ebiten.FPSModeVsyncOffMinimum)
	ebiten.SetVsyncEnabled(true)
	ebiten.SetTPS(30)
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
