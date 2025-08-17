// internal/game/ort_darwin.go
//go:build darwin

package game

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

//
//go:embed assets/libonnxruntime.dylib
var onnxruntimeDYLIB []byte

var (
	dylibOnce sync.Once
	dylibPath string
	dylibErr  error
)

func prepareORTSharedLib() (string, error) {
	dylibOnce.Do(func() {
		exe, _ := os.Executable()
		wd := filepath.Dir(exe)
		p := filepath.Join(wd, "libonnxruntime.dylib")

		// 1) 如果文件已存在：直接使用，不覆盖
		if _, err := os.Stat(p); err == nil {
			dylibPath = p
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			// 不是“不存在”的其他错误
			dylibErr = fmt.Errorf("stat %s: %w", p, err)
			return
		}

		// 2) 不存在：写入内置的 dylib（避免并发下的覆盖）
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			// 如果并发下已被其他协程/进程创建，也直接用它
			if errors.Is(err, os.ErrExist) {
				dylibPath = p
				return
			}
			dylibErr = fmt.Errorf("create %s: %w", p, err)
			return
		}
		defer f.Close()

		if _, err := f.Write(onnxruntimeDYLIB); err != nil {
			dylibErr = fmt.Errorf("write %s: %w", p, err)
			return
		}
		if err := f.Sync(); err != nil {
			dylibErr = fmt.Errorf("sync %s: %w", p, err)
			return
		}

		dylibPath = p
	})
	return dylibPath, dylibErr
}
