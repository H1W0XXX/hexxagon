// game/mcts.go
package game

import (
	"math"
	"math/rand"
	"time"
)

type mctsNode struct {
	parent       *mctsNode
	move         Move      // 走到本节点所下的那步（root 的 move 为零值）
	playerToMove CellState // 轮到谁落子（在“进入本节点的局面”）
	children     map[Move]*mctsNode
	prior        float64 // 先验（这里先均匀 = 1/len）
	visits       int
	valueSum     float64 // 累积价值（从 rootPlayer 视角）
	unexpanded   []Move  // 还未展开的走法
	hash         uint64  // 可选：用来做跨层转置表
	terminal     bool

	rootPlayer CellState // 这棵树的“AI 方”
	aiCanJump  bool      // 是否允许 AI 方在本次搜索里考虑跳越
}

func newNode(b *Board, player CellState, parent *mctsNode, mv Move, rootPlayer CellState, aiCanJump bool) *mctsNode {
	mvs := GenerateMoves(b, player)
	mvs = filterMovesForSide(b, player, rootPlayer, aiCanJump, mvs)

	n := &mctsNode{
		parent:       parent,
		move:         mv,
		playerToMove: player,
		children:     make(map[Move]*mctsNode),
		unexpanded:   make([]Move, 0, len(mvs)),
		hash:         b.Hash(),
		terminal:     len(mvs) == 0,
		rootPlayer:   rootPlayer,
		aiCanJump:    aiCanJump,
	}
	n.unexpanded = append(n.unexpanded, mvs...)
	return n
}
func (n *mctsNode) q() float64 {
	if n.visits == 0 {
		return 0
	}
	return n.valueSum / float64(n.visits)
}

// UCT 选择（用 prior 当成 c_puct 里的 P；纯 MCTS 时取均匀）
func selectChild(n *mctsNode, cPUCT float64) (Move, *mctsNode) {
	var best Move
	var bestChild *mctsNode
	bestScore := -math.MaxFloat64
	parentVisits := math.Max(1, float64(n.visits))
	for mv, ch := range n.children {
		u := cPUCT * ch.prior * math.Sqrt(parentVisits) / (1.0 + float64(ch.visits))
		score := ch.q() + u
		if score > bestScore {
			bestScore = score
			best = mv
			bestChild = ch
		}
	}
	return best, bestChild
}

// 简单的 rollout 策略：优先克隆、丢弃0感染跳、否则随机
func rolloutPolicy(b *Board, side, rootPlayer CellState, aiCanJump bool) (Move, bool) {
	mvs := GenerateMoves(b, side)
	mvs = filterMovesForSide(b, side, rootPlayer, aiCanJump, mvs)
	if len(mvs) == 0 {
		return Move{}, false
	}
	// 先选克隆
	clones := mvs[:0]
	for _, m := range mvs {
		if m.IsClone() {
			clones = append(clones, m)
		}
	}
	cand := mvs
	if len(clones) > 0 {
		cand = clones
	} else {
		// 丢弃0感染跳
		tmp := cand[:0]
		for _, m := range cand {
			if m.IsJump() && previewInfectedCount(b, m, side) == 0 {
				continue
			}
			tmp = append(tmp, m)
		}
		if len(tmp) > 0 {
			cand = tmp
		}
	}
	return cand[rand.Intn(len(cand))], true
}

// 模拟到终局或步限，返回 [-1,1] 结果（rootPlayer 视角）
func rollout(b *Board, toMove, rootPlayer CellState, aiCanJump bool, maxPlies int) float64 {
	cur := toMove
	canJump := aiCanJump // 模拟过程中可动态解锁

	for ply := 0; ply < maxPlies; ply++ {
		// rolloutPolicy 内部会在 side==rootPlayer 且 !canJump 时过滤掉跳越
		mv, ok := rolloutPolicy(b, cur, rootPlayer, canJump)
		if !ok {
			break
		}

		u := mMakeMoveWithUndo(b, mv, cur)

		// 动态解锁：如果刚才走子的是“对手”（相对 rootPlayer）
		// 且他这步感染了我方，那么之后允许 AI 跳越
		if b.LastMover == Opponent(rootPlayer) && b.LastInfect > 0 {
			canJump = true
		}

		cur = Opponent(cur)
		b.UnmakeMove(u)
	}

	// 终结评分：仅子数差（rootPlayer 视角）
	diff := b.CountPieces(rootPlayer) - b.CountPieces(Opponent(rootPlayer))
	if diff > 0 {
		return 1
	} else if diff < 0 {
		return -1
	}
	return 0
}

// 主入口：给定迭代次数或时间预算，返回访问最多的子
func FindBestMoveMCTS(rootBoard *Board, player CellState, sims int, timeBudget time.Duration, allowJump bool) (Move, bool) {
	if sims <= 0 && timeBudget <= 0 {
		sims = 2000
	}
	rand.Seed(time.Now().UnixNano())

	// 根节点闸门：由 UI 持久传入，不看 LastInfect
	aiCanJump := allowJump

	root := newNode(rootBoard, player, nil, Move{}, player, aiCanJump)

	deadline := time.Now().Add(timeBudget)
	for iter := 0; ; iter++ {
		if sims > 0 && iter >= sims {
			break
		}
		if timeBudget > 0 && time.Now().After(deadline) {
			break
		}

		b := rootBoard.Clone()
		cur := root
		path := make([]undoInfo, 0, 128)

		// Selection
		for !cur.terminal && len(cur.unexpanded) == 0 && len(cur.children) > 0 {
			mv, child := selectChild(cur, 1.4)
			u := mMakeMoveWithUndo(b, mv, cur.playerToMove)
			path = append(path, u)
			cur = child
		}

		// Expansion（把闸门透传给子节点）
		if !cur.terminal && len(cur.unexpanded) > 0 {
			last := len(cur.unexpanded) - 1
			mv := cur.unexpanded[last]
			cur.unexpanded = cur.unexpanded[:last]

			u := mMakeMoveWithUndo(b, mv, cur.playerToMove)
			path = append(path, u)

			child := newNode(b, Opponent(cur.playerToMove), cur, mv, root.rootPlayer, root.aiCanJump)

			total := len(child.unexpanded) + len(child.children)
			prior := 1.0
			if total > 0 {
				prior = 1.0 / float64(total)
			}
			child.prior = prior

			cur.children[mv] = child
			cur = child
		}

		// Evaluation / Rollout（用根的闸门；不在模拟中改写它）
		v := rollout(b, cur.playerToMove, root.rootPlayer, root.aiCanJump, 64)

		// 回溯
		for i := len(path) - 1; i >= 0; i-- {
			b.UnmakeMove(path[i])
		}

		// Backup
		for n := cur; n != nil; n = n.parent {
			n.visits++
			if n.playerToMove == player {
				n.valueSum += v
			} else {
				n.valueSum -= v
			}
		}
	}

	if len(root.children) == 0 {
		return Move{}, false
	}
	var best Move
	bestN := -1
	for mv, ch := range root.children {
		if ch.visits > bestN {
			bestN = ch.visits
			best = mv
		}
	}
	return best, true
}

// FindBestMoveMCTSWithVisits：带 root 访问计数分布的 MCTS（可选 NN 先验）
// 返回：最佳走法、每个 9x9 格的访问次数（未在棋盘上的格子为 0）、是否成功找到走法
func FindBestMoveMCTSWithVisits(rootBoard *Board, player CellState, sims int, timeBudget time.Duration, allowJump bool) (Move, []int, bool) {
	if sims <= 0 && timeBudget <= 0 {
		sims = 800
	}
	rand.Seed(time.Now().UnixNano())

	aiCanJump := allowJump

	root := newNode(rootBoard, player, nil, Move{}, player, aiCanJump)

	// 根节点 NN 先验（softmax 概率）；失败则退化为均匀
	rootPrior, _, err := PolicyValueNN(rootBoard, player)
	if err != nil || len(rootPrior) != GridSize*GridSize {
		rootPrior = nil
	}

	deadline := time.Now().Add(timeBudget)
	for iter := 0; ; iter++ {
		if sims > 0 && iter >= sims {
			break
		}
		if timeBudget > 0 && time.Now().After(deadline) {
			break
		}

		b := rootBoard.Clone()
		cur := root
		playerToMove := player
		pathUndos := make([]undoInfo, 0, 128)

		// Selection
		for !cur.terminal && len(cur.unexpanded) == 0 && len(cur.children) > 0 {
			mv, child := selectChild(cur, 1.4)
			u := mMakeMoveWithUndo(b, mv, playerToMove)
			pathUndos = append(pathUndos, u)
			playerToMove = Opponent(playerToMove)
			cur = child
		}

		// Expansion
		if !cur.terminal && len(cur.unexpanded) > 0 {
			last := len(cur.unexpanded) - 1
			mv := cur.unexpanded[last]
			cur.unexpanded = cur.unexpanded[:last]

			u := mMakeMoveWithUndo(b, mv, playerToMove)
			pathUndos = append(pathUndos, u)

			child := newNode(b, Opponent(playerToMove), cur, mv, root.rootPlayer, root.aiCanJump)

			// 设置先验：根节点用 NN，其他节点均匀
			pr := 1.0
			if cur.parent == nil && rootPrior != nil {
				idx := AxialToIndex(mv.To)
				if idx >= 0 && idx < len(rootPrior) {
					pr = float64(rootPrior[idx]) + 1e-6
				}
			} else {
				total := len(child.unexpanded) + len(child.children)
				if total > 0 {
					pr = 1.0 / float64(total)
				}
			}
			child.prior = pr

			cur.children[mv] = child
			cur = child
			playerToMove = Opponent(playerToMove)
		}

		// Evaluation：如果没有子则终局，否则用 NN value
		var leafValue float64
		if cur.terminal {
			diff := b.CountPieces(root.rootPlayer) - b.CountPieces(Opponent(root.rootPlayer))
			switch {
			case diff > 0:
				leafValue = 1.0
			case diff < 0:
				leafValue = -1.0
			default:
				leafValue = 0.0
			}
		} else {
			vProb := float64(EvaluateNN3(b, playerToMove)) / 100.0 // 当前行棋方胜率
			if playerToMove != root.rootPlayer {
				vProb = 1.0 - vProb
			}
			leafValue = vProb*2 - 1 // 转到 rootPlayer 视角 [-1,1]
		}

		// Backup
		for n := cur; n != nil; n = n.parent {
			n.visits++
			if n.playerToMove == root.rootPlayer {
				n.valueSum += leafValue
			} else {
				n.valueSum -= leafValue
			}
		}

		// 回溯棋盘
		for i := len(pathUndos) - 1; i >= 0; i-- {
			b.UnmakeMove(pathUndos[i])
		}
	}

	if len(root.children) == 0 {
		return Move{}, nil, false
	}
	var best Move
	bestN := -1
	for mv, ch := range root.children {
		if ch.visits > bestN {
			bestN = ch.visits
			best = mv
		}
	}

	visits := make([]int, GridSize*GridSize)
	for mv, ch := range root.children {
		idx := AxialToIndex(mv.To)
		if idx >= 0 && idx < len(visits) {
			visits[idx] = ch.visits
		}
	}
	return best, visits, true
}

// 仅当 side==rootPlayer 且 aiCanJump==false 时，过滤掉跳越（保底：若没有克隆则不删）
func filterMovesForSide(b *Board, side, rootPlayer CellState, aiCanJump bool, moves []Move) []Move {
	if side != rootPlayer || aiCanJump {
		return moves
	}
	clones := make([]Move, 0, len(moves))
	for _, m := range moves {
		if m.IsClone() {
			clones = append(clones, m)
		}
	}
	if len(clones) > 0 {
		return clones
	}
	return moves
}
