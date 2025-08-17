// internal/game/encode.go
package game

const (
	GridSize  = 9 // 把半径4六角映射到9×9
	PlaneCnt  = 3 // [我方, 对方, Blocked]
	TensorLen = PlaneCnt * GridSize * GridSize
)

// EncodeBoardTensor 把棋盘即时编码成 [243]float32 张量
func EncodeBoardTensor(b *Board, me CellState) [TensorLen]float32 {
	var t [TensorLen]float32
	for q := -4; q <= 4; q++ {
		for r := -4; r <= 4; r++ {
			c := HexCoord{Q: q, R: r}
			x, y := q+4, r+4 // 0..8
			if x < 0 || x >= GridSize || y < 0 || y >= GridSize {
				continue
			}
			idx := y*GridSize + x // 0..80
			switch b.Get(c) {
			case me:
				t[idx] = 1 // plane 0
			case Opponent(me):
				t[GridSize*GridSize+idx] = 1 // plane 1
			case Blocked:
				t[2*GridSize*GridSize+idx] = 1 // plane 2
			}
		}
	}
	return t
}

// AxialToIndex 把落子坐标映射到 0..80 的 move 索引
func AxialToIndex(c HexCoord) int { return (c.R+4)*GridSize + (c.Q + 4) }
