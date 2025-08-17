// File ui/input.go
package ui

import (
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"math"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"hexxagon_go/internal/game"
)

type UIState struct {
	From       *game.HexCoord            // 当前选中的起点（nil 表示未选中）
	MoveScores map[game.HexCoord]float64 // 起点到各个合法终点的评估分数
}

func getBoardTransform(tileImg *ebiten.Image) (scale, orgX, orgY, tileW, tileH, vs float64) {
	tileW = float64(tileImg.Bounds().Dx())
	tileH = float64(tileImg.Bounds().Dy())
	vs = tileH * math.Sqrt(3) / 2

	cols := 2*BoardRadius + 1
	rows := 2*BoardRadius + 1
	boardW := float64(cols-1)*tileW*0.75 + tileW
	boardH := vs*float64(rows-1) + tileH

	scale = math.Min(float64(WindowWidth)/boardW, float64(WindowHeight)/boardH)
	orgX = (float64(WindowWidth) - boardW*scale) / 2
	orgY = (float64(WindowHeight) - boardH*scale) / 2
	return
}

func cubeRound(xf, yf, zf float64) (int, int, int) {
	rx := math.Round(xf)
	ry := math.Round(yf)
	rz := math.Round(zf)

	dx := math.Abs(rx - xf)
	dy := math.Abs(ry - yf)
	dz := math.Abs(rz - zf)

	if dx >= dy && dx >= dz {
		rx = -ry - rz
	} else if dy >= dz {
		ry = -rx - rz
	} else {
		rz = -rx - ry
	}
	return int(rx), int(ry), int(rz)
}

// pixelToAxial 把屏幕像素坐标反算成 (q,r)
func pixelToAxial(fx, fy float64, board *game.Board, tileImg *ebiten.Image) (game.HexCoord, bool) {
	scale, orgX, orgY, tileWf, tileHf, vs := getBoardTransform(tileImg)
	dx := tileWf * 0.75

	// 1. 去掉平移、缩放
	x := (fx - orgX) / scale
	y := (fy - orgY) / scale

	// 2. 再去掉把中心移到 (0,0)
	x -= float64(BoardRadius) * dx
	y -= float64(BoardRadius) * vs

	// *** 关键补偿：移回半个瓦片的中心 ***
	x -= tileWf / 2 // ← 新增
	y -= tileHf / 2 // ← 新增

	// 3. 浮点轴向
	qf := x / dx
	rf := y/vs - qf/2

	// 4. 立方整体取整
	xf, zf := qf, rf
	yf := -xf - zf
	rx, _, rz := cubeRound(xf, yf, zf)

	coord := game.HexCoord{Q: rx, R: rz}
	return coord, board.InBounds(coord)
}

// handleInput 处理鼠标点击事件，用于选中、移动并播放音效
func (gs *GameScreen) handleInput() {
	// 只处理鼠标左键刚按下事件
	if !inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		return
	}

	// 将屏幕坐标转为棋盘坐标
	mx, my := ebiten.CursorPosition()
	coord, ok := pixelToAxial(float64(mx), float64(my), gs.state.Board, gs.tileImage)
	if !ok {
		gs.audioManager.Play("cancel_select_piece")
		return
	}

	player := gs.state.CurrentPlayer

	// 还没选中任何棋子，负责选中逻辑
	if gs.selected == nil {
		if gs.state.Board.Get(coord) == player {
			gs.selected = &game.HexCoord{Q: coord.Q, R: coord.R}
			gs.audioManager.Play("select_piece")

			/* === 新增：计算 MoveScores === */
			gs.ui.From = gs.selected
			gs.ui.MoveScores = make(map[game.HexCoord]float64)

			moves := game.GenerateMoves(gs.state.Board, gs.state.CurrentPlayer)
			for _, mv := range moves {
				// 只关心从选中起点出的走法
				if mv.From != *gs.selected {
					continue
				}
				// 在副本上模拟这步
				bCopy := gs.state.Board.Clone()
				bCopy.LastMove = mv

				if _, err := mv.Apply(bCopy, gs.state.CurrentPlayer); err != nil {
					continue
				}
				// 评分：深度 4 举例
				//score := game.AlphaBeta(bCopy, gs.state.CurrentPlayer, 4)
				//score := game.Evaluate(bCopy, gs.state.CurrentPlayer)
				if gs.showScores {
					score := game.Evaluate(bCopy, player)
					gs.ui.MoveScores[mv.To] = float64(score)
				}
			}
			/* === end 新增 === */

		} else {
			gs.audioManager.Play("cancel_select_piece")
		}
		return
	}

	// 准备落子
	move := game.Move{From: *gs.selected, To: coord}

	// —— 新增校验：目标格必须是空的 ——
	if gs.state.Board.Get(coord) != game.Empty {
		// 如果点到了自己棋子，就切换选中；否则取消选中
		if gs.state.Board.Get(coord) == player {
			gs.selected = &game.HexCoord{Q: coord.Q, R: coord.R}
			gs.audioManager.Play("select_piece")
			gs.refreshMoveScores()
		} else {
			gs.selected = nil
			gs.audioManager.Play("cancel_select_piece")
		}
		return
	}

	// —— 新增校验：六边形“立方坐标”距离只能是 1（复制）或 2（跳跃） ——
	dq := coord.Q - gs.selected.Q
	dr := coord.R - gs.selected.R
	ds := -dq - dr
	dist := math.Max(
		math.Max(math.Abs(float64(dq)), math.Abs(float64(dr))),
		math.Abs(float64(ds)),
	)
	if dist < 1 || dist > 2 {
		if gs.state.Board.Get(coord) == player {
			gs.selected = &game.HexCoord{Q: coord.Q, R: coord.R}
			gs.audioManager.Play("select_piece")
			gs.refreshMoveScores()
		} else {
			gs.selected = nil
			gs.audioManager.Play("cancel_select_piece")
		}
		return
	}

	// 校验通过，调用 performMove 真正落子
	if total, err := gs.performMove(move, player); err != nil {
		// 走子失败，保持“重新选中/取消”逻辑
		if gs.state.Board.Get(coord) == player {
			gs.selected = &game.HexCoord{Q: coord.Q, R: coord.R}
			gs.audioManager.Play("select_piece")
		} else {
			gs.selected = nil
			gs.audioManager.Play("cancel_select_piece")
		}
	} else {
		// 成功走子，设置 AI 延迟并清空选中
		gs.aiDelayUntil = time.Now().Add(total)
		gs.selected = nil
		leavePerf()
	}
	gs.refreshMoveScores()

}
