// file: internal/game/evaluate.go
package game

// 可调参数
var (
	cloneThresh = 0.25      // 克隆/跳跃阈值
	jumpThresh  = 1.0 / 3.0 // 跳跃/残局阈值
	dangerW     = 40        // 暴露惩罚权重
)

// 加分上下限
const (
	CLONE_BONUS_MAX       = 30
	CLONE_BONUS_MIN       = 14
	JUMP_BONUS_MAX        = 3
	JUMP_BONUS_MIN        = 0
	blockW                = 2
	zeroInfectJumpPenalty = -5000
)

// ========== 新增部分：加权感染数与开局惩罚常量 ==========
const (
	// 开局阶段阈值：当空位比例 r ≥ 0.82 时，视为“开局”
	openingPhaseThresh = 0.82
	// 开局被对手感染时的额外惩罚权重
	openingPenaltyWeight = 10

	// 跳跃感染权重（跳跃感染得分较低）
	jumpInfWeight = 1
	// 克隆感染权重（克隆感染得分较高）
	cloneInfWeight = 2

	// ========= 新增 =========
	// 如果处于开局阶段(r ≥ openingPhaseThresh)，且我方存在可用克隆却没有使用克隆，
	// 那么静态评估扣分（值可以根据调试再微调）
	earlyJumpPenalty = -50
)

// HexCoord.Add：方便邻格计算
func (h HexCoord) Add(o HexCoord) HexCoord {
	return HexCoord{h.Q + o.Q, h.R + o.R}
}

// ApplyPreview：在不修改棋盘的情况下预览感染数
func (m Move) ApplyPreview(b *Board, player CellState) (infected int, ok bool) {
	coords, undo := m.MakeMove(b, player)
	b.UnmakeMove(undo)
	return len(coords), true
}

// 对外导出
func Evaluate(b *Board, player CellState) int {
	return evaluateStatic(b, player)
}

func isOuter(c HexCoord, radius int) bool {
	ring := max3(abs(c.Q), abs(c.R), abs(c.Q+c.R))
	return ring == radius // 最外一圈
}

func outerRingCoords(b *Board) []HexCoord {
	var ring []HexCoord
	for _, c := range b.AllCoords() {
		if isOuter(c, b.radius) && b.Get(c) != Blocked {
			ring = append(ring, c)
		}
	}
	return ring
}

func compSizeAt(b *Board, start HexCoord, color CellState) int {
	if b.Get(start) != color {
		return 0
	}
	visited := make(map[HexCoord]bool, 16)
	stack := []HexCoord{start}
	visited[start] = true
	size := 0
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		size++
		for _, d := range cloneDirs {
			nb := HexCoord{cur.Q + d.Q, cur.R + d.R}
			if !visited[nb] && b.Get(nb) == color {
				visited[nb] = true
				stack = append(stack, nb)
			}
		}
	}
	return size
}

// 判断一个连通分量是否包含“紧密三角形”
// 判定：在分量内存在某个格 c，使得 c 的相邻两个“相邻方向”(i, i+1) 都在分量中
func hasTightTriangle(comp []HexCoord, compSet map[HexCoord]struct{}) bool {
	for _, c := range comp {
		for i := 0; i < 6; i++ {
			n1 := HexCoord{c.Q + Directions[i].Q, c.R + Directions[i].R}
			n2 := HexCoord{c.Q + Directions[(i+1)%6].Q, c.R + Directions[(i+1)%6].R}
			if _, ok1 := compSet[n1]; ok1 {
				if _, ok2 := compSet[n2]; ok2 {
					return true
				}
			}
		}
	}
	return false
}

// 统计“包含至少一个紧密三角形”的连通块数量（每个块最多计 1）
func countTriangleBlocks(b *Board, side CellState) int {
	visited := make(map[HexCoord]bool)
	count := 0

	for _, start := range b.AllCoords() {
		if visited[start] || b.Get(start) != side {
			continue
		}
		// BFS 收集该分量
		comp := make([]HexCoord, 0, 8)
		st := []HexCoord{start}
		visited[start] = true
		for len(st) > 0 {
			cur := st[len(st)-1]
			st = st[:len(st)-1]
			comp = append(comp, cur)
			for _, nb := range b.Neighbors(cur) {
				// 若 Neighbors 已保证在盘内，inBounds 可省
				if !inBounds(nb.Q, nb.R) {
					continue
				}
				if visited[nb] || b.Get(nb) != side {
					continue
				}
				visited[nb] = true
				st = append(st, nb)
			}
		}
		if len(comp) < 3 {
			continue
		}
		// 建 set 做 O(1) 查询
		compSet := make(map[HexCoord]struct{}, len(comp))
		for _, c := range comp {
			compSet[c] = struct{}{}
		}
		if hasTightTriangle(comp, compSet) {
			count++
		}
	}
	return count
}

//	func evaluateStatic(b *Board, player CellState) int {
//		op := Opponent(player)
//		return b.CountPieces(player) - b.CountPieces(op)*3
//	}
func evaluateStatic(b *Board, player CellState) int {
	op := Opponent(player)

	// —— 可调权重 —— //
	const (
		pieceW          = 5
		edgeW           = 6
		triW            = 6
		cloneInfW       = 6 // 本步克隆感染权重
		jumpInfW        = 2 // 本步跳越感染权重
		weakJumpPenalty = 500
	)

	// 基础统计
	coords := b.AllCoords()
	myCnt, opCnt := 0, 0
	for _, c := range coords {
		switch b.Get(c) {
		case player:
			myCnt++
		case op:
			opCnt++
		}
	}

	pieceScore := (myCnt - opCnt) * pieceW

	myEdge := 0
	for _, c := range coords {
		if b.Get(c) == player && isOuter(c, b.radius) {
			myEdge++
		}
	}
	edgeScore := myEdge * edgeW

	// 仅三角加分
	myTri := countTriangleBlocks(b, player)
	opTri := countTriangleBlocks(b, op)
	triangleScore := (myTri - opTri) * triW

	// 弱跳越重罚（沿用你原先逻辑）
	weakJumpScore := 0
	if b.LastMove.IsJump() {
		mover := b.Get(b.LastMove.To)
		if mover == PlayerA || mover == PlayerB {
			sameAdj := 0
			for _, d := range cloneDirs {
				nb := HexCoord{b.LastMove.To.Q + d.Q, b.LastMove.To.R + d.R}
				if b.Get(nb) == mover {
					sameAdj++
				}
			}
			if sameAdj <= 1 {
				if mover == player {
					weakJumpScore -= weakJumpPenalty
					//} else {
					//	weakJumpScore += weakJumpPenalty
				}
			}
		}
	}

	// ★ 本步真实感染得分（不看下一手潜力）
	infNowScore := 0
	if b.LastInfect > 0 && (b.LastMover == PlayerA || b.LastMover == PlayerB) {
		w := jumpInfW
		if b.LastMove.IsClone() {
			w = cloneInfW
		}
		if b.LastMover == player {
			infNowScore += b.LastInfect * w
		} else {
			infNowScore -= b.LastInfect * w
		}
	}

	// —— 汇总 —— //
	return pieceScore +
		edgeScore +
		triangleScore +
		weakJumpScore +
		infNowScore
}

// “预览”一次感染数，而不实际修改棋盘
func previewInfectedCount(b *Board, mv Move, player CellState) int {
	count := 0
	for _, dir := range Directions {
		nb := mv.To.Add(dir)
		if b.Get(nb) == Opponent(player) {
			count++
		}
	}
	return count
}
func addHex(a, b HexCoord) HexCoord { return HexCoord{Q: a.Q + b.Q, R: a.R + b.R} }

// Predict 改为调用 CNN 的 value，失败则回退到静态评估
//func Predict(b *Board, player CellState) int {
//	if _, v, err := CNNPredict(b, player); err == nil {
//		// value ∈ (-1,1) → 映射到分数区间（可按你原有量级调）
//		fmt.Printf("模型错误 %v \n", err)
//		return int(math.Round(float64(v) * 100.0))
//	}
//	// 回退：原 evaluateStatic
//	return evaluateStatic(b, player)
//}
