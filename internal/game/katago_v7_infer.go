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
	katagoInSpatial   *ort.Tensor[float32]
	katagoInGlobal    *ort.Tensor[float32]
	katagoOutPolicy   *ort.Tensor[float32]
	katagoOutValue    *ort.Tensor[float32]

	// 批量推理张量
	katagoInSpatialB  *ort.Tensor[float32]
	katagoInGlobalB   *ort.Tensor[float32]
	katagoOutPolicyB  *ort.Tensor[float32]
	katagoOutValueB   *ort.Tensor[float32]

	katagoModelBytes  []byte
	katagoPolicyHeads = 4 
)

func ensureKataONNX() error {
	katagoOnce.Do(func() {
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
					if err != nil { continue }

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
	if !encodeTablesInit { initEncodeTables() }
	for i := range spatial { spatial[i] = 0 }
	for i := range global { global[i] = 0 }
	planeSize := katagoGrid * katagoGrid
	ch := func(c, idx int) int { return c*planeSize + idx }
	for idx := 0; idx < planeSize; idx++ { spatial[ch(0, idx)] = 1.0 }
	opp := Opponent(me)
	for i := 0; i < BoardN; i++ {
		g := boardIndexToGrid[i]
		if g < 0 || g >= planeSize { continue }
		switch b.Cells[i] {
		case me: spatial[ch(1, g)] = 1.0
		case opp: spatial[ch(2, g)] = 1.0
		case Blocked: spatial[ch(3, g)] = 1.0
		}
	}
	stageOne := selectedIdx >= 0
	if stageOne && selectedIdx < planeSize { spatial[ch(4, selectedIdx)] = 1.0 }
	if stageOne { global[0] = 1.0 }
	global[9] = 1.0
}

func KataBatchValueScore(boards []*Board, me CellState) ([]int, error) {
	if err := ensureKataONNX(); err != nil { return nil, err }
	n := len(boards)
	if n == 0 { return nil, nil }
	
	katagoMu.Lock()
	defer katagoMu.Unlock()

	sData := katagoInSpatialB.GetData()
	gData := katagoInGlobalB.GetData()
	for i := 0; i < maxBatchSize; i++ {
		startS, startG := i*katagoPlanes*katagoGrid*katagoGrid, i*katagoGlobals
		if i < n {
			encodeKataInputs(boards[i], me, sData[startS:startS+katagoPlanes*katagoGrid*katagoGrid], gData[startG:startG+katagoGlobals], -1)
		} else {
			// 填充零数据
			for j := startS; j < startS+katagoPlanes*katagoGrid*katagoGrid; j++ { sData[j] = 0 }
			for j := startG; j < startG+katagoGlobals; j++ { gData[j] = 0 }
		}
	}

	if err := katagoSessBatch.Run(); err != nil { return nil, err }

	res := make([]int, n)
	vals := katagoOutValueB.GetData()
	for i := 0; i < n; i++ {
		v := vals[i*3 : (i+1)*3]
		maxVal := v[0]
		if v[1] > maxVal { maxVal = v[1] }
		if v[2] > maxVal { maxVal = v[2] }
		e0 := math.Exp(float64(v[0] - maxVal))
		e1 := math.Exp(float64(v[1] - maxVal))
		e2 := math.Exp(float64(v[2] - maxVal))
		score := float32((e0 - e1) / (e0 + e1 + e2))
		res[i] = int(score * 1000)
	}
	return res, nil
}

func KataBatchValueScoreWithSelection(boards []*Board, me CellState, selectedIndices []int) ([]int, error) {
	if err := ensureKataONNX(); err != nil { return nil, err }
	n := len(boards)
	if n == 0 { return nil, nil }

	katagoMu.Lock()
	defer katagoMu.Unlock()

	sData := katagoInSpatialB.GetData()
	gData := katagoInGlobalB.GetData()
	for i := 0; i < maxBatchSize; i++ {
		startS, startG := i*katagoPlanes*katagoGrid*katagoGrid, i*katagoGlobals
		if i < n {
			encodeKataInputs(boards[i], me, sData[startS:startS+katagoPlanes*katagoGrid*katagoGrid], gData[startG:startG+katagoGlobals], selectedIndices[i])
		} else {
			for j := startS; j < startS+katagoPlanes*katagoGrid*katagoGrid; j++ { sData[j] = 0 }
			for j := startG; j < startG+katagoGlobals; j++ { gData[j] = 0 }
		}
	}

	if err := katagoSessBatch.Run(); err != nil { return nil, err }

	res := make([]int, n)
	vals := katagoOutValueB.GetData()
	for i := 0; i < n; i++ {
		v := vals[i*3 : (i+1)*3]
		maxVal := v[0]
		if v[1] > maxVal { maxVal = v[1] }
		if v[2] > maxVal { maxVal = v[2] }
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
	if err := ensureKataONNX(); err != nil { return nil, 0, err }
	
	katagoMu.Lock()
	defer katagoMu.Unlock()

	encodeKataInputs(b, me, katagoInSpatial.GetData(), katagoInGlobal.GetData(), selectedIdx)
	if err := katagoSess.Run(); err != nil { return nil, 0, err }

	logits := make([]float32, katagoGrid*katagoGrid+1)
	copy(logits, katagoOutPolicy.GetData()[:len(logits)])

	// 强制 Softmax 得到概率 [0, 1]
	maxLogit := float32(-1e30)
	for _, v := range logits {
		if v > maxLogit { maxLogit = v }
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

	vals := katagoOutValue.GetData()
	maxVal := vals[0]
	if vals[1] > maxVal { maxVal = vals[1] }
	if vals[2] > maxVal { maxVal = vals[2] }
	e0 := math.Exp(float64(vals[0] - maxVal))
	e1 := math.Exp(float64(vals[1] - maxVal))
	e2 := math.Exp(float64(vals[2] - maxVal))
	score := float32((e0 - e1) / (e0 + e1 + e2))

	return logits, score, nil
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
