package assets

import (
	"bytes"
	"embed"
	"fmt"
	"sync"
	"time"

	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/audio/mp3"
)

//go:embed audio/*.mp3
var soundsFS embed.FS

type AudioManager struct {
	ctx     *audio.Context
	buffers map[string][]byte

	mu         sync.Mutex
	players    []*audio.Player
	lastPlayer *audio.Player // 保留最近一次播放的 player，防止被 GC
}

// NewAudioManager 接收 main 创建好的 *audio.Context，不再 NewContext
func NewAudioManager(ctx *audio.Context) (*AudioManager, error) {
	names := []string{
		"cancel_select_piece",
		"game_over",
		"red_capture_white_after",
		"red_capture_white_before",
		"red_split",
		"select_piece",
		"white_capture_red_after",
		"white_capture_red_before",
		"white_jump",
		"white_split",
		"all_capture_after",
	}
	buf := make(map[string][]byte, len(names))
	for _, name := range names {
		data, err := soundsFS.ReadFile("audio/" + name + ".mp3")
		if err != nil {
			return nil, fmt.Errorf("加载音频 %s 失败: %w", name, err)
		}
		buf[name] = data
	}
	return &AudioManager{ctx: ctx, buffers: buf}, nil
}

// Play 播放 key 对应音效，并保存引用，防止被 GC
func (m *AudioManager) Play(key string) {
	data, ok := m.buffers[key]
	if !ok {
		fmt.Println("AudioManager.Play：未找到音效", key)
		return
	}
	r := bytes.NewReader(data)
	s, err := mp3.Decode(m.ctx, r)
	if err != nil {
		fmt.Println("AudioManager.Play：解码失败", err)
		return
	}
	p, err := m.ctx.NewPlayer(s)
	if err != nil {
		fmt.Println("AudioManager.Play：创建 Player 失败", err)
		return
	}
	p.Play()
	// **关键**：保留引用，防止 GC
	m.lastPlayer = p
}

// Update 应每帧调用一次，清理已停止的播放器
func (m *AudioManager) Update() {
	m.mu.Lock()
	defer m.mu.Unlock()
	alive := m.players[:0]
	for _, p := range m.players {
		if p.IsPlaying() {
			alive = append(alive, p)
		}
	}
	m.players = alive
}

func (m *AudioManager) PlaySequential(keys ...string) {
	go func() {
		for _, key := range keys {
			data, ok := m.buffers[key]
			if !ok {
				continue
			}
			r := bytes.NewReader(data)
			s, err := mp3.DecodeWithSampleRate(44100, r)
			if err != nil {
				continue
			}
			p, err := m.ctx.NewPlayer(s)
			if err != nil {
				continue
			}
			p.Play()
			// 等待这个 player 播放完毕
			for p.IsPlaying() {
				time.Sleep(10 * time.Millisecond)
			}
			// 这里循环结束，自动进入下一个 key
		}
	}()
}

func (m *AudioManager) Busy() bool {
	if m.lastPlayer == nil {
		return false
	}
	return m.lastPlayer.IsPlaying()
}
