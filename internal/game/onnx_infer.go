// internal/game/onnx_infer.go
package game

import (
	"embed"
	"fmt"
	"log"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// —— 把 ONNX 模型打进二进制 ——
//
//go:embed assets/*.onnx.gz
var embeddedFS embed.FS

// 如果你的导出脚本用了别的输入/输出名，请改这里：
// 训练脚本里通常是 input_names=["state"], output_names=["policy","value"]
const (
	onnxInputName  = "state"
	onnxPolicyName = "policy"
	onnxValueName  = "value"
	grid           = 9
	radius         = 4
	featPlanes     = 3 // [my, opp, mask]
	policyOutDim   = 81
)

var (
	ortOnce       sync.Once
	ortErr        error
	ortSess       *ort.AdvancedSession
	ortMu         sync.Mutex // AdvancedSession 里绑定了固定的张量，这里串行化 Run，先稳妥跑通
	inTensor      *ort.Tensor[float32]
	outP          *ort.Tensor[float32]
	outV          *ort.Tensor[float32]
	tmpModel      string
	externalModel []byte
)

// 初始化 ONNX Runtime & 会话
func ensureONNX() error {
	//log.Printf("[ensureONNX] invoked")
	ortOnce.Do(func() {
		// 0) 外部模型路径优先：设置 HEX_ONNX_PATH 指定
		if path := os.Getenv("HEX_ONNX_PATH"); path != "" {
			b, err := os.ReadFile(path)
			if err != nil {
				ortErr = fmt.Errorf("read HEX_ONNX_PATH %s: %w", path, err)
				return
			}
			externalModel = b
			log.Printf("[ensureONNX] using external ONNX: %s", path)
		} else {
			// 尝试从 embed 的 assets 目录找任意 .onnx
			entries, err := embeddedFS.ReadDir("assets")
			if err == nil {
				for _, e := range entries {
					if e.IsDir() {
						continue
					}
					if strings.HasSuffix(strings.ToLower(e.Name()), ".onnx") {
						b, rerr := embeddedFS.ReadFile("assets/" + e.Name())
						if rerr == nil {
							externalModel = b
							log.Printf("[ensureONNX] using embedded ONNX: assets/%s", e.Name())
							break
						}
						ortErr = fmt.Errorf("read embedded assets/%s: %w", e.Name(), rerr)
						return
					}
				}
			}
		}

		// 1) 准备并加载 ORT 动态库（平台函数 prepareORTSharedLib/ensureDLLInCWD）
		libPath, err := prepareORTSharedLib()
		if err != nil {
			ortErr = fmt.Errorf("prepare ORT lib: %w", err)
			return
		}
		log.Printf("[ensureONNX] using ORT shared lib: %s", libPath)
		ort.SetSharedLibraryPath(libPath)

		// 2) 初始化 ORT
		if err := ort.InitializeEnvironment(); err != nil {
			ortErr = fmt.Errorf("InitializeEnvironment: %w", err)
			log.Printf("[ensureONNX] InitializeEnvironment failed: %v", ortErr)
			return
		}
		log.Printf("[ensureONNX] InitializeEnvironment succeeded")

		// 3) 模型字节自检（外部优先，否则 embed）
		modelBytes := externalModel
		if len(modelBytes) == 0 {
			ortErr = fmt.Errorf("no ONNX model: set HEX_ONNX_PATH or place any .onnx under internal/game/assets")
			return
		}
		inputs, outputs, gierr := ort.GetInputOutputInfoWithONNXData(modelBytes)
		if gierr != nil {
			ortErr = fmt.Errorf("GetInputOutputInfoWithONNXData: %w", gierr)
			return
		}
		log.Printf("[ensureONNX] model IO info: inputs=%v outputs=%v", inputs, outputs)

		// 4) 创建 I/O 张量（必须在 InitializeEnvironment 之后）
		var e error
		inTensor, e = ort.NewTensor(ort.NewShape(1, featPlanes, grid, grid), make([]float32, featPlanes*grid*grid))
		if e != nil || inTensor == nil {
			ortErr = fmt.Errorf("NewTensor input failed: %v", e)
			return
		}
		outP, e = ort.NewEmptyTensor[float32](ort.NewShape(1, policyOutDim))
		if e != nil || outP == nil {
			ortErr = fmt.Errorf("NewEmptyTensor policy failed: %v", e)
			return
		}
		outV, e = ort.NewEmptyTensor[float32](ort.NewShape(1, 1))
		if e != nil || outV == nil {
			ortErr = fmt.Errorf("NewEmptyTensor value failed: %v", e)
			return
		}

		// 5) 用内存模型创建 AdvancedSession
		so, err := ort.NewSessionOptions()
		if err != nil {
			ortErr = fmt.Errorf("NewSessionOptions: %w", err)
			return
		}

		// 可选：图优化更激进（不同版本接口名可能略有不同，可省略）
		// _ = so.SetGraphOptimizationLevel(ort.GraphOptimizationLevelAll)

		if runtime.GOOS == "windows" {
			if cudaOpts, e := ort.NewCUDAProviderOptions(); e == nil && cudaOpts != nil {
				// 可选：设置 device_id 等；值用字符串
				// 参考官方配置键：https://onnxruntime.ai/docs/execution-providers/CUDA-ExecutionProvider.html#configuration-options
				_ = cudaOpts.Update(map[string]string{
					"device_id": "0",
					// "arena_extend_strategy": "kNextPowerOfTwo",
					// "gpu_mem_limit": "0",
				})
				if err := so.AppendExecutionProviderCUDA(cudaOpts); err != nil {
					log.Printf("[ensureONNX] CUDA EP init failed, fallback to CPU: %v", err)
				}
				_ = cudaOpts.Destroy()
			} else {
				log.Printf("[ensureONNX] NewCUDAProviderOptions failed, fallback to CPU: %v", e)
			}
		}

		ortSess, e = ort.NewAdvancedSessionWithONNXData(
			modelBytes,
			[]string{onnxInputName},
			[]string{onnxPolicyName, onnxValueName},
			[]ort.Value{inTensor},
			[]ort.Value{outP, outV},
			so,
		)
		if e != nil || ortSess == nil {
			ortErr = fmt.Errorf("NewAdvancedSessionWithONNXData: %v", e)
			return
		}
		log.Printf("[ensureONNX] AdvancedSession created successfully (memory)")
	})
	if ortErr != nil {
		log.Printf("[ensureONNX] returning error: %v", ortErr)
	}
	return ortErr
}

// 可选：在程序退出时调用，清理临时文件与环境
func ShutdownONNX() {
	if tmpModel != "" {
		_ = os.Remove(tmpModel)
	}
	if ortSess != nil {
		ortSess.Destroy()
		ortSess = nil
	}
	ort.DestroyEnvironment()
}

// 计算 (q,r) 是否在半径为 4 的六边形棋盘内
func inBounds(q, r int) bool {
	return abs(q) <= radius && abs(r) <= radius && abs(-q-r) <= radius
}
func toIndex(q, r int) int { // 9x9 平面索引
	return (r+radius)*grid + (q + radius)
}

// 把 Board 编成 3×9×9：my=1 / opp=1 / mask=1
func encodeBoard(b *Board, me CellState, dst []float32) {
	for i := range dst {
		dst[i] = 0
	}
	// plane offsets
	offMy, offOpp, offMask := 0, grid*grid, 2*grid*grid
	for r := -radius; r <= radius; r++ {
		for q := -radius; q <= radius; q++ {
			idx := toIndex(q, r)
			if !inBounds(q, r) {
				continue
			}
			for i := 0; i < BoardN; i++ {
				switch b.Cells[i] {
				case me:
					dst[offMy+i] = 1
				case Opponent(me):
					dst[offOpp+i] = 1
				}
			}
			dst[offMask+idx] = 1
		}
	}
}

// 只取 value 头做静态评估（返回 int，方便接到你的评分框架）
//func EvaluateNN(b *Board, me CellState) int {
//	if err := ensureONNX(); err != nil {
//		// 回退到旧静态评估也行：
//		// return evaluateStatic(b, me)
//		fmt.Fprintln(os.Stderr, "Failed to init ONNX:", err)
//		return 0
//	}
//	// 填充输入
//	data := inTensor.GetData()
//	encodeBoard(b, me, data)
//
//	// 跑一次
//	ortMu.Lock()
//	err := ortSess.Run()
//	ortMu.Unlock()
//	if err != nil {
//		return 0
//	}
//	// 读取 value，范围(-1,1)，放大到可比较的整数
//	v := outV.GetData()[0]
//	return int(v * 100.0)
//}

func EvaluateNN3(b *Board, me CellState) int {
	if err := ensureONNX(); err != nil {
		// 回退到旧静态评估
		fmt.Fprintln(os.Stderr, "Failed to init ONNX:", err)
		return 0
	}
	// 填充输入
	data := inTensor.GetData()
	encodeBoard(b, me, data)

	// 跑一次
	ortMu.Lock()
	err := ortSess.Run()
	ortMu.Unlock()
	if err != nil {
		return 0
	}
	// 读取 value，logits -> sigmoid 转换为概率
	vLogit := outV.GetData()[0]
	vProb := 1 / (1 + math.Exp(float64(-vLogit))) // Sigmoid

	// 将概率放大为整数，方便评估
	return int(vProb * 100.0)
}

func PolicyNN(b *Board, me CellState) ([]float32, error) {
	if err := ensureONNX(); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to init ONNX:", err)
		return nil, err
	}
	// 输入
	data := inTensor.GetData()
	encodeBoard(b, me, data)

	// 跑一次
	ortMu.Lock()
	err := ortSess.Run()
	ortMu.Unlock()
	if err != nil {
		return nil, err
	}

	// 获取 policy logits
	logits := make([]float32, policyOutDim)
	copy(logits, outP.GetData())

	// 如果你需要概率，可以做 softmax：
	// softmax
	expSum := float32(0)
	for _, logit := range logits {
		expSum += float32(math.Exp(float64(logit)))
	}
	softmax := make([]float32, len(logits))
	for i, logit := range logits {
		softmax[i] = float32(math.Exp(float64(logit))) / expSum
	}
	// softmax 数组包含每个动作的概率

	return softmax, nil
}

// PolicyValueNN：一次前向同时取 policy 概率与 value 概率
// policy 是 81 维 softmax，valueProb 为当前执子方获胜概率 [0,1]
func PolicyValueNN(b *Board, me CellState) ([]float32, float32, error) {
	if err := ensureONNX(); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to init ONNX:", err)
		return nil, 0, err
	}
	// 输入
	data := inTensor.GetData()
	encodeBoard(b, me, data)

	// 跑一次
	ortMu.Lock()
	err := ortSess.Run()
	ortMu.Unlock()
	if err != nil {
		return nil, 0, err
	}

	// policy softmax
	logits := make([]float32, policyOutDim)
	copy(logits, outP.GetData())
	expSum := float32(0)
	for _, l := range logits {
		expSum += float32(math.Exp(float64(l)))
	}
	policy := make([]float32, len(logits))
	if expSum == 0 {
		uni := 1.0 / float32(len(logits))
		for i := range policy {
			policy[i] = uni
		}
	} else {
		for i, l := range logits {
			policy[i] = float32(math.Exp(float64(l))) / expSum
		}
	}

	// value：logit -> prob
	vLogit := outV.GetData()[0]
	vProb := float32(1.0 / (1.0 + math.Exp(float64(-vLogit))))

	return policy, vProb, nil
}

// —— 小工具 ——
// 直接给 policy 向量打非法格 -Inf
func MaskPolicyInPlace(p []float32) {
	const negInf = -1.0e30
	i := 0
	for r := -radius; r <= radius; r++ {
		for q := -radius; q <= radius; q++ {
			if !inBounds(q, r) {
				p[i] = negInf
			}
			i++
		}
	}
}
