// File /ui/screen.go
package ui

import (
	"fmt"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/text"
	"golang.org/x/image/font/basicfont"

	//"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"strings"

	"image/color"
	"math"
	"time"

	"github.com/hajimehoshi/ebiten/v2"

	"hexxagon_go/internal/assets"
	"hexxagon_go/internal/game"

	"golang.org/x/image/font"
)

var lastUpdate time.Time

var fontFace = basicfont.Face7x13

// AnimOffset 给每个动画 key 一个手动微调 (X, Y)，单位：像素
var AnimOffset = map[string]struct{ X, Y float64 }{
	// ★ 你手动调整过的
	"redClone/down": {X: -10, Y: -45},

	// ↓ redClone
	"redClone/lowerleft":  {X: -65, Y: -45},
	"redClone/lowerright": {X: 0, Y: -45},
	"redClone/up":         {X: -10, Y: -55},
	"redClone/upperleft":  {X: -50, Y: -40},
	"redClone/upperright": {X: 0, Y: -45},

	// ↓ redJump动画错误
	"redJump/down":       {X: -11, Y: -45},
	"redJump/lowerleft":  {X: 0, Y: 0},   // 动画错误
	"redJump/lowerright": {X: 0, Y: 0},   // 动画错误
	"redJump/up":         {X: 0, Y: -45}, // 动画错误
	"redJump/upperleft":  {X: 0, Y: 0},
	"redJump/upperright": {X: 0, Y: 0},

	// ↓ whiteClone
	"whiteClone/down":       {X: -60, Y: -60},
	"whiteClone/lowerleft":  {X: -60, Y: -65},
	"whiteClone/lowerright": {X: -50, Y: -60},
	"whiteClone/up":         {X: -60, Y: -60},
	"whiteClone/upperleft":  {X: -60, Y: -60},
	"whiteClone/upperright": {X: -60, Y: -60},

	// ↓ whiteJump动画错误
	"whiteJump/down":       {X: 0, Y: 0},
	"whiteJump/lowerleft":  {X: 0, Y: 0},
	"whiteJump/lowerright": {X: 0, Y: 0},
	"whiteJump/up":         {X: 0, Y: 0},
	"whiteJump/upperleft":  {X: 0, Y: 0},
	"whiteJump/upperright": {X: 0, Y: 0},

	// ↓ 感染动画（不分方向）
	"redEatWhite":             {X: 0, Y: 0},
	"whiteEatRed":             {X: 0, Y: 0},
	"afterRedInfectedByWhite": {X: 0, Y: 0},
}
var soundDurations = map[string]time.Duration{
	"white_split":              470 * time.Millisecond,
	"white_jump":               496 * time.Millisecond,
	"white_capture_red_before": 653 * time.Millisecond,
	"white_capture_red_after":  548 * time.Millisecond,
	"all_capture_after":        400 * time.Millisecond,
	// 如果还有别的 key 也记得加上
}

const depth = 4 //人机思考步数
const (
	// 窗口尺寸
	WindowWidth  = 800
	WindowHeight = 600
	// 棋盘半径
	BoardRadius = 4
)

type pendingClone struct {
	move     game.Move
	player   game.CellState
	execTime time.Time // 何时真正执行 MakeMove
}

// GameScreen 实现 ebiten.Game 接口，管理游戏主循环和渲染
// selected 用于存储当前选中的源格
type GameScreen struct {
	state       *game.GameState                  // 游戏状态
	tileImage   *ebiten.Image                    // 棋盘格子贴图
	pieceImages map[game.CellState]*ebiten.Image // 棋子贴图映射
	selected    *game.HexCoord                   // 当前选中的源格
	// 高亮提示图
	hintGreenImage  *ebiten.Image // 复制移动近距离高亮图
	hintYellowImage *ebiten.Image // 跳跃移动远距离高亮图
	audioManager    *assets.AudioManager
	aiDelayUntil    time.Time
	offscreen       *ebiten.Image
	anims           []*FrameAnim  // 正在播放的动画列表
	aiEnabled       bool          // true=人机；false=人人
	isAnimating     bool          // 标记是否正在播放动画
	pendingClone    *pendingClone // 等待执行的 Clone 动作

	mode               string // "pve", "pvp", "replay"
	lastAdvance        time.Time
	replayDelay        time.Duration
	replayMi, replaySi int
	replayMatches      []ReplayMatch

	ui             UIState
	showScores     bool
	aiJumpUnlocked bool // 一旦为 true，后续所有搜索都允许跳越
	fontFace       font.Face
}

type ReplayStep struct {
	Move game.Move `json:"move"`
}

type ReplayMatch struct {
	Winner string       `json:"winner"`
	Steps  []ReplayStep `json:"steps"`
}

// NewGameScreen 构造并初始化游戏界面
func NewGameScreen(ctx *audio.Context, aiEnabled, showScores bool) (*GameScreen, error) {
	var err error
	gs := &GameScreen{
		state:       game.NewGameState(BoardRadius),
		pieceImages: make(map[game.CellState]*ebiten.Image),
		aiEnabled:   aiEnabled,
		showScores:  showScores,
		ui:          UIState{}, // 初始化 UIState
		fontFace:    basicfont.Face7x13,
	}

	// 加载贴图
	if gs.tileImage, err = assets.LoadImage("hex_space"); err != nil {
		return nil, err
	}
	if gs.pieceImages[game.PlayerA], err = assets.LoadImage("red_piece"); err != nil {
		return nil, err
	}
	if gs.pieceImages[game.PlayerB], err = assets.LoadImage("white_piece"); err != nil {
		return nil, err
	}
	if gs.hintGreenImage, err = assets.LoadImage("move_hint_green"); err != nil {
		return nil, err
	}
	if gs.hintYellowImage, err = assets.LoadImage("move_hint_yellow"); err != nil {
		return nil, err
	}

	// 如果启动时就要显示评分，先计算一次
	if gs.showScores {
		gs.refreshMoveScores()
	}

	// 初始化音频管理器
	if gs.audioManager, err = assets.NewAudioManager(ctx); err != nil {
		return nil, fmt.Errorf("初始化音频管理器失败: %w", err)
	}

	// 画板缓冲
	gs.offscreen = ebiten.NewImage(WindowWidth, WindowHeight)
	return gs, nil
}

// performMove 执行一次完整落子，返回本次行动需要的总耗时（用于 aiDelayUntil）
func (gs *GameScreen) performMove(move game.Move, player game.CellState) (time.Duration, error) {
	gs.isAnimating = true // 开始动画时设置为 true

	if move.IsJump() {
		// Jump 类型：立即更新棋盘状态
		infectedCoords, undo, err := gs.state.MakeMove(move)
		_ = undo
		if err != nil {
			gs.isAnimating = false
			return 0, err
		}
		if len(infectedCoords) > 0 {
			//fmt.Println("感染一个棋子，开始跳越")
			gs.aiJumpUnlocked = true
		}
		// 播放移动动画
		gs.addMoveAnim(move, player)

		// 计算移动动画时长
		dirKey := directionKey(move.From, move.To)
		var moveBase string
		if player == game.PlayerA {
			moveBase = "redJump/" + dirKey
		} else {
			moveBase = "whiteJump/" + dirKey
		}
		frames := assets.AnimFrames[moveBase]
		const fps = 30
		moveDur := time.Duration(float64(len(frames)) / fps * float64(time.Second))

		// 添加感染动画（延迟到移动动画结束后）
		for _, inf := range infectedCoords {
			gs.addInfectAnim(move.To, inf, player, moveDur)
		}

		// 组装音效队列
		var seq []string
		if player == game.PlayerA {
			seq = append(seq, "red_split") // 红方 jump 暂时复用 split 音效
		} else {
			seq = append(seq, "white_jump")
		}
		if len(infectedCoords) > 0 {
			if player == game.PlayerA {
				seq = append(seq, "red_capture_white_before", "red_capture_white_after")
			} else {
				seq = append(seq, "white_capture_red_before", "white_capture_red_after")
			}
		}
		seq = append(seq, "all_capture_after")

		// 延迟播放音效
		time.AfterFunc(moveDur, func() { gs.audioManager.PlaySequential(seq...) })

		// 返回移动动画时长
		return moveDur, nil
	} else { // —— Clone 类型 —— //
		// 1) 先播移动动画
		gs.addMoveAnim(move, player)

		// 2) 计算动画时长
		dirKey := directionKey(move.From, move.To)
		var moveBase string
		if player == game.PlayerA {
			moveBase = "redClone/" + dirKey
		} else {
			moveBase = "whiteClone/" + dirKey
		}
		frames := assets.AnimFrames[moveBase]
		const fps = 30
		moveDur := time.Duration(float64(len(frames)) / fps * float64(time.Second))

		// 3) 把“真正落子”推迟到主线程 Update() 中执行
		gs.pendingClone = &pendingClone{
			move:     move,
			player:   player,
			execTime: time.Now().Add(moveDur),
		}

		// 4) 返回动画持续时间（供 AI 延迟）
		return moveDur, nil
	}
}

//var firstFrame = true

// Update 更新游戏状态
func (gs *GameScreen) Update() error {
	//if firstFrame {
	//	firstFrame = false
	//	ebiten.SetFPSMode(ebiten.FPSModeVsyncOffMinimum) // 已经进入事件循环，安全
	//}

	now := time.Now()
	if !lastUpdate.IsZero() {
		elapsed := now.Sub(lastUpdate)
		targetFrameTime := time.Second / 30 // 约等于 33ms
		if elapsed < targetFrameTime {
			time.Sleep(targetFrameTime - elapsed) // 主动 sleep 剩余时间
		}
	}
	lastUpdate = time.Now()

	// 1) 更新音频
	gs.audioManager.Update()
	if gs.state.GameOver {
		return nil
	}

	// 2) 清理已结束的动画
	for i := 0; i < len(gs.anims); {
		if gs.anims[i].Done {
			gs.anims = append(gs.anims[:i], gs.anims[i+1:]...)
			continue
		}
		i++
	}
	// 更新动画标志

	// 3) 延迟执行 Clone 落子（在主线程里，绝不会和 Draw 并发）
	if pc := gs.pendingClone; pc != nil && time.Now().After(pc.execTime) {
		// 真正更新棋盘
		infectedCoords, _, err := gs.state.MakeMove(pc.move)
		if err != nil {
			fmt.Println("MakeMove error:", err)
		} else {
			if len(infectedCoords) > 0 {
				//fmt.Println("感染一个棋子，开始跳越")
				gs.aiJumpUnlocked = true
			}
			// 播放感染动画
			for _, inf := range infectedCoords {
				gs.addInfectAnim(pc.move.To, inf, pc.player, 0)
			}
			// 播放音效
			var seq []string
			if pc.player == game.PlayerA {
				seq = append(seq, "red_split")
			} else {
				seq = append(seq, "white_split")
			}
			if len(infectedCoords) > 0 {
				if pc.player == game.PlayerA {
					seq = append(seq, "red_capture_white_before", "red_capture_white_after")
				} else {
					seq = append(seq, "white_capture_red_before", "white_capture_red_after")
				}
			}
			seq = append(seq, "all_capture_after")
			gs.audioManager.PlaySequential(seq...)
		}
		// 清空 pendingClone
		gs.pendingClone = nil
	}

	gs.isAnimating = len(gs.anims) > 0

	// 4) AI 回合
	if gs.aiEnabled && gs.state.CurrentPlayer == game.PlayerB {
		if gs.isAnimating || time.Now().Before(gs.aiDelayUntil) {
			return nil
		}
		if move, _, ok := game.IterativeDeepening(gs.state.Board, game.PlayerB, depth, gs.aiJumpUnlocked); ok {
			if total, err := gs.performMove(move, game.PlayerB); err == nil {
				gs.aiDelayUntil = time.Now().Add(total)
			}
		}
		gs.selected = nil
		return nil
	}
	//if gs.aiEnabled && gs.state.CurrentPlayer == game.PlayerB {
	//	if gs.isAnimating || time.Now().Before(gs.aiDelayUntil) {
	//		return nil
	//	}
	//
	//	// —— MCTS：给“模拟次数”或“时间预算”二选一（另一个填 0）——
	//	// 例1：固定 4000 次模拟
	//	mv, ok := game.FindBestMoveMCTS(gs.state.Board, game.PlayerB, 4000, 0, gs.aiJumpUnlocked)
	//
	//	// 例2：固定 500ms 时间预算
	//	//mv, ok := game.FindBestMoveMCTS(gs.state.Board, game.PlayerB, 0, 500*time.Millisecond)
	//
	//	if ok {
	//		if total, err := gs.performMove(mv, game.PlayerB); err == nil {
	//			gs.aiDelayUntil = time.Now().Add(total)
	//		}
	//	}
	//	gs.selected = nil
	//	return nil
	//}

	// 5) 人类回合
	enterPerf()
	gs.handleInput()
	return nil
}

// Draw 每帧渲染：先清空背景，再绘制棋盘与棋子
func (gs *GameScreen) Draw(screen *ebiten.Image) {
	// 1) 清空屏幕背景（window 上）
	screen.Fill(color.Black)

	// 2) 清空 offscreen 画布（800×600）
	gs.offscreen.Fill(color.Black)

	// 3) 所有棋盘+高亮+棋子都画到 offscreen
	DrawBoardAndPiecesWithHints(
		gs.offscreen,
		gs.state.Board,
		gs.tileImage,
		gs.hintGreenImage,
		gs.hintYellowImage,
		gs.pieceImages,
		gs.selected,
	)

	boardScale, originX, originY, tileW, tileH, vs := getBoardTransform(gs.tileImage)

	// —— 新增：把评分画到每个目标格的中心 ——
	if gs.showScores {
		for to, score := range gs.ui.MoveScores {
			// 1) 计算格子在 offscreen 上的像素中心（未缩放、未平移）
			cx := (float64(to.Q)+BoardRadius)*tileW*0.75 + tileW/2
			cy := (float64(to.R)+BoardRadius+float64(to.Q)/2)*vs + tileH/2

			// 2) 应用缩放和平移，得到最终绘制位置
			px := originX + cx*boardScale
			py := originY + cy*boardScale

			// 3) 格式化分数，选颜色
			str := fmt.Sprintf("%.1f", score)
			clr := color.RGBA{0x20, 0xFF, 0x20, 0xFF} // 绿色
			if score < 0 {
				clr = color.RGBA{0xFF, 0x60, 0x60, 0xFF} // 负分红色
			}

			// 4) 画字（-10, +4 是为了让文本大致居中）
			text.Draw(gs.offscreen, str, fontFace, int(px)-10, int(py)+4, clr)
		}
	}
	//fmt.Println(gs.anims)
	for _, a := range gs.anims {
		img := a.Current()
		if img == nil {
			continue
		}
		w, h := img.Size()
		op := &ebiten.DrawImageOptions{}

		if strings.HasPrefix(a.Key, "redEatWhite") || strings.HasPrefix(a.Key, "whiteEatRed") {
			// —— 感染动画：绕 图片中心 旋转 —— //
			// 1) 把图片中心移到 (0,0)
			op.GeoM.Translate(-float64(w)/2, -float64(h)/2)
			// 2) 旋转
			op.GeoM.Rotate(a.Angle)
			// 3) 缩放
			op.GeoM.Scale(boardScale, boardScale)
			// 4) 最终平移到 midX, midY
			op.GeoM.Translate(
				originX+a.MidX*boardScale,
				originY+a.MidY*boardScale,
			)
		} else {
			// —— 普通动画：保持老逻辑 —— //
			data := assets.AnimDatas[a.Key]
			ax, ay := data.AX, data.AY
			off := AnimOffset[a.Key]

			// 先把原本的 anim anchor 移到 (0,0)
			op.GeoM.Translate(-ax, -ay)
			// 再旋转、缩放
			op.GeoM.Rotate(a.Angle)
			op.GeoM.Scale(boardScale, boardScale)
			// 最后平移到格子的左上 + offset + origin
			x0 := (float64(a.Coord.Q)+BoardRadius)*float64(tileW)*0.75 + ax + off.X
			y0 := (float64(a.Coord.R)+BoardRadius+float64(a.Coord.Q)/2)*vs + ay + off.Y
			op.GeoM.Translate(
				originX+x0*boardScale,
				originY+y0*boardScale,
			)
		}

		gs.offscreen.DrawImage(img, op)
	}

	// 4) 把 offscreen 缩放、居中到 screen
	w, h := screen.Bounds().Dx(), screen.Bounds().Dy()
	scaleX := float64(w) / float64(WindowWidth)
	scaleY := float64(h) / float64(WindowHeight)
	scale := math.Min(scaleX, scaleY)

	op := &ebiten.DrawImageOptions{}

	op.GeoM.Scale(scale, scale)
	dx := (float64(w) - float64(WindowWidth)*scale) / 2
	dy := (float64(h) - float64(WindowHeight)*scale) / 2
	op.GeoM.Translate(dx, dy)

	screen.DrawImage(gs.offscreen, op)

	aCnt := gs.state.Board.CountPieces(game.PlayerA)
	bCnt := gs.state.Board.CountPieces(game.PlayerB)

	info := fmt.Sprintf("Red: %d     White: %d", aCnt, bCnt)
	text.Draw(screen, info, gs.fontFace, 20, 24, color.White)
}

// Layout 定义窗口尺寸
func (gs *GameScreen) Layout(outsideWidth, outsideHeight int) (int, int) {
	return WindowWidth, WindowHeight
}

// return boardScale, originX, originY, tileW, tileH, vs
func boardTransform(tileImg *ebiten.Image) (float64, float64, float64, int, int, float64) {
	tileW := tileImg.Bounds().Dx()
	tileH := tileImg.Bounds().Dy()
	vs := float64(tileH) * math.Sqrt(3) / 2

	cols, rows := 2*BoardRadius+1, 2*BoardRadius+1
	boardW := float64(cols-1)*float64(tileW)*0.75 + float64(tileW)
	boardH := vs*float64(rows-1) + float64(tileH)

	scale := math.Min(float64(WindowWidth)/boardW, float64(WindowHeight)/boardH)
	originX := (float64(WindowWidth) - boardW*scale) / 2
	originY := (float64(WindowHeight) - boardH*scale) / 2
	return scale, originX, originY, tileW, tileH, vs
}

//func loadUIFont() font.Face {
//	data, _ := os.ReadFile("assets/font/Roboto-Regular.ttf")
//	ft, _ := opentype.Parse(data)
//	face, _ := opentype.NewFace(ft, &opentype.FaceOptions{
//		Size:    18,
//		DPI:     72,
//		Hinting: font.HintingFull,
//	})
//	return face
//}
