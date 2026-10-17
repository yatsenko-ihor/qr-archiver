package main

// QR-кодер: байтовий режим, версії 1–40, рівні корекції L/M/Q/H.
// Той самий алгоритм, що й на HTML-сторінці: Рід–Соломон над GF(2^8),
// інтерлівінг блоків, зиґзаґове розміщення, вибір маски за штрафами.
// Додатково повертає все, що потрібно для «оптимального» зчитування:
// карту службових модулів, порядок розміщення та карту деінтерлівінгу.

import (
	"errors"
	"fmt"
)

const (
	eclL = 0
	eclM = 1
	eclQ = 2
	eclH = 3
)

var formatBitsFor = [4]int{1, 0, 3, 2}
var eclByFormatBits = map[int]int{1: eclL, 0: eclM, 3: eclQ, 2: eclH}

var eccCodewordsPerBlock = [4][41]int{
	{-1, 7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26, 30, 22, 24, 28, 30, 28, 28, 28, 28, 30, 30, 26, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30},
	{-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28},
	{-1, 13, 22, 18, 26, 18, 24, 18, 22, 20, 24, 28, 26, 24, 20, 30, 24, 28, 28, 26, 30, 28, 30, 30, 30, 30, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30},
	{-1, 17, 28, 22, 16, 22, 28, 26, 26, 24, 28, 24, 28, 22, 24, 24, 30, 28, 28, 26, 28, 30, 24, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30},
}

var numErrorCorrectionBlocks = [4][41]int{
	{-1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 4, 4, 6, 6, 6, 6, 7, 8, 8, 9, 9, 10, 12, 12, 12, 13, 14, 15, 16, 17, 18, 19, 19, 20, 21, 22, 24, 25},
	{-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49},
	{-1, 1, 1, 2, 2, 4, 4, 6, 6, 8, 8, 8, 10, 12, 16, 12, 17, 16, 18, 21, 20, 23, 23, 25, 27, 29, 34, 34, 35, 38, 40, 43, 45, 48, 51, 53, 56, 59, 62, 65, 68},
	{-1, 1, 1, 2, 4, 4, 4, 5, 6, 8, 8, 11, 11, 16, 16, 18, 16, 19, 21, 25, 25, 25, 34, 30, 32, 35, 37, 40, 42, 45, 48, 51, 54, 57, 60, 63, 66, 70, 74, 77, 81},
}

// Типи службових модулів (лише для статистики та налагодження).
const (
	ftNone = iota
	ftFinder
	ftSeparator
	ftTiming
	ftAlign
	ftFormat
	ftVersion
	ftDark
)

// Code — згенерований QR-код разом із картами, потрібними для зчитування.
type Code struct {
	Version, Size, Mask int
	ECL                 int
	NoEcc               bool
	Modules             []uint8 // size*size, 1 = темний
	IsFunc              []uint8 // 1 = службовий модуль
	Order               []int32 // індекси неслужбових модулів у порядку розміщення
	CwMap               []int32 // кодове слово в порядку розміщення -> логічний індекс
	DataCodewords       int
	TotalCodewords      int
	PayloadLen          int
}

func numRawDataModules(v int) int {
	r := (16*v+128)*v + 64
	if v >= 2 {
		na := v/7 + 2
		r -= (25*na-10)*na - 55
		if v >= 7 {
			r -= 36
		}
	}
	return r
}

func numDataCodewords(v, ecl int, noEcc bool) int {
	if noEcc {
		return numRawDataModules(v) / 8
	}
	return numRawDataModules(v)/8 - eccCodewordsPerBlock[ecl][v]*numErrorCorrectionBlocks[ecl][v]
}

func headerBits(v int) int {
	if v < 10 {
		return 4 + 8
	}
	return 4 + 16
}

// Capacity — скільки байтів корисних даних уміщує код версії v рівня L.
// Поле довжини байтового сегмента має 8 біт у версіях 1–9, тому понад 255 байт
// туди не записати, навіть якщо без корекції місце є.
func Capacity(v int, noEcc bool) int {
	c := (numDataCodewords(v, eclL, noEcc)*8 - headerBits(v)) / 8
	if v < 10 && c > 255 {
		c = 255
	}
	return c
}

// ChooseVersion — найменша версія, яка вміщує total байтів тією ж кількістю кодів, що й версія 40.
func ChooseVersion(total int, noEcc bool) int {
	n := (total + Capacity(40, noEcc) - 1) / Capacity(40, noEcc)
	if n < 1 {
		n = 1
	}
	for v := 1; v <= 40; v++ {
		if Capacity(v, noEcc)*n >= total {
			return v
		}
	}
	return 40
}

// ---- Рід–Соломон над GF(2^8), поліном 0x11D ----

func gfMul(x, y int) int {
	z := 0
	for i := 7; i >= 0; i-- {
		z = (z << 1) ^ ((z >> 7) * 0x11D)
		z ^= ((y >> i) & 1) * x
	}
	return z & 0xFF
}

func rsDivisor(deg int) []int {
	r := make([]int, deg)
	r[deg-1] = 1
	root := 1
	for i := 0; i < deg; i++ {
		for j := 0; j < deg; j++ {
			r[j] = gfMul(r[j], root)
			if j+1 < deg {
				r[j] ^= r[j+1]
			}
		}
		root = gfMul(root, 2)
	}
	return r
}

func rsRemainder(data []int, div []int) []int {
	r := make([]int, len(div))
	for _, b := range data {
		f := b ^ r[0]
		copy(r, r[1:])
		r[len(r)-1] = 0
		for i, c := range div {
			r[i] ^= gfMul(c, f)
		}
	}
	return r
}

// blockLayout описує розбиття кодових слів на блоки для версії v і рівня ecl.
type blockLayout struct {
	numBlocks, eccLen, numShort, shortLen, dataLen int
}

func layoutFor(v, ecl int) blockLayout {
	nb := numErrorCorrectionBlocks[ecl][v]
	bel := eccCodewordsPerBlock[ecl][v]
	raw := numRawDataModules(v) / 8
	return blockLayout{numBlocks: nb, eccLen: bel, numShort: nb - raw%nb, shortLen: raw / nb, dataLen: raw - nb*bel}
}

// interleaveMap: для кожного кодового слова в порядку розміщення — логічний індекс
// (спершу всі кодові слова даних поспіль, потім корекція блоку 0, блоку 1, …).
func interleaveMap(v, ecl int) []int32 {
	L := layoutFor(v, ecl)
	dataOff := make([]int, L.numBlocks)
	acc := 0
	for j := 0; j < L.numBlocks; j++ {
		dataOff[j] = acc
		if j < L.numShort {
			acc += L.shortLen - L.eccLen
		} else {
			acc += L.shortLen - L.eccLen + 1
		}
	}
	m := make([]int32, 0, numRawDataModules(v)/8)
	for i := 0; i <= L.shortLen; i++ {
		for j := 0; j < L.numBlocks; j++ {
			if i == L.shortLen-L.eccLen && j < L.numShort {
				continue // у коротких блоках ця позиція — заглушка
			}
			dl := L.shortLen - L.eccLen
			if j >= L.numShort {
				dl++
			}
			if i < dl {
				m = append(m, int32(dataOff[j]+i))
			} else {
				m = append(m, int32(L.dataLen+j*L.eccLen+(i-(L.shortLen-L.eccLen+1))))
			}
		}
	}
	return m
}

func addEcc(data []byte, v, ecl int) []byte {
	L := layoutFor(v, ecl)
	div := rsDivisor(L.eccLen)
	blocks := make([][]int, L.numBlocks)
	k := 0
	for i := 0; i < L.numBlocks; i++ {
		dl := L.shortLen - L.eccLen
		if i >= L.numShort {
			dl++
		}
		dat := make([]int, 0, L.shortLen+1)
		for _, b := range data[k : k+dl] {
			dat = append(dat, int(b))
		}
		k += dl
		ecc := rsRemainder(dat, div)
		if i < L.numShort {
			dat = append(dat, 0)
		}
		blocks[i] = append(dat, ecc...)
	}
	out := make([]byte, 0, numRawDataModules(v)/8)
	for i := 0; i < len(blocks[0]); i++ {
		for j, b := range blocks {
			if i != L.shortLen-L.eccLen || j >= L.numShort {
				out = append(out, byte(b[i]))
			}
		}
	}
	return out
}

// ---- Маски ----

func maskBit(m, x, y int) bool {
	switch m {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (x/3+y/2)%2 == 0
	case 5:
		return (x*y)%2+(x*y)%3 == 0
	case 6:
		return ((x*y)%2+(x*y)%3)%2 == 0
	default:
		return ((x+y)%2+(x*y)%3)%2 == 0
	}
}

// ---- Службові шаблони ----

func alignPositions(v int) []int {
	if v == 1 {
		return nil
	}
	na := v/7 + 2
	size := v*4 + 17
	step := 26
	if v != 32 {
		step = ((v*4 + 4) + (na*2 - 2) - 1) / (na*2 - 2) * 2 // ceil((4v+4)/(2na-2))*2
	}
	r := []int{6}
	for pos := size - 7; len(r) < na; pos -= step {
		r = append(r[:1], append([]int{pos}, r[1:]...)...)
	}
	return r
}

func (c *Code) setF(x, y int, dark bool) {
	i := y*c.Size + x
	if dark {
		c.Modules[i] = 1
	} else {
		c.Modules[i] = 0
	}
	c.IsFunc[i] = 1
}

func absi(a int) int {
	if a < 0 {
		return -a
	}
	return a
}
func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// drawFunctionPatterns малює всі службові шаблони й біти формату для маски mask.
func (c *Code) drawFunctionPatterns(mask int) {
	size := c.Size
	for i := 0; i < size; i++ {
		c.setF(6, i, i%2 == 0)
		c.setF(i, 6, i%2 == 0)
	}
	finder := func(cx, cy int) {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				d := maxi(absi(dx), absi(dy))
				x, y := cx+dx, cy+dy
				if x >= 0 && y >= 0 && x < size && y < size {
					c.setF(x, y, d != 2 && d != 4)
				}
			}
		}
	}
	finder(3, 3)
	finder(size-4, 3)
	finder(3, size-4)
	ap := alignPositions(c.Version)
	for i := range ap {
		for j := range ap {
			if (i == 0 && j == 0) || (i == 0 && j == len(ap)-1) || (i == len(ap)-1 && j == 0) {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					c.setF(ap[i]+dx, ap[j]+dy, maxi(absi(dx), absi(dy)) != 1)
				}
			}
		}
	}
	c.drawFormatBits(mask)
	if c.Version >= 7 {
		rem := c.Version
		for i := 0; i < 12; i++ {
			rem = (rem << 1) ^ ((rem >> 11) * 0x1F25)
		}
		bits := c.Version<<12 | rem
		for i := 0; i < 18; i++ {
			b := (bits>>i)&1 == 1
			a, bb := size-11+i%3, i/3
			c.setF(a, bb, b)
			c.setF(bb, a, b)
		}
	}
}

func (c *Code) drawFormatBits(mask int) {
	size := c.Size
	data := formatBitsFor[c.ECL]<<3 | mask
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return (bits>>i)&1 == 1 }
	for i := 0; i <= 5; i++ {
		c.setF(8, i, bit(i))
	}
	c.setF(8, 7, bit(6))
	c.setF(8, 8, bit(7))
	c.setF(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		c.setF(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		c.setF(size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		c.setF(8, size-15+i, bit(i))
	}
	c.setF(8, size-8, true)
}

// placementOrder — усі неслужбові модулі в стандартному зиґзаґовому порядку.
func (c *Code) placementOrder() []int32 {
	size := c.Size
	order := make([]int32, 0, size*size)
	for right := size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < size; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				up := ((right + 1) & 2) == 0
				y := vert
				if up {
					y = size - 1 - vert
				}
				idx := y*size + x
				if c.IsFunc[idx] == 0 {
					order = append(order, int32(idx))
				}
			}
		}
	}
	return order
}

// ---- Кодування ----

// Encode кодує payload у код версії v рівня L. noEcc — експериментальний режим без корекції.
func Encode(payload []byte, v int, noEcc bool) (*Code, error) {
	if v < 1 || v > 40 {
		return nil, errors.New("версія QR має бути від 1 до 40")
	}
	ecl := eclL
	size := v*4 + 17
	dataCap := numDataCodewords(v, ecl, noEcc) * 8
	bb := make([]uint8, 0, dataCap)
	push := func(val, n int) {
		for i := n - 1; i >= 0; i-- {
			bb = append(bb, uint8((val>>i)&1))
		}
	}
	push(4, 4)
	push(len(payload), headerBits(v)-4)
	for _, b := range payload {
		push(int(b), 8)
	}
	if len(bb) > dataCap {
		return nil, fmt.Errorf("%d байт не вміщуються у версію %d (місткість %d)", len(payload), v, Capacity(v, noEcc))
	}
	term := dataCap - len(bb)
	if term > 4 {
		term = 4
	}
	push(0, term)
	push(0, (8-len(bb)%8)%8)
	for pad := 0xEC; len(bb) < dataCap; pad ^= 0xEC ^ 0x11 {
		push(pad, 8)
	}
	dataCw := make([]byte, len(bb)/8)
	for i, b := range bb {
		dataCw[i>>3] |= b << (7 - uint(i&7))
	}
	var allCw []byte
	var cwMap []int32
	if noEcc {
		allCw = dataCw
		cwMap = make([]int32, len(dataCw))
		for i := range cwMap {
			cwMap[i] = int32(i)
		}
	} else {
		allCw = addEcc(dataCw, v, ecl)
		cwMap = interleaveMap(v, ecl)
	}

	c := &Code{Version: v, Size: size, ECL: ecl, NoEcc: noEcc, Modules: make([]uint8, size*size), IsFunc: make([]uint8, size*size),
		CwMap: cwMap, DataCodewords: len(dataCw), TotalCodewords: len(allCw), PayloadLen: len(payload)}
	c.drawFunctionPatterns(0)
	c.Order = c.placementOrder()
	total := len(allCw) * 8
	for k, idx := range c.Order {
		if k < total {
			c.Modules[idx] = (allCw[k>>3] >> (7 - uint(k&7))) & 1
		}
	}
	best, minPen := 0, int(^uint(0)>>1)
	for m := 0; m < 8; m++ {
		c.applyMask(m)
		c.drawFormatBits(m)
		p := c.penalty()
		if p < minPen {
			minPen, best = p, m
		}
		c.applyMask(m)
	}
	c.applyMask(best)
	c.drawFormatBits(best)
	c.Mask = best
	return c, nil
}

func (c *Code) applyMask(m int) {
	size := c.Size
	for _, idx := range c.Order {
		x, y := int(idx)%size, int(idx)/size
		if maskBit(m, x, y) {
			c.Modules[idx] ^= 1
		}
	}
}

func (c *Code) penalty() int {
	size := c.Size
	get := func(x, y int) uint8 { return c.Modules[y*size+x] }
	res := 0
	line := make([]uint8, size)
	for pass := 0; pass < 2; pass++ {
		for a := 0; a < size; a++ {
			run, prev := 0, uint8(255)
			for b := 0; b < size; b++ {
				var val uint8
				if pass == 1 {
					val = get(a, b)
				} else {
					val = get(b, a)
				}
				line[b] = val
				if val == prev {
					run++
					if run == 5 {
						res += 3
					} else if run > 5 {
						res++
					}
				} else {
					prev, run = val, 1
				}
			}
			for b := 0; b <= size-7; b++ {
				if line[b] == 1 && line[b+1] == 0 && line[b+2] == 1 && line[b+3] == 1 && line[b+4] == 1 && line[b+5] == 0 && line[b+6] == 1 {
					before, after := true, true
					for q := 1; q <= 4; q++ {
						if b-q >= 0 && line[b-q] == 1 {
							before = false
						}
						if b+6+q < size && line[b+6+q] == 1 {
							after = false
						}
					}
					if before || after {
						res += 40
					}
				}
			}
		}
	}
	for y := 0; y < size-1; y++ {
		for x := 0; x < size-1; x++ {
			v := get(x, y)
			if v == get(x+1, y) && v == get(x, y+1) && v == get(x+1, y+1) {
				res += 3
			}
		}
	}
	dark := 0
	for _, m := range c.Modules {
		dark += int(m)
	}
	tot := size * size
	k := (absi(dark*20-tot*10)+tot-1)/tot - 1
	res += k * 10
	return res
}
