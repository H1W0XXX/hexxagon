// internal/nnue/nnue.go (简化单线程 FP32)
package nnue

import (
	"encoding/binary"
	"math"
	"os"
)

type Net struct {
	l1w, l2w, valw []float32
	l1b, l2b, valb []float32
	inDim          int
}

func Load(path string) (*Net, error) {
	f, _ := os.Open(path)
	defer f.Close()
	var hdr [4]int32
	binary.Read(f, binary.LittleEndian, &hdr)
	in := int(hdr[0])
	net := &Net{inDim: in}
	read := func(n int) []float32 {
		buf := make([]float32, n)
		binary.Read(f, binary.LittleEndian, buf)
		return buf
	}
	net.l1w = read(in * 512)
	net.l1b = read(512)
	net.l2w = read(512 * 64)
	net.l2b = read(64)
	net.valw = read(64)
	net.valb = read(1)
	return net, nil
}

func (n *Net) Eval(inp []float32) float32 {
	// FC1
	h1 := make([]float32, 512)
	for o := 0; o < 512; o++ {
		s := n.l1b[o]
		for i := 0; i < n.inDim; i++ {
			s += inp[i] * n.l1w[o*n.inDim+i]
		}
		if s < 0 {
			s = 0
		}
		h1[o] = s
	}
	// FC2
	h2 := make([]float32, 64)
	for o := 0; o < 64; o++ {
		s := n.l2b[o]
		for i := 0; i < 512; i++ {
			s += h1[i] * n.l2w[o*512+i]
		}
		if s < 0 {
			s = 0
		}
		h2[o] = s
	}
	// Val
	val := n.valb[0]
	for i := 0; i < 64; i++ {
		val += h2[i] * n.valw[i]
	}
	return float32(math.Tanh(float64(val))) * 32000.0
}
