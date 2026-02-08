package game

import (
	"math"
	"sort"
)

// 两阶段搜索：stage0 选子，stage1 落子（克隆/跳跃），以对齐 C++ 训练时的特征含义。
// Board 不存 stage，由搜索栈维护。

// twoPhaseSearch 返回 original 视角的分值与选定的实际落子（从 stage1 执行的 Move）。
// stage==0: 还未选子；stage==1: 已选定 fromIdx。
func twoPhaseSearch(
	b *Board,
	current CellState,
	original CellState,
	depth int64,
	stage int,
	selectedIdx int,
	allowJump bool,
	alpha int,
	beta int,
) (bestScore int, bestMove Move, ok bool) {
	const inf = math.MaxInt32
	alphaOrig, betaOrig := alpha, beta

	// 置换表 key：包含 stage/selectedIdx
	depthKey := int(depth*2 + int64(stage))
	key := ttKeyForTwoPhase(b, current, stage, selectedIdx)

	// 置换表探测
	if hit, valCur, flag := probeTT(key, depthKey); hit {
		val := valCur
		if current != original {
			val = -valCur
		}
		switch flag {
		case ttExact:
			return val, Move{}, true
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
			return val, Move{}, true
		}
	}

	// 深度耗尽：尽量用 stage1 评估（与训练一致），否则在 stage0 选子后评估
	if depth < 0 {
		if stage == 1 {
			bestScore = EvaluateWithSelection(b, original, boardIndexToGrid[selectedIdx])
			valTT := bestScore
			if current != original {
				valTT = -bestScore
			}
			storeTT(key, depthKey, valTT, ttExact)
			return bestScore, Move{}, true
		}
		// stage0：尝试选子后评估，不递减 depth
		selectables := selectablePieces(b, current, allowJump)
		if len(selectables) == 0 {
			bestScore = EvaluateWithSelection(b, original, -1)
			valTT := bestScore
			if current != original {
				valTT = -bestScore
			}
			storeTT(key, depthKey, valTT, ttExact)
			return bestScore, Move{}, true
		}
		// policy 加权的期望/最大化：对每个选子取 value 和最大 prior，按先验调整
		type selVal struct {
			val   int
			prior float32
		}
		cands := make([]selVal, 0, len(selectables))
		for _, idx := range selectables {
			v := EvaluateWithSelection(b, original, boardIndexToGrid[idx])
			pr := float32(0)
			if priors, _, err := KataPolicyValueWithSelection(b, current, boardIndexToGrid[idx]); err == nil && priors != nil {
				for _, mv := range movesFromSelected(b, current, idx, allowJump) {
					if toIdx, ok := IndexOf[mv.To]; ok {
						g := boardIndexToGrid[toIdx]
						if g >= 0 && g < len(priors) && priors[g] > pr {
							pr = priors[g]
						}
					}
				}
			}
			cands = append(cands, selVal{val: v, prior: pr})
		}
		if current == original {
			bestScore = math.MinInt32
			for _, c := range cands {
				score := int(float32(c.val) * (1 + c.prior))
				if score > bestScore {
					bestScore = score
				}
			}
		} else {
			bestScore = math.MaxInt32
			for _, c := range cands {
				score := int(float32(c.val) * (1 + c.prior))
				if score < bestScore {
					bestScore = score
				}
			}
		}
		valTT := bestScore
		if current != original {
			valTT = -bestScore
		}
		storeTT(key, depthKey, valTT, ttExact)
		return bestScore, Move{}, true
	}

	// stage0: 选子
	if stage == 0 {
		selectables := selectablePieces(b, current, allowJump)
		if len(selectables) == 0 {
			bestScore = EvaluateWithSelection(b, original, -1)
			valTT := bestScore
			if current != original {
				valTT = -bestScore
			}
			storeTT(key, depthKey, valTT, ttExact)
			return bestScore, Move{}, true
		}

		// 用 policy 先验对“选子”排序：每个候选子取其合法落点里的最大 prior
		type sel struct {
			idx   int
			prior float32
		}
		ordered := make([]sel, len(selectables))
		for i, idx := range selectables {
			// 获取选中该子的 policy
			pr := float32(0)
			if priors, _, err := KataPolicyValueWithSelection(b, current, boardIndexToGrid[idx]); err == nil && priors != nil {
				for _, mv := range movesFromSelected(b, current, idx, allowJump) {
					if toIdx, ok := IndexOf[mv.To]; ok {
						g := boardIndexToGrid[toIdx]
						if g >= 0 && g < len(priors) && priors[g] > pr {
							pr = priors[g]
						}
					}
				}
			}
			ordered[i] = sel{idx: idx, prior: pr}
		}
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].prior > ordered[j].prior })
		// TT 提示最佳选子：bestIdx 存的是“棋盘下标”，按照匹配移动到队首。
		if hit, bi := probeBestIdx(key); hit {
			tgt := int(bi)
			for i, it := range ordered {
				if it.idx == tgt && i > 0 {
					ordered[0], ordered[i] = ordered[i], ordered[0]
					break
				}
			}
		}

		if current == original {
			bestScore = math.MinInt32
			bestIdxStored := uint8(0)
			for _, it := range ordered {
				idx := it.idx
				score, mv, childOK := twoPhaseSearch(b, current, original, depth, 1, idx, allowJump, alpha, beta)
				if !childOK {
					continue
				}
				if score > bestScore {
					bestScore = score
					bestMove = mv
					if idx >= 0 && idx < 256 {
						bestIdxStored = uint8(idx)
					}
				}
				if score > alpha {
					alpha = score
					if alpha >= beta {
						break
					}
				}
			}
			storeBestIdx(key, bestIdxStored)
		} else {
			bestScore = math.MaxInt32
			bestIdxStored := uint8(0)
			for _, it := range ordered {
				idx := it.idx
				score, mv, childOK := twoPhaseSearch(b, current, original, depth, 1, idx, allowJump, alpha, beta)
				if !childOK {
					continue
				}
				if score < bestScore {
					bestScore = score
					bestMove = mv
					if idx >= 0 && idx < 256 {
						bestIdxStored = uint8(idx)
					}
				}
				if score < beta {
					beta = score
					if beta <= alpha {
						break
					}
				}
			}
			storeBestIdx(key, bestIdxStored)
		}
		// 写 TT
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
		storeTT(key, depthKey, valTT, flag)
		return bestScore, bestMove, true
	}

	// stage1: 从 selectedIdx 落子
	moves := movesFromSelected(b, current, selectedIdx, allowJump)
	if len(moves) == 0 {
		bestScore = EvaluateWithSelection(b, original, boardIndexToGrid[selectedIdx])
		valTT := bestScore
		if current != original {
			valTT = -bestScore
		}
		storeTT(key, depthKey, valTT, ttExact)
		return bestScore, Move{}, true
	}

	// policy 先验排序：一次获取 policy 概率
	type pmove struct {
		mv    Move
		prior float32
		toIdx int
	}
	ordered := make([]pmove, len(moves))
	var priors []float32
	if p, _, err := KataPolicyValueWithSelection(b, current, boardIndexToGrid[selectedIdx]); err == nil {
		priors = p
	}
	for i, mv := range moves {
		p := float32(0)
		toIdx := -1
		if priors != nil {
			if idx, ok := IndexOf[mv.To]; ok {
				toIdx = idx
				g := boardIndexToGrid[idx]
				if g >= 0 && g < len(priors) {
					p = priors[g]
				}
			}
		}
		ordered[i] = pmove{mv: mv, prior: p, toIdx: toIdx}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].prior > ordered[j].prior })
	// TT 提示最佳“落点 toIdx”，匹配后移到队首。
	if hit, bi := probeBestIdx(key); hit {
		tgt := int(bi)
		for i, pm := range ordered {
			if pm.toIdx == tgt && i > 0 {
				ordered[0], ordered[i] = ordered[i], ordered[0]
				break
			}
		}
	}

	if current == original {
		bestScore = math.MinInt32
		bestIdxStored := uint8(0)
		for _, pm := range ordered {
			mv := pm.mv
			undo := mMakeMoveWithUndo(b, mv, current)
			score, _, childOK := twoPhaseSearch(b, Opponent(current), original, depth-1, 0, -1, allowJump, alpha, beta)
			b.UnmakeMove(undo)
			if !childOK {
				continue
			}
			if score > bestScore {
				bestScore = score
				bestMove = mv
				if pm.toIdx >= 0 && pm.toIdx < 256 {
					bestIdxStored = uint8(pm.toIdx)
				}
			}
			if score > alpha {
				alpha = score
				if alpha >= beta {
					break
				}
			}
		}
		storeBestIdx(key, bestIdxStored)
	} else {
		bestScore = math.MaxInt32
		bestIdxStored := uint8(0)
		for _, pm := range ordered {
			mv := pm.mv
			undo := mMakeMoveWithUndo(b, mv, current)
			score, _, childOK := twoPhaseSearch(b, Opponent(current), original, depth-1, 0, -1, allowJump, alpha, beta)
			b.UnmakeMove(undo)
			if !childOK {
				continue
			}
			if score < bestScore {
				bestScore = score
				bestMove = mv
				if pm.toIdx >= 0 && pm.toIdx < 256 {
					bestIdxStored = uint8(pm.toIdx)
				}
			}
			if score < beta {
				beta = score
				if beta <= alpha {
					break
				}
			}
		}
		storeBestIdx(key, bestIdxStored)
	}
	// 写 TT
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
	storeTT(key, depthKey, valTT, flag)
	return bestScore, bestMove, true
}

// selectablePieces：stage0 下可选择的己方棋子（至少有合法落点）。
func selectablePieces(b *Board, player CellState, allowJump bool) []int {
	out := make([]int, 0, 16)
	for i := 0; i < BoardN; i++ {
		if b.Cells[i] != player {
			continue
		}
		if len(movesFromSelected(b, player, i, allowJump)) == 0 {
			continue
		}
		out = append(out, i)
	}
	return out
}

// movesFromSelected：stage1 下从指定棋子出发的合法落点。
func movesFromSelected(b *Board, player CellState, fromIdx int, allowJump bool) []Move {
	if fromIdx < 0 || fromIdx >= BoardN {
		return nil
	}
	moves := make([]Move, 0, len(NeighI[fromIdx])+len(JumpI[fromIdx]))
	fromCoord := CoordOf[fromIdx]

	// 克隆
	for _, to := range NeighI[fromIdx] {
		if b.Cells[to] == Empty {
			moves = append(moves, Move{From: fromCoord, To: CoordOf[to]})
		}
	}
	// 跳跃
	if allowJump {
		for _, to := range JumpI[fromIdx] {
			if b.Cells[to] == Empty {
				moves = append(moves, Move{From: fromCoord, To: CoordOf[to]})
			}
		}
	}
	return moves
}

// FindBestMoveTwoPhase：入口，深度按“完整一步”（选子+落子算1 ply）。
func FindBestMoveTwoPhase(b *Board, player CellState, depth int64, allowJump bool) (Move, bool) {
	score, mv, ok := twoPhaseSearch(b, player, player, depth, 0, -1, allowJump, math.MinInt32/4, math.MaxInt32/4)
	_ = score
	return mv, ok
}
