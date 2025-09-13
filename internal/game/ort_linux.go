// internal/game/ort_linux.go
//go:build linux

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
//go:embed assets/libonnxruntime.so
var onnxruntimeSO []byte

var (
	soOnce sync.Once
	soPath string
	soErr  error
)

// prepareORTSharedLib 确保 ORT 的 .so 在可执行文件旁边可被加载，并返回其绝对路径。
// 与 darwin 版一致：若已存在则直接复用；不存在则从内置资源写出（并发安全）。
func prepareORTSharedLib() (string, error) {
	soOnce.Do(func() {
		exe, _ := os.Executable()
		wd := filepath.Dir(exe)
		p := filepath.Join(wd, "libonnxruntime.so")

		// 1) 已存在：直接用
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			soPath = p
			return
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			soErr = fmt.Errorf("stat %s: %w", p, err)
			return
		}

		// 2) 不存在：尝试独占创建，避免并发覆盖
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			// 并发下被别的进程/协程先创建也 OK，直接复用
			if errors.Is(err, os.ErrExist) {
				soPath = p
				return
			}
			soErr = fmt.Errorf("create %s: %w", p, err)
			return
		}
		defer f.Close()

		if _, err := f.Write(onnxruntimeSO); err != nil {
			soErr = fmt.Errorf("write %s: %w", p, err)
			return
		}
		if err := f.Sync(); err != nil {
			soErr = fmt.Errorf("sync %s: %w", p, err)
			return
		}

		soPath = p
	})
	return soPath, soErr
}
