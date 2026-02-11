// internal/game/katago_v7_infer.go
package game

import (
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"log"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// setNativeEnv 跨平台设置进程环境变量，确保底层 DLL/SO 能正确读取
func setNativeEnv(key, value string) {
	os.Setenv(key, value) // 默认 Go 设置

	if runtime.GOOS == "windows" {
		// Windows 特有的原生调用，同步到 DLL
		setWinEnv(key, value)
	}
}

//go:embed assets/*.onnx.gz
var katagoFS embed.FS

const (
	katagoInputSpatial = "input_spatial"
	katagoInputGlobal  = "input_global"
	katagoOutputPolicy = "out_policy"
	katagoOutputValue  = "out_value"
	katagoGrid         = 9
	katagoPlanes       = 22
	katagoGlobals      = 19
	maxBatchSize       = 64 // 固定 Batch 大小用于加速
)

var (
	katagoOnce      sync.Once
	katagoErr       error
	katagoSess      *ort.AdvancedSession
	katagoSessBatch *ort.AdvancedSession
	katagoMu        sync.Mutex

	// 单步推理张量
	katagoInSpatial *ort.Tensor[float32]
	katagoInGlobal  *ort.Tensor[float32]
	katagoOutPolicy *ort.Tensor[float32]
	katagoOutValue  *ort.Tensor[float32]

	// 批量推理张量
	katagoInSpatialB *ort.Tensor[float32]
	katagoInGlobalB  *ort.Tensor[float32]
	katagoOutPolicyB *ort.Tensor[float32]
	katagoOutValueB  *ort.Tensor[float32]

	katagoModelBytes  []byte
	katagoPolicyHeads = 4

	// 预计算静态平面
	staticSpatialOnce sync.Once
	staticSpatial     []float32 // 包含 Plane 0 (all 1) 和 Plane 3 (Blocked)
)

const ansiReset = "\033[0m"

func init() {
	// 极致尽早重定向日志，防止 PowerShell 将 stderr 误认为错误而变红
	log.SetOutput(os.Stdout)
}

func ensureStaticSpatial() {
	staticSpatialOnce.Do(func() {
		staticSpatial = make([]float32, katagoPlanes*katagoGrid*katagoGrid)
		planeSize := katagoGrid * katagoGrid
		// Plane 0: All 1s
		for i := 0; i < planeSize; i++ {
			staticSpatial[i] = 1.0
		}
		// Plane 3: Blocked (Out of board AND Internal Blocks)
		if !encodeTablesInit {
			initEncodeTables()
		}
		for g := 0; g < planeSize; g++ {
			// 棋盘外
			if !gridInBoard[g] {
				staticSpatial[3*planeSize+g] = 1.0
			}
		}
		// 棋盘内固定障碍物 (来自 state.go: {1, 0}, {-1, 1}, {0, -1})
		internalBlocks := []HexCoord{{1, 0}, {-1, 1}, {0, -1}}
		for _, c := range internalBlocks {
			if idx, ok := IndexOf[c]; ok {
				g := boardIndexToGrid[idx]
				if g >= 0 && g < planeSize {
					staticSpatial[3*planeSize+g] = 1.0
				}
			}
		}
	})
}

func ensureKataONNX() error {
	katagoOnce.Do(func() {
		ensureStaticSpatial()

		// 1. 路径标准化
		exePath, _ := os.Executable()
		baseDir := filepath.Dir(exePath)
		absCachePath := filepath.Join(baseDir, "trt_cache")
		os.MkdirAll(absCachePath, 0755)
		
		// 2. 极致同步环境变量 (设为较高级别以减少干扰日志)
		setNativeEnv("ORT_TENSORRT_ENGINE_CACHE_ENABLE", "1")
		setNativeEnv("ORT_TENSORRT_ENGINE_CACHE_PATH", absCachePath)
		setNativeEnv("ORT_TENSORRT_CACHE_ENABLE", "1")
		setNativeEnv("ORT_TENSORRT_CACHE_PATH", absCachePath)
		setNativeEnv("ORT_TRT_ENGINE_CACHE_ENABLE", "1")
		setNativeEnv("ORT_TRT_CACHE_PATH", absCachePath)
		setNativeEnv("ORT_TENSORRT_TIMING_CACHE_ENABLE", "1") 
		setNativeEnv("ORT_TENSORRT_TIMING_CACHE_PATH", absCachePath)
		setNativeEnv("ORT_TENSORRT_FP16_ENABLE", "1")
		setNativeEnv("ORT_TENSORRT_MAX_WORKSPACE_SIZE", "2147483648")
		
		// 设为 Error 级别 (3)，屏蔽加载失败等警告，防止 stderr 变红
		setNativeEnv("ORT_LOGGING_LEVEL", "3") 
		
		log.Printf("[katago] TRT Debug: Syncing Cache to %s%s", absCachePath, ansiReset)

		// 3. 初始化环境（环境变量设置必须在此之前）
		libPath, _ := prepareORTSharedLib()
		ort.SetSharedLibraryPath(libPath)
		ort.InitializeEnvironment()
		fmt.Print(ansiReset) // 强行重置可能由 ORT 产生的颜色码

		// 4. 模型加载 (直接加载到内存)
		var modelData []byte
		if path := os.Getenv("KATAGO_ONNX_PATH"); path != "" {
			modelData, _ = os.ReadFile(path)
		} else {
			entries, _ := katagoFS.ReadDir("assets")
			for _, e := range entries {
				name := strings.ToLower(e.Name())
				if strings.HasSuffix(name, ".onnx") || strings.HasSuffix(name, ".onnx.gz") {
					b, err := katagoFS.ReadFile("assets/" + e.Name())
					if err != nil { continue }

					if strings.HasSuffix(name, ".gz") {
						gr, err := gzip.NewReader(bytes.NewReader(b))
						if err == nil {
							modelData, _ = io.ReadAll(gr)
							gr.Close()
						}
					} else {
						modelData = b
					}
					break
				}
			}
		}

		if len(modelData) == 0 {
			katagoErr = fmt.Errorf("no KataGo ONNX model found")
			return
		}

		// 4. 初始化推理张量 (这些可以复用)
		katagoInSpatial, _ = ort.NewTensor(ort.NewShape(1, katagoPlanes, katagoGrid, katagoGrid), make([]float32, katagoPlanes*katagoGrid*katagoGrid))
		katagoInGlobal, _ = ort.NewTensor(ort.NewShape(1, katagoGlobals), make([]float32, katagoGlobals))
		katagoOutPolicy, _ = ort.NewEmptyTensor[float32](ort.NewShape(1, int64(katagoPolicyHeads), katagoGrid*katagoGrid+1))
		katagoOutValue, _ = ort.NewEmptyTensor[float32](ort.NewShape(1, 3))

		katagoInSpatialB, _ = ort.NewTensor(ort.NewShape(maxBatchSize, katagoPlanes, katagoGrid, katagoGrid), make([]float32, maxBatchSize*katagoPlanes*katagoGrid*katagoGrid))
		katagoInGlobalB, _ = ort.NewTensor(ort.NewShape(maxBatchSize, katagoGlobals), make([]float32, maxBatchSize*katagoGlobals))
		katagoOutPolicyB, _ = ort.NewEmptyTensor[float32](ort.NewShape(maxBatchSize, int64(katagoPolicyHeads), katagoGrid*katagoGrid+1))
		katagoOutValueB, _ = ort.NewEmptyTensor[float32](ort.NewShape(maxBatchSize, 3))

		// 5. 定义并尝试多种策略
		type strategy struct {
			name  string
			setup func(*ort.SessionOptions) error
		}

		var strategies []strategy
		if runtime.GOOS == "darwin" {
			strategies = []strategy{
				{"CoreML", func(so *ort.SessionOptions) error {
					return so.AppendExecutionProviderCoreMLV2(map[string]string{"use_ane": "1"})
				}},
				{"CPU", func(so *ort.SessionOptions) error { return nil }},
			}
		} else if runtime.GOOS == "windows" {
			strategies = []strategy{
				{"TensorRT", func(so *ort.SessionOptions) error {
					trtOpts, e := ort.NewTensorRTProviderOptions()
					if e != nil {
						return e
					}
					defer trtOpts.Destroy()
					// 显式设置配置以确保缓存复用
					trtOpts.Update(map[string]string{
						"device_id":               "0",
						"trt_engine_cache_enable": "1",
						"trt_engine_cache_path":   absCachePath,
						"trt_fp16_enable":         "1",
						"trt_max_workspace_size":  "2147483648",
						"trt_timing_cache_enable": "1",
						"trt_timing_cache_path":   absCachePath,
					})
					return so.AppendExecutionProviderTensorRT(trtOpts)
				}},
				{"CUDA", func(so *ort.SessionOptions) error {
					cudaOpts, e := ort.NewCUDAProviderOptions()
					if e != nil {
						return e
					}
					defer cudaOpts.Destroy()
					return so.AppendExecutionProviderCUDA(cudaOpts)
				}},
				{"DirectML", func(so *ort.SessionOptions) error {
					return so.AppendExecutionProviderDirectML(0)
				}},
				{"CPU", func(so *ort.SessionOptions) error { return nil }},
			}
		} else {
			// Linux or other
			strategies = []strategy{
				{"TensorRT", func(so *ort.SessionOptions) error {
					if trtOpts, e := ort.NewTensorRTProviderOptions(); e == nil {
						defer trtOpts.Destroy()
						trtOpts.Update(map[string]string{
							"device_id":               "0",
							"trt_engine_cache_enable": "1",
							"trt_engine_cache_path":   absCachePath,
							"trt_fp16_enable":         "1",
							"trt_max_workspace_size":  "2147483648",
							"trt_timing_cache_enable": "1",
							"trt_timing_cache_path":   absCachePath,
						})
						return so.AppendExecutionProviderTensorRT(trtOpts)
					}
					return fmt.Errorf("TensorRT options creation failed")
				}},
				{"CUDA", func(so *ort.SessionOptions) error {
					if cudaOpts, e := ort.NewCUDAProviderOptions(); e == nil {
						defer cudaOpts.Destroy()
						return so.AppendExecutionProviderCUDA(cudaOpts)
					}
					return fmt.Errorf("CUDA options creation failed")
				}},
				{"CPU", func(so *ort.SessionOptions) error { return nil }},
			}
		}

		var success bool
		for _, st := range strategies {
			log.Printf("[katago] Attempting to initialize with %s...%s", st.name, ansiReset)

			so, err := ort.NewSessionOptions()
			if err != nil {
				continue
			}
			// 设置日志级别为 Error (3)，避免输出警告和信息，防止变红
			_ = so.SetLogSeverityLevel(3)

			if err := st.setup(so); err != nil {
				log.Printf("[katago] %s setup failed: %v%s", st.name, err, ansiReset)
				so.Destroy()
				continue
			}

			// 尝试创建会话
			s1, err1 := ort.NewAdvancedSessionWithONNXData(
				modelData,
				[]string{katagoInputSpatial, katagoInputGlobal},
				[]string{katagoOutputPolicy, katagoOutputValue},
				[]ort.Value{katagoInSpatial, katagoInGlobal},
				[]ort.Value{katagoOutPolicy, katagoOutValue},
				so,
			)
			if err1 != nil {
				log.Printf("[katago] %s session creation failed: %v%s", st.name, err1, ansiReset)
				so.Destroy()
				continue
			}

			s2, err2 := ort.NewAdvancedSessionWithONNXData(
				modelData,
				[]string{katagoInputSpatial, katagoInputGlobal},
				[]string{katagoOutputPolicy, katagoOutputValue},
				[]ort.Value{katagoInSpatialB, katagoInGlobalB},
				[]ort.Value{katagoOutPolicyB, katagoOutValueB},
				so,
			)
			if err2 != nil {
				log.Printf("[katago] %s batch session creation failed: %v%s", st.name, err2, ansiReset)
				s1.Destroy()
				so.Destroy()
				continue
			}

			// 热身
			log.Printf("[katago] Warming up %s...%s", st.name, ansiReset)
			if errR1 := s1.Run(); errR1 != nil {
				log.Printf("[katago] %s warm-up 1 failed: %v%s", st.name, errR1, ansiReset)
				s1.Destroy()
				s2.Destroy()
				so.Destroy()
				continue
			}
			if errR2 := s2.Run(); errR2 != nil {
				log.Printf("[katago] %s warm-up 2 failed: %v%s", st.name, errR2, ansiReset)
				s1.Destroy()
				s2.Destroy()
				so.Destroy()
				continue
			}

			// 成功！
			katagoSess = s1
			katagoSessBatch = s2
			katagoErr = nil
			success = true
			log.Printf("[katago] Successfully initialized with %s.%s", st.name, ansiReset)
			so.Destroy()
			break
		}

		if !success {
			katagoErr = fmt.Errorf("failed to initialize KataGo ONNX with any strategy")
		}
	})
	return katagoErr
}

func encodeKataInputs(b *Board, me CellState, spatial []float32, global []float32, selectedIdx int) {
	if !encodeTablesInit {
		initEncodeTables()
	}
	// 拷贝静态平面 (Plane 0 和 Plane 3) - 现在 Plane 3 已包含所有障碍物
	copy(spatial, staticSpatial)
	// 清空 Global
	for i := range global {
		global[i] = 0
	}

	planeSize := katagoGrid * katagoGrid
	
	// 使用位掩码加速特征提取
	var myBit, opBit uint64
	if me == PlayerA {
		myBit, opBit = b.bitA, b.bitB
	} else {
		myBit, opBit = b.bitB, b.bitA
	}

	// Plane 1: Me
	tempMy := myBit
	for tempMy != 0 {
		i := bits.TrailingZeros64(tempMy)
		tempMy &= ^(uint64(1) << uint(i))
		g := boardIndexToGrid[i]
		if g >= 0 && g < planeSize {
			spatial[planeSize+g] = 1.0
		}
	}

	// Plane 2: Opponent
	tempOp := opBit
	for tempOp != 0 {
		i := bits.TrailingZeros64(tempOp)
		tempOp &= ^(uint64(1) << uint(i))
		g := boardIndexToGrid[i]
		if g >= 0 && g < planeSize {
			spatial[2*planeSize+g] = 1.0
		}
	}

	// Plane 3: Blocked 已在 copy(spatial, staticSpatial) 中处理，无需再遍历

	stageOne := selectedIdx >= 0
	if stageOne && selectedIdx < planeSize {
		spatial[4*planeSize+selectedIdx] = 1.0 // Plane 4
	}
	if stageOne {
		global[0] = 1.0
	}
	global[9] = 1.0
}

func KataBatchValueScore(boards []*Board, me CellState) ([]int, error) {
	return KataBatchValueScoreWithSelection(boards, me, nil)
}

func KataBatchValueScoreWithSelection(boards []*Board, me CellState, selectedIndices []int) ([]int, error) {
	if err := ensureKataONNX(); err != nil {
		return nil, err
	}
	n := len(boards)
	if n == 0 {
		return nil, nil
	}
	if n > maxBatchSize {
		n = maxBatchSize
	}

	// 1. 并行编码 (不需要持锁)
	// 使用预分配的本地切片，减少 GC
	localSpatial := make([]float32, maxBatchSize*katagoPlanes*katagoGrid*katagoGrid)
	localGlobal := make([]float32, maxBatchSize*katagoGlobals)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			startS := idx * katagoPlanes * katagoGrid * katagoGrid
			startG := idx * katagoGlobals
			selIdx := -1
			if selectedIndices != nil {
				selIdx = selectedIndices[idx]
			}
			encodeKataInputs(boards[idx], me,
				localSpatial[startS:startS+katagoPlanes*katagoGrid*katagoGrid],
				localGlobal[startG:startG+katagoGlobals],
				selIdx)
		}(i)
	}
	wg.Wait()

	// 2. 拷贝数据到张量并执行推理 (持锁)
	katagoMu.Lock()
	copy(katagoInSpatialB.GetData(), localSpatial)
	copy(katagoInGlobalB.GetData(), localGlobal)

	// 如果 n < maxBatchSize，对于剩余部分需要显式清零（或者利用 staticSpatial 填充，但最安全是清零 Plane 1,2,4...）
	if n < maxBatchSize {
		sData := katagoInSpatialB.GetData()
		gData := katagoInGlobalB.GetData()
		for i := n; i < maxBatchSize; i++ {
			startS := i * katagoPlanes * katagoGrid * katagoGrid
			startG := i * katagoGlobals
			// 简单起见，全填 0。Plane 0 虽然应该是 1，但在 Batch 尾部不影响结果。
			for j := startS; j < startS+katagoPlanes*katagoGrid*katagoGrid; j++ {
				sData[j] = 0
			}
			for j := startG; j < startG+katagoGlobals; j++ {
				gData[j] = 0
			}
		}
	}

	if err := katagoSessBatch.Run(); err != nil {
		katagoMu.Unlock()
		return nil, err
	}

	// 3. 拷贝结果 (尽快解锁)
	valsRaw := katagoOutValueB.GetData()
	vals := make([]float32, n*3)
	copy(vals, valsRaw[:n*3])
	katagoMu.Unlock()

	// 4. 后处理结果 (不需要持锁)
	res := make([]int, n)
	for i := 0; i < n; i++ {
		v := vals[i*3 : (i+1)*3]
		maxVal := v[0]
		if v[1] > maxVal {
			maxVal = v[1]
		}
		if v[2] > maxVal {
			maxVal = v[2]
		}
		e0 := math.Exp(float64(v[0] - maxVal))
		e1 := math.Exp(float64(v[1] - maxVal))
		e2 := math.Exp(float64(v[2] - maxVal))
		score := float32((e0 - e1) / (e0 + e1 + e2))
		res[i] = int(score * 1000)
	}
	return res, nil
}


// 补全 ai_twophase.go 需要的底层函数
func KataPolicyValueWithSelection(b *Board, me CellState, selectedIdx int) ([]float32, float32, error) {
	if err := ensureKataONNX(); err != nil {
		return nil, 0, err
	}

	katagoMu.Lock()
	defer katagoMu.Unlock()

	encodeKataInputs(b, me, katagoInSpatial.GetData(), katagoInGlobal.GetData(), selectedIdx)
	if err := katagoSess.Run(); err != nil {
		return nil, 0, err
	}

	logits := make([]float32, katagoGrid*katagoGrid+1)
	copy(logits, katagoOutPolicy.GetData()[:len(logits)])

	// Softmax for policy
	maxLogit := float32(-1e30)
	for _, v := range logits {
		if v > maxLogit {
			maxLogit = v
		}
	}
	var sumP float64
	for i, v := range logits {
		ev := math.Exp(float64(v - maxLogit))
		logits[i] = float32(ev)
		sumP += ev
	}
	for i := range logits {
		logits[i] /= float32(sumP)
	}

	// Value probabilities
	vals := katagoOutValue.GetData()
	maxVal := vals[0]
	if vals[1] > maxVal {
		maxVal = vals[1]
	}
	if vals[2] > maxVal {
		maxVal = vals[2]
	}
	e0 := math.Exp(float64(vals[0] - maxVal))
	e1 := math.Exp(float64(vals[1] - maxVal))
	e2 := math.Exp(float64(vals[2] - maxVal))
	sumV := e0 + e1 + e2
	score := float32((e0 - e1) / sumV)

	return logits, score, nil
}

func KataWinProb(b *Board, me CellState) (float32, error) {
	if err := ensureKataONNX(); err != nil {
		return 0, err
	}

	katagoMu.Lock()
	defer katagoMu.Unlock()

	encodeKataInputs(b, me, katagoInSpatial.GetData(), katagoInGlobal.GetData(), -1)
	if err := katagoSess.Run(); err != nil {
		return 0, err
	}

	vals := katagoOutValue.GetData()
	maxVal := vals[0]
	if vals[1] > maxVal {
		maxVal = vals[1]
	}
	if vals[2] > maxVal {
		maxVal = vals[2]
	}
	e0 := math.Exp(float64(vals[0] - maxVal))
	e1 := math.Exp(float64(vals[1] - maxVal))
	e2 := math.Exp(float64(vals[2] - maxVal))
	sumV := e0 + e1 + e2
	return float32(e0 / sumV), nil
}

func KataPolicyValue(b *Board, me CellState) ([]float32, float32, error) {
	return KataPolicyValueWithSelection(b, me, -1)
}

func KataValueScore(b *Board, me CellState) (int, error) {
	_, score, err := KataPolicyValue(b, me)
	if err != nil { return 0, err }
	return int(score * 1000), nil
}

func KataValueScoreWithSelection(b *Board, me CellState, selectedIdx int) (int, error) {
	res, err := KataBatchValueScoreWithSelection([]*Board{b}, me, []int{selectedIdx})
	if err != nil || len(res) == 0 {
		return 0, err
	}
	return res[0], nil
}

// PreloadModels 预加载模型，触发 TensorRT 编译或加载缓存
func PreloadModels() {
	go func() {
		log.Printf("[katago] Preloading models and initializing ONNX session...%s", ansiReset)
		if err := ensureKataONNX(); err != nil {
			log.Printf("[katago] Model preloading failed: %v%s", err, ansiReset)
		} else {
			log.Printf("[katago] Model preloading complete.%s", ansiReset)
		}
	}()
}
