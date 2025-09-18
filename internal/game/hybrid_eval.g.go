package game

import (
	"math"
	"math/rand"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
)

// 可调参数：全局混合比例（可以做成 flag）
var (
	// NN 输出范围约 [-100,100]（你现在是 v * 100.0）
	// 静态评估的量级更大（棋子差*10 + 结构项），所以默认给 NN 小一点权重
	nnBaseW     = 0.45 // 叶子阶段 NN 基础权重
	staticBaseW = 0.55 // 叶子阶段 静态基础权重

	// 分期：开局更信静态（形状&边缘），残局更信 NN（收官价值）
	phaseOpenThresh    = 0.75 // 空位比例 ≥ 0.75 视为开局/前期
	phaseEndgameThresh = 0.45 // 空位比例 ≤ 0.20 视为残局

	// NN 置信调节：|v| 大（极端局面）时，适当提高 NN 占比
	nnConfBoostThr = 70   // |v|>=70 认为 NN 置信较高
	nnConfBoost    = 0.10 // 额外+10% 给 NN
)

type PhaseSwitch struct {
	UseNNOpening bool
	UseNNMidgame bool
	UseNNEndgame bool
	ROpen        float64 // r ≥ ROpen → 开局
	REnd         float64 // r ≤ REnd  → 残局
}

var phaseSwitch = PhaseSwitch{
	UseNNOpening: true,
	UseNNMidgame: true,
	UseNNEndgame: true,
	ROpen:        0.75,
	REnd:         0.25,
}

var NodesSearched int64

func ResetNodes() { NodesSearched = 0 }
func incNodes()   { atomic.AddInt64(&NodesSearched, 1) }

func SetPhaseSwitch(ps PhaseSwitch) { phaseSwitch = ps }

// 只在一个阶段里用 CNN；其余阶段一律用“你的静态评估”
// 不做混合，便于看清谁强谁弱
func PhaseSelectEval(b *Board, me CellState) int {
	r := emptyRatio(b) // 你已有的统计函数；若没有就复制 earlier 版本
	useNN := false
	switch {
	case r >= phaseSwitch.ROpen:
		useNN = phaseSwitch.UseNNOpening
	case r <= phaseSwitch.REnd:
		useNN = phaseSwitch.UseNNEndgame
	default:
		useNN = phaseSwitch.UseNNMidgame
	}
	if useNN {
		v := EvaluateNN(b, me)
		return v
	}
	return EvaluateStatic(b, me)
}

// 统计空位比例
func emptyRatio(b *Board) float64 {
	total := len(b.AllCoords())
	empties := 0
	for i := 0; i < BoardN; i++ {
		if b.Cells[i] == Empty {
			empties++
		}
	}
	return float64(empties) / float64(total)
}

// HybridEval: 叶子用它；根排序也可以用它（再叠轻启发）
func HybridEval(b *Board, me CellState) int {
	// 1) 先拿两路分
	staticVal := EvaluateStatic(b, me) // 你已有的静态评估
	nnVal := 0
	nnOk := true
	{
		// 你的 EvaluateNN 返回 int（-100~100），失败时目前 return 0
		// 建议你把 EvaluateNN 改成失败回退 evaluateStatic，
		// 如果暂时不改，这里也能兜一下
		nnVal = EvaluateNN(b, me)
		// 这里简单判断“是否初始化成功”的信号不太好拿，就容错当作 nnOk=true
		// 如果想更严谨，可以让 EvaluateNN 返回 (int,bool)
	}

	// 2) 动态权重：按棋局阶段微调
	r := emptyRatio(b)
	nnW, stW := nnBaseW, staticBaseW
	if r >= phaseOpenThresh {
		// 开局更靠静态（形状/边缘占优）
		nnW *= 0.75
		stW = 1.0 - nnW
	} else if r <= phaseEndgameThresh {
		// 残局更靠 NN（收官+价值）
		nnW *= 1.35
		if nnW > 0.85 {
			nnW = 0.85
		}
		stW = 1.0 - nnW
	}
	// NN 置信增强：|v| 大就再给点权重
	if nnOk && int(math.Abs(float64(nnVal))) >= nnConfBoostThr {
		nnW = math.Min(0.95, nnW+nnConfBoost)
		stW = 1.0 - nnW
	}

	// 3) 小启发（很轻）：感染预估、机动性差，避免喧宾夺主
	// 注意 scale：静态分量级很大，不要让轻启发影响过猛
	infLight := 0
	{
		// 如果上一手存在，可以用上一手落点周围形势；否则就不加
		// 你也可以用“双方 mobility 差 * 0.5”之类的极轻项
		// 这里先留 0，根排序再加。
		_ = infLight
	}

	// 4) 线性混合
	// nnVal 量级 ~100；staticVal 往往 |几百~几千|
	// 直接线性混合可行，因为 stW 通常更大
	mix := int(nnW*float64(nnVal) + stW*float64(staticVal))
	return mix // + infLight（如果需要）
}

func FindBestMoveAtDepthHybrid(b *Board, player CellState, depth int64, allowJump bool) (Move, bool) {

	// 统计 TT（可选）
	//ttProbeCount = 0
	//ttHitCount = 0

	// 0) 快速挖胜/保胜（仅克隆→避免被反超的跳）
	if mv, ok := findImmediateWinOnly(b, player); ok {
		return mv, true
	}

	// 1) 生成根走法
	moves := GenerateMoves(b, player)
	if len(moves) == 0 {
		return Move{}, false
	}

	// 2) 根层一次性计算空位比例 r
	total := len(b.AllCoords())
	empties := 0
	for i := 0; i < BoardN; i++ {
		if b.Cells[i] == Empty {
			empties++
		}
	}
	r := float64(empties) / float64(total)

	// 3) 开局极早期：只保留“外圈克隆”
	const earlyCloneThresh = 0.84
	if r >= earlyCloneThresh {
		edgeClones := make([]Move, 0, len(moves))
		for _, m := range moves {
			if !m.IsClone() {
				continue
			}
			if idx, ok := IndexOf[m.To]; ok && isOuterI[idx] {
				edgeClones = append(edgeClones, m)
			}
		}
		if len(edgeClones) > 0 {
			moves = edgeClones
		}
	}

	// 4) UI 门控禁跳
	moves = filterJumpsByFlag(b, player, moves, allowJump)

	// 5) 根层启发式过滤：剔除0感染跳 & 危险跳跃 & 危险克隆
	moves = filterLowInfectJumpsOrFallback(b, player, moves, 1)
	moves = filterDangerousRecaptureJumps(b, player, moves)
	moves = filterDangerousIsolatedClones(b, player, moves)
	if len(moves) == 0 {
		return Move{}, false
	}

	// 6) policy 先验修剪（可选）
	if pruned := policyPruneRoot(b, player, moves); len(pruned) > 0 {
		moves = pruned
	}

	// 7) 根层粗评分排序（零分配 make/unmake）
	type scored struct {
		mv    Move
		score int
	}
	order := make([]scored, len(moves))
	for i, m := range moves {
		undo := mMakeMoveWithUndo(b, m, player)
		//s := PhaseSelectEval(b, player)
		s := func() int {
			if useLearned2 {
				return EvaluateNN(b, player)
			}
			return EvaluateStatic(b, player)
		}()
		// 轻量启发：感染数加权，能明显稳定排序（尤其早中期）
		inf := previewInfectedCount(b, m, player)
		s += 2 * inf

		b.UnmakeMove(undo)

		// 只在根层，对我方跳跃降权（递归里保持中立）
		if !useLearned2 && m.IsJump() {
			s -= jumpMovePenalty
		}

		order[i] = scored{mv: m, score: s}
	}

	sort.Slice(order, func(i, j int) bool {
		if order[i].score != order[j].score {
			return order[i].score > order[j].score
		}
		// 同分优先克隆更稳（建议保留）
		if order[i].mv.IsClone() != order[j].mv.IsClone() {
			return order[i].mv.IsClone()
		}
		return false
	})

	// 8) 根并行：固定 worker pool，每 worker 仅克隆一次并复用棋盘
	const inf = 1 << 30
	type result struct {
		mv    Move
		score int
	}

	jobs := make(chan Move, len(order))
	results := make(chan result, len(order))

	workers := runtime.GOMAXPROCS(0)
	if workers > len(order) {
		workers = len(order)
	}
	if workers < 1 {
		workers = 1
	}

	var wg sync.WaitGroup
	wg.Add(workers)

	alphaRoot, betaRoot := -inf, inf

	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			// 只做一次 O(N) 克隆，其余走法复用 + 回溯
			nb := cloneBoard(b) // 如使用对象池，也可改为 cloneBoardPool(b)/releaseBoard(nb)
			defer func() {
				// 如果是 cloneBoardPool(b)，这里改为 releaseBoard(nb)
				_ = nb
			}()

			for mv := range jobs {
				undo := mMakeMoveWithUndo(nb, mv, player)
				score := alphaBeta(nb, 0, Opponent(player), player, depth-1, alphaRoot, betaRoot, true)
				nb.UnmakeMove(undo)
				results <- result{mv: mv, score: score}
			}
		}()
	}

	for _, it := range order {
		jobs <- it.mv
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	// 9) 汇总最优解（同分优先克隆；差距小做轻随机）
	bestScore, secondScore := -inf, -inf
	bestMoves := make([]Move, 0, 4)

	for r := range results {
		s := r.score
		if s > bestScore {
			secondScore = bestScore
			bestScore = s
			bestMoves = bestMoves[:0]
			bestMoves = append(bestMoves, r.mv)
		} else if s == bestScore {
			bestMoves = append(bestMoves, r.mv)
		} else if s > secondScore {
			secondScore = s
		}
	}

	if len(bestMoves) == 0 {
		return Move{}, false
	}

	// 同分优先克隆
	//if len(bestMoves) > 1 {
	//	clones := bestMoves[:0]
	//	for _, m := range bestMoves {
	//		if m.IsClone() {
	//			clones = append(clones, m)
	//		}
	//	}
	//	if len(clones) > 0 {
	//		bestMoves = clones
	//	}
	//}

	choice := bestMoves[0]
	if len(bestMoves) > 1 && bestScore-secondScore < 3 {
		choice = bestMoves[rand.Intn(len(bestMoves))]
	}
	return choice, true
}
