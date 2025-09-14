// File: cmd/anim_tuner/main.go
package main

import (
	"encoding/json"
	"fmt"
	"image/color"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text"
	"golang.org/x/image/font/basicfont"

	"hexxagon_go/internal/assets"
	"hexxagon_go/internal/game"
	"hexxagon_go/internal/ui"
)

const (
	WindowW      = 1000
	WindowH      = 720
	boardRadius  = ui.BoardRadius
	saveFilename = "anim_offset.json"
)

type Offset struct{ X, Y float64 }

type Tuner struct {
	// resources
	tileImg        *ebiten.Image
	pieceRed       *ebiten.Image
	pieceWhite     *ebiten.Image
	hintGreen      *ebiten.Image
	hintYellow     *ebiten.Image
	font           = basicfont.Face7x13
	board          *game.Board
	scale          float64
	originX        float64
	originY        float64
	tileW, tileH   int
	vs             float64
	// selection
	fromSelected   *game.HexCoord
	toSelected     *game.HexCoord
	// playback
	playerColor    game.CellState // PlayerA=red, PlayerB=white
	animType       string         // "Clone" or "Jump"
	animKey        string         // e.g. "redClone/upperleft"
	angle          float64
	play           bool
	fps            float64
	speed          float64
	frameIdx       float64
	frames         []*ebiten.Image
	anchorAX       float64
	anchorAY       float64
	// offset tuning
	offsetMap      map[string]Offset // loaded/saved
	curOffset      Offset            // live editing for current key
	dragging       bool
	dragStartX     float64
	dragStartY     float64
	dragStartOff   Offset
	// misc
	last           time.Time
	helpOn         bool
}

func NewTuner() (*Tuner, error) {
	t := &Tuner{
		board:       game.NewBoard(boardRadius),
		playerColor: game.PlayerA,
		animType:    "Clone",
		fps:         30,
		speed:       0.25,  // 慢速回放
		play:        true,
		offsetMap:   map[string]Offset{},
		helpOn:      true,
	}

	var err error
	if t.tileImg, err = assets.LoadImage("hex_space"); err != nil {
		return nil, err
	}
	if t.pieceRed, err = assets.LoadImage("red_piece"); err != nil {
		return nil, err
	}
	if t.pieceWhite, err = assets.LoadImage("white_piece"); err != nil {
		return nil, err
	}
	if t.hintGreen, err = assets.LoadImage("move_hint_green"); err != nil {
		return nil, err
	}
	if t.hintYellow, err = assets.LoadImage("move_hint_yellow"); err != nil {
		return nil, err
	}

	t.scale, t.originX, t.originY, t.tileW, t.tileH, t.vs = boardTransform(t.tileImg)

	// 载入已有偏移（可选）
	_ = t.loadOffsets(saveFilename)

	// 初始一个可点的默认起点终点（中心与其上方）
	c0 := game.HexCoord{Q: 0, R: 0}
	c1 := game.HexCoord{Q: 0, R: -1}
	t.fromSelected = &c0
	t.toSelected = &c1
	t.rebuildAnimKeyAndFrames()

	return t, nil
}

func (t *Tuner) Update() error {
	now := time.Now()
	if !t.last.IsZero() {
		dt := now.Sub(t.last).Seconds()
		if t.play && len(t.frames) > 0 {
			t.frameIdx += t.fps * t.speed * dt
			if t.frameIdx >= float64(len(t.frames)) {
				t.frameIdx = 0
			}
		}
	}
	t.last = now

	// 鼠标选格：左键第一次选 from，第二次选 to
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		mx, my := ebiten.CursorPosition()
		qr, ok := t.pixelToHex(float64(mx), float64(my))
		if ok {
			if t.fromSelected == nil {
				tmp := qr
				t.fromSelected = &tmp
			} else if t.toSelected == nil {
				tmp := qr
				t.toSelected = &tmp
				t.rebuildAnimKeyAndFrames()
			} else {
				// 两个都选过了，再点则替换离鼠标更近的那个
				if t.distToCell(*t.fromSelected, float64(mx), float64(my)) <
					t.distToCell(*t.toSelected, float64(mx), float64(my)) {
					tmp := qr
					t.fromSelected = &tmp
				} else {
					tmp := qr
					t.toSelected = &tmp
				}
				t.rebuildAnimKeyAndFrames()
			}
		}
	}

	// 右键：拖拽偏移
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonRight) {
		t.dragging = true
		x, y := ebiten.CursorPosition()
		t.dragStartX, t.dragStartY = float64(x), float64(y)
		t.dragStartOff = t.curOffset
	}
	if t.dragging && ebiten.IsMouseButtonPressed(ebiten.MouseButtonRight) {
		x, y := ebiten.CursorPosition()
		dx := float64(x) - t.dragStartX
		dy := float64(y) - t.dragStartY
		t.curOffset.X = t.dragStartOff.X + dx
		t.curOffset.Y = t.dragStartOff.Y + dy
		t.offsetMap[t.animKey] = t.curOffset
	}
	if t.dragging && inpututil.IsMouseButtonJustReleased(ebiten.MouseButtonRight) {
		t.dragging = false
	}

	// 键盘控制
	// 空格：播放/暂停
	if inpututil.IsKeyJustPressed(ebiten.KeySpace) {
		t.play = !t.play
	}

	// 左右：逐帧
	if inpututil.IsKeyJustPressed(ebiten.KeyLeft) && len(t.frames) > 0 {
		t.play = false
		t.frameIdx--
		if t.frameIdx < 0 {
			t.frameIdx = float64(len(t.frames) - 1)
		}
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyRight) && len(t.frames) > 0 {
		t.play = false
		t.frameIdx++
		if int(t.frameIdx) >= len(t.frames) {
			t.frameIdx = 0
		}
	}

	// 加减速度
	if ebiten.IsKeyPressed(ebiten.KeyBracketLeft) {
		t.speed = math.Max(0.01, t.speed-0.01)
	}
	if ebiten.IsKeyPressed(ebiten.KeyBracketRight) {
		t.speed = math.Min(3, t.speed+0.01)
	}

	// 颜色切换：1=红 2=白
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit1) {
		t.playerColor = game.PlayerA
		t.rebuildAnimKeyAndFrames()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyDigit2) {
		t.playerColor = game.PlayerB
		t.rebuildAnimKeyAndFrames()
	}

	// 类型切换：C=Clone, J=Jump
	if inpututil.IsKeyJustPressed(ebiten.KeyC) {
		t.animType = "Clone"
		t.rebuildAnimKeyAndFrames()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyJ) {
		t.animType = "Jump"
		t.rebuildAnimKeyAndFrames()
	}

	// 偏移微调：方向键；Shift=×10
	step := 1.0
	if ebiten.IsKeyPressed(ebiten.KeyShift) {
		step = 10
	}
	if ebiten.IsKeyPressed(ebiten.KeyUp) {
		t.curOffset.Y -= step
		t.offsetMap[t.animKey] = t.curOffset
	}
	if ebiten.IsKeyPressed(ebiten.KeyDown) {
		t.curOffset.Y += step
		t.offsetMap[t.animKey] = t.curOffset
	}
	if ebiten.IsKeyPressed(ebiten.KeyLeft) && !inpututil.IsKeyJustPressed(ebiten.KeyLeft) {
		t.curOffset.X -= step
		t.offsetMap[t.animKey] = t.curOffset
	}
	if ebiten.IsKeyPressed(ebiten.KeyRight) && !inpututil.IsKeyJustPressed(ebiten.KeyRight) {
		t.curOffset.X += step
		t.offsetMap[t.animKey] = t.curOffset
	}

	// 保存/加载
	if inpututil.IsKeyJustPressed(ebiten.KeyS) {
		_ = t.saveOffsets(saveFilename)
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyL) {
		_ = t.loadOffsets(saveFilename)
		t.curOffset = t.offsetMap[t.animKey] // 应用
	}

	// 重置当前key偏移
	if inpututil.IsKeyJustPressed(ebiten.KeyR) {
		delete(t.offsetMap, t.animKey)
		t.curOffset = Offset{}
	}

	// 帮助
	if inpututil.IsKeyJustPressed(ebiten.KeyH) {
		t.helpOn = !t.helpOn
	}

	return nil
}

func (t *Tuner) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{20, 20, 20, 255})

	// 画棋盘与起点/终点提示
	ui.DrawBoardOnly(screen, t.board, t.tileImg) // 只画格子

	// 高亮 from/to
	if t.fromSelected != nil {
		t.drawCellHint(screen, *t.fromSelected, t.hintGreen)
	}
	if t.toSelected != nil {
		t.drawCellHint(screen, *t.toSelected, t.hintYellow)
	}
	// 画起点棋子（用当前颜色），终点位置画一个半透明棋子辅助对齐
	if t.fromSelected != nil {
		t.drawPieceAt(screen, *t.fromSelected, t.pieceFor(t.playerColor), 1.0)
	}
	if t.toSelected != nil {
		t.drawPieceAt(screen, *t.toSelected, t.pieceFor(t.playerColor), 0.35)
	}

	// 画动画当前帧（带偏移/锚点/角度）
	img := t.currentFrame()
	if img != nil && t.toSelected != nil {
		op := &ebiten.DrawImageOptions{}
		w, h := img.Size()

		// 将锚点移动到(0,0) → 旋转 → 缩放 → 平移
		op.GeoM.Translate(-t.anchorAX, -t.anchorAY)
		op.GeoM.Rotate(t.angle)
		op.GeoM.Scale(t.scale, t.scale)

		// 计算该格左上角（未缩放）
		ax := t.anchorAX
		ay := t.anchorAY
		dest := *t.toSelected
		x0 := (float64(dest.Q)+boardRadius)*float64(t.tileW)*0.75 + ax + t.curOffset.X
		y0 := (float64(dest.R)+boardRadius+float64(dest.Q)/2)*t.vs + ay + t.curOffset.Y

		// 平移到屏幕坐标
		op.GeoM.Translate(t.originX+x0*t.scale, t.originY+y0*t.scale)

		screen.DrawImage(img, op)

		// 可视化锚点
		drawCross(screen, t.originX+(x0)*t.scale, t.originY+(y0)*t.scale, color.RGBA{255, 120, 40, 255})
		_ = w
		_ = h
	}

	// HUD
	y := 18
	write := func(s string, c color.Color) {
		text.Draw(screen, s, t.font, 12, y, c)
		y += 18
	}

	write(fmt.Sprintf("From: %v  To: %v", t.fromSelected, t.toSelected), color.White)
	write(fmt.Sprintf("Color: %s  Type: %s  Key: %s", colorName(t.playerColor), t.animType, t.animKey), color.White)
	write(fmt.Sprintf("FPS: %.0f  Speed: %.2fx  Frame: %d/%d  Angle: %.1f°",
		t.fps, t.speed, int(t.frameIdx)%maxi(1, len(t.frames)), len(t.frames), t.angle*180/math.Pi), color.White)
	write(fmt.Sprintf("Offset: X=%.1f  Y=%.1f  (右键拖拽 / 方向键微调，Shift×10)", t.curOffset.X, t.curOffset.Y), color.RGBA{180, 255, 180, 255})
	write(fmt.Sprintf("Save: S   Load: L   Reset Current Key: R   Help: H"), color.Gray{200})

	if t.helpOn {
		y += 6
		write("[操作提示]", color.RGBA{120, 200, 255, 255})
		write("左键依次选择起点与终点；再次点击替换更近的一个。", color.Gray{220})
		write("1/2 切换红/白；C/J 切换 Clone/Jump。空格 播放/暂停；[/] 调速。←/→ 逐帧。", color.Gray{220})
		write("右键按住拖动偏移；方向键微调，Shift=×10；R 重置当前 Key 偏移。", color.Gray{220})
		write("S 保存到 anim_offset.json；L 加载。把 JSON 内容合并到代码中的 AnimOffset 即可。", color.Gray{220})

		// 列出当前 JSON 里已有的 keys
		keys := make([]string, 0, len(t.offsetMap))
		for k := range t.offsetMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			write("已保存的 Keys:", color.RGBA{220, 220, 255, 255})
			for i := 0; i < len(keys) && i < 10; i++ {
				k := keys[i]
				off := t.offsetMap[k]
				write("  "+k+fmt.Sprintf(" => {X:%.1f, Y:%.1f}", off.X, off.Y), color.Gray{200})
			}
			if len(keys) > 10 {
				write(fmt.Sprintf("  ... 共 %d 项", len(keys)), color.Gray{200})
			}
		}
	}
}

func (t *Tuner) Layout(outsideW, outsideH int) (int, int) { return WindowW, WindowH }

func (t *Tuner) currentFrame() *ebiten.Image {
	if len(t.frames) == 0 {
		return nil
	}
	idx := int(t.frameIdx) % len(t.frames)
	return t.frames[idx]
}

func (t *Tuner) pieceFor(p game.CellState) *ebiten.Image {
	if p == game.PlayerA {
		return t.pieceRed
	}
	return t.pieceWhite
}

func (t *Tuner) drawPieceAt(dst *ebiten.Image, c game.HexCoord, img *ebiten.Image, alpha float64) {
	op := &ebiten.DrawImageOptions{}
	op.ColorScale.Scale(1, 1, 1, float32(alpha))
	cx := (float64(c.Q)+boardRadius)*float64(t.tileW)*0.75 + float64(t.tileW)/2
	cy := (float64(c.R)+boardRadius+float64(c.Q)/2)*t.vs + float64(t.tileH)/2
	cx = t.originX + cx*t.scale
	cy = t.originY + cy*t.scale
	w, h := img.Size()
	op.GeoM.Translate(-float64(w)/2, -float64(h)/2)
	op.GeoM.Scale(t.scale, t.scale)
	op.GeoM.Translate(cx, cy)
	dst.DrawImage(img, op)
}

func (t *Tuner) drawCellHint(dst *ebiten.Image, c game.HexCoord, img *ebiten.Image) {
	op := &ebiten.DrawImageOptions{}
	x := (float64(c.Q)+boardRadius)*float64(t.tileW)*0.75
	y := (float64(c.R)+boardRadius+float64(c.Q)/2) * t.vs
	op.GeoM.Scale(t.scale, t.scale)
	op.GeoM.Translate(t.originX+x*t.scale, t.originY+y*t.scale)
	dst.DrawImage(img, op)
}

func (t *Tuner) pixelToHex(px, py float64) (game.HexCoord, bool) {
	// 反向坐标：把屏幕坐标转到棋盘未缩放空间，然后取最近格
	x := (px - t.originX) / t.scale
	y := (py - t.originY) / t.scale
	// 粗暴枚举找最近（半径只有4，性能足够）
	best := game.HexCoord{}
	bestD := 1e18
	found := false
	for _, c := range t.board.AllCoords() {
		cx := (float64(c.Q)+boardRadius)*float64(t.tileW)*0.75 + float64(t.tileW)/2
		cy := (float64(c.R)+boardRadius+float64(c.Q)/2)*t.vs + float64(t.tileH)/2
		d := (cx-x)*(cx-x) + (cy-y)*(cy-y)
		if d < bestD {
			bestD = d
			best = c
			found = true
		}
	}
	return best, found
}

func (t *Tuner) rebuildAnimKeyAndFrames() {
	if t.fromSelected == nil || t.toSelected == nil {
		t.animKey = ""
		t.frames = nil
		return
	}
	dir := directionKey(*t.fromSelected, *t.toSelected)
	col := "red"
	if t.playerColor == game.PlayerB {
		col = "white"
	}
	key := col + t.animType + "/" + dir
	t.animKey = key
	// 角度（只在感染旋转用，移动帧不需要旋转；保持0）
	t.angle = 0

	// 取帧、锚点与已保存偏移
	t.frames = assets.AnimFrames[key]
	if data, ok := assets.AnimDatas[key]; ok {
		t.anchorAX = data.AX
		t.anchorAY = data.AY
	} else {
		t.anchorAX, t.anchorAY = 0, 0
	}
	if off, ok := t.offsetMap[key]; ok {
		t.curOffset = off
	} else {
		t.curOffset = Offset{}
	}

	// 起点终点距离显示一下（调试用）
	d := game.HexDist(*t.fromSelected, *t.toSelected)
	log.Printf("[key=%s] frames=%d  dist=%d  anchor(%.1f,%.1f)  curOff(%.1f,%.1f)",
		key, len(t.frames), d, t.anchorAX, t.anchorAY, t.curOffset.X, t.curOffset.Y)
}

// ———— utils ————

func colorName(p game.CellState) string {
	if p == game.PlayerA {
		return "Red"
	}
	return "White"
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func directionKey(from, to game.HexCoord) string {
	dq := to.Q - from.Q
	dr := to.R - from.R
	// Axial 6 方向
	switch {
	case dq == 0 && dr < 0:
		return "up"
	case dq == 0 && dr > 0:
		return "down"
	case dq < 0 && dr == 0:
		return "upperleft"
	case dq > 0 && dr == 0:
		return "lowerright"
	case dq < 0 && dr > 0:
		return "lowerleft"
	case dq > 0 && dr < 0:
		return "upperright"
	}
	// 非邻接（跳跃）时，用“朝向”近似：归一到六方向
	ang := math.Atan2(float64(dr), float64(dq))
	// 将角度切成6扇区
	sector := int(math.Round((ang / math.Pi) * 3)) // -3..3
	switch (sector + 6) % 6 {
	case 0:
		return "lowerright"
	case 1:
		return "down"
	case 2:
		return "lowerleft"
	case 3:
		return "upperleft"
	case 4:
		return "up"
	default:
		return "upperright"
	}
}

func boardTransform(tileImg *ebiten.Image) (float64, float64, float64, int, int, float64) {
	tileW := tileImg.Bounds().Dx()
	tileH := tileImg.Bounds().Dy()
	vs := float64(tileH) * math.Sqrt(3) / 2

	cols, rows := 2*boardRadius+1, 2*boardRadius+1
	boardW := float64(cols-1)*float64(tileW)*0.75 + float64(tileW)
	boardH := vs*float64(rows-1) + float64(tileH)

	scale := math.Min(float64(WindowW)/boardW, float64(WindowH)/boardH)
	originX := (float64(WindowW) - boardW*scale) / 2
	originY := (float64(WindowH) - boardH*scale) / 2
	return scale, originX, originY, tileW, tileH, vs
}

func drawCross(dst *ebiten.Image, x, y float64, c color.Color) {
	for dx := -6; dx <= 6; dx++ {
		dst.Set(int(x)+dx, int(y), c)
	}
	for dy := -6; dy <= 6; dy++ {
		dst.Set(int(x), int(y)+dy, c)
	}
}

func (t *Tuner) saveOffsets(path string) error {
	tmp := make(map[string][2]float64, len(t.offsetMap))
	for k, v := range t.offsetMap {
		tmp[k] = [2]float64{v.X, v.Y}
	}
	b, err := json.MarshalIndent(tmp, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		return err
	}
	log.Printf("saved offsets → %s", path)
	return nil
}

func (t *Tuner) loadOffsets(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var tmp map[string][2]float64
	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}
	t.offsetMap = map[string]Offset{}
	for k, v := range tmp {
		t.offsetMap[k] = Offset{X: v[0], Y: v[1]}
	}
	log.Printf("loaded offsets from %s (count=%d)", path, len(t.offsetMap))
	return nil
}

// small helper to export current key as a Go line (optional, press 'E')
func (t *Tuner) exportGoLine() string {
	// "redClone/upperleft": {X: -60, Y: -65},
	off := t.curOffset
	return strconv.Quote(t.animKey) + ": {X: " + fmt.Sprintf("%.0f", off.X) + ", Y: " + fmt.Sprintf("%.0f", off.Y) + "},"
}

func main() {
	// 工作目录打印（方便找到输出的 anim_offset.json）
	wd, _ := os.Getwd()
	log.Println("Working dir:", wd)
	log.Println("If you want, copy anim_offset.json into your repo root or", filepath.Join(wd, saveFilename))

	t, err := NewTuner()
	if err != nil {
		log.Fatal(err)
	}
	ebiten.SetWindowSize(WindowW, WindowH)
	ebiten.SetWindowTitle("Hexxagon Anim Tuner (拖拽偏移/慢播/逐帧/保存)")
	ebiten.SetTPS(60)
	if err := ebiten.RunGame(t); err != nil {
		log.Fatal(err)
	}
}

// ———— 仅供 ui.DrawBoardOnly 使用的小适配 ————
// 你在 ui 包里已经有一个 DrawBoardAndPiecesWithHints，这里要一个“不画棋子”的版本。
// 如果你已有类似函数，可直接删除此段并改成调用已有的。
package ui

import (
"github.com/hajimehoshi/ebiten/v2"
"hexxagon_go/internal/game"
)

func DrawBoardOnly(dst *ebiten.Image, b *game.Board, tile *ebiten.Image) {
	boardScale, originX, originY, tileW, tileH, vs := boardTransform(tile)
	_ = tileH
	_ = vs
	for _, c := range b.AllCoords() {
		x := (float64(c.Q)+BoardRadius)*float64(tileW)*0.75
		y := (float64(c.R)+BoardRadius+float64(c.Q)/2) * vs
		op := &ebiten.DrawImageOptions{}
		op.GeoM.Scale(boardScale, boardScale)
		op.GeoM.Translate(originX+x*boardScale, originY+y*boardScale)
		dst.DrawImage(tile, op)
	}
}

// 复用 ui.boardTransform
