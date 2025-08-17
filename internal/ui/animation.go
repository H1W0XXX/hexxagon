// internal/ui/animation.go
package ui

import (
	"fmt"
	"github.com/hajimehoshi/ebiten/v2"
	"hexxagon_go/internal/assets"
	"hexxagon_go/internal/game"
	"math"
	"time"
)

type FrameAnim struct {
	Frames     []*ebiten.Image
	FPS        float64   // 每秒多少帧
	Start      time.Time // 动画开始时间
	Done       bool
	Coord      game.HexCoord // 要播放在哪个棋盘格
	Angle      float64       // 旋转角 (弧度)
	Key        string
	From       game.HexCoord // 新增：动画发起位置
	To         game.HexCoord // 目标格
	MidX, MidY float64       // new: pixel midpoint in offscreen coords
}

func (a *FrameAnim) Current() *ebiten.Image {
	if a.Done || len(a.Frames) == 0 {
		return nil
	}
	elapsed := time.Since(a.Start).Seconds()
	// 延迟播放：还没到 Start，就返回 nil
	if elapsed < 0 {
		return nil
	}
	idx := int(elapsed * a.FPS)
	//fmt.Printf("Anim Current: elapsed=%.3f idx=%d/%d done=%v\n",
	//	elapsed, idx, len(a.Frames), a.Done)
	if idx >= len(a.Frames) {
		a.Done = true
		return nil
	}
	return a.Frames[idx]
}

var dirAngle = map[[2]int]float64{
	{+1, 0}:  0,
	{+1, -1}: -math.Pi / 3,
	{0, -1}:  -2 * math.Pi / 3,
	{-1, 0}:  math.Pi,
	{-1, +1}: 2 * math.Pi / 3,
	{0, +1}:  math.Pi / 3,
}

func (gs *GameScreen) startInfectAnim(from, to game.HexCoord, player game.CellState) {
	dq := to.Q - from.Q
	dr := to.R - from.R
	key := [2]int{dq, dr}

	base := "redEatWhite"
	if player == game.PlayerB { // 白吃红，用另一套
		base = "whiteEatRed"
	}
	frames := assets.AnimFrames[base] // 左→右 素材

	anim := &FrameAnim{
		Frames: frames,
		FPS:    5,
		Start:  time.Now(),
		Coord:  to,            // 在被感染格播放
		Angle:  dirAngle[key], // 旋转角
	}
	gs.anims = append(gs.anims, anim)
}

// 启动跳跃 / 复制动画
func (gs *GameScreen) addMoveAnim(move game.Move, player game.CellState) {
	dirKey := directionKey(move.From, move.To)

	base := ""
	switch {
	//case move.IsJump() && player == game.PlayerA:
	//	base = "redJump/" + dirKey
	//case move.IsJump() && player == game.PlayerB:
	//	base = "whiteJump/" + dirKey
	case move.IsClone() && player == game.PlayerA:
		base = "redClone/" + dirKey
	case move.IsClone() && player == game.PlayerB:
		base = "whiteClone/" + dirKey
	}
	//fmt.Println(base)
	frames := assets.AnimFrames[base]
	if len(frames) == 0 {
		//fmt.Printf("!跳跃或者复制动画资源缺失: %s\n", base)
		return
	}
	gs.anims = append(gs.anims, &FrameAnim{
		Frames: frames,
		FPS:    30,
		Start:  time.Now(),
		Coord:  move.From,
		Angle:  0,
		Key:    base,
	})
}

// 启动感染动画（direction 由 from→to 决定）
// from 是发起感染的格子，to 是被感染的格子
// 增加一个 delay 参数，允许延迟多少时间后开始
func (gs *GameScreen) addInfectAnim(
	from, to game.HexCoord,
	player game.CellState,
	delay time.Duration, // 新增：启动延迟
) {
	base := "redEatWhite"
	if player == game.PlayerB {
		base = "whiteEatRed"
	}
	frames := assets.AnimFrames[base]
	if len(frames) == 0 {
		fmt.Printf("!感染动画资源缺失: %s\n", base)
		return
	}

	// 直接用像素方向计算角度，不再用死表
	_, _, _, tileW, tileH, vs := getBoardTransform(gs.tileImage)
	// 计算 offscreen 上 from/​to 的中心
	fx0 := (float64(from.Q) + BoardRadius) * float64(tileW) * 0.75
	fy0 := (float64(from.R) + BoardRadius + float64(from.Q)/2) * vs
	tx0 := (float64(to.Q) + BoardRadius) * float64(tileW) * 0.75
	ty0 := (float64(to.R) + BoardRadius + float64(to.Q)/2) * vs
	fx := fx0 + float64(tileW)/2
	fy := fy0 + float64(tileH)/2
	tx := tx0 + float64(tileW)/2
	ty := ty0 + float64(tileH)/2
	midX := (fx + tx) / 2
	midY := (fy + ty) / 2
	ang := math.Atan2(ty-fy, tx-fx)

	//fmt.Printf("ang %v", ang)
	gs.anims = append(gs.anims, &FrameAnim{
		Frames: frames,
		FPS:    30,
		Start:  time.Now().Add(delay), // ← 这里用 delay
		Coord:  from,
		From:   from,
		To:     to,
		Angle:  ang,
		Key:    base,
		MidY:   midY,
		MidX:   midX,
	})
}

// 6 个方向关键词（根据 dq,dr 返回）
func directionKey(from, to game.HexCoord) string {
	dq, dr := to.Q-from.Q, to.R-from.R
	switch [2]int{dq, dr} {
	case [2]int{+1, 0}:
		return "lowerright"
	case [2]int{+1, -1}:
		return "upperright"
	case [2]int{0, -1}:
		return "up"
	case [2]int{-1, 0}:
		return "upperleft"
	case [2]int{-1, +1}:
		return "lowerleft"
	case [2]int{0, +1}:
		return "down"
	}
	return "down"
}
