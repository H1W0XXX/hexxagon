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

	pendingCommit *struct {
		move   game.Move
		player game.CellState
		when   time.Time
		// 仅用于 Sparkle/音效：这回合新增
		newborns []game.HexCoord // move.To + infections
	}
	// 思考图标与AI缓存
	aiThinkingStart time.Time
	aiThinkingUntil time.Time
	aiQueuedMove    *game.Move // 已算出但尚未应用
	showThinking    bool
	aiThinkingImg   *ebiten.Image // 思考中图标

	tempGhosts []tempGhost                // 幽灵棋子（视觉层）
	tempHide   map[game.HexCoord]struct{} // 临时隐藏：坐标→到期时间（跳跃旧位）

	boardBaked   *ebiten.Image // 预渲染好的整盘底图(含渐变)
	boardBakedOK bool          // 标志是否已烘焙

	aiResultCh chan game.Move // 后台AI结果传回（容量1）
	aiCancelCh chan struct{}  // 取消信号（close 即取消）
	aiRunning  bool           // 是否有AI在后台跑
}
type tempGhost struct {
	coord  game.HexCoord
	player game.CellState
	showAt time.Time // 动画结束出现
	hideAt time.Time // 提交时隐藏（提交后棋盘有真子）
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
	gs.tempHide = make(map[game.HexCoord]struct{})
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

	if gs.aiThinkingImg, err = assets.LoadImage("aiThinking"); err != nil {
		return nil, fmt.Errorf("加载 aiThinking.png 失败: %w", err)
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

	gs.aiResultCh = make(chan game.Move, 1)
	gs.aiCancelCh = make(chan struct{})
	return gs, nil
}

var frameEps = time.Second / 60

// performMove 执行一次完整落子，返回本次行动需要的总耗时（用于 aiDelayUntil）
func (gs *GameScreen) performMove(move game.Move, player game.CellState) (time.Duration, error) {
	baseNow := time.Now() // 用一个固定基准时间，避免多次 time.Now() 造成边界帧误差
	gs.isAnimating = true

	infected := computeInfections(gs.state.Board, move, player)
	gs.addMoveAnim(move, player)

	dirKey := directionKey(move.From, move.To)
	var moveBase string
	switch {
	case move.IsJump() && player == game.PlayerA:
		moveBase = "redJump/" + dirKey
	case move.IsJump() && player == game.PlayerB:
		moveBase = "whiteJump/" + dirKey
	case move.IsClone() && player == game.PlayerA:
		moveBase = "redClone/" + dirKey
	default:
		moveBase = "whiteClone/" + dirKey
	}
	moveDur := animDuration(moveBase, 30)

	// ---- 只在确实有感染时，添加感染动画并计算感染时长 ----
	var infectDur time.Duration
	if len(infected) > 0 {
		infectBase := "redEatWhite"
		if player == game.PlayerB {
			infectBase = "whiteEatRed"
		}
		infectDur = animDuration(infectBase, 30)
		for _, inf := range infected {
			// 感染动画从“移动动画结束”开始
			gs.addInfectAnim(move.To, inf, player, moveDur)
		}
	} else {
		infectDur = 0
	}

	// ---- 音效在移动动画结束点触发序列（保留你的原逻辑）----
	time.AfterFunc(moveDur, func() {
		var seq []string
		if move.IsJump() {
			if player == game.PlayerA {
				seq = append(seq, "red_split")
			} else {
				seq = append(seq, "white_jump")
			}
		} else {
			if player == game.PlayerA {
				seq = append(seq, "red_split")
			} else {
				seq = append(seq, "white_split")
			}
		}
		if len(infected) > 0 {
			if player == game.PlayerA {
				seq = append(seq, "red_capture_white_before", "red_capture_white_after")
			} else {
				seq = append(seq, "white_capture_red_before", "white_capture_red_after")
			}
			seq = append(seq, "all_capture_after")
		}
		gs.audioManager.PlaySequential(seq...)
	})

	// ---- 统一用基准时间计算关键时间点 ----
	commitAt := baseNow.Add(moveDur + infectDur)

	// 幽灵棋子：在移动动画结束“略早半帧”出现，直到提交时隐藏
	showAt := baseNow.Add(moveDur - frameEps)
	if showAt.Before(baseNow) {
		showAt = baseNow
	}

	gs.tempGhosts = append(gs.tempGhosts, tempGhost{
		coord:  move.To,
		player: player,
		showAt: showAt,
		hideAt: commitAt,
	})

	if move.IsJump() {
		// 跳跃：源格立即隐藏，直到提交后恢复由真实棋盘决定
		gs.tempHide[move.From] = struct{}{}
	}

	// 记录本回合新增（落点 + 被感染），用于提交后 sparkle（你现在先不画也行）
	newborns := make([]game.HexCoord, 0, 1+len(infected))
	newborns = append(newborns, move.To)
	newborns = append(newborns, infected...)

	// 安排真正提交（动画全部结束后一次性改盘面）
	gs.pendingCommit = &struct {
		move     game.Move
		player   game.CellState
		when     time.Time
		newborns []game.HexCoord
	}{
		move:     move,
		player:   player,
		when:     commitAt,
		newborns: newborns,
	}

	// 返回给 AI 的“延迟到动画结束”的时长
	return moveDur + infectDur, nil
}

//var firstFrame = true

// Update 更新游戏状态
func (gs *GameScreen) Update() error {

	// 扫一遍过期的隐藏
	//for c, until := range gs.tempHide {
	//	if time.Now().After(until) {
	//		delete(gs.tempHide, c)
	//	}
	//}
	// 扫一遍过期的幽灵（未到提交却被其它逻辑打断等）
	now := time.Now()
	kept := gs.tempGhosts[:0]
	for _, g := range gs.tempGhosts {
		if now.Before(g.hideAt) {
			kept = append(kept, g)
		}
	}
	gs.tempGhosts = kept

	// 1) 音频
	gs.audioManager.Update()
	if gs.state.GameOver {
		if gs.aiRunning {
			close(gs.aiCancelCh) // 通知后台线程退出（如果你能改搜索层，那里要检查ctx/cancel）
			gs.aiRunning = false
		}
		gs.showThinking = false
		gs.aiQueuedMove = nil
		gs.aiThinkingUntil = time.Time{}
		gs.aiDelayUntil = time.Time{}
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
	gs.isAnimating = len(gs.anims) > 0

	// 3) pendingClone：现在不再在这里做真正落子，直接清空即可（提交由 pendingCommit 统一完成）
	if pc := gs.pendingClone; pc != nil && time.Now().After(pc.execTime) {
		gs.pendingClone = nil
	}

	// 4) pendingCommit：动画全部结束后，真正把这步写入 Board，并派发“星星眨眼”
	if pc := gs.pendingCommit; pc != nil && time.Now().After(pc.when) {
		// —— 先真正更新棋盘 —— //
		infectedCoords, _, err := gs.state.MakeMove(pc.move)
		if err != nil {
			fmt.Println("MakeMove error:", err)
		} else {
			if len(infectedCoords) > 0 {
				gs.aiJumpUnlocked = true
			}
			// （可选）sparkle
			// for _, c := range pc.newborns { gs.addSparkleAt(c, 650*time.Millisecond) }
		}

		// —— 清理“临时隐藏” —— //
		// 对于跳跃，从旧位移除隐藏（到期时间已过或直接删）
		delete(gs.tempHide, pc.move.From)

		// —— 清理“幽灵棋子” —— //
		now := time.Now()
		kept := gs.tempGhosts[:0]
		for _, g := range gs.tempGhosts {
			// 提交后，所有 hideAt <= now 的幽灵都应移除
			if now.Before(g.hideAt) {
				kept = append(kept, g)
			}
		}
		gs.tempGhosts = kept

		gs.pendingCommit = nil
	}

	// 5) AI 回合（White）
	// 5) AI 回合（White）
	if gs.aiEnabled && gs.state.CurrentPlayer == game.PlayerB {

		// 动画没完 / 等提交 / 延迟窗口：先别启动AI，UI照常跑
		if gs.isAnimating || gs.pendingCommit != nil || time.Now().Before(gs.aiDelayUntil) {
			return nil
		}

		now := time.Now()

		// —— 优先：如果已有结果，且思考图标展示达到下限 —— //
		if gs.aiQueuedMove != nil && now.After(gs.aiThinkingUntil) {
			mv := *gs.aiQueuedMove
			gs.aiQueuedMove = nil
			gs.showThinking = false

			if total, err := gs.performMove(mv, game.PlayerB); err == nil {
				gs.aiDelayUntil = time.Now().Add(total) // 让下一次AI启动等动画播完
			}
			gs.selected = nil
			return nil
		}

		// —— 若没有在跑且也没有排队结果：启动一次后台搜索 —— //
		if !gs.aiRunning && gs.aiQueuedMove == nil {
			gs.aiThinkingStart = now
			gs.aiThinkingUntil = gs.aiThinkingStart.Add(1 * time.Second) // 至少展示1秒思考中
			gs.showThinking = true
			gs.aiRunning = true

			// 每次新任务换一个 cancel 通道
			gs.aiCancelCh = make(chan struct{})

			boardCopy := gs.state.Board.Clone()
			allowJump := gs.aiJumpUnlocked
			depthLim := depth

			go func(b *game.Board, d int, allow bool, out chan<- game.Move, cancel <-chan struct{}) {
				mv, _, ok := game.IterativeDeepening(b, game.PlayerB, d, allow)
				select {
				case <-cancel:
					return // 已取消
				default:
				}
				if ok {
					select { // 非阻塞投递
					case out <- mv:
					default:
					}
				}
			}(boardCopy, depthLim, allowJump, gs.aiResultCh, gs.aiCancelCh)
		}

		// —— 非阻塞尝试收取结果（仅缓存，不立刻落子）—— //
		select {
		case mv := <-gs.aiResultCh:
			gs.aiQueuedMove = &mv
			gs.aiRunning = false
		default:
			// 还在计算/未有结果：仅维持UI刷新（思考图标/动画）
		}

		return nil
	}

	// 6) 人类回合
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
	skip := map[game.HexCoord]bool{}
	for c := range gs.tempHide {
		skip[c] = true
	}

	gs.drawBoardAndPiecesWithHints(
		gs.offscreen,
		gs.state.Board,
		gs.tileImage,
		gs.hintGreenImage,
		gs.hintYellowImage,
		gs.pieceImages,
		gs.selected,
		skip,
	)
	// —— 思考图标（右上角）——
	if gs.showThinking && gs.aiThinkingImg != nil {
		iw, ih := gs.aiThinkingImg.Bounds().Dx(), gs.aiThinkingImg.Bounds().Dy()

		// 想要固定高度（比如 48px），太大就等比缩放；小于48就原尺寸
		targetH := 48.0
		scale := 1.0
		if float64(ih) > targetH {
			scale = targetH / float64(ih)
		}

		// 计算在 offscreen(800x600) 的右上角坐标
		margin := 12.0
		drawW := float64(iw) * scale
		x := float64(WindowWidth) - drawW - margin
		y := margin

		op := &ebiten.DrawImageOptions{}
		op.GeoM.Scale(scale, scale)
		op.GeoM.Translate(x, y)
		gs.offscreen.DrawImage(gs.aiThinkingImg, op)
	}
	boardScale, originX, originY, tileW, tileH, vs := getBoardTransform(gs.tileImage)

	now := time.Now()
	for _, g := range gs.tempGhosts {
		if now.Before(g.showAt) || !now.Before(g.hideAt) {
			continue
		}
		// 用与真实棋子相同的 drawPiece 叠加（你也可以降低 alpha 做“淡入”）
		drawPiece(gs.offscreen, gs.pieceImages[g.player], g.coord, originX, originY, int(tileW), int(tileH), vs, boardScale)
	}
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
