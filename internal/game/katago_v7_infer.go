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
	"os"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

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
	katagoOnce        sync.Once
	katagoErr         error
	katagoSess        *ort.AdvancedSession
	katagoSessBatch   *ort.AdvancedSession
	katagoMu          sync.Mutex

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

func ensureStaticSpatial() {
	staticSpatialOnce.Do(func() {
		staticSpatial = make([]float32, katagoPlanes*katagoGrid*katagoGrid)
		planeSize := katagoGrid * katagoGrid
		// Plane 0: All 1s
		for i := 0; i < planeSize; i++ {
			staticSpatial[i] = 1.0
		}
		// Plane 3: Blocked (Out of board)
		if !encodeTablesInit {
			initEncodeTables()
		}
		for g := 0; g < planeSize; g++ {
			if !gridInBoard[g] {
				staticSpatial[3*planeSize+g] = 1.0
			}
		}
	})
}

func ensureKataONNX() error {
	katagoOnce.Do(func() {
		ensureStaticSpatial()
		// 1. 加载模型字节
		if path := os.Getenv("KATAGO_ONNX_PATH"); path != "" {
			if b, err := os.ReadFile(path); err == nil {
				katagoModelBytes = b
				log.Printf("[katago] using external ONNX: %s", path)
			}
		} else {
			entries, _ := katagoFS.ReadDir("assets")
			for _, e := range entries {
				name := strings.ToLower(e.Name())
				if strings.HasSuffix(name, ".onnx") || strings.HasSuffix(name, ".onnx.gz") {
					b, err := katagoFS.ReadFile("assets/" + e.Name())
					if err != nil {
						continue
					}

					if strings.HasSuffix(name, ".gz") {
						gr, err := gzip.NewReader(bytes.NewReader(b))
						if err == nil {
							decompressed, _ := io.ReadAll(gr)
							gr.Close()
							katagoModelBytes = decompressed
							log.Printf("[katago] using compressed embedded ONNX: %s", e.Name())
						}
					} else {
						katagoModelBytes = b
						log.Printf("[katago] using embedded ONNX: %s", e.Name())
					}
					break
				}
			}
		}
		if len(katagoModelBytes) == 0 {
			katagoErr = fmt.Errorf("no KataGo ONNX model found")
			return
		}

		// 2. 初始化环境
		libPath, _ := prepareORTSharedLib()
		ort.SetSharedLibraryPath(libPath)
		ort.InitializeEnvironment()

		// 3. 读取模型信息
		if _, outs, err := ort.GetInputOutputInfoWithONNXData(katagoModelBytes); err == nil {
			for _, o := range outs {
				if strings.EqualFold(o.Name, katagoOutputPolicy) {
					if len(o.Dimensions) >= 2 && o.Dimensions[1] > 0 {
						katagoPolicyHeads = int(o.Dimensions[1])
					}
					break
				}
			}
		}

		so, _ := ort.NewSessionOptions()
		if cudaOpts, e := ort.NewCUDAProviderOptions(); e == nil {
			so.AppendExecutionProviderCUDA(cudaOpts)
			cudaOpts.Destroy()
		}

		// 4. 初始化单步推理会话
		katagoInSpatial, _ = ort.NewTensor(ort.NewShape(1, katagoPlanes, katagoGrid, katagoGrid), make([]float32, katagoPlanes*katagoGrid*katagoGrid))
		katagoInGlobal, _ = ort.NewTensor(ort.NewShape(1, katagoGlobals), make([]float32, katagoGlobals))
		katagoOutPolicy, _ = ort.NewEmptyTensor[float32](ort.NewShape(1, int64(katagoPolicyHeads), katagoGrid*katagoGrid+1))
		katagoOutValue, _ = ort.NewEmptyTensor[float32](ort.NewShape(1, 3))

		katagoSess, katagoErr = ort.NewAdvancedSessionWithONNXData(
			katagoModelBytes,
			[]string{katagoInputSpatial, katagoInputGlobal},
			[]string{katagoOutputPolicy, katagoOutputValue},
			[]ort.Value{katagoInSpatial, katagoInGlobal},
			[]ort.Value{katagoOutPolicy, katagoOutValue},
			so,
		)

		// 5. 初始化批量推理会话 (Fixed Batch Size = 64)
		katagoInSpatialB, _ = ort.NewTensor(ort.NewShape(maxBatchSize, katagoPlanes, katagoGrid, katagoGrid), make([]float32, maxBatchSize*katagoPlanes*katagoGrid*katagoGrid))
		katagoInGlobalB, _ = ort.NewTensor(ort.NewShape(maxBatchSize, katagoGlobals), make([]float32, maxBatchSize*katagoGlobals))
		katagoOutPolicyB, _ = ort.NewEmptyTensor[float32](ort.NewShape(maxBatchSize, int64(katagoPolicyHeads), katagoGrid*katagoGrid+1))
		katagoOutValueB, _ = ort.NewEmptyTensor[float32](ort.NewShape(maxBatchSize, 3))

		katagoSessBatch, _ = ort.NewAdvancedSessionWithONNXData(
			katagoModelBytes,
			[]string{katagoInputSpatial, katagoInputGlobal},
			[]string{katagoOutputPolicy, katagoOutputValue},
			[]ort.Value{katagoInSpatialB, katagoInGlobalB},
			[]ort.Value{katagoOutPolicyB, katagoOutValueB},
			so,
		)
	})
	return katagoErr
}

func encodeKataInputs(b *Board, me CellState, spatial []float32, global []float32, selectedIdx int) {
	if !encodeTablesInit {
		initEncodeTables()
	}
	// 拷贝静态平面 (Plane 0 和 Plane 3)
	copy(spatial, staticSpatial)
	// 清空 Global
	for i := range global {
		global[i] = 0
	}

	planeSize := katagoGrid * katagoGrid
	opp := Opponent(me)

	// 遍历棋盘格，只设置 Plane 1 (me), Plane 2 (opp) 和内部的 Plane 3 (Blocked)
	for i := 0; i < BoardN; i++ {
		g := boardIndexToGrid[i]
		if g < 0 || g >= planeSize {
			continue
		}
		switch b.Cells[i] {
		case me:
			spatial[planeSize+g] = 1.0 // Plane 1
		case opp:
			spatial[2*planeSize+g] = 1.0 // Plane 2
		case Blocked:
			spatial[3*planeSize+g] = 1.0 // Plane 3
		}
	}

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
	type encodedData struct {
		spatial []float32
		global  []float32
	}
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
	if err != nil || len(res) == 0 { return 0, err }
	return res[0], nil
}
