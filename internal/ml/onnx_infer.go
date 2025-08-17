package ml

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"hexxagon_go/internal/game"

	ort "github.com/yalue/onnxruntime_go"
)

// ───────────────── embed 模型 ─────────────────
// 把 onnx 文件放到 internal/ml/assets/hex_cnn.onnx
//
//go:embed assets/hex_cnn.onnx
var embeddedONNX []byte

// 可选：环境变量覆盖
// HEX_ONNX_PATH: 指定外部模型路径（优先）
// HEX_ONNX_CUDA: "1" 则启用 CUDA EP（需 onnxruntime-gpu）
const (
	envModelPath = "HEX_ONNX_PATH"
	envUseCUDA   = "HEX_ONNX_CUDA"
)

// onnxruntime_go 使用全局环境，无需保存 *Environment；
// 我们保留会话及输出形状，便于每次推理创建输出张量。
type onnxState struct {
	session     *ort.DynamicAdvancedSession
	tmpPath     string // 如果用 embedded，需要落地到临时文件（当选择走文件路径时）
	useCUDA     bool
	logitsShape ort.Shape // 模型输出"logits"的形状
	valueShape  ort.Shape // 模型输出"value"的形状
	gotShapes   bool
}

var (
	state    onnxState
	onceInit sync.Once
	initErr  error
)

// 懒加载初始化：首次推理时触发
func ensureInit() error {
	onceInit.Do(func() {
		// 1) 选择模型来源（路径或内嵌）
		var (
			useCUDA   = os.Getenv(envUseCUDA) == "1"
			modelPath string
			useBytes  bool
			modelData []byte
		)

		if p := os.Getenv(envModelPath); p != "" {
			// 优先：外部路径
			modelPath = p
		} else if len(embeddedONNX) > 0 {
			// 直接使用内嵌字节（优先走 bytes 接口）
			useBytes = true
			modelData = embeddedONNX
		} else {
			// 兜底：当前目录/默认文件名
			modelPath = "hex_cnn.onnx"
			if _, err := os.Stat(modelPath); err != nil {
				initErr = fmt.Errorf("no model found: set %s or place hex_cnn.onnx", envModelPath)
				return
			}
		}

		// 2) 初始化 ORT 环境（全局一次）
		if !ort.IsInitialized() {
			if err := ort.InitializeEnvironment(); err != nil {
				initErr = fmt.Errorf("ort.InitializeEnvironment: %w", err)
				return
			}
		}

		// 3) 会话选项（可选 CUDA）
		sessOpts, err := ort.NewSessionOptions()
		if err != nil {
			initErr = fmt.Errorf("ort.NewSessionOptions: %w", err)
			return
		}
		defer sessOpts.Destroy()

		if useCUDA {
			cuOpts, err := ort.NewCUDAProviderOptions()
			if err == nil {
				// 不设置额外选项，按默认 deviceID=0 等
				if err = sessOpts.AppendExecutionProviderCUDA(cuOpts); err != nil {
					// 失败退回 CPU，不报错
					useCUDA = false
				}
				_ = cuOpts.Destroy()
			} else {
				useCUDA = false
			}
		}

		// 4) 创建 DynamicAdvancedSession（推理时再绑定输入/输出张量）
		const (
			inputName  = "x"
			logitsName = "logits"
			valueName  = "value"
		)

		var sess *ort.DynamicAdvancedSession
		if useBytes {
			sess, err = ort.NewDynamicAdvancedSessionWithONNXData(modelData,
				[]string{inputName}, []string{logitsName, valueName}, sessOpts)
		} else {
			sess, err = ort.NewDynamicAdvancedSession(modelPath,
				[]string{inputName}, []string{logitsName, valueName}, sessOpts)
		}
		if err != nil {
			initErr = fmt.Errorf("ort.NewDynamicAdvancedSession: %w", err)
			return
		}

		// 5) 读取模型 I/O 信息，获取输出张量形状（更稳）
		var outsInfo []ort.InputOutputInfo
		if useBytes {
			if oi, _, err := getIOInfoWithBytes(modelData); err == nil {
				outsInfo = oi
			}
		} else {
			if oi, _, err := getIOInfoWithPath(modelPath, sessOpts); err == nil {
				outsInfo = oi
			}
		}
		// 默认形状（若读取失败也可工作）：logits -> (1, 81), value -> (1)
		logitsShape := ort.NewShape(1, 81)
		valueShape := ort.NewShape(1)

		for _, o := range outsInfo {
			switch o.Name {
			case logitsName:
				logitsShape = o.Dimensions
			case valueName:
				valueShape = o.Dimensions
			}
		}

		state.session = sess
		state.useCUDA = useCUDA
		state.logitsShape = logitsShape
		state.valueShape = valueShape
		state.gotShapes = true
	})
	return initErr
}

// 读取 I/O 信息（bytes 版本）
func getIOInfoWithBytes(data []byte) (outs []ort.InputOutputInfo, ins []ort.InputOutputInfo, err error) {
	ins, outs, err = ort.GetInputOutputInfoWithONNXData(data)
	return
}

// 读取 I/O 信息（路径版本，可传 options 以适配需要特定选项的模型）
func getIOInfoWithPath(path string, opts *ort.SessionOptions) (outs []ort.InputOutputInfo, ins []ort.InputOutputInfo, err error) {
	ins, outs, err = ort.GetInputOutputInfoWithOptions(path, opts)
	return
}

// Close 进程退出前可选调用
func Close() {
	if state.session != nil {
		_ = state.session.Destroy()
	}
	// 全局环境与共享库由包级管理；这里一并释放
	if ort.IsInitialized() {
		_ = ort.DestroyEnvironment()
	}
	if state.tmpPath != "" {
		_ = os.Remove(state.tmpPath)
	}
	state = onnxState{}
}

// CNNPredict 返回 (policy[81], value[-1..1])
// 注意：输出名需与导出 ONNX 的名字一致：logits / value
func PredictRaw(feat []float32) (policy [81]float32, value float32, err error) {
	if err := ensureInit(); err != nil {
		return policy, 0, err
	}
	if len(feat) != 243 {
		return policy, 0, fmt.Errorf("got %d, want 243", len(feat))
	}

	dims := []int64{1, 3, 9, 9}
	input, err := onnx.NewTensor(onnx.TENSOR_FLOAT, dims, feat)
	if err != nil {
		return policy, 0, fmt.Errorf("NewTensor: %w", err)
	}
	defer input.Release()

	outs, err := state.session.Run(map[string]*onnx.Value{"x": input})
	if err != nil {
		return policy, 0, fmt.Errorf("Run: %w", err)
	}
	defer func() {
		for _, v := range outs {
			v.Release()
		}
	}()

	logits := mustFloat32s(outs["logits"])
	val := mustFloat32s(outs["value"])
	copy(policy[:], logits[:min(81, len(logits))])
	return policy, val[0], nil
}

// 便捷：从内存字节设置模型（比如你想在外面下载不同版本）
// 这会覆盖已存在的 session
func SetModelFromBytes(onxxBytes []byte, useCUDA bool) error {
	// 关闭旧的
	Close()
	// 写入临时文件再初始化（保持与原逻辑一致：通过路径走）
	if len(onxxBytes) == 0 {
		return fmt.Errorf("empty model bytes")
	}
	dir := filepath.Join(os.TempDir(), "hex-onnx")
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.CreateTemp(dir, "hex_cnn_*.onnx")
	if err != nil {
		return err
	}
	if _, err := bytes.NewReader(onxxBytes).WriteTo(f); err != nil {
		_ = f.Close()
		return err
	}
	_ = f.Close()

	// 用环境变量告诉 ensureInit 走这个路径
	_ = os.Setenv(envModelPath, f.Name())
	if useCUDA {
		_ = os.Setenv(envUseCUDA, "1")
	} else {
		_ = os.Unsetenv(envUseCUDA)
	}
	// 重置 once，让下次 ensureInit 生效
	onceInit = sync.Once{}
	return nil
}
