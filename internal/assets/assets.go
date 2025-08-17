package assets

import (
	"bytes"
	"embed"
	"fmt"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio/mp3"
	"github.com/hajimehoshi/ebiten/v2/audio/wav"
	"image/png"
	"os"
	"path/filepath"

	"github.com/hajimehoshi/ebiten/v2/audio"
)

// audioContext 用于播放音效，采样率 44.1kHz
// var audioContext = audio.NewContext(44100)
var audioContext = audio.CurrentContext()

//go:embed images/*.png
var imageFS embed.FS

// LoadImage 通过名称加载嵌入的 PNG 图片（不含扩展名）
func LoadImage(name string) (*ebiten.Image, error) {
	// 直接从 embed.FS 读取
	data, err := imageFS.ReadFile("images/" + name + ".png")
	if err != nil {
		return nil, fmt.Errorf("读取嵌入图片 %s 失败: %w", name, err)
	}
	// 解码
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("解码嵌入图片 %s 失败: %w", name, err)
	}
	// 转为 Ebiten Image
	return ebiten.NewImageFromImage(img), nil
}

//func LoadImage(name string) (*ebiten.Image, error) {
//	path := filepath.Join("assets", "images", name+".png")
//	f, err := os.Open(path)
//	if err != nil {
//		return nil, fmt.Errorf("打开图片 %s 失败: %w", path, err)
//	}
//	defer f.Close()
//	img, err := png.Decode(f)
//	if err != nil {
//		return nil, fmt.Errorf("解码图片 %s 失败: %w", path, err)
//	}
//	return ebiten.NewImageFromImage(img), nil
//}

// LoadAudio 从项目根目录下的 assets/audio 目录加载音频文件（支持 WAV 和 MP3，不含扩展名），返回可播放的 Player
func LoadAudio(name string) (*audio.Player, error) {
	// 尝试 WAV
	wavPath := filepath.Join("assets", "audio", name+".wav")
	if f, err := os.Open(wavPath); err == nil {
		defer f.Close()
		decoded, err := wav.DecodeWithSampleRate(audioContext.SampleRate(), f)
		if err != nil {
			return nil, fmt.Errorf("解码音频 %s 失败: %w", wavPath, err)
		}
		player, err := audioContext.NewPlayer(decoded)
		if err != nil {
			return nil, fmt.Errorf("创建音频播放器失败: %w", err)
		}
		return player, nil
	}
	// 尝试 MP3
	mp3Path := filepath.Join("assets", "audio", name+".mp3")
	if f, err := os.Open(mp3Path); err == nil {
		defer f.Close()
		// mp3.Decode 使用 Context 解码
		decoded, err := mp3.DecodeWithSampleRate(audioContext.SampleRate(), f)
		if err != nil {
			return nil, fmt.Errorf("解码音频 %s 失败: %w", mp3Path, err)
		}
		player, err := audioContext.NewPlayer(decoded)
		if err != nil {
			return nil, fmt.Errorf("创建音频播放器失败: %w", err)
		}
		return player, nil
	}
	return nil, fmt.Errorf("未找到音频文件 %s (wav/mp3)", name)
}
