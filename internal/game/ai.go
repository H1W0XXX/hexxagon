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
const useLearned2 = true
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
		LastMove:   b.LastMove,
		LastMover:  b.LastMover,
		LastInfect: b.LastInfect,
	}
	return nb
}

func FindBestMoveAtDepth(b *Board, player CellState, depth int64, allowJump bool) (Move, bool) {

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
		s := func() int {
			if useLearned {
				return HybridEval(b, player)
			}
			return evaluateStatic(b, player)
		}()
		// 轻量启发：感染数加权，能明显稳定排序（尤其早中期）
		inf := previewInfectedCount(b, m, player)
		s += 2 * inf

		b.UnmakeMove(undo)

		// 只在根层，对我方跳跃降权（递归里保持中立）
		if !useLearned && m.IsJump() {
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
) int {
	incNodes()
	// 1) 走法生成（含 UI 禁跳）
	moves := GenerateMoves(b, current)
	moves = filterJumpsByFlag(b, current, moves, allowJump)

	if depth == 0 || len(moves) == 0 {
		// original 视角的评估（和你原来一致）
		var valOrig int
		if useLearned {
			valOrig = HybridEval(b, original)
		} else {
			valOrig = evaluateStatic(b, original)
		}

		// 存 TT 用 current 视角（与 key 里的 current 对齐）
		valTT := valOrig
		if current != original {
			valTT = -valOrig
		}
		ttKey := ttKeyFor(b, current)
		storeTT(ttKey, int(depth), valTT, ttExact)

		// 返回仍用 original 视角
		return valOrig
	}

	// 3) 置换表探测（混入 side）
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

			score := alphaBeta(b, 0, Opponent(current), original, depth-1, alpha, beta, allowJump)

			b.UnmakeMove(undo)

			// 可选：对跳跃加一个固定惩罚，与你现有逻辑一致
			//if mv.IsJump() && !useLearned {
			//	score -= jumpMovePenalty
			//}

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

			score := alphaBeta(b, 0, Opponent(current), original, depth-1, alpha, beta, allowJump)

			b.UnmakeMove(undo)

			// 与你现有逻辑一致：如果也想让 MIN 讨厌跳跃，就加正分
			//if mv.IsJump() && !useLearned {
			//	score += jumpMovePenalty
			//}

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

	return alphaBeta(b, hash, side, side, int64(depth), -32000, 32000, true)
}

func IterativeDeepening(
	root *Board,
	player CellState,
	maxDepth int,
	allowJump bool,
) (best Move, bestScore int, ok bool) {

	for depth := 1; depth <= maxDepth; depth++ {
		// 用“根节点的 TT key”写入 bestIdx 提示（这里写 0 作用很有限，但至少 key 是对的）
		storeBestIdx(ttKeyFor(root, player), 0)

		// 残局加深
		fullDepth := chooseEndgameDepth(root, depth)

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
		true)
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
		return evaluateStatic(b, original)
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

// 根节点/任意节点可复用的过滤器：尽量剔除“0 感染跳跃”，但保证不至于空集合
func filterZeroInfectJumpsOrFallback(b *Board, side CellState, moves []Move) []Move {
	filtered := make([]Move, 0, len(moves))
	for _, mv := range moves {
		if mv.IsJump() && previewInfectedCount(b, mv, side) == 0 {
			continue
		}
		filtered = append(filtered, mv)
	}
	if len(filtered) > 0 {
		return filtered
	}
	// 如果全被剔空了，至少保留克隆；再不行就原样返回，避免无解
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

// 过滤“跳跃且只感染1子，但对手可一手同时反吃落点+该子”的招法。
// 保守起见：若全被删光，则回退原 moves。
func filterDangerousRecaptureJumps(b *Board, me CellState, moves []Move) []Move {
	op := Opponent(me)
	out := make([]Move, 0, len(moves))

	for _, mv := range moves {
		// 只针对跳跃
		if !mv.IsJump() {
			out = append(out, mv)
			continue
		}
		toIdx, ok := IndexOf[mv.To]
		if !ok {
			out = append(out, mv)
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
			out = append(out, mv)
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
			out = append(out, mv)
		}
	}

	if len(out) == 0 {
		return moves
	}
	return out
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

	out := make([]Move, 0, len(moves))
	for _, mv := range moves {
		if isDangerousIsolatedClone(b, me, mv) {
			continue
		}
		out = append(out, mv)
	}
	if len(out) > 0 {
		return out
	}
	return moves // 全被删光就回退
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
