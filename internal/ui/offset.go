// File ui/offset.go
package ui

import "time"

// AnimOffset 给每个动画 key 一个手动微调 (X, Y)，单位：像素
var AnimOffset = map[string]struct{ X, Y float64 }{
	// ↓ redClone
	"redClone/down":       {X: -130, Y: -450}, //
	"redClone/lowerleft":  {X: -560, Y: -490}, //
	"redClone/lowerright": {X: -150, Y: -500}, //
	"redClone/up":         {X: -140, Y: -550}, //
	"redClone/upperleft":  {X: -600, Y: -420}, //
	"redClone/upperright": {X: -200, Y: -450}, //

	// ↓ redJump动画
	"redJump/01": {X: 0, Y: -350},    //
	"redJump/02": {X: -50, Y: -400},  //
	"redJump/03": {X: 50, Y: -400},   //
	"redJump/04": {X: 0, Y: -300},    //
	"redJump/05": {X: -100, Y: -500}, //
	"redJump/06": {X: -150, Y: -500}, //
	"redJump/07": {X: -650, Y: -600}, //
	"redJump/08": {X: -600, Y: -500}, //
	"redJump/09": {X: -700, Y: -600}, //
	"redJump/10": {X: -650, Y: -600}, //
	"redJump/11": {X: -650, Y: -600}, //
	"redJump/12": {X: -600, Y: -600}, //

	// ↓ whiteClone
	"whiteClone/down":       {X: -600, Y: -700}, //
	"whiteClone/lowerleft":  {X: -600, Y: -670}, //
	"whiteClone/lowerright": {X: -500, Y: -650}, //
	"whiteClone/up":         {X: -600, Y: -350}, //
	"whiteClone/upperleft":  {X: -600, Y: -600}, //
	"whiteClone/upperright": {X: -600, Y: -600}, //

	// ↓ whiteJump动画
	"whiteJump/01": {X: -500, Y: -500}, //？
	"whiteJump/02": {X: -500, Y: -600}, //？
	"whiteJump/03": {X: -400, Y: -600}, //
	"whiteJump/04": {X: -500, Y: -500}, //
	"whiteJump/05": {X: -500, Y: -500}, //
	"whiteJump/06": {X: -600, Y: -400},
	"whiteJump/07": {X: -500, Y: -500}, //？
	"whiteJump/08": {X: -850, Y: -400},
	"whiteJump/09": {X: -650, Y: -550}, //
	"whiteJump/10": {X: -650, Y: -550},
	"whiteJump/11": {X: -600, Y: -500}, //
	"whiteJump/12": {X: -600, Y: -400}, //？

	// ↓ 感染动画（不分方向）
	"redEatWhite":             {X: 0, Y: 0}, //
	"whiteEatRed":             {X: 0, Y: 0}, //
	"afterRedInfectedByWhite": {X: 0, Y: 0}, //

	// ↓ 变色动画（在格子中心贴图）
	"redBecomeWhite": {X: 35, Y: -1},
	"whiteBecomeRed": {X: 117, Y: -1},
}

var soundDurations = map[string]time.Duration{
	"white_split":              470 * time.Millisecond,
	"white_jump":               496 * time.Millisecond,
	"white_capture_red_before": 653 * time.Millisecond,
	"white_capture_red_after":  548 * time.Millisecond,
	"all_capture_after":        400 * time.Millisecond,
	// 如果还有别的 key 也记得加上
}
