// game/ai.go
package game

import (
	//"fmt"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"sync"
	"time"
)

func init() {
	rand.Seed(time.Now().UnixNano())
	runtime.GOMAXPROCS(runtime.NumCPU() - 2) // 吃满物理/逻辑核心

}

// const useLearned = true
const useLearned = false
const useLearned2 = false
const jumpMovePenalty = 25

// ------------------------------------------------------------
// 公共入口
// ------------------------------------------------------------
// 用对象池拿一块 Board，然后把当前盘面“整块拷贝”过去。
// 注意：array 赋值是深拷贝，O(37)；比逐个 map 复制快多了。
func cloneBoardPool(b *Board) *Board {
	nb := acquireBoard(b.radius) // 已清空并重置 hash/标记
	// 直接结构字段拷贝（array 是值拷贝）
	nb.Cells = b.Cells
	nb.hash = b.hash
	nb.bitA = b.bitA
	nb.bitB = b.bitB

	nb.LastMove = b.LastMove
	nb.LastMover = b.LastMover
	nb.LastInfect = b.LastInfect
	return nb
}

// 分配一块新的 Board，做一次性拷贝。
// 若你在根并行的 worker 内部“只克隆一次后复用”，也可以用这个。
func cloneBoard(b *Board) *Board {
	nb := &Board{
		radius:     b.radius,
		Cells:      b.Cells, // 数组值拷贝
		hash:       b.hash,
		bitA:       b.bitA,
		bitB:       b.bitB,
		LastMove:   b.LastMove,
		LastMover:  b.LastMover,
		LastInfect: b.LastInfect,
	}
	return nb
}

func FindBestMoveAtDepth(b *Board, player CellState, depth int64, allowJump bool) (Move, bool) {
	moves := GenerateMoves(b, player)
	moves = applyMoveFilters(b, player, moves, allowJump)
	if len(moves) == 0 {
		return Move{}, false
	}

	useNN := (player == PlayerA && UseONNXForPlayerA) || (player == PlayerB && UseONNXForPlayerB)

	// 计算并行度：核心数/8，向上取偶数，范围 [2, 8]
	numWorkers := (runtime.NumCPU() + 7) / 8
	if numWorkers%2 != 0 {
		numWorkers++
	}
	if numWorkers < 2 {
		numWorkers = 2
	}
	if numWorkers > 8 {
		numWorkers = 8
	}

	type scored struct {
		mv    Move
		score int
	}
	results := make([]scored, len(moves))

	// 特殊优化：如果深度为 1 且启用 NN，直接使用批量推理
	if depth == 1 && useNN {
		// 使用池化棋盘以减少内存分配
		batchBoards := make([]*Board, len(moves))
		opp := Opponent(player)
		
		for i, mv := range moves {
			// 从池中获取或临时克隆一个，但尽量复用
			nb := acquireBoard(b.radius)
			nb.Cells = b.Cells
			nb.bitA = b.bitA
			nb.bitB = b.bitB
			nb.ApplyMove(mv, player)
			batchBoards[i] = nb
		}
		
		scores, err := KataBatchValueScore(batchBoards, opp)
		
		// 释放棋盘回池
		for _, nb := range batchBoards {
			releaseBoard(nb)
		}
		
		if err == nil {
			for i, s := range scores {
				results[i] = scored{mv: moves[i], score: -s}
			}
			sort.Slice(results, func(i, j int) bool { return results[i].score > results[j].score })
			return results[0].mv, true
		}
	}

	// 任务分发管道
	type task struct {
		idx int
		mv  Move
	}
	taskChan := make(chan task, len(moves))

	// 启发式排序：提升剪枝效率
	if useNN {
		batchBoards := make([]*Board, len(moves))
		selectedIndices := make([]int, len(moves))
		for i, mv := range moves {
			batchBoards[i] = b
			selectedIndices[i] = boardIndexToGrid[IndexOf[mv.To]]
		}
		scores, err := KataBatchValueScoreWithSelection(batchBoards, player, selectedIndices)
		if err == nil {
			type moveWithScore struct {
				mv    Move
				score int
			}
			mvs := make([]moveWithScore, len(moves))
			for i := range moves {
				mvs[i] = moveWithScore{moves[i], scores[i]}
			}
			sort.Slice(mvs, func(i, j int) bool { return mvs[i].score > mvs[j].score })
			for i, it := range mvs {
				taskChan <- task{i, it.mv}
			}
		} else {
			for i, mv := range moves {
				taskChan <- task{i, mv}
			}
		}
	} else {
		sort.Slice(moves, func(i, j int) bool {
			return previewInfectedCount(b, moves[i], player) > previewInfectedCount(b, moves[j], player)
		})
		for i, mv := range moves {
			taskChan <- task{i, mv}
		}
	}
	close(taskChan)

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localBoard := b.Clone() // 每个线程私有 Board
			var localNodes int64
			for t := range taskChan {
				undo := mMakeMoveWithUndo(localBoard, t.mv, player)
				// 初始 alpha/beta 窗口
				score := hybridAlphaBeta(localBoard, 0, Opponent(player), player, depth-1, -1000000, 1000000, allowJump, &localNodes)
				localBoard.UnmakeMove(undo)
				results[t.idx] = scored{mv: t.mv, score: score}
			}
			// 同步剩余节点
			if localNodes > 0 {
				AddNodes(localNodes)
			}
		}()
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool { return results[i].score > results[j].score })

	if useNN {
		return results[0].mv, true
	}

	if len(results) >= 2 && results[0].score > results[1].score+200 {
		return results[0].mv, true
	}
	topK := 2
	if len(results) < topK {
		topK = len(results)
	}
	pick := rand.Intn(topK)
	return results[pick].mv, true
}


func hybridAlphaBeta(
	b *Board,
	_ uint64,
	current, original CellState,
	depth int64,
	alpha, beta int,
	allowJump bool,
	localNodes *int64, // 新增：局部计数器
) int {
	useNN := (original == PlayerA && UseONNXForPlayerA) || (original == PlayerB && UseONNXForPlayerB)

	if depth <= 0 {
		if useNN {
			// 始终以“轮到谁走”的视角评估，然后根据是否是 original 决定正负
			v := EvaluateNN(b, current)
			if current != original {
				return -v
			}
			return v
		}
		return EvaluateBitBoard(b, original)
	}

	ttKey := ttKeyFor(b, current)
	if hit, valCur, flag := probeTT(ttKey, int(depth)); hit {
		val := valCur
		if current != original {
			val = -valCur
		}
		switch flag {
		case ttExact:
			return val
		case ttLower:
			if val > alpha {
				alpha = val
			}
		case ttUpper:
			if val < beta {
				beta = val
			}
		}
		if alpha >= beta {
			return val
		}
	}

	if localNodes != nil {
		*localNodes++
		if *localNodes >= 1024 {
			AddNodes(*localNodes)
			*localNodes = 0
		}
	} else {
		incNodes()
	}

	moves := GenerateMoves(b, current)
	moves = applyMoveFilters(b, current, moves, allowJump)

	if len(moves) == 0 {
		if useNN {
			v := EvaluateNN(b, current)
			if current != original {
				return -v
			}
			return v
		}
		return EvaluateBitBoard(b, original)
	}

	// 深度 2 优化：在叶子节点上一层进行批量评估
	if depth == 1 && useNN {
		batchBoards := make([]*Board, len(moves))
		for i, mv := range moves {
			nb := acquireBoard(b.radius)
			nb.Cells = b.Cells
			nb.bitA = b.bitA
			nb.bitB = b.bitB
			nb.ApplyMove(mv, current)
			batchBoards[i] = nb
		}
		// 关键修复：落子后轮到 Opponent(current) 走，以此视角评估
		nextP := Opponent(current)
		scores, err := KataBatchValueScore(batchBoards, nextP)
		
		// 释放棋盘
		for _, nb := range batchBoards {
			releaseBoard(nb)
		}
		
		if err == nil {
			best := 0
			if current == original { // MAX 节点
				best = -1000000
				for _, s := range scores {
					// 这里的 s 是 nextP 的分，我们要 original 的分
					// 因为 nextP != original (因为 current == original)，所以 sOrig = -s
					sOrig := -s
					if sOrig > best {
						best = sOrig
					}
				}
			} else { // MIN 节点
				best = 1000000
				for _, s := range scores {
					// 这里 nextP == original (因为 current != original)，所以 sOrig = s
					sOrig := s
					if sOrig < best {
						best = sOrig
					}
				}
			}
			return best
		}
	}

	alphaOrig, betaOrig := alpha, beta

	if ok, idx := probeBestIdx(ttKey); ok {
		i := int(idx)
		if i >= 0 && i < len(moves) {
			moves[0], moves[i] = moves[i], moves[0]
		}
	}

	var bestScore int
	var bestIdx uint8

	if current == original {
		bestScore = math.MinInt32
		for i, mv := range moves {
			undo := mMakeMoveWithUndo(b, mv, current)
			score := hybridAlphaBeta(b, 0, Opponent(current), original, depth-1, alpha, beta, allowJump, localNodes)
			b.UnmakeMove(undo)
			if score > bestScore {
				bestScore = score
				bestIdx = uint8(i)
			}
			if score > alpha {
				alpha = score
				if alpha >= beta {
					break
				}
			}
		}
	} else {
		bestScore = math.MaxInt32
		for i, mv := range moves {
			undo := mMakeMoveWithUndo(b, mv, current)
			score := hybridAlphaBeta(b, 0, Opponent(current), original, depth-1, alpha, beta, allowJump, localNodes)
			b.UnmakeMove(undo)
			if score < bestScore {
				bestScore = score
				bestIdx = uint8(i)
			}
			if score < beta {
				beta = score
				if beta <= alpha {
					break
				}
			}
		}
	}

	var flag ttFlag
	switch {
	case bestScore <= alphaOrig:
		flag = ttUpper
	case bestScore >= betaOrig:
		flag = ttLower
	default:
		flag = ttExact
	}
	valTT := bestScore
	if current != original {
		valTT = -bestScore
	}
	storeTT(ttKey, int(depth), valTT, flag)
	storeBestIdx(ttKey, bestIdx)
	return bestScore
}


// ------------------------------------------------------------
// α-β + 置换表
// ------------------------------------------------------------
func mMakeMoveWithUndo(b *Board, mv Move, player CellState) undoInfo {
	u := undoInfo{
		prevLastMove:   b.LastMove,
		prevLastMover:  b.LastMover,
		prevLastInfect: b.LastInfect,
	}
	b.LastMove = mv
	infected, inner := mv.MakeMove(b, player) // 这里会改 cells/hash
	b.LastMover = player
	b.LastInfect = len(infected)
	u.changed = inner.changed
	return u
}

// alphaBeta —— 统一使用 Make/Unmake 维护 b.hash；TT 键 = b.hash ^ sideKey(current)
// 说明：第二个参数 hash 已弃用，这里命名为 "_" 以避免未使用报错。
func alphaBeta(
	b *Board,
	_ uint64, // 已弃用：不再手搓 childHash；保留签名以减少你其它调用处的改动
	current, original CellState,
	depth int64,
	alpha, beta int,
	allowJump bool,
	localNodes *int64, // 新增：局部计数器
) int {
	if depth <= 0 {
		return Evaluate(b, original)
	}

	ttKey := ttKeyFor(b, current)
	if hit, valCur, flag := probeTT(ttKey, int(depth)); hit {
		// valCur 是 current 视角；转回 original
		val := valCur
		if current != original {
			val = -valCur
		}
		switch flag {
		case ttExact:
			return val
		case ttLower:
			if val > alpha {
				alpha = val
			}
		case ttUpper:
			if val < beta {
				beta = val
			}
		}
		if alpha >= beta {
			return val
		}
	}

	if localNodes != nil {
		*localNodes++
		if *localNodes >= 1024 {
			AddNodes(*localNodes)
			*localNodes = 0
		}
	} else {
		incNodes()
	}

	// 1) 走法生成（含 UI 禁跳）
	moves := GenerateMoves(b, current)
	moves = applyMoveFilters(b, current, moves, allowJump)

	if len(moves) == 0 {
		return Evaluate(b, original)
	}

	alphaOrig, betaOrig := alpha, beta

	// 4) 如果 TT 里存了该节点的最佳索引，交换到首位以提升剪枝效率
	if ok, idx := probeBestIdx(ttKey); ok {
		i := int(idx)
		if i >= 0 && i < len(moves) {
			moves[0], moves[i] = moves[i], moves[0]
		}
	}

	// 5) 极大/极小节点搜索
	var bestScore int
	var bestIdx uint8

	if current == original {
		// === MAX 节点 ===
		bestScore = math.MinInt32

		for i, mv := range moves {
			undo := mMakeMoveWithUndo(b, mv, current)

			score := alphaBeta(b, 0, Opponent(current), original, depth-1, alpha, beta, allowJump, localNodes)

			b.UnmakeMove(undo)

			if score > bestScore {
				bestScore = score
				bestIdx = uint8(i)
			}
			if score > alpha {
				alpha = score
				if alpha >= beta {
					break
				}
			}
		}
	} else {
		// === MIN 节点 ===
		bestScore = math.MaxInt32

		for i, mv := range moves {
			undo := mMakeMoveWithUndo(b, mv, current)

			score := alphaBeta(b, 0, Opponent(current), original, depth-1, alpha, beta, allowJump, localNodes)

			b.UnmakeMove(undo)

			if score < bestScore {
				bestScore = score
				bestIdx = uint8(i)
			}
			if score < beta {
				beta = score
				if beta <= alpha {
					break
				}
			}
		}
	}

	// 6) 写回置换表（注意 flag 的 α/β 比较用原始窗口）
	var flag ttFlag
	switch {
	case bestScore <= alphaOrig:
		flag = ttUpper
	case bestScore >= betaOrig:
		flag = ttLower
	default:
		flag = ttExact
	}

	// bestScore 是 original 视角；转成 current 再存
	valTT := bestScore
	if current != original {
		valTT = -bestScore
	}
	storeTT(ttKey, int(depth), valTT, flag)
	storeBestIdx(ttKey, bestIdx)

	return bestScore
}


// ------------------------------------------------------------
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func chooseEndgameDepth(b *Board, base int) int {
	// 统计空格
	empties := 0
	for i := 0; i < BoardN; i++ {
		if b.Cells[i] == Empty {
			empties++
		}
	}
	switch {
	case empties <= 6:
		// 残局很小，基本可以搜到底（每回合至少占/改变1格，给点冗余）
		return base + 4
	case empties <= 10:
		return base + 2
	//case empties <= 14:
	//	return base + 2
	default:
		return base
	}
}

func findImmediateWinOnly(b *Board, p CellState) (Move, bool) {
	op := Opponent(p)
	for _, mv := range GenerateMoves(b, p) {
		undo := mMakeMoveWithUndo(b, mv, p)

		empties := 0
		for i := 0; i < BoardN; i++ {
			if b.Cells[i] == Empty {
				empties++
			}
		}
		noOpp := len(GenerateMoves(b, op)) == 0
		b.UnmakeMove(undo)

		if noOpp || empties == 0 {
			return mv, true
		}
	}
	return Move{}, false
}

func DeepSearch(b *Board, hash uint64, side CellState, depth int) int {

	return alphaBeta(b, hash, side, side, int64(depth), -32000, 32000, true, nil)
}

func IterativeDeepening(
	root *Board,
	player CellState,
	maxDepth int,
	allowJump bool,
) (best Move, bestScore int, ok bool) {

	for depth := 1; depth <= maxDepth; depth++ {
		// 暂时关闭残局加深，确保混合搜索时间稳定
		fullDepth := depth

		// 根搜索
		mv, hit := FindBestMoveAtDepth(root, player, int64(fullDepth), allowJump)
		if !hit {
			break
		}
		best, bestScore, ok = mv, 0, true
	}
	return
}
func AlphaBeta(b *Board, player CellState, depth int) int {
	// 1) 把“行棋方”也异或进哈希，确保置换表区分 Max/Min
	initialHash := b.hash ^ zobristSide[sideIdx(player)]

	// 2) 调用内层实现：先轮到对手走，再到 player
	return alphaBeta(
		b,
		initialHash,
		Opponent(player), // current = 对手
		player,           // original = 我方
		int64(depth),
		math.MinInt, // 初始 α
		math.MaxInt, // 初始 β
		true,
		nil)
}

// alphaBetaNoTT 在 b 上执行一次不带置换表的 α–β 搜索。
// - current: 当前行棋方
// - original: 根节点的行棋方，用于 evaluate 判断
// - depth: 剩余深度
// - alpha, beta: 剪枝界限
// ------------------------------------------------------------
// 对外包装器 —— 只要 3 个参数即可调用
// ------------------------------------------------------------
func AlphaBetaNoTT(b *Board, player CellState, depth int64) int {
	// 从对手开始递归（current），original = player
	return alphaBetaNoTT(
		b,
		Opponent(player), // current
		player,           // original
		int(depth),
		math.MinInt32,
		math.MaxInt32,
	)
}

// ------------------------------------------------------------
// 内部递归实现 —— 不暴露、多参数
// ------------------------------------------------------------
func alphaBetaNoTT(
	b *Board,
	current, original CellState,
	depth, alpha, beta int,
) int {
	// 递归终止：深度到 0 或无空位
	if depth == 0 || b.CountPieces(PlayerA)+b.CountPieces(PlayerB) == len(b.AllCoords()) {
		return Evaluate(b, original)
	}

	moves := GenerateMoves(b, current)

	if current == original {
		// -------- MAX 节点 --------
		best := math.MinInt32
		for _, mv := range moves {
			undo := mMakeMoveWithUndo(b, mv, current)
			score := alphaBetaNoTT(b, Opponent(current), original, depth-1, alpha, beta)
			b.UnmakeMove(undo)

			if score > best {
				best = score
			}
			if score > alpha {
				alpha = score
			}
			if alpha >= beta {
				break
			}
		}
		return best
	}

	// -------- MIN 节点 --------
	best := math.MaxInt32
	for _, mv := range moves {
		undo := mMakeMoveWithUndo(b, mv, current)
		score := alphaBetaNoTT(b, Opponent(current), original, depth-1, alpha, beta)
		b.UnmakeMove(undo)

		if score < best {
			best = score
		}
		if score < beta {
			beta = score
		}
		if beta <= alpha {
			break
		}
	}
	return best
}

func applyMoveFilters(b *Board, side CellState, moves []Move, allowJump bool) []Move {
	// 如果当前执子方使用的是 NN 评估，我们只保留最关键的过滤器。
	useNN := false
	if side == PlayerA && UseONNXForPlayerA {
		useNN = true
	} else if side == PlayerB && UseONNXForPlayerB {
		useNN = true
	}
	
	// 这里必须小心：如果 GenerateMoves 返回的是预分配缓冲区的切片，或者我们连续调用多个原地过滤器，
	// 逻辑必须闭环。
	out := filterJumpsByFlag(b, side, moves, allowJump)
	
	if useNN {
		// NN 玩家仍然应用这些核心的防御性过滤，防止 1 层搜索时的低级错误
		out = filterZeroInfectJumpsOrFallback(b, side, out)
		if allowJump {
			out = filterDangerousRecaptureJumps(b, side, out)
		}
		out = filterVulnerableZeroInfClones(b, side, out)
		return out
	}

	out = filterOpeningEdgeOnly(b, side, out)
	out = filterZeroInfectJumpsOrFallback(b, side, out)
	if allowJump {
		out = filterDangerousRecaptureJumps(b, side, out)
	}
	out = filterVulnerableZeroInfClones(b, side, out)
	out = filterDangerousIsolatedClones(b, side, out)
	return out
}

// 根节点/任意节点可复用的过滤器：尽量剔除“0 感染跳跃”，但保证不至于空集合
func filterZeroInfectJumpsOrFallback(b *Board, side CellState, moves []Move) []Move {
	n := 0
	for _, mv := range moves {
		if mv.IsJump() && previewInfectedCount(b, mv, side) == 0 {
			continue
		}
		moves[n] = mv
		n++
	}
	if n > 0 {
		return moves[:n]
	}
	// 如果全被剔空了，至少保留克隆；再不行就原样返回，避免无解
	n = 0
	for _, mv := range moves {
		if mv.IsClone() {
			moves[n] = mv
			n++
		}
	}
	if n > 0 {
		return moves[:n]
	}
	return moves
}

// 过滤“跳跃且只感染1子，但对手可一手同时反吃落点+该子”的招法。
// 保守起见：若全被删光，则回退原 moves。
func filterDangerousRecaptureJumps(b *Board, me CellState, moves []Move) []Move {
	op := Opponent(me)
	n := 0
	fullCount := len(moves)

	for i := 0; i < fullCount; i++ {
		mv := moves[i]
		// 只针对跳跃
		if !mv.IsJump() {
			moves[n] = mv
			n++
			continue
		}
		toIdx, ok := IndexOf[mv.To]
		if !ok {
			moves[n] = mv
			n++
			continue
		}

		// 统计“即时被你感染”的邻格（这里只关心 == 1 的情形）
		inf := -1
		for _, nb := range NeighI[toIdx] {
			if b.Cells[nb] == op {
				if inf == -1 {
					inf = nb
				} else {
					inf = -2 // 多于1个
					break
				}
			}
		}
		if inf != -1 && inf != -2 {
			// inf == 单一被感染格的下标
		} else {
			// 0 或 >=2，不做这个危险判定（按你描述只针对“感染1子”）
			moves[n] = mv
			n++
			continue
		}

		// 找“同时邻接 落点(toIdx) 和 被感染(inf) 的空位 x”
		// 也就是 x ∈ Neigh(toIdx) ∩ Neigh(inf)
		danger := false
		for _, x := range NeighI[toIdx] {
			if b.Cells[x] != Empty {
				continue
			}
			// x 也必须邻接 inf
			if !isNeighborI(inf, x) {
				continue
			}
			// 对手下一手能到 x（克隆/跳），则这步判危险
			if opponentCanReachNextI(b, op, x) {
				danger = true
				break
			}
		}

		if !danger {
			moves[n] = mv
			n++
		}
	}

	if n == 0 {
		return moves[:fullCount]
	}
	return moves[:n]
}

// 开局启发：在未发生过感染前，只允许沿边缘的克隆（from/to 都在外圈，且是克隆）。
// 若因此删空则回退原 moves，保证有解。
func filterOpeningEdgeOnly(b *Board, side CellState, moves []Move) []Move {
	if infectionUnlocked(b) {
		return moves
	}
	n := 0
	fullCount := len(moves)
	for i := 0; i < fullCount; i++ {
		mv := moves[i]
		if mv.IsClone() && isOuterI[IndexOf[mv.From]] && isOuterI[IndexOf[mv.To]] {
			moves[n] = mv
			n++
		}
	}
	if n == 0 {
		return moves[:fullCount]
	}
	return moves[:n]
}

// 粗略判定是否已“解锁”：上一手有感染，或当前棋面存在相邻异色棋子。
func infectionUnlocked(b *Board) bool {
	if b.LastInfect > 0 {
		return true
	}
	for i := 0; i < BoardN; i++ {
		if b.Cells[i] == Empty || b.Cells[i] == Blocked {
			continue
		}
		for _, nb := range NeighI[i] {
			if b.Cells[nb] == Empty || b.Cells[nb] == Blocked {
				continue
			}
			if b.Cells[nb] != b.Cells[i] {
				return true
			}
		}
	}
	return false
}
func opponentCanReachNextI(b *Board, op CellState, dst int) bool {
	if b.Cells[dst] != Empty {
		return false
	}
	// 克隆一步（邻接）
	for _, from := range NeighI[dst] {
		if b.Cells[from] == op {
			return true
		}
	}
	// 跳两步（距离2）
	for _, from := range JumpI[dst] {
		if b.Cells[from] == op {
			return true
		}
	}
	return false
}

// 删掉“感染数 < minInf”的跳越。例：minInf=2 => 删掉0和1感染跳越。
// 若全删光，则至少保留所有克隆；再不行就原样返回，保证不至于无解。
func filterLowInfectJumpsOrFallback(b *Board, side CellState, moves []Move, minInf int) []Move {
	filtered := make([]Move, 0, len(moves))
	for _, mv := range moves {
		if mv.IsJump() && previewInfectedCount(b, mv, side) < minInf {
			continue
		}
		filtered = append(filtered, mv)
	}
	if len(filtered) > 0 {
		return filtered
	}
	// 回退：至少保留克隆
	clones := make([]Move, 0, len(moves))
	for _, mv := range moves {
		if mv.IsClone() {
			clones = append(clones, mv)
		}
	}
	if len(clones) > 0 {
		return clones
	}
	return moves
}

func isIsolated(b *Board, who CellState, at HexCoord) bool {
	i, ok := IndexOf[at]
	if !ok {
		return false // 越界
	}
	if b.Cells[i] != who {
		return false
	}
	for _, j := range NeighI[i] {
		if b.Cells[j] == who {
			return false
		}
	}
	return true
}

func sharedNeighbors(a, b HexCoord) []HexCoord {
	m := make(map[HexCoord]bool, 6)
	for _, d := range Directions {
		m[HexCoord{a.Q + d.Q, a.R + d.R}] = true
	}
	out := make([]HexCoord, 0, 2)
	for _, d := range Directions {
		c := HexCoord{b.Q + d.Q, b.R + d.R}
		if m[c] {
			out = append(out, c)
		}
	}
	return out
}

func isDangerousIsolatedClone(b *Board, me CellState, mv Move) bool {
	if !mv.IsClone() {
		return false
	}
	if !isIsolated(b, me, mv.From) {
		return false
	}
	op := Opponent(me)
	// from/to 的共同邻居作为“对手一跳双吃”的落点候选
	for _, x := range sharedNeighbors(mv.From, mv.To) {
		if opponentCanReachNext(b, op, x) {
			return true
		}
	}
	return false
}

// 删掉“危险孤立克隆”。若删光了，就回退为原 moves（避免无解）；
func filterDangerousIsolatedClones(b *Board, me CellState, moves []Move) []Move {
	// 只在开局/前中期更有意义，降低误杀：空位比例大时才启用
	total := len(b.AllCoords())
	empties := 0
	for i := 0; i < BoardN; i++ {
		if b.Cells[i] == Empty {
			empties++
		}
	}
	r := float64(empties) / float64(total)
	if r < 0.65 { // 阈值可调：开局/前中期才启用
		return moves
	}

	n := 0
	originalCount := len(moves)
	for i := 0; i < originalCount; i++ {
		mv := moves[i]
		if isDangerousIsolatedClone(b, me, mv) {
			continue
		}
		moves[n] = mv
		n++
	}
	if n > 0 {
		return moves[:n]
	}
	return moves[:originalCount] // 全被删光就回退
}

// 过滤“克隆且不吃子，但对手下一步可以到达 from/to 的共同邻居并感染这两个子”的招法。
// 若全删光则回退原 moves。
func filterVulnerableZeroInfClones(b *Board, me CellState, moves []Move) []Move {
	op := Opponent(me)
	n := 0
	originalCount := len(moves)
	for i := 0; i < originalCount; i++ {
		mv := moves[i]
		if !mv.IsClone() {
			moves[n] = mv
			n++
			continue
		}
		// 仅关注“未吃子”的克隆
		if previewInfectedCount(b, mv, me) != 0 {
			moves[n] = mv
			n++
			continue
		}
		// 寻找 from/to 的共同邻居空位，若对手可一手到达则视为危险
		danger := false
		for _, x := range sharedNeighbors(mv.From, mv.To) {
			if idx, ok := IndexOf[x]; ok {
				if b.Cells[idx] != Empty {
					continue
				}
				if opponentCanReachNext(b, op, x) {
					danger = true
					break
				}
			}
		}
		if !danger {
			moves[n] = mv
			n++
		}
	}
	if n == 0 {
		return moves[:originalCount]
	}
	return moves[:n]
}
func opponentCanJumpTo(b *Board, op CellState, dst HexCoord) bool {
	// 坐标 -> 下标
	to, ok := IndexOf[dst]
	if !ok {
		return false
	}
	// 目标必须是空
	if b.Cells[to] != Empty {
		return false
	}
	// 任何一个“能跳到 to 的起点(from)”上有对手棋子即可
	for _, from := range JumpI[to] {
		if b.Cells[from] == op {
			return true
		}
	}
	return false
}

func opponentCanCloneTo(b *Board, op CellState, dst HexCoord) bool {
	to, ok := IndexOf[dst]
	if !ok {
		return false
	}
	if b.Cells[to] != Empty {
		return false
	}
	// 任何一个相邻格有对手棋子即可克隆到 to
	for _, from := range NeighI[to] {
		if b.Cells[from] == op {
			return true
		}
	}
	return false
}

// 对手下一手“能占到 dst 吗”（克隆或跳越任一成立）
func opponentCanReachNext(b *Board, op CellState, dst HexCoord) bool {
	return opponentCanCloneTo(b, op, dst) || opponentCanJumpTo(b, op, dst)
}
