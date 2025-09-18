package ui

import "github.com/hajimehoshi/ebiten/v2"

var (
	perfOn = true // 默认以高刷新启动，保证首帧流程正常
	booted bool   // 首帧是否已经进入稳定状态
)

func enterPerf() {
	if perfOn {
		return
	}
	ebiten.SetFPSMode(ebiten.FPSModeVsyncOn)
	ebiten.SetMaxTPS(30)
	perfOn = true
}

func leavePerf(force bool) {
	if !perfOn && !force {
		return
	}
	ebiten.SetFPSMode(ebiten.FPSModeVsyncOffMinimum)
	ebiten.SetMaxTPS(10)
	perfOn = false
}

func ensurePerf(active bool) {
	if active {
		enterPerf()
	} else {
		leavePerf(false)
	}
}

func markBooted() {
	if booted {
		return
	}
	booted = true
	leavePerf(true) // 首次进入时立即尝试降档
}
