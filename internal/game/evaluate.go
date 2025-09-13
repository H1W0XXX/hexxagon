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

func isOuter(c HexCoord, _ int) bool {
	idx, ok := IndexOf[c] // 你已有的“坐标 -> 下标”映射
	if !ok {
		return false
	}
	return isOuterI[idx]
}

//func outerRingCoords(b *Board) []HexCoord {
//	var ring []HexCoord
//	for _, c := range b.AllCoords() {
//		if isOuter(c, b.radius) && b.Get(c) != Blocked {
//			ring = append(ring, c)
//		}
//	}
//	return ring
//}

//func compSizeAt(b *Board, start HexCoord, color CellState) int {
//	if b.Get(start) != color {
//		return 0
//	}
//	visited := make(map[HexCoord]bool, 16)
//	stack := []HexCoord{start}
//	visited[start] = true
//	size := 0
//	for len(stack) > 0 {
//		cur := stack[len(stack)-1]
//		stack = stack[:len(stack)-1]
//		size++
//		for _, d := range cloneDirs {
//			nb := HexCoord{cur.Q + d.Q, cur.R + d.R}
//			if !visited[nb] && b.Get(nb) == color {
//				visited[nb] = true
//				stack = append(stack, nb)
//			}
//		}
//	}
//	return size
//}

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
	visited := make([]bool, BoardN)
	count := 0

	for i := 0; i < BoardN; i++ {
		if visited[i] || b.Cells[i] != side {
			continue
		}
		// —— BFS 收集该连通分量（同色、按 6 邻接）——
		comp := make([]int, 0, 8)
		stack := []int{i}
		visited[i] = true

		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			comp = append(comp, cur)

			for _, nb := range NeighI[cur] {
				if visited[nb] || b.Cells[nb] != side {
					continue
				}
				visited[nb] = true
				stack = append(stack, nb)
			}
		}

		if len(comp) < 3 {
			continue
		}

		// —— 建一个该分量内的布尔集合，O(1) 判定 ——
		inComp := make([]bool, BoardN)
		for _, v := range comp {
			inComp[v] = true
		}

		// —— 是否存在“紧三角” ——
		if hasTightTriangleI(comp, inComp) {
			count++
		}
	}
	return count
}

// 任意三点两两相邻即视为“紧三角”
func hasTightTriangleI(comp []int, inComp []bool) bool {
	for _, a := range comp {
		// 枚举 a 的分量内邻居 b
		for _, b := range NeighI[a] {
			if !inComp[b] || b == a {
				continue
			}
			// 在 a 的邻居里再找一个 c，要求 c 在分量内、且 b 与 c 也相邻
			for _, c := range NeighI[a] {
				if !inComp[c] || c == a || c == b {
					continue
				}
				if isNeighborI(b, c) {
					return true
				}
			}
		}
	}
	return false
}

//	func evaluateStatic(b *Board, player CellState) int {
//		op := Opponent(player)
//		return b.CountPieces(player) - b.CountPieces(op)*3
//	}

const (
	pieceW    = 10 // 子数差
	edgeW     = 2  // 外圈差
	triW      = 15 // “紧三角”差
	mobilityW = 1  // 机动性（去重后的可走空位数）差
	supportW  = 2  // 弱支撑惩罚（同色邻居≤1 的子数）差
)

func mobilityCount(b *Board, side CellState) int {
	vis := make([]bool, BoardN)
	cnt := 0
	for i := 0; i < BoardN; i++ {
		if b.Cells[i] != side {
			continue
		}
		for _, nb := range NeighI[i] {
			if b.Cells[nb] == Empty && !vis[nb] {
				vis[nb] = true
				cnt++
			}
		}
		for _, j := range JumpI[i] {
			if b.Cells[j] == Empty && !vis[j] {
				vis[j] = true
				cnt++
			}
		}
	}
	return cnt
}

func weakSupportCount(b *Board, side CellState) int {
	bad := 0
	for i := 0; i < BoardN; i++ {
		if b.Cells[i] != side {
			continue
		}
		same := 0
		for _, nb := range NeighI[i] {
			if b.Cells[nb] == side {
				same++
			}
		}
		if same <= 1 {
			bad++
		}
	}
	return bad
}

func evaluateStatic(b *Board, player CellState) int {
	op := Opponent(player)

	// 子数差
	myCnt, opCnt := 0, 0
	for i := 0; i < BoardN; i++ {
		if b.Cells[i] == player {
			myCnt++
		}
		if b.Cells[i] == op {
			opCnt++
		}
	}
	pieceScore := (myCnt - opCnt) * pieceW

	// 外圈差（差值！而不是只加我方）
	myEdge, opEdge := 0, 0
	for i := 0; i < BoardN; i++ {
		if !isOuterI[i] {
			continue
		}
		if b.Cells[i] == player {
			myEdge++
		}
		if b.Cells[i] == op {
			opEdge++
		}
	}
	edgeScore := (myEdge - opEdge) * edgeW

	// 紧三角差（你已有的 countTriangleBlocks）
	myTri := countTriangleBlocks(b, player)
	opTri := countTriangleBlocks(b, op)
	triangleScore := (myTri - opTri) * triW

	// 弱支撑差：我方“同色邻居≤1”的子越多越糟
	//myWeak := weakSupportCount(b, player)
	//opWeak := weakSupportCount(b, op)
	//supportScore := (opWeak - myWeak) * supportW // 惩我方=负，惩对手=正

	return pieceScore + edgeScore + triangleScore
}

// “预览”一次感染数，而不实际修改棋盘
func previewInfectedCount(b *Board, mv Move, player CellState) int {
	to, ok := IndexOf[mv.To]
	if !ok {
		return 0
	}
	opp := Opponent(player)
	count := 0
	for _, nb := range NeighI[to] {
		if b.Cells[nb] == opp {
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

// 小工具：判断两个下标是否相邻（查 a 的 6 邻居表）
func isNeighborI(a, b int) bool {
	for _, x := range NeighI[a] {
		if x == b {
			return true
		}
	}
	return false
}
