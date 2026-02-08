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
	WinProbA   float64                   // 始终存储玩家 A (红色) 的胜率 [0, 1]
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
	// 只处理鼠标左键刚按下
	if !inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		return
	}

	// 屏幕坐标 -> 棋盘坐标
	mx, my := ebiten.CursorPosition()
	coord, ok := pixelToAxial(float64(mx), float64(my), gs.state.Board, gs.tileImage)
	if !ok {
		gs.audioManager.Play("cancel_select_piece")
		return
	}

	player := gs.state.CurrentPlayer

	// 坐标 -> 下标
	toIdx, okTo := game.IndexOf[coord] // 如果你没导出 indexOf，就在本包内用 indexOf[coord]
	if !okTo {
		gs.audioManager.Play("cancel_select_piece")
		return
	}

	// —— 尚未选中：尝试选中自己的棋子 —— //
	if gs.selected == nil {
		if gs.state.Board.Cells[toIdx] == player { // 数组下标直读
			gs.selected = &game.HexCoord{Q: coord.Q, R: coord.R}
			gs.audioManager.Play("select_piece")
			if gs.showScores {
				gs.refreshMoveScores()
			}
		} else {
			gs.audioManager.Play("cancel_select_piece")
		}
		return
	}

	// —— 已选中：准备尝试走子 —— //
	move := game.Move{From: *gs.selected, To: coord}

	// 目标必须为空；若点到自己棋子＝切换选中；否则取消
	if gs.state.Board.Cells[toIdx] != game.Empty {
		if gs.state.Board.Cells[toIdx] == player {
			gs.selected = &game.HexCoord{Q: coord.Q, R: coord.R}
			gs.audioManager.Play("select_piece")
		} else {
			gs.selected = nil
			gs.audioManager.Play("cancel_select_piece")
		}
		if gs.showScores {
			gs.refreshMoveScores()
		}
		return
	}

	// 校验“合法步”：用邻接表判断是否 1 步(克隆) 或 2 步(跳跃)
	fromIdx := game.IndexOf[*gs.selected] 
	valid := false
	for _, nb := range game.NeighI[fromIdx] {
		if nb == toIdx {
			valid = true
			break
		}
	}
	if !valid {
		for _, j := range game.JumpI[fromIdx] {
			if j == toIdx {
				valid = true
				break
			}
		}
	}
	if !valid {
		// 非法落点：同上逻辑，点到自己＝切换选中；否则取消
		if gs.state.Board.Cells[toIdx] == player {
			gs.selected = &game.HexCoord{Q: coord.Q, R: coord.R}
			gs.audioManager.Play("select_piece")
		} else {
			gs.selected = nil
			gs.audioManager.Play("cancel_select_piece")
		}
		if gs.showScores {
			gs.refreshMoveScores()
		}
		return
	}

	// 真正落子
	if total, err := gs.performMove(move, player); err != nil {
		if gs.state.Board.Cells[toIdx] == player {
			enterPerf()
			gs.selected = &game.HexCoord{Q: coord.Q, R: coord.R}
			gs.audioManager.Play("select_piece")
		} else {
			gs.selected = nil
			gs.audioManager.Play("cancel_select_piece")
		}
	} else {
		// 成功：设置 AI 延迟并清空选中
		gs.aiDelayUntil = time.Now().Add(total)
		gs.selected = nil
	}
	if gs.showScores {
		gs.refreshMoveScores()
	}
}
