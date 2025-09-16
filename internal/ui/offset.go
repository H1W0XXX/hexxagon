// File ui/offset.go
package ui

import (
	"github.com/hajimehoshi/ebiten/v2"
	"hexxagon_go/internal/assets"
	"image"
	"math"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

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
var trimOffsets = map[string][]struct{ X, Y int }{}

func getTrimOffset(key string, i int) (float64, float64) {
	if arr, ok := trimOffsets[key]; ok && i >= 0 && i < len(arr) {
		return float64(arr[i].X), float64(arr[i].Y)
	}
	return 0, 0
}

var (
	spriteScale float64 = 0.4
	shrinkOnce  sync.Once
)

// 目标清晰度：源纹理密度 ≈ 屏幕像素的 2 倍（很锐但不浪费）
const oversampleTarget = 2.0

// 根据“当前(未缩)tile图片算出的 boardScale”估算合适缩放
func setSpriteScale(boardScaleBefore float64) {
	s := oversampleTarget * boardScaleBefore // S = 2 * boardScale
	if s > 1 {
		s = 1
	}
	if s < 0.05 { // 给个保底，避免极端过小
		s = 0.05
	}
	spriteScale = s
}

func scaleImage(src *ebiten.Image, s float64) *ebiten.Image {
	if src == nil || s == 1 {
		return src
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	nw := int(math.Max(1, math.Round(float64(w)*s)))
	nh := int(math.Max(1, math.Round(float64(h)*s)))
	dst := ebiten.NewImage(nw, nh)

	op := &ebiten.DrawImageOptions{}
	op.Filter = ebiten.FilterLinear
	op.GeoM.Scale(s, s) // 把大图缩绘到小图里
	dst.DrawImage(src, op)
	return dst
}

// 一次性：把动画帧缩小，并把动画锚点等比缩小
// 找到非透明像素的包围盒（alpha>0）
func alphaBBox(img *ebiten.Image) (minX, minY, maxX, maxY int, ok bool) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	buf := make([]byte, 4*w*h)
	img.ReadPixels(buf)

	minX, minY = w, h
	maxX, maxY = -1, -1

	for y := 0; y < h; y++ {
		row := y * w * 4
		for x := 0; x < w; x++ {
			a := buf[row+x*4+3]
			if a != 0 { // 只要有一点点不透明
				if x < minX {
					minX = x
				}
				if y < minY {
					minY = y
				}
				if x > maxX {
					maxX = x
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < 0 {
		return 0, 0, 0, 0, false // 全透明
	}
	// 右边/下边要 +1 才是开区间
	return minX, minY, maxX + 1, maxY + 1, true
}

// 把 img 裁成 bbox 区域的新纹理，并返回左上角偏移
func cropToBBox(img *ebiten.Image, bbox image.Rectangle) (*ebiten.Image, int, int) {
	nw, nh := bbox.Dx(), bbox.Dy()
	dst := ebiten.NewImage(nw, nh)
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(float64(-bbox.Min.X), float64(-bbox.Min.Y))
	dst.DrawImage(img, op)
	// 显式释放旧纹理
	img.Dispose()
	return dst, bbox.Min.X, bbox.Min.Y
}

func disposeFrames(frames []*ebiten.Image) {
	for i := range frames {
		if frames[i] != nil {
			frames[i].Dispose()
			frames[i] = nil
		}
	}
}

// 按缩放 → 紧致裁剪 → 回填 AX/AY 补偿
func shrinkAllSprites() {
	for k, frames := range assets.AnimFrames {
		if len(frames) == 0 {
			continue
		}
		out := make([]*ebiten.Image, len(frames))
		d := assets.AnimDatas[k]

		// 只做等比缩放
		ax := d.AX * spriteScale
		ay := d.AY * spriteScale

		perFrameTrim := make([]struct{ X, Y int }, len(frames))

		for i, f := range frames {
			small := scaleImage(f, spriteScale)

			// 计算 alpha 包围盒，裁剪后记录“左上角被裁掉多少像素”
			if minX, minY, maxX, maxY, ok := alphaBBox(small); ok {
				var dx, dy int
				small, dx, dy = cropToBBox(small, image.Rect(minX, minY, maxX, maxY))
				perFrameTrim[i] = struct{ X, Y int }{X: dx, Y: dy}
			}
			out[i] = small

			if f != nil {
				f.Dispose()
				frames[i] = nil
			}
		}

		// 覆盖两张表，并保存每帧裁剪偏移
		assets.AnimFrames[k] = out
		d.Frames = out
		d.AX, d.AY = ax, ay // 仅缩放，不做裁剪补偿
		assets.AnimDatas[k] = d
		trimOffsets[k] = perFrameTrim // 记录到全局
	}

	runtime.GC()
	debug.FreeOSMemory()
}

// 读取你写死的偏移时，自动乘同一比例（offset 表本身不改）
func getScaledOffset(key string) (float64, float64) {
	o := AnimOffset[key]
	return o.X * spriteScale, o.Y * spriteScale
}
