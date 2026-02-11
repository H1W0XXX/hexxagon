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
	"syscall"
	"unsafe"
)

// 嵌入 Windows 运行所需的轻量 DLL
//go:embed assets/onnxruntime.dll
var onnxruntimeDLL []byte

//go:embed assets/DirectML.dll
var directmlDLL []byte

//go:embed assets/onnxruntime_providers_shared.dll
var providersSharedDLL []byte

var (
	modkernel32W = syscall.NewLazyDLL("kernel32.dll")
	procSetEnvW  = modkernel32W.NewProc("SetEnvironmentVariableW")

	winLibOnce sync.Once
	winLibPath string
	winLibErr  error
)

// setWinEnv 强制修改 Windows 进程环境变量，确保底层 C++ DLL 能读取到
func setWinEnv(key, value string) {
	k, _ := syscall.UTF16PtrFromString(key)
	v, _ := syscall.UTF16PtrFromString(value)
	_, _, _ = procSetEnvW.Call(uintptr(unsafe.Pointer(k)), uintptr(unsafe.Pointer(v)))
}

func prepareORTSharedLib() (string, error) {
	winLibOnce.Do(func() {
		if p := os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH"); p != "" {
			winLibPath = p
			return
		}

		exe, _ := os.Executable()
		wd := filepath.Dir(exe)

		// 1. 释放 DirectML.dll
		dmlPath := filepath.Join(wd, "DirectML.dll")
		if err := ensureFile(dmlPath, directmlDLL); err != nil {
			winLibErr = fmt.Errorf("failed to extract DirectML.dll: %w", err)
			return
		}

		// 1.5 释放 onnxruntime_providers_shared.dll (部分版本运行需要)
		sharedPath := filepath.Join(wd, "onnxruntime_providers_shared.dll")
		if err := ensureFile(sharedPath, providersSharedDLL); err != nil {
			winLibErr = fmt.Errorf("failed to extract onnxruntime_providers_shared.dll: %w", err)
			return
		}

		// 2. 再释放 onnxruntime.dll
		ortPath := filepath.Join(wd, "onnxruntime.dll")
		if err := ensureFile(ortPath, onnxruntimeDLL); err != nil {
			winLibErr = fmt.Errorf("failed to extract onnxruntime.dll: %w", err)
			return
		}

		winLibPath = ortPath
	})
	return winLibPath, winLibErr
}

// ensureFile 检查文件是否存在，不存在则写入
func ensureFile(path string, data []byte) error {
	if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
		return nil // 已存在
	}

	// 尝试独占创建，避免多进程冲突
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	_, err = f.Write(data)
	return err
}
