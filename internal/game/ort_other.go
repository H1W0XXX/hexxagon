// internal/game/ort_other.go
//go:build !windows

package game

// 非 Windows 平台不需要此原生调用
func setWinEnv(key, value string) {}
