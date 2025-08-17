// File /ui/render.go
package ui

import (
	"github.com/hajimehoshi/ebiten/v2"
	"hexxagon_go/internal/game"
	"math"
)

// DrawBoardAndPiecesWithHints 在 dst 上绘制棋盘、提示和棋子。
// dst 尺寸应当是 WindowWidth×WindowHeight（800×600）。
func DrawBoardAndPiecesWithHints(
	dst *ebiten.Image,
	board *game.Board,
	tileImg *ebiten.Image,
	hintGreenImg *ebiten.Image,
	hintYellowImg *ebiten.Image,
	pieceImgs map[game.CellState]*ebiten.Image,
	selected *game.HexCoord,
) {
	// 0) 预计算 cloneTargets/jumpTargets
	cloneTargets := map[game.HexCoord]struct{}{}
	jumpTargets := map[game.HexCoord]struct{}{}
	if selected != nil {
		from := *selected
		for _, to := range board.AllCoords() {
			if board.Get(to) != game.Empty {
				continue // 目的地必须是空
			}
			switch game.HexDist(from, to) {
			case 1:
				cloneTargets[to] = struct{}{}
			case 2:
				jumpTargets[to] = struct{}{}
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

	// 5) 绘制棋盘底板
	for _, c := range board.AllCoords() {
		if board.Get(c) == game.Blocked {
			continue // 跳过 Blocked 格子
		}
		drawHex(dst, tileImg, c, originX, originY, tileW, tileH, vs, scale)
	}

	// 6) 先画提示：绿色=复制造型，黄色=跳跃
	for _, c := range board.AllCoords() {
		if _, ok := cloneTargets[c]; ok {
			drawHex(dst, hintGreenImg, c, originX, originY, tileW, tileH, vs, scale)
		}
	}
	for _, c := range board.AllCoords() {
		if _, ok := jumpTargets[c]; ok {
			drawHex(dst, hintYellowImg, c, originX, originY, tileW, tileH, vs, scale)
		}
	}

	// 7) 最后绘制棋子
	for _, c := range board.AllCoords() {
		st := board.Get(c)
		if st == game.PlayerA || st == game.PlayerB {
			drawPiece(dst, pieceImgs[st], c, originX, originY, tileW, tileH, vs, scale)
		}
	}
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

		// 关键：告诉评估“上一手就是这步”
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
