package main

// «Оптимальне» зчитування матриці модулів: службові шаблони не читаємо
// (їх домальовує сам зчитувач за версією), знімаємо XOR-маску, збираємо
// кодові слова в логічному порядку блоків і беремо лише байти даних —
// без кодів корекції, заголовка й заповнювачів. Корекція помилок не
// виконується: зчитувач розрахований на чисті растрові зображення кодів,
// а не на фотографії.

import (
	"errors"
	"fmt"
)

// DecodeMatrix повертає корисні байти з матриці модулів size×size.
func DecodeMatrix(modules []uint8, size int, noEcc bool) ([]byte, *Code, error) {
	if size < 21 || (size-17)%4 != 0 || len(modules) != size*size {
		return nil, nil, fmt.Errorf("неправильний розмір матриці: %d", size)
	}
	v := (size - 17) / 4
	if v > 40 {
		return nil, nil, fmt.Errorf("версія %d поза діапазоном", v)
	}
	// біти формату з верхнього лівого кута
	at := func(x, y int) int { return int(modules[y*size+x]) }
	bits := 0
	for i := 0; i <= 5; i++ {
		bits |= at(8, i) << i
	}
	bits |= at(8, 7) << 6
	bits |= at(8, 8) << 7
	bits |= at(7, 8) << 8
	for i := 9; i < 15; i++ {
		bits |= at(14-i, 8) << i
	}
	data := (bits ^ 0x5412) >> 10
	ecl, ok := eclByFormatBits[data>>3]
	if !ok {
		return nil, nil, errors.New("не вдалося прочитати біти формату")
	}
	mask := data & 7

	c := &Code{Version: v, Size: size, ECL: ecl, NoEcc: noEcc, Mask: mask, Modules: make([]uint8, size*size), IsFunc: make([]uint8, size*size)}
	c.drawFunctionPatterns(mask)
	c.Order = c.placementOrder()
	c.TotalCodewords = numRawDataModules(v) / 8
	c.DataCodewords = numDataCodewords(v, ecl, noEcc)
	if noEcc {
		c.CwMap = make([]int32, c.TotalCodewords)
		for i := range c.CwMap {
			c.CwMap[i] = int32(i)
		}
	} else {
		c.CwMap = interleaveMap(v, ecl)
	}
	// кодові слова в логічному порядку
	logical := make([]byte, c.TotalCodewords)
	for k := 0; k < c.TotalCodewords; k++ {
		var b byte
		for j := 0; j < 8; j++ {
			idx := int(c.Order[k*8+j])
			x, y := idx%size, idx/size
			bit := modules[idx]
			if maskBit(mask, x, y) {
				bit ^= 1
			}
			b = b<<1 | bit
		}
		logical[c.CwMap[k]] = b
	}
	copy(c.Modules, modules)
	// заголовок байтового сегмента
	br := bitReader{data: logical[:c.DataCodewords]}
	mode := br.read(4)
	if mode != 4 {
		return nil, c, fmt.Errorf("очікував байтовий режим (0100), отримав %04b", mode)
	}
	n := br.read(headerBits(v) - 4)
	if n < 0 || n*8 > br.remaining() {
		return nil, c, fmt.Errorf("довжина %d не вміщується в кодові слова даних", n)
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(br.read(8))
	}
	c.PayloadLen = n
	return out, c, nil
}

type bitReader struct {
	data []byte
	pos  int
}

func (r *bitReader) remaining() int { return len(r.data)*8 - r.pos }

func (r *bitReader) read(n int) int {
	v := 0
	for i := 0; i < n; i++ {
		if r.pos >= len(r.data)*8 {
			return -1
		}
		v = v<<1 | int((r.data[r.pos>>3]>>(7-uint(r.pos&7)))&1)
		r.pos++
	}
	return v
}
