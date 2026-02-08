// cmd/selfplay/main.go
// 自博弈数据生成：输出二进制样本，供 Python 侧直接读取训练
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"hexxagon_go/internal/game"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type rawSample struct {
	state  []float32
	policy []float32
	side   game.CellState
}
type finishedSample struct {
	state  []float32
	policy []float32
	value  int8
}

// chunkWriter 把样本写成分片：X.bin (float32)、P.bin (float32)、Z.bin (int8)，并写 meta.json 记录计数
type chunkWriter struct {
	outDir    string
	chunkSize int

	idx         int
	count       int
	currentBase string
	fx          *os.File
	fp          *os.File
	fz          *os.File
}

func newChunkWriter(outDir string, chunkSize int) *chunkWriter {
	return &chunkWriter{outDir: outDir, chunkSize: chunkSize}
}

func (w *chunkWriter) rotate() error {
	if w.fx != nil {
		_ = w.fx.Close()
		_ = w.fp.Close()
		_ = w.fz.Close()
		_ = w.writeMeta()
	}
	w.idx++
	w.count = 0
	w.currentBase = fmt.Sprintf("chunk_%05d", w.idx)
	xPath := filepath.Join(w.outDir, w.currentBase+"_X.bin")
	pPath := filepath.Join(w.outDir, w.currentBase+"_P.bin")
	zPath := filepath.Join(w.outDir, w.currentBase+"_Z.bin")

	var err error
	w.fx, err = os.Create(xPath)
	if err != nil {
		return err
	}
	w.fp, err = os.Create(pPath)
	if err != nil {
		return err
	}
	w.fz, err = os.Create(zPath)
	if err != nil {
		return err
	}
	return nil
}

func (w *chunkWriter) writeMeta() error {
	meta := map[string]any{
		"samples": w.count,
	}
	b, _ := json.MarshalIndent(meta, "", "  ")
	metaPath := filepath.Join(w.outDir, w.currentBase+"_meta.json")
	return os.WriteFile(metaPath, b, 0644)
}

func (w *chunkWriter) writeSample(s finishedSample) error {
	if w.fx == nil || w.count >= w.chunkSize {
		if err := w.rotate(); err != nil {
			return err
		}
	}
	if err := binary.Write(w.fx, binary.LittleEndian, s.state); err != nil {
		return err
	}
	if err := binary.Write(w.fp, binary.LittleEndian, s.policy); err != nil {
		return err
	}
	if _, err := w.fz.Write([]byte{byte(s.value)}); err != nil {
		return err
	}
	w.count++
	return nil
}

func (w *chunkWriter) close() {
	if w.fx != nil {
		_ = w.fx.Close()
	}
	if w.fp != nil {
		_ = w.fp.Close()
	}
	if w.fz != nil {
		_ = w.fz.Close()
	}
	if w.count > 0 {
		_ = w.writeMeta()
	}
}

func (w *chunkWriter) run(ch <-chan []finishedSample, done chan<- struct{}) {
	defer close(done)
	for batch := range ch {
		for _, s := range batch {
			if err := w.writeSample(s); err != nil {
				log.Printf("[writer] write sample failed: %v", err)
				return
			}
		}
	}
	w.close()
}

// ------------------------------------

func main() {
	numGames := flag.Int("n", 2000, "要生成的对局数")
	sims := flag.Int("sims", 800, "每步 MCTS 模拟次数")
	workers := flag.Int("workers", 0, "并发局数（默认=CPU/2，至少1）")
	outDir := flag.String("out", "selfplay_out", "输出目录")
	chunkSize := flag.Int("chunk", 5000, "每个分片的样本数")
	seed := flag.Int64("seed", time.Now().UnixNano(), "随机种子")
	flag.Parse()

	if *workers <= 0 {
		*workers = runtime.NumCPU() / 2
		if *workers < 1 {
			*workers = 1
		}
	}
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		log.Fatalf("mkdir %s: %v", *outDir, err)
	}

	// 初始化坐标/编码表
	_ = game.AllCoords(4)
	rand.Seed(*seed)

	log.Printf("selfplay: games=%d sims=%d workers=%d out=%s chunk=%d", *numGames, *sims, *workers, *outDir, *chunkSize)

	jobs := make(chan int, *workers*2)
	samplesCh := make(chan []finishedSample, *workers)

	writerDone := make(chan struct{})
	go newChunkWriter(*outDir, *chunkSize).run(samplesCh, writerDone)

	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(*seed + int64(wid)))
			for range jobs {
				samps, ok := playOneGame(*sims, r)
				if ok && len(samps) > 0 {
					samplesCh <- samps
				}
			}
		}(i)
	}

	for g := 0; g < *numGames; g++ {
		jobs <- g
	}
	close(jobs)
	wg.Wait()
	close(samplesCh)
	<-writerDone
	log.Println("selfplay done")
}

// playOneGame 打完一局，返回带价值标签的样本
func playOneGame(sims int, r *rand.Rand) ([]finishedSample, bool) {
	const maxMoves, minMoves = 400, 20
	state := game.NewGameState(4)
	player := game.PlayerA

	addRandomOpening(state, 2, r)

	raws := make([]rawSample, 0, 128)

	for move := 0; move < maxMoves; move++ {
		mv, visits, ok := game.FindBestMoveMCTSWithVisits(state.Board, player, sims, 0, true)
		if !ok {
			break
		}

		// 记录样本
		t := game.EncodeBoardTensor(state.Board, player)
		stateCopy := make([]float32, len(t))
		copy(stateCopy, t[:])
		policy := normalizeVisits(visits)

		raws = append(raws, rawSample{
			state:  stateCopy,
			policy: policy,
			side:   player,
		})

		_, _, err := state.MakeMove(mv)
		if err != nil {
			break
		}
		if state.GameOver {
			break
		}
		player = game.Opponent(player)
	}

	if len(raws) < minMoves {
		return nil, false
	}

	winner := winnerValue(state)
	finished := make([]finishedSample, len(raws))
	for i, s := range raws {
		val := int8(0)
		switch winner {
		case game.PlayerA, game.PlayerB:
			if winner == s.side {
				val = 1
			} else {
				val = -1
			}
		default:
			val = 0
		}
		finished[i] = finishedSample{
			state:  s.state,
			policy: s.policy,
			value:  val,
		}
	}
	return finished, true
}

// normalizeVisits 把访问次数归一化为概率；若全 0 则均匀分布
func normalizeVisits(visits []int) []float32 {
	out := make([]float32, len(visits))
	sum := 0
	for _, v := range visits {
		sum += v
	}
	if sum == 0 {
		uni := float32(1.0 / float64(len(visits)))
		for i := range out {
			out[i] = uni
		}
		return out
	}
	inv := 1.0 / float32(sum)
	for i, v := range visits {
		out[i] = float32(v) * inv
	}
	return out
}

// winnerValue：返回 1/-1/0
func winnerValue(st *game.GameState) game.CellState {
	a := st.Board.CountPieces(game.PlayerA)
	b := st.Board.CountPieces(game.PlayerB)
	if a > b {
		return game.PlayerA
	}
	if b > a {
		return game.PlayerB
	}
	return game.Empty
}

// 随机开局：双方各走 n 手
func addRandomOpening(st *game.GameState, n int, r *rand.Rand) {
	for i := 0; i < n; i++ {
		for _, pl := range []game.CellState{game.PlayerA, game.PlayerB} {
			moves := game.GenerateMoves(st.Board, pl)
			if len(moves) == 0 {
				continue
			}
			mv := moves[r.Intn(len(moves))]
			_, _, _ = st.MakeMove(mv)
		}
	}
}
