// internal/game/ort_windows.go
//go:build windows

package game

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

//go:embed assets/onnxruntime.dll
var onnxruntimeDLL []byte

var (
	dllOnce sync.Once
	dllPath string
	dllErr  error
)

func prepareORTSharedLib() (string, error) {
	dllOnce.Do(func() {
		// 0) 环境变量优先（允许手动指定 GPU 版 DLL）
		if envPath := os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH"); envPath != "" {
			if _, err := os.Stat(envPath); err == nil {
				dllPath = envPath
				return
			} else {
				dllErr = fmt.Errorf("ONNXRUNTIME_SHARED_LIBRARY_PATH=%s not found: %w", envPath, err)
				return
			}
		}

		exe, _ := os.Executable()
		wd := filepath.Dir(exe) // 推荐放可执行文件同目录
		p := filepath.Join(wd, "onnxruntime.dll")

		// 1) 如果已存在：直接用
		if _, err := os.Stat(p); err == nil {
			dllPath = p
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			dllErr = fmt.Errorf("stat %s: %w", p, err)
			return
		}

		// 2) 不存在：写入内置 DLL
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			// 并发下可能已创建
			if errors.Is(err, os.ErrExist) {
				dllPath = p
				return
			}
			dllErr = fmt.Errorf("create %s: %w", p, err)
			return
		}
		defer f.Close()

		if _, err := f.Write(onnxruntimeDLL); err != nil {
			dllErr = fmt.Errorf("write %s: %w", p, err)
			return
		}
		if err := f.Sync(); err != nil {
			dllErr = fmt.Errorf("sync %s: %w", p, err)
			return
		}

		dllPath = p
	})
	return dllPath, dllErr
}
