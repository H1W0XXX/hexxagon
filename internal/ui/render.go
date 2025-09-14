// File /ui/render.go
package ui

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text"
	"golang.org/x/image/font/basicfont"
	"hexxagon_go/internal/game"
	"image/color"
	"math"
)

// 渐变 shader，修复了坐标计算
const gradKage = `
package main

var UBright float // 左上角亮度，比如 1.35
var UDark   float // 右下角亮度，比如 0.70

func Fragment(pos vec4, uv vec2, col vec4) vec4 {
    // 取底图颜色（Images[0]）
    c := imageSrc0At(uv)

    // 从左上角(0,0)到右下角(1,1)的线性渐变
    // t 从 0(左上) 到 1(右下)
    t := clamp((uv.x + uv.y) * 0.5, 0.0, 1.0)
    f := mix(UBright, UDark, t)

    return vec4(c.rgb * f, c.a)
}
`

var gradShader *ebiten.Shader

func init() {
	s, err := ebiten.NewShader([]byte(gradKage))
	if err != nil {
		panic(err)
	}
	gradShader = s
}

// 放到文件顶部做简单缓存
var hexBaseCache = map[[2]int]*ebiten.Image{}

// 生成一个与 tile 同尺寸的实心六边形底色
func hexBase(w, h int, fill color.Color) *ebiten.Image {
	key := [2]int{w, h}
	if img := hexBaseCache[key]; img != nil {
		return img
	}

	// 2x 超采样，在更大画布上画，再缩回原大小，边缘更顺滑
	const spp = 2
	W, H := w*spp, h*spp

	// 六边形顶点（竖向扁一点的正六边形，和你的布局参数一致）
	vs := float64(H) * math.Sqrt(3) / 2 // 行高
	cx, cy := float64(W)/2, float64(H)/2
	r := float64(H) / 2 * 0.92 // 稍缩一点避免露边

	pts := [6][2]float32{
		{float32(cx + r), float32(cy)},
		{float32(cx + r/2), float32(cy + vs/2)},
		{float32(cx - r/2), float32(cy + vs/2)},
		{float32(cx - r), float32(cy)},
		{float32(cx - r/2), float32(cy - vs/2)},
		{float32(cx + r/2), float32(cy - vs/2)},
	}

	// 用 1x1 白图 + 顶点色 来填充多三角形
	white := ebiten.NewImage(1, 1)
	white.Fill(color.White)

	big := ebiten.NewImage(W, H) // 透明背景

	// 六角形可以用"中心扇形"分成 6 个三角形
	fillRGBA := color.RGBA64Model.Convert(fill).(color.RGBA64)
	center := ebiten.Vertex{
		DstX:   float32(cx),
		DstY:   float32(cy),
		ColorR: float32(fillRGBA.R) / 65535,
		ColorG: float32(fillRGBA.G) / 65535,
		ColorB: float32(fillRGBA.B) / 65535,
		ColorA: float32(fillRGBA.A) / 65535,
	}

	// 依次画 6 个三角形 (center, pts[i], pts[i+1])
	for i := 0; i < 6; i++ {
		j := (i + 1) % 6
		v1 := center
		v2 := ebiten.Vertex{DstX: pts[i][0], DstY: pts[i][1], ColorR: v1.ColorR, ColorG: v1.ColorG, ColorB: v1.ColorB, ColorA: v1.ColorA}
		v3 := ebiten.Vertex{DstX: pts[j][0], DstY: pts[j][1], ColorR: v1.ColorR, ColorG: v1.ColorG, ColorB: v1.ColorB, ColorA: v1.ColorA}
		// 贴 1x1 白图，UV 固定 0..1 都行
		v1.SrcX, v1.SrcY = 0, 0
		v2.SrcX, v2.SrcY = 1, 0
		v3.SrcX, v3.SrcY = 0, 1
		big.DrawTriangles([]ebiten.Vertex{v1, v2, v3}, []uint16{0, 1, 2}, white, nil)
	}

	// 缩回到 w×h（线性过滤做下采样防锯齿）
	small := ebiten.NewImage(w, h)
	op := &ebiten.DrawImageOptions{}
	op.Filter = ebiten.FilterLinear
	op.GeoM.Scale(1.0/float64(spp), 1.0/float64(spp))
	small.DrawImage(big, op)

	hexBaseCache[key] = small
	return small
}

// 在中心坐标的基础上，上下额外加 gapY 像素间距
func drawHexHintXY(
	dst *ebiten.Image, img *ebiten.Image, c game.HexCoord,
	originX, originY float64,
	tileW, tileH int, vs, scale float64,
	sx, sy float64, // sx=1 保持X不变；sy<1 就是只压扁Y
) {
	// axial -> pixel
	x0 := float64(c.Q) * float64(tileW) * 0.75
	y0 := vs * (float64(c.R) + float64(c.Q)/2)
	xpix := x0 + float64(BoardRadius)*float64(tileW)*0.75
	ypix := y0 + float64(BoardRadius)*vs

	// 瓦片中心（放大后）
	cx := originX + (xpix+float64(tileW)/2)*scale
	cy := originY + (ypix+float64(tileH)/2)*scale

	// 以中心为锚点缩放 + 平移
	w := float64(img.Bounds().Dx())
	h := float64(img.Bounds().Dy())
	drawW := w * scale * sx
	drawH := h * scale * sy

	op := &ebiten.DrawImageOptions{}
	op.Filter = ebiten.FilterLinear
	op.GeoM.Scale(scale*sx, scale*sy)
	op.GeoM.Translate(cx-drawW/2, cy-drawH/2)
	dst.DrawImage(img, op)
}

// 添加一个用于缓存渐变结果的变量，避免每帧重新计算
var (
	lastBoardLayer *ebiten.Image
	cachedShaded   *ebiten.Image
)

// DrawBoardAndPiecesWithHints 在 dst 上绘制棋盘、提示和棋子。
func DrawBoardAndPiecesWithHints(
	dst *ebiten.Image,
	board *game.Board,
	tileImg *ebiten.Image,
	hintGreenImg *ebiten.Image,
	hintYellowImg *ebiten.Image,
	pieceImgs map[game.CellState]*ebiten.Image,
	selected *game.HexCoord,
) {
	// 清空目标图像
	dst.Clear()

	// 0) 预计算 cloneTargets/jumpTargets
	cloneTargets := map[game.HexCoord]struct{}{}
	jumpTargets := map[game.HexCoord]struct{}{}
	if selected != nil {
		from := *selected
		fromIdx, ok := game.IndexOf[from] // 坐标转下标
		if ok {
			// 克隆目标：1 步邻居
			for _, toIdx := range game.NeighI[fromIdx] {
				if board.Cells[toIdx] == game.Empty {
					cloneTargets[game.CoordOf[toIdx]] = struct{}{}
				}
			}
			// 跳跃目标：2 步邻居
			for _, toIdx := range game.JumpI[fromIdx] {
				if board.Cells[toIdx] == game.Empty {
					jumpTargets[game.CoordOf[toIdx]] = struct{}{}
				}
			}
		}
	}

	// 1) 计算瓦片原始尺寸与竖直行高
	tileW := tileImg.Bounds().Dx()
	tileH := tileImg.Bounds().Dy()
	vs := float64(tileH) * math.Sqrt(3) / 2

	// 2) 计算棋盘在原始尺寸下的宽高
	cols := 2*BoardRadius + 1
	rows := 2*BoardRadius + 1
	boardW := float64(cols-1)*float64(tileW)*0.75 + float64(tileW)
	boardH := vs*float64(rows-1) + float64(tileH)

	// 3) 同时适配宽度和高度：scaleX, scaleY，取最小值
	scaleX := float64(WindowWidth) / boardW
	scaleY := float64(WindowHeight) / boardH
	scale := math.Min(scaleX, scaleY)

	// 4) 计算居中偏移：让棋盘在 dst（800×600）中央
	originX := (float64(WindowWidth) - boardW*scale) / 2
	originY := (float64(WindowHeight) - boardH*scale) / 2

	// 5) 先把"整盘底板（底色+紫环）"画到一个临时层 boardLayer
	boardLayer := ebiten.NewImage(WindowWidth, WindowHeight)
	base := hexBase(tileW, tileH, color.RGBA{49, 83, 127, 0xFF})

	// 想要上下各多出 gapY 像素（屏幕像素）
	hintSY := 0.9
	hintSX := 1.05
	for i := 0; i < game.BoardN; i++ {
		if board.Cells[i] == game.Blocked {
			continue
		}
		c := game.CoordOf[i]
		drawHexHintXY(boardLayer, base, c, originX, originY, tileW, tileH, vs, scale, hintSX, hintSY)
		// 再叠紫环贴图
		drawHexHintXY(boardLayer, tileImg, c, originX, originY, tileW, tileH, vs, scale, hintSX, hintSY)
	}

	// 6) 检查是否需要重新生成渐变（避免每帧都重新计算）
	needsUpdate := lastBoardLayer == nil || cachedShaded == nil
	if needsUpdate {
		// 用 Shader 对"整盘底板"做一次左上亮→右下暗的乘性渐变
		cachedShaded = ebiten.NewImage(WindowWidth, WindowHeight)
		op := &ebiten.DrawRectShaderOptions{}
		op.Images[0] = boardLayer // 确保 shader 有输入图像
		op.Uniforms = map[string]any{
			"UBright": float32(1.35),
			"UDark":   float32(0.70),
		}
		cachedShaded.DrawRectShader(WindowWidth, WindowHeight, gradShader, op)
		lastBoardLayer = boardLayer
	}

	// 7) 把渐变结果画到 dst
	dst.DrawImage(cachedShaded, nil)

	// 克隆/跳跃目标分别画（用已有的集合）
	for _, c := range board.AllCoords() {
		if _, ok := cloneTargets[c]; ok {
			drawHexHintXY(dst, hintGreenImg, c, originX, originY, tileW, tileH, vs, scale, hintSX, hintSY)
		}
	}
	for _, c := range board.AllCoords() {
		if _, ok := jumpTargets[c]; ok {
			drawHexHintXY(dst, hintYellowImg, c, originX, originY, tileW, tileH, vs, scale, hintSX, hintSY)
		}
	}

	// 9) 最后绘制棋子
	for i := 0; i < game.BoardN; i++ {
		st := board.Cells[i]
		if st == game.PlayerA || st == game.PlayerB {
			c := game.CoordOf[i]
			drawPiece(dst, pieceImgs[st], c, originX, originY, tileW, tileH, vs, scale)
		}
	}

	// +++ —— 在每个网格中心绘制轴坐标 (q,r) —— +++
	//for i := 0; i < game.BoardN; i++ {
	//	if board.Cells[i] == game.Blocked {
	//		continue
	//	}
	//	c := game.CoordOf[i]
	//
	//	// 计算该格中心的屏幕坐标（与你其他函数一致）
	//	x0 := (float64(c.Q) + float64(BoardRadius)) * float64(tileW) * 0.75
	//	y0 := (float64(c.R) + float64(BoardRadius) + float64(c.Q)/2) * vs
	//	cx := originX + (x0+float64(tileW)/2)*scale
	//	cy := originY + (y0+float64(tileH)/2)*scale
	//
	//	// 阴影 + 本体，提升对比度
	//	label := fmt.Sprintf("%d,%d", c.Q, c.R)
	//	drawTextCentered(dst, label, cx+1, cy+1, color.RGBA{0, 0, 0, 255})   // shadow
	//	drawTextCentered(dst, label, cx, cy, color.RGBA{250, 250, 250, 255}) // text
	//}
}

// drawHexHint 专门用于绘制提示框，支持缩放避免重叠
func drawHexHint(dst *ebiten.Image, img *ebiten.Image, c game.HexCoord,
	originX, originY float64,
	tileW, tileH int, vs, scale, hintScale float64,
) {
	// ① axial → pixel (相对棋盘中心)
	x0 := float64(c.Q) * float64(tileW) * 0.75
	y0 := vs * (float64(c.R) + float64(c.Q)/2)

	// ② 再把左上角当作 (0,0) —— 加半个棋盘宽/高
	xpix := x0 + float64(BoardRadius)*float64(tileW)*0.75
	ypix := y0 + float64(BoardRadius)*vs

	// ③ 计算提示图像的中心位置
	centerX := originX + (xpix+float64(tileW)/2)*scale
	centerY := originY + (ypix+float64(tileH)/2)*scale

	// ④ 计算放大后的尺寸
	imgW := float64(img.Bounds().Dx()) * scale * hintScale
	imgH := float64(img.Bounds().Dy()) * scale * hintScale

	op := &ebiten.DrawImageOptions{}
	op.Filter = ebiten.FilterLinear
	op.GeoM.Scale(scale*hintScale, scale*hintScale)
	// 从中心位置减去一半宽高来得到左上角位置
	op.GeoM.Translate(centerX-imgW/2, centerY-imgH/2)
	dst.DrawImage(img, op)
}

// drawHex 把一个瓦片或提示图等比放到 c 处
func drawHex(dst *ebiten.Image, img *ebiten.Image, c game.HexCoord,
	originX, originY float64,
	tileW, tileH int, vs, scale float64,
) {
	// ① axial → pixel (相对棋盘中心)
	x0 := float64(c.Q) * float64(tileW) * 0.75
	y0 := vs * (float64(c.R) + float64(c.Q)/2)

	// ② 再把左上角当作 (0,0) —— 加半个棋盘宽/高
	xpix := x0 + float64(BoardRadius)*float64(tileW)*0.75
	ypix := y0 + float64(BoardRadius)*vs

	op := &ebiten.DrawImageOptions{}
	op.Filter = ebiten.FilterLinear
	op.GeoM.Scale(scale, scale)
	op.GeoM.Translate(originX+xpix*scale, originY+ypix*scale)
	dst.DrawImage(img, op)
}

// drawPiece 把棋子图居中绘制到瓦片 c 的正中心
func drawPiece(dst *ebiten.Image, img *ebiten.Image, c game.HexCoord,
	originX, originY float64, tileW, tileH int, vs, scale float64) {

	// 瓦片左上角（已移到中心原点右下）
	x := (float64(c.Q) + float64(BoardRadius)) * float64(tileW) * 0.75
	y := (float64(c.R) + float64(BoardRadius) + (float64(c.Q) / 2)) * vs

	// 放大后瓦片中心
	cx := originX + (x+float64(tileW)/2)*scale
	cy := originY + (y+float64(tileH)/2)*scale

	pw, ph := float64(img.Bounds().Dx())*scale, float64(img.Bounds().Dy())*scale

	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(scale, scale)
	op.GeoM.Translate(cx-pw/2, cy-ph/2)
	dst.DrawImage(img, op)
}

// createCombined 将格子底图与棋子图合并，棋子居中于格子中央
func createCombined(tileImg, pieceImg *ebiten.Image) *ebiten.Image {
	w, h := tileImg.Bounds().Dx(), tileImg.Bounds().Dy()
	img := ebiten.NewImage(w, h)
	img.DrawImage(tileImg, nil)
	// 棋子偏移到中央
	pw, ph := pieceImg.Bounds().Dx(), pieceImg.Bounds().Dy()
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(float64(w-pw)/2, float64(h-ph)/2)
	img.DrawImage(pieceImg, op)
	return img
}

// axialToScreen 把一个 HexCoord 映射成 screen（窗口）像素坐标中心点
func axialToScreen(c game.HexCoord,
	tileImg *ebiten.Image, screen *ebiten.Image) (float64, float64) {

	// 1) 取出棋盘到 offscreen 的变换
	boardScale, originX, originY, tileW, tileH, vs := getBoardTransform(tileImg)

	// 2) 把 offscreen → screen 的缩放 & 居中
	w, h := screen.Bounds().Dx(), screen.Bounds().Dy()
	screenScale := math.Min(float64(w)/float64(WindowWidth),
		float64(h)/float64(WindowHeight))
	dx := (float64(w) - float64(WindowWidth)*screenScale) / 2
	dy := (float64(h) - float64(WindowHeight)*screenScale) / 2

	// 3) 在 offscreen 坐标系里算出该格子左上角
	x0 := (float64(c.Q) + BoardRadius) * float64(tileW) * 0.75
	y0 := (float64(c.R) + BoardRadius + float64(c.Q)/2) * vs
	// 再加半个瓦片宽高得到中心
	cx0 := x0 + float64(tileW)/2
	cy0 := y0 + float64(tileH)/2

	// 4) 把 offscreen 上的 (cx0,cy0) 缩放 & 平移到 screen
	offX := originX + cx0*boardScale
	offY := originY + cy0*boardScale
	sx := offX*screenScale + dx
	sy := offY*screenScale + dy
	return sx, sy
}

func (gs *GameScreen) refreshMoveScores() {
	if gs.selected == nil {
		gs.ui = UIState{}
		return
	}
	sel := *gs.selected
	player := gs.state.CurrentPlayer

	gs.ui.From = &sel
	gs.ui.MoveScores = make(map[game.HexCoord]float64)

	moves := game.GenerateMoves(gs.state.Board, player)
	for _, mv := range moves {
		if mv.From != sel {
			continue
		}

		bCopy := gs.state.Board.Clone()

		// 关键：告诉评估"上一手就是这步"
		bCopy.LastMove = mv

		// 真正把这步下到 bCopy 上（会翻子）
		if _, err := mv.Apply(bCopy, player); err != nil {
			continue
		}

		// 用静态评估（或深度=0 的 AlphaBetaNoTT）
		// score := game.AlphaBetaNoTT(bCopy, player, 0)
		score := game.Evaluate(bCopy, player)

		gs.ui.MoveScores[mv.To] = float64(score)
	}
}

// 居中绘制文本（用 basicfont）
// x, y 传入“目标中心点”的屏幕坐标
func drawTextCentered(dst *ebiten.Image, s string, x, y float64, col color.Color) {
	face := basicfont.Face7x13
	b := text.BoundString(face, s)
	w := float64(b.Dx())
	h := float64(b.Dy())
	// 基本居中：x - w/2；y + h/2（让基线略微下移）
	text.Draw(dst, s, face, int(x-w/2), int(y+h/2)-2, col)
}
