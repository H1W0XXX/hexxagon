// cmd/replay_ebiten/main.go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image/color"
	"io/ioutil"
	"log"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"hexxagon_go/internal/game"
)

const (
	screenW     = 800
	screenH     = 600
	hexSize     = 20 // 六边形边长，按需调整
	boardRadius = 4  // 和 NewGameState 一致
)

type Step struct {
	Move  game.Move      `json:"move"`
	Board map[string]int `json:"board"`
}
type Match struct {
	Winner string `json:"winner"`
	Steps  []Step `json:"steps"`
}

type ReplayGame struct {
	matches     []Match
	mi, si      int
	lastAdvance time.Time
	delay       time.Duration
	board       *game.Board

	playing bool // 新增：是否自动播放
}

func NewReplayGame(path string, delay time.Duration) (*ReplayGame, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var matches []Match
	if err := json.Unmarshal(data, &matches); err != nil {
		return nil, fmt.Errorf("unmarshal JSON: %w", err)
	}
	// 初始化第一盘、第一步前的 Board
	state := game.NewGameState(boardRadius)
	return &ReplayGame{
		matches:     matches,
		mi:          0,
		si:          -1, // -1 意味着先画初始局面
		lastAdvance: time.Now(),
		delay:       delay,
		board:       state.Board,
	}, nil
}

func (g *ReplayGame) Layout(outsideWidth, outsideHeight int) (w, h int) {
	return screenW, screenH
}
func (g *ReplayGame) Update() error {
	// --- 1) 处理按键 ---
	// 空格：切换 播放/暂停
	if inpututil.IsKeyJustPressed(ebiten.KeySpace) {
		g.playing = !g.playing
	}
	// 右方向：单步前进
	if inpututil.IsKeyJustPressed(ebiten.KeyRight) {
		g.playing = false
		g.advance()
	}
	// 左方向：单步后退
	if inpututil.IsKeyJustPressed(ebiten.KeyLeft) {
		g.playing = false
		g.rewind()
	}

	// --- 2) 自动播放 ---
	if g.playing && time.Since(g.lastAdvance) >= g.delay {
		g.advance()
	}
	return nil
}

// advance 做一步前进（或切到下盘／结束）
func (g *ReplayGame) advance() {
	g.lastAdvance = time.Now()
	g.si++
	if g.mi >= len(g.matches) {
		return
	}
	match := g.matches[g.mi]
	if g.si >= len(match.Steps) {
		g.mi++
		g.si = -1
		g.board = game.NewGameState(boardRadius).Board
		return
	}
	if g.si >= 0 {
		step := match.Steps[g.si]
		pl := game.PlayerA
		if g.si%2 == 1 {
			pl = game.PlayerB
		}
		step.Move.MakeMove(g.board, pl)
		g.board.LastMove = step.Move
	}
}

// rewind 往回一步（重建到 si-1）
func (g *ReplayGame) rewind() {
	if g.si < 0 {
		// 如果已经在初始，那就退到上一盘最后一步
		if g.mi > 0 {
			g.mi--
			g.si = len(g.matches[g.mi].Steps) - 1
		}
	} else {
		g.si--
	}
	// 重建 board 到当前 mi, si
	g.board = game.NewGameState(boardRadius).Board
	for i := 0; i <= g.si; i++ {
		step := g.matches[g.mi].Steps[i]
		pl := game.PlayerA
		if i%2 == 1 {
			pl = game.PlayerB
		}
		step.Move.MakeMove(g.board, pl)
		g.board.LastMove = step.Move
	}
}

func (g *ReplayGame) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{0x10, 0x10, 0x30, 0xff})

	// 画 HexGrid + 棋子
	centerX, centerY := screenW/2, screenH/2
	for _, c := range g.board.AllCoords() {
		// axial to pixel
		x := centerX + (c.Q*2+c.R)*hexSize
		y := centerY + c.R*3*hexSize/2
		// 边
		ebitenutil.DrawRect(screen, float64(x-hexSize), float64(y-2), float64(2*hexSize), 4, color.White)
		// 棋子
		st := g.board.Get(c)
		switch st {
		case game.PlayerA:
			ebitenutil.DrawRect(screen, float64(x-hexSize/2), float64(y-hexSize/2), float64(hexSize), float64(hexSize), color.RGBA{0xff, 0x00, 0x00, 0xff})
		case game.PlayerB:
			ebitenutil.DrawRect(screen, float64(x-hexSize/2), float64(y-hexSize/2), float64(hexSize), float64(hexSize), color.RGBA{0x00, 0xff, 0x00, 0xff})
		}
	}

	// 文字提示
	info := fmt.Sprintf("Match %d/%d  Step %d/%d  Winner=%s",
		g.mi+1, len(g.matches),
		g.si+1, len(g.matches[g.mi].Steps),
		g.matches[g.mi].Winner,
	)
	// 再画操作提示 / 按钮
	const btnW, btnH = 100, 30
	// 按钮背景
	ebitenutil.DrawRect(screen, 10, screenH-40, btnW, btnH, color.RGBA{0x33, 0x33, 0x33, 0xff})
	ebitenutil.DebugPrint(screen, "＜ 上一步")
	ebitenutil.DrawRect(screen, 120, screenH-40, btnW, btnH, color.RGBA{0x33, 0x33, 0x33, 0xff})
	//ebitenutil.DebugPrint(screen, "暂停/▶", 130, screenH-32)
	ebitenutil.DrawRect(screen, 240, screenH-40, btnW, btnH, color.RGBA{0x33, 0x33, 0x33, 0xff})
	//ebitenutil.DebugPrint(screen, "下一步 ＞", 250, screenH-32)

	// 然后你的状态行
	info = fmt.Sprintf("Match %d/%d  Step %d/%d  Winner=%s  [%s]",
		g.mi+1, len(g.matches),
		g.si+1, len(g.matches[g.mi].Steps),
		g.matches[g.mi].Winner,
		map[bool]string{true: "播放中", false: "已暂停"}[g.playing],
	)
	ebitenutil.DebugPrint(screen, info)
}

func main() {
	jsonPath := flag.String("in", "selfplay.json", "自对弈 JSON 文件")
	delay := flag.Duration("delay", 300*time.Millisecond, "每步播放间隔")
	flag.Parse()

	game, err := NewReplayGame(*jsonPath, *delay)
	if err != nil {
		log.Fatal(err)
	}

	ebiten.SetWindowSize(screenW, screenH)
	ebiten.SetWindowTitle("Hexxagon 自我对弈回放")
	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
