package assets

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/png"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/hajimehoshi/ebiten/v2"
)

//go:embed animation
var animFS embed.FS

type AnimData struct {
	Frames []*ebiten.Image
	AX, AY float64
	FPS    float64
}

var (
	AnimDatas  = map[string]AnimData{}
	AnimFrames = map[string][]*ebiten.Image{}
)

func init() {
	loadDir("animation")
}

func loadDir(dir string) {
	entries, err := fs.ReadDir(animFS, dir)
	if err != nil {
		fmt.Printf("错误：无法读取目录 %s: %v\n", dir, err)
		return
	}

	var pngFiles []string
	for _, e := range entries {
		sub := path.Join(dir, e.Name()) // ← 用 path.Join 保证分隔符是 '/'
		if e.IsDir() {
			//fmt.Printf("进入子目录：%s\n", sub)
			loadDir(sub)
		} else if strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			pngFiles = append(pngFiles, sub)
		}
	}

	if len(pngFiles) == 0 {
		//fmt.Printf("警告：%s 下没有找到PNG文件\n", dir)
		return
	}

	sort.Strings(pngFiles)
	key := strings.TrimPrefix(dir, "animation/")

	//fmt.Printf("加载动画：%s，帧数：%d\n", key, len(pngFiles))

	var frames []*ebiten.Image
	var ax, ay float64
	for i, fp := range pngFiles {
		data, err := animFS.ReadFile(fp)
		if err != nil {
			fmt.Printf("无法读取文件 %s: %v\n", fp, err)
			continue
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			fmt.Printf("无法解码PNG %s: %v\n", fp, err)
			continue
		}
		frame := ebiten.NewImageFromImage(img)
		frames = append(frames, frame)
		if i == 0 {
			ax, ay = autoAnchor(img)
		}
	}

	AnimFrames[key] = frames
	AnimDatas[key] = AnimData{
		Frames: frames,
		AX:     ax,
		AY:     ay,
		FPS:    30,
	}
}

// autoAnchor 函数保持不变
func autoAnchor(img image.Image) (float64, float64) {
	minX, minY := img.Bounds().Max.X, img.Bounds().Max.Y
	maxX, maxY := 0, 0
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a != 0 {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if minX > maxX || minY > maxY {
		return float64(img.Bounds().Dx()) / 2, float64(img.Bounds().Dy()) / 2
	}
	return float64(minX+maxX) / 2, float64(minY+maxY) / 2
}
