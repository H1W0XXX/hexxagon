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

	hideWindows []timedHide

	didShrink bool
}

type timedHide struct {
	coord  game.HexCoord
	start  time.Time // 到这个时间点开始隐藏
	end    time.Time // 到这个时间点结束（恢复显示）
	active bool      // 是否已把该格加入 tempHide
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

	// —— 计算合适的缩放，并缩小贴图（尺寸视觉不变，显存大降） —— //
	// 用“未缩的 tileImage”先算一遍当前 boardScale
	//boardScaleBefore, _, _, _, _, _ := getBoardTransform(gs.tileImage)

	// 根据目标清晰度=2×屏幕像素，得出统一缩放值
	//setSpriteScale(boardScaleBefore)

	// 缩小动画帧 & 动画锚点
	//shrinkAllSprites()

	// 把静态贴图也缩一下（棋格/棋子/提示圈/思考图标）
	gs.tileImage = scaleImage(gs.tileImage, spriteScale)
	gs.pieceImages[game.PlayerA] = scaleImage(gs.pieceImages[game.PlayerA], spriteScale)
	gs.pieceImages[game.PlayerB] = scaleImage(gs.pieceImages[game.PlayerB], spriteScale)
	gs.hintGreenImage = scaleImage(gs.hintGreenImage, spriteScale)
	gs.hintYellowImage = scaleImage(gs.hintYellowImage, spriteScale)
	gs.aiThinkingImg = scaleImage(gs.aiThinkingImg, spriteScale)
	// 注意：boardScale 将在每帧由 getBoardTransform(gs.tileImage) 重新计算，
	// 因为 tile 变小了，boardScale 会自动变大，两者互相抵消，屏幕尺寸保持不变。

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

var frameEps = time.Second / 30

// performMove 执行一次完整落子，返回本次行动需要的总耗时（用于 aiDelayUntil）
// 在 performMove 函数中，修改幽灵棋子的时机设置

// 在 performMove 函数中，修改幽灵棋子的时机设置

func (gs *GameScreen) performMove(move game.Move, player game.CellState) (time.Duration, error) {
	baseNow := time.Now()
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

	var infectDur, becomeDur time.Duration
	if len(infected) > 0 {
		infectBase := "redEatWhite"
		becomeBase := "whiteBecomeRed"
		if player == game.PlayerB {
			infectBase = "whiteEatRed"
			becomeBase = "redBecomeWhite"
		}
		infectDur = animDuration(infectBase, 30)
		becomeDur = animDuration(becomeBase, 30)

		for _, inf := range infected {
			gs.addInfectAnim(move.To, inf, player, moveDur)
			gs.addBecomeAnim(inf, player, moveDur+infectDur)

			becomeStart := baseNow.Add(moveDur + infectDur)
			becomeEnd := baseNow.Add(moveDur + infectDur + becomeDur)

			gs.hideWindows = append(gs.hideWindows, timedHide{
				coord: inf,
				start: becomeStart.Add(-frameEps),
				end:   becomeEnd,
			})
		}
	} else {
		infectDur, becomeDur = 0, 0
	}

	// 音效触发保持不变
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
		}
		seq = append(seq, "all_capture_after")
		gs.audioManager.PlaySequential(seq...)
	})

	// 🔧 关键修改：让真实棋子和幽灵棋子完美衔接
	commitAt := baseNow.Add(moveDur + infectDur + becomeDur)

	//showAt := baseNow.Add(moveDur - frameEps/2)
	showAt := baseNow.Add(moveDur)
	if showAt.Before(baseNow) {
		showAt = baseNow
	}

	// 🔧 修复关键：幽灵棋子应该在真实棋子出现后再消失，确保无缝衔接
	hideAt := commitAt.Add(frameEps * 3)

	gs.tempGhosts = append(gs.tempGhosts, tempGhost{
		coord:  move.To,
		player: player,
		showAt: showAt,
		hideAt: hideAt,
	})

	// ✅ 关键：to 位的隐藏只在"跳跃"时生效，克隆不隐藏
	if move.IsJump() {
		gs.hideWindows = append(gs.hideWindows, timedHide{
			coord: move.To,
			start: showAt,
			end:   hideAt,
		})
	}
	if move.IsJump() {
		gs.tempHide[move.From] = struct{}{}
	}

	newborns := make([]game.HexCoord, 0, 1+len(infected))
	newborns = append(newborns, move.To)
	newborns = append(newborns, infected...)

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

	return moveDur + infectDur, nil
}

//var firstFrame = true

// Update 更新游戏状态
func (gs *GameScreen) Update() error {
	now := time.Now()

	if !gs.didShrink {
		// 需要的话先计算 spriteScale（固定值就不用算）
		// setSpriteScale(boardScaleBefore)  // 如果你走自动模式

		shrinkAllSprites() // << 这里调用，ReadPixels 就不会报错了
		gs.didShrink = true
	}

	// 1) 音频更新
	gs.audioManager.Update()

	// 2) prune finished animations before handling game over
	for i := 0; i < len(gs.anims); {
		if gs.anims[i].Done {
			gs.anims = append(gs.anims[:i], gs.anims[i+1:]...)
			continue
		}
		i++
	}
	gs.isAnimating = len(gs.anims) > 0

	if gs.state.GameOver {
		if gs.aiRunning {
			close(gs.aiCancelCh)
			gs.aiRunning = false
		}
		gs.showThinking = false
		gs.aiQueuedMove = nil
		gs.aiThinkingUntil = time.Time{}
		gs.aiDelayUntil = time.Time{}
		return nil
	}

	// 3) pendingClone清理
	if pc := gs.pendingClone; pc != nil && now.After(pc.execTime) {
		gs.pendingClone = nil
	}

	// 4) 优先处理pendingCommit：确保真实棋盘状态及时更新
	if pc := gs.pendingCommit; pc != nil && now.After(pc.when) {
		// 真正更新棋盘
		infectedCoords, _, err := gs.state.MakeMove(pc.move)
		if err != nil {
			fmt.Println("MakeMove error:", err)
		} else {
			if len(infectedCoords) > 0 {
				gs.aiJumpUnlocked = true
			}
		}

		// 清理临时隐藏
		delete(gs.tempHide, pc.move.From)
		for _, c := range pc.newborns {
			delete(gs.tempHide, c)
		}

		gs.pendingCommit = nil
	}

	// 5) 处理隐藏窗口（在pendingCommit之后）
	kept := gs.hideWindows[:0]
	for _, w := range gs.hideWindows {
		// >= start 当帧就生效
		if !w.active && !now.Before(w.start) {
			gs.tempHide[w.coord] = struct{}{}
			w.active = true
		}

		// >= end 当帧就解除（仍保留 pendingCommit == nil 的保护）
		if !now.Before(w.end) {
			if gs.pendingCommit == nil {
				delete(gs.tempHide, w.coord)
				continue
			}

		}
		kept = append(kept, w)
	}
	gs.hideWindows = kept

	// 6) 清理过期的幽灵棋子（在pendingCommit之后）
	keptGhosts := gs.tempGhosts[:0]
	for _, g := range gs.tempGhosts {
		// 只在pendingCommit已处理且时间到期时才清理
		if gs.pendingCommit == nil && now.After(g.hideAt) {
			continue
		}
		keptGhosts = append(keptGhosts, g)
	}
	gs.tempGhosts = keptGhosts

	// 7) AI回合处理（保持不变）
	if gs.aiEnabled && gs.state.CurrentPlayer == game.PlayerB {
		if gs.isAnimating || gs.pendingCommit != nil || now.Before(gs.aiDelayUntil) {
			return nil
		}

		if gs.aiQueuedMove != nil && now.After(gs.aiThinkingUntil) {
			mv := *gs.aiQueuedMove
			gs.aiQueuedMove = nil
			gs.showThinking = false

			if total, err := gs.performMove(mv, game.PlayerB); err == nil {
				gs.aiDelayUntil = now.Add(total)
			}
			gs.selected = nil
			return nil
		}

		if !gs.aiRunning && gs.aiQueuedMove == nil {
			gs.aiThinkingStart = now
			gs.aiThinkingUntil = gs.aiThinkingStart.Add(2 * time.Second)
			gs.showThinking = true
			gs.aiRunning = true

			gs.aiCancelCh = make(chan struct{})
			boardCopy := gs.state.Board.Clone()
			allowJump := gs.aiJumpUnlocked
			depthLim := depth

			go func(b *game.Board, d int, allow bool, out chan<- game.Move, cancel <-chan struct{}) {
				mv, _, ok := game.IterativeDeepening(b, game.PlayerB, d, allow)
				select {
				case <-cancel:
					return
				default:
				}
				if ok {
					select {
					case out <- mv:
					default:
					}
				}
			}(boardCopy, depthLim, allowJump, gs.aiResultCh, gs.aiCancelCh)
		}

		select {
		case mv := <-gs.aiResultCh:
			gs.aiQueuedMove = &mv
			gs.aiRunning = false
		default:
		}

		return nil
	}

	// 8) 人类输入处理
	gs.handleInput()
	markBooted()

	ensurePerf(gs.isAnimating || gs.aiRunning || gs.aiQueuedMove != nil || gs.selected != nil)
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
		if now.Before(g.showAt) || now.After(g.hideAt) {
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
		} else if a.Key == "redBecomeWhite" || a.Key == "whiteBecomeRed" {
			// —— 变色动画：与普通动画用同一锚点/偏移，唯一差别：不旋转 —— //
			data := assets.AnimDatas[a.Key]
			ax, ay := data.AX, data.AY

			// 🚩改这里：读取“按统一缩放后”的偏移
			ox, oy := getScaledOffset(a.Key)
			tx, ty := getTrimOffset(a.Key, a.FrameIndex)

			// 先把帧图的动画锚点移到 (0,0)
			op.GeoM.Translate(-ax, -ay)
			// 不旋转
			// op.GeoM.Rotate(0)
			// 按棋盘缩放
			op.GeoM.Scale(boardScale, boardScale)

			// 贴到目标格的左上 + (ax,ay) + 偏移
			x0 := (float64(a.Coord.Q)+BoardRadius)*float64(tileW)*0.75 + ax + ox + tx
			y0 := (float64(a.Coord.R)+BoardRadius+float64(a.Coord.Q)/2)*vs + ay + oy + ty
			op.GeoM.Translate(originX+x0*boardScale, originY+y0*boardScale)
		} else {
			// —— 普通动画：保持老逻辑 —— //
			data := assets.AnimDatas[a.Key]
			ax, ay := data.AX, data.AY

			// 🚩改这里：读取“按统一缩放后”的偏移
			ox, oy := getScaledOffset(a.Key)
			tx, ty := getTrimOffset(a.Key, a.FrameIndex)

			op.GeoM.Translate(-ax, -ay)
			op.GeoM.Rotate(a.Angle)
			op.GeoM.Scale(boardScale, boardScale)
			x0 := (float64(a.Coord.Q)+BoardRadius)*float64(tileW)*0.75 + ax + ox + tx
			y0 := (float64(a.Coord.R)+BoardRadius+float64(a.Coord.Q)/2)*vs + ay + oy + ty
			op.GeoM.Translate(originX+x0*boardScale, originY+y0*boardScale)
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
