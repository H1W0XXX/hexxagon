package assets

import (
	"bytes"
	"embed"
	"fmt"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio/mp3"
	"github.com/hajimehoshi/ebiten/v2/audio/wav"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"

	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

// audioContext 用于播放音效，采样率 44.1kHz
// var audioContext = audio.NewContext(44100)
var audioContext = audio.CurrentContext()

//go:embed images/*.png
var imageFS embed.FS

// —— 可选：简单缓存，避免重复渲染 SVG —— //
var imgCache = map[string]*ebiten.Image{}

// LoadImage 通过名称加载嵌入的 PNG 图片（不含扩展名）
// 原来的：只加载 PNG（保持不变）
func LoadImage(name string) (*ebiten.Image, error) {
	data, err := imageFS.ReadFile("images/" + name + ".png")
	if err != nil {
		return nil, fmt.Errorf("读取嵌入图片 %s 失败: %w", name, err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("解码嵌入图片 %s 失败: %w", name, err)
	}
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

// —— 把 SVG 字节渲染为 Ebiten Image —— //
func rasterizeSVG(svgData []byte, targetW, targetH int) (*ebiten.Image, error) {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(svgData))
	if err != nil {
		return nil, err
	}
	vb := icon.ViewBox

	// 决定像素尺寸（保持比例）
	w := float64(targetW)
	h := float64(targetH)
	switch {
	case w <= 0 && h <= 0:
		w, h = vb.W, vb.H
	case w <= 0:
		w = h * vb.W / vb.H
	case h <= 0:
		h = w * vb.H / vb.W
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}

	icon.SetTarget(0, 0, w, h)

	dstW, dstH := int(w+0.5), int(h+0.5)
	rgba := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	// 透明底
	draw.Draw(rgba, rgba.Bounds(), image.Transparent, image.Point{}, draw.Src)

	scanner := rasterx.NewScannerGV(dstW, dstH, rgba, rgba.Bounds())
	dasher := rasterx.NewDasher(dstW, dstH, scanner)
	icon.Draw(dasher, 1.0)

	return ebiten.NewImageFromImage(rgba), nil
}
