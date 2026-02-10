// internal/game/ort_windows.go
//go:build windows

package game

import (

	"os"

	"syscall"

	"unsafe"

)



var (

	modkernel32W = syscall.NewLazyDLL("kernel32.dll")

	procSetEnvW  = modkernel32W.NewProc("SetEnvironmentVariableW")

)



// setWinEnv 强制修改 Windows 进程环境变量，确保底层 C++ DLL 能读取到

func setWinEnv(key, value string) {

	k, _ := syscall.UTF16PtrFromString(key)

	v, _ := syscall.UTF16PtrFromString(value)

	_, _, _ = procSetEnvW.Call(uintptr(unsafe.Pointer(k)), uintptr(unsafe.Pointer(v)))

}



func prepareORTSharedLib() (string, error) {

	if p := os.Getenv("ONNXRUNTIME_SHARED_LIBRARY_PATH"); p != "" {

		return p, nil

	}

	// 默认返回库文件名，依赖系统 PATH 搜索

	return "onnxruntime.dll", nil

}
