// internal/game/policy_prune.go
package game

import (
	"math"
	"sort"
)

// 开关 & 策略参数（可以按需微调）
var (
	// 总开关：只要 true，就在根节点用 CNN policy 先验修剪
	policyPruneEnabled = true

	// 保留比例 + 下限/上限：keep = clamp(minKeep, int(len(moves)*keepRatio), maxKeep)
	policyKeepRatio = 0.6
	policyMinKeep   = 6
	policyMaxKeep   = 8
)

// 覆盖率阈值（基础值）；当熵高时会提高该阈值
var policyCoverBase = 0.90
var policyEntropyHigh = 2.2 // 熵阈值（经验），高于它认为不确定
var policyCoverHigh = 0.96  // 不确定时更高的覆盖率
var policyTemp = 1.1        // softmax 温度（>1 更平，<1 更尖）

// 9x9 平面 index （不引入 ml 包，避免 import cycle）
func toIndex9(b *Board, c HexCoord) int {
	grid := 2*b.radius + 1 // radius=4 -> grid=9
	return (c.R+b.radius)*grid + (c.Q + b.radius)
}

// 计算即时感染数（不改盘）
func instantInfect(b *Board, mv Move, side CellState) int {
	cnt := 0
	toIdx, ok := IndexOf[mv.To]
	if !ok {
		return 0 // 非法坐标
	}
	opp := Opponent(side)
	for _, nb := range NeighI[toIdx] {
		if b.Cells[nb] == opp {
			cnt++
		}
	}
	return cnt
}

func policyPruneRoot(b *Board, player CellState, moves []Move) []Move {
	if !policyPruneEnabled || len(moves) <= policyMinKeep {
		return moves
	}

	logits, _, err := KataPolicyValue(b, player) // policy 已经 softmax & 掩蔽，len=82(含pass)
	if err != nil || len(logits) < 81 {
		return moves // 推理失败就不动
	}

type rec struct {
	mv    Move
	p     float64
	inf   int
}
recs := make([]rec, 0, len(moves))

	// 先收集每个合法走法的概率与“即时感染数”
	for _, m := range moves {
		idx := toIndex9(b, m.To)
		p := 0.0
		if idx >= 0 && idx < len(logits) {
			p = float64(logits[idx])
		}
		recs = append(recs, rec{
			mv:    m,
			p:     p,
			inf:   instantInfect(b, m, player),
		})
	}
	// 归一化（保险起见）
	var sum float64
	for _, r := range recs {
		sum += r.p
	}
	if sum > 0 {
		inv := 1.0 / sum
		for i := range recs {
			recs[i].p *= inv
		}
	} else {
		// 退化：全等概率
		u := 1.0 / float64(len(recs))
		for i := range recs {
			recs[i].p = u
		}
	}

	// 计算熵，决定覆盖率阈值自适应
	var entropy float64
	for _, r := range recs {
		if r.p > 0 {
			entropy -= r.p * math.Log(r.p+1e-12)
		}
	}
	coverTarget := policyCoverBase
	if entropy >= policyEntropyHigh {
		coverTarget = policyCoverHigh
	}

	// 先按概率从大到小排
	sort.Slice(recs, func(i, j int) bool { return recs[i].p > recs[j].p })

	// 先构建白名单集合：即时感染>=3 的都保留
	// 另外保证至少保留一手克隆 & 一手跳跃
	keepSet := make(map[Move]struct{}, len(recs))
	hasClone, hasJump := false, false
	for _, r := range recs {
		if r.inf >= 3 {
			keepSet[r.mv] = struct{}{}
			if r.mv.IsClone() {
				hasClone = true
			} else {
				hasJump = true
			}
		}
	}
	// 如果白名单里没有克隆/跳跃，各补一个概率最高的
	if !hasClone {
		for _, r := range recs {
			if r.mv.IsClone() {
				keepSet[r.mv] = struct{}{}
				break
			}
		}
	}
	if !hasJump {
		for _, r := range recs {
			if r.mv.IsJump() {
				keepSet[r.mv] = struct{}{}
				break
			}
		}
	}

	// 然后从高到低累加概率，直到覆盖率达标
	cum := 0.0
	for _, r := range recs {
		if _, ok := keepSet[r.mv]; ok {
			cum += r.p
			continue
		}
		if cum >= coverTarget {
			break
		}
		keepSet[r.mv] = struct{}{}
		cum += r.p
	}

	// 应用 min/max/ratio 限制
	kept := make([]rec, 0, len(recs))
	for _, r := range recs {
		if _, ok := keepSet[r.mv]; ok {
			kept = append(kept, r)
		}
	}
	// 如果太少/太多，按参数夹紧
	want := int(float64(len(moves)) * policyKeepRatio)
	if want < policyMinKeep {
		want = policyMinKeep
	}
	if want > policyMaxKeep {
		want = policyMaxKeep
	}
	if want < 1 {
		want = 1
	}
	if want > len(recs) {
		want = len(recs)
	}
	// 如果 kept 少于 want，则从剩余中按概率继续补满；多于 want 则截断
	if len(kept) < want {
		used := make(map[Move]struct{}, len(kept))
		for _, r := range kept {
			used[r.mv] = struct{}{}
		}
		for _, r := range recs {
			if len(kept) >= want {
				break
			}
			if _, ok := used[r.mv]; !ok {
				kept = append(kept, r)
			}
		}
	} else if len(kept) > want {
		kept = kept[:want]
	}

	// 输出：先修剪，再排序
	out := make([]rec, 0, len(kept))
	for _, r := range kept {
		out = append(out, r)
	}

	// 按 policy 概率从大到小排，概率相同则按启发式次序
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].p != out[j].p {
			return out[i].p > out[j].p
		}
		// tie-break：克隆优先，其次感染数高的优先
		if out[i].mv.IsClone() != out[j].mv.IsClone() {
			return out[i].mv.IsClone()
		}
		return out[i].inf > out[j].inf
	})

	// 提取出 Move 切片
	final := make([]Move, len(out))
	for i := range out {
		final[i] = out[i].mv
	}
	return final
}
