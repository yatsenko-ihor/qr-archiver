package main

import (
	"bytes"
	"math/rand"
	"reflect"
	"testing"
)

// ---------- таблиці та місткість ----------

func TestNumRawDataModules(t *testing.T) {
	// відомі значення зі стандарту ISO/IEC 18004
	want := map[int]int{1: 208, 2: 359, 3: 567, 4: 807, 5: 1079, 6: 1383, 7: 1568, 8: 1936, 9: 2336, 10: 2768, 20: 8683, 30: 17483, 40: 29648}
	for v, w := range want {
		if got := numRawDataModules(v); got != w {
			t.Errorf("numRawDataModules(%d) = %d, очікував %d", v, got, w)
		}
	}
}

func TestNumDataCodewords(t *testing.T) {
	cases := []struct {
		v, ecl int
		noEcc  bool
		want   int
	}{
		{1, eclL, false, 19}, {1, eclM, false, 16}, {1, eclQ, false, 13}, {1, eclH, false, 9},
		{40, eclL, false, 2956}, {40, eclH, false, 1276},
		{1, eclL, true, 26}, {40, eclL, true, 3706},
	}
	for _, c := range cases {
		if got := numDataCodewords(c.v, c.ecl, c.noEcc); got != c.want {
			t.Errorf("numDataCodewords(%d,%d,%v) = %d, очікував %d", c.v, c.ecl, c.noEcc, got, c.want)
		}
	}
}

func TestHeaderBits(t *testing.T) {
	if headerBits(9) != 12 || headerBits(10) != 20 || headerBits(1) != 12 || headerBits(40) != 20 {
		t.Fatal("headerBits: 4 біти режиму + 8 біт довжини до версії 9, + 16 біт від версії 10")
	}
}

func TestCapacityStandard(t *testing.T) {
	// байтова місткість рівня L за таблицею стандарту
	want := map[int]int{1: 17, 2: 32, 3: 53, 4: 78, 5: 106, 6: 134, 7: 154, 8: 192, 9: 230, 10: 271, 20: 858, 30: 1732, 40: 2953}
	for v, w := range want {
		if got := Capacity(v, false); got != w {
			t.Errorf("Capacity(%d) = %d, очікував %d", v, got, w)
		}
	}
}

func TestCapacityNoEcc(t *testing.T) {
	if got := Capacity(40, true); got != 3703 {
		t.Errorf("Capacity(40, noEcc) = %d, очікував 3703", got)
	}
	// поле довжини 8 біт у версіях 1–9: не більше 255 байт
	for v := 1; v <= 9; v++ {
		if Capacity(v, true) > 255 {
			t.Errorf("Capacity(%d, noEcc) = %d перевищує 255", v, Capacity(v, true))
		}
	}
	if Capacity(9, true) != 255 || Capacity(7, true) != 194 {
		t.Errorf("v9 без корекції має впертися в 255, v7 — 194, отримав %d і %d", Capacity(9, true), Capacity(7, true))
	}
	for v := 1; v <= 40; v++ {
		if Capacity(v, true) < Capacity(v, false) {
			t.Errorf("без корекції місткість версії %d менша, ніж із корекцією", v)
		}
	}
}

func TestChooseVersion(t *testing.T) {
	cases := []struct {
		total int
		noEcc bool
		want  int
	}{
		{0, false, 1}, {17, false, 1}, {18, false, 2}, {2953, false, 40},
		{2954, false, 31},                  // 2 коди: найменша версія з місткістю ≥ 1477 → v27? ні: перевіряємо нижче формулою
		{3590, false, 31},                  // як на сторінці: 2 коди версії 31 (1840 ≥ 1795)
		{3703, true, 40}, {3704, true, -1}, // -1: перевіряємо лише властивості нижче
	}
	for _, c := range cases {
		got := ChooseVersion(c.total, c.noEcc)
		if c.total == 2954 {
			// n=2, потрібно cap(v) ≥ 1477: v27 = 1465 замало, v28 = 1528
			c.want = 28
		}
		if c.want >= 0 && got != c.want {
			t.Errorf("ChooseVersion(%d,%v) = %d, очікував %d", c.total, c.noEcc, got, c.want)
		}
		n := (c.total + Capacity(40, c.noEcc) - 1) / Capacity(40, c.noEcc)
		if n < 1 {
			n = 1
		}
		if Capacity(got, c.noEcc)*n < c.total {
			t.Errorf("ChooseVersion(%d): версія %d не вміщує дані у %d кодів", c.total, got, n)
		}
		if got > 1 && Capacity(got-1, c.noEcc)*n >= c.total {
			t.Errorf("ChooseVersion(%d): версія %d не мінімальна", c.total, got)
		}
	}
}

// ---------- GF(2^8) і Рід–Соломон ----------

func TestGfMul(t *testing.T) {
	if gfMul(0, 77) != 0 || gfMul(77, 1) != 77 || gfMul(1, 200) != 200 {
		t.Fatal("нейтральний і нульовий елементи")
	}
	if gfMul(2, 0x80) != 0x1D {
		t.Fatalf("2·x^7 має дати редукцію за поліномом 0x11D: 0x1D, отримав %#x", gfMul(2, 0x80))
	}
	rnd := rand.New(rand.NewSource(5))
	for i := 0; i < 1000; i++ {
		a, b, c := rnd.Intn(256), rnd.Intn(256), rnd.Intn(256)
		if gfMul(a, b) != gfMul(b, a) {
			t.Fatalf("комутативність: %d·%d", a, b)
		}
		if gfMul(a, b^c) != gfMul(a, b)^gfMul(a, c) {
			t.Fatalf("дистрибутивність: %d·(%d^%d)", a, b, c)
		}
		if gfMul(gfMul(a, b), c) != gfMul(a, gfMul(b, c)) {
			t.Fatalf("асоціативність: %d,%d,%d", a, b, c)
		}
	}
	// α = 2 породжує всю мультиплікативну групу: період 255
	x, period := 1, 0
	for {
		x = gfMul(x, 2)
		period++
		if x == 1 {
			break
		}
	}
	if period != 255 {
		t.Fatalf("період елемента 2 = %d, очікував 255", period)
	}
}

func TestRsDivisor(t *testing.T) {
	// генератор для 7 кодових слів корекції (версія 1-L) з таблиць стандарту
	want := []int{127, 122, 154, 164, 11, 68, 117}
	if got := rsDivisor(7); !reflect.DeepEqual(got, want) {
		t.Fatalf("rsDivisor(7) = %v, очікував %v", got, want)
	}
	if got := rsDivisor(1); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("rsDivisor(1) = %v, очікував [1] (x + α^0)", got)
	}
	for _, d := range []int{10, 13, 30} {
		if len(rsDivisor(d)) != d {
			t.Fatalf("rsDivisor(%d) має %d коефіцієнтів", d, len(rsDivisor(d)))
		}
	}
}

func TestRsRemainderKnownVector(t *testing.T) {
	// «HELLO WORLD», версія 1-M: 16 кодових слів даних, 10 корекції (приклад зі стандарту)
	data := []int{32, 91, 11, 120, 209, 114, 220, 77, 67, 64, 236, 17, 236, 17, 236, 17}
	want := []int{196, 35, 39, 119, 235, 215, 231, 226, 93, 23}
	if got := rsRemainder(data, rsDivisor(10)); !reflect.DeepEqual(got, want) {
		t.Fatalf("rsRemainder = %v, очікував %v", got, want)
	}
	if got := rsRemainder(nil, rsDivisor(4)); !reflect.DeepEqual(got, []int{0, 0, 0, 0}) {
		t.Fatalf("остача від порожніх даних має бути нульовою, отримав %v", got)
	}
}

// ---------- розкладка блоків ----------

func TestLayoutFor(t *testing.T) {
	L := layoutFor(40, eclL)
	if L.numBlocks != 25 || L.eccLen != 30 || L.dataLen != 2956 || L.numShort != 19 || L.shortLen != 148 {
		t.Fatalf("розкладка 40-L: %+v", L)
	}
	L1 := layoutFor(1, eclL)
	if L1.numBlocks != 1 || L1.eccLen != 7 || L1.dataLen != 19 || L1.shortLen != 26 {
		t.Fatalf("розкладка 1-L: %+v", L1)
	}
	for v := 1; v <= 40; v++ {
		for ecl := 0; ecl < 4; ecl++ {
			L := layoutFor(v, ecl)
			raw := numRawDataModules(v) / 8
			long := L.numBlocks - L.numShort
			if L.numShort*L.shortLen+long*(L.shortLen+1) != raw {
				t.Fatalf("v%d ecl%d: блоки не складають %d кодових слів", v, ecl, raw)
			}
		}
	}
}

func TestInterleaveMap(t *testing.T) {
	// один блок — тотожна перестановка
	m1 := interleaveMap(1, eclL)
	for i, x := range m1 {
		if int(x) != i {
			t.Fatalf("v1: map[%d] = %d", i, x)
		}
	}
	// 6-L: два блоки по 68 даних + 18 корекції; у порядку розміщення чергуються блоки
	m6 := interleaveMap(6, eclL)
	if len(m6) != 172 || m6[0] != 0 || m6[1] != 68 || m6[2] != 1 || m6[3] != 69 || m6[136] != 136 || m6[137] != 136+18 {
		t.Fatalf("v6-L: %v…", m6[:6])
	}
	// для всіх версій і рівнів: перестановка; дані йдуть перед корекцією в логічному порядку
	for v := 1; v <= 40; v++ {
		for ecl := 0; ecl < 4; ecl++ {
			m := interleaveMap(v, ecl)
			raw := numRawDataModules(v) / 8
			if len(m) != raw {
				t.Fatalf("v%d ecl%d: довжина %d, очікував %d", v, ecl, len(m), raw)
			}
			seen := make([]bool, raw)
			for _, x := range m {
				if x < 0 || int(x) >= raw || seen[x] {
					t.Fatalf("v%d ecl%d: не перестановка (повтор або вихід за межі %d)", v, ecl, x)
				}
				seen[x] = true
			}
			dataLen := numDataCodewords(v, ecl, false)
			// перші numBlocks*(shortLen-eccLen) позицій розміщення — лише дані
			L := layoutFor(v, ecl)
			for k := 0; k < L.numBlocks*(L.shortLen-L.eccLen); k++ {
				if int(m[k]) >= dataLen {
					t.Fatalf("v%d ecl%d: позиція %d має бути даними, а вказує на %d", v, ecl, k, m[k])
				}
			}
			// останні numBlocks*eccLen позицій — лише корекція
			for k := raw - L.numBlocks*L.eccLen; k < raw; k++ {
				if int(m[k]) < dataLen {
					t.Fatalf("v%d ecl%d: позиція %d має бути корекцією", v, ecl, k)
				}
			}
		}
	}
}

func TestAddEcc(t *testing.T) {
	// один блок: результат = дані + остача
	data := bytes.Repeat([]byte{0x12, 0x34}, 10)[:19]
	out := addEcc(data, 1, eclL)
	if len(out) != 26 || !bytes.Equal(out[:19], data) {
		t.Fatalf("1-L: довжина %d, префікс не збігається", len(out))
	}
	ints := make([]int, 19)
	for i, b := range data {
		ints[i] = int(b)
	}
	for i, e := range rsRemainder(ints, rsDivisor(7)) {
		if int(out[19+i]) != e {
			t.Fatalf("1-L: корекція не збігається з rsRemainder")
		}
	}
	// 40-L: 3706 кодових слів; деінтерлівінг через interleaveMap повертає дані на місце
	d40 := make([]byte, 2956)
	rand.New(rand.NewSource(9)).Read(d40)
	out40 := addEcc(d40, 40, eclL)
	if len(out40) != 3706 {
		t.Fatalf("40-L: %d кодових слів", len(out40))
	}
	logical := make([]byte, 3706)
	for k, lg := range interleaveMap(40, eclL) {
		logical[lg] = out40[k]
	}
	if !bytes.Equal(logical[:2956], d40) {
		t.Fatal("40-L: після деінтерлівінгу дані не збігаються")
	}
}

// ---------- маски та службові шаблони ----------

func TestMaskBit(t *testing.T) {
	// визначення масок зі стандарту для кількох точок
	type pt struct {
		m, x, y int
		want    bool
	}
	for _, p := range []pt{
		{0, 0, 0, true}, {0, 1, 0, false}, {0, 1, 1, true},
		{1, 5, 0, true}, {1, 5, 1, false},
		{2, 3, 7, true}, {2, 4, 7, false},
		{3, 1, 2, true}, {3, 1, 1, false},
		{4, 2, 1, true}, {4, 3, 1, false}, {4, 3, 2, true},
		{5, 0, 0, true}, {5, 1, 1, false}, {5, 6, 1, true},
		{6, 0, 5, true}, {6, 1, 1, true}, {6, 1, 3, false},
		{7, 0, 0, true}, {7, 1, 0, false}, {7, 3, 0, false},
	} {
		if got := maskBit(p.m, p.x, p.y); got != p.want {
			t.Errorf("maskBit(%d, %d, %d) = %v, очікував %v", p.m, p.x, p.y, got, p.want)
		}
	}
	// маска 0 — шахівниця: сусіди завжди протилежні
	for y := 0; y < 10; y++ {
		for x := 0; x < 9; x++ {
			if maskBit(0, x, y) == maskBit(0, x+1, y) {
				t.Fatal("маска 0 має бути шахівницею")
			}
		}
	}
}

func TestAlignPositions(t *testing.T) {
	want := map[int][]int{1: nil, 2: {6, 18}, 7: {6, 22, 38}, 14: {6, 26, 46, 66}, 32: {6, 34, 60, 86, 112, 138}, 40: {6, 30, 58, 86, 114, 142, 170}}
	for v, w := range want {
		if got := alignPositions(v); !reflect.DeepEqual(got, w) {
			t.Errorf("alignPositions(%d) = %v, очікував %v", v, got, w)
		}
	}
}

func newBlank(v int) *Code {
	size := v*4 + 17
	return &Code{Version: v, Size: size, ECL: eclL, Modules: make([]uint8, size*size), IsFunc: make([]uint8, size*size)}
}

func TestDrawFunctionPatterns(t *testing.T) {
	for v := 1; v <= 40; v++ {
		c := newBlank(v)
		c.drawFunctionPatterns(0)
		cnt := 0
		for _, f := range c.IsFunc {
			cnt += int(f)
		}
		if want := c.Size*c.Size - numRawDataModules(v); cnt != want {
			t.Fatalf("v%d: службових модулів %d, очікував %d", v, cnt, want)
		}
		at := func(x, y int) uint8 { return c.Modules[y*c.Size+x] }
		// пошуковий шаблон: 7×7 темна рамка, біле кільце, темне ядро 3×3
		if at(0, 0) != 1 || at(1, 1) != 0 || at(2, 2) != 1 || at(3, 3) != 1 || at(6, 6) != 1 || at(7, 7) != 0 {
			t.Fatalf("v%d: пошуковий шаблон у лівому верхньому куті не той", v)
		}
		if at(c.Size-1, 0) != 1 || at(0, c.Size-1) != 1 || at(c.Size-8, 7) != 0 {
			t.Fatalf("v%d: інші пошукові шаблони або роздільники не ті", v)
		}
		// синхродоріжки чергуються, починаючи з темного на парних
		for i := 8; i < c.Size-8; i++ {
			if at(6, i) != uint8(1-i%2) || at(i, 6) != uint8(1-i%2) {
				t.Fatalf("v%d: синхродоріжка не чергується в %d", v, i)
			}
		}
		if at(8, c.Size-8) != 1 {
			t.Fatalf("v%d: темний модуль відсутній", v)
		}
		// вирівнювальні мітки: центр темний, кільце навколо світле
		for _, ax := range alignPositions(v) {
			for _, ay := range alignPositions(v) {
				if c.IsFunc[ay*c.Size+ax] == 0 {
					continue
				}
				if (ax < 9 && ay < 9) || (ax > c.Size-10 && ay < 9) || (ax < 9 && ay > c.Size-10) {
					continue // перекриття з пошуковими — їх там немає
				}
				if at(ax, ay) != 1 || at(ax+1, ay) != 0 || at(ax+2, ay) != 1 {
					t.Fatalf("v%d: вирівнювальна мітка в (%d,%d) не та", v, ax, ay)
				}
			}
		}
	}
}

func readFormat(c *Code) int {
	at := func(x, y int) int { return int(c.Modules[y*c.Size+x]) }
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
	return bits
}

func TestDrawFormatBits(t *testing.T) {
	c := newBlank(1)
	c.drawFormatBits(0)
	if got := readFormat(c); got != 0x77C4 { // L, маска 0: 111011111000100 за таблицею стандарту
		t.Fatalf("формат L/0 = %015b, очікував 111011111000100", got)
	}
	c.ECL = eclM
	c.drawFormatBits(0)
	if got := readFormat(c); got != 0x5412 { // M, маска 0: дані нульові, лишається лише XOR-маска формату
		t.Fatalf("формат M/0 = %#x, очікував 0x5412", got)
	}
	// друга копія бітів формату (біля інших пошукових шаблонів) дорівнює першій
	c.ECL = eclL
	for m := 0; m < 8; m++ {
		c.drawFormatBits(m)
		first := readFormat(c)
		at := func(x, y int) int { return int(c.Modules[y*c.Size+x]) }
		second := 0
		for i := 0; i < 8; i++ {
			second |= at(c.Size-1-i, 8) << i
		}
		for i := 8; i < 15; i++ {
			second |= at(8, c.Size-15+i) << i
		}
		if first != second {
			t.Fatalf("маска %d: копії бітів формату різняться: %015b і %015b", m, first, second)
		}
		data := (first ^ 0x5412) >> 10
		if data&7 != m || data>>3 != formatBitsFor[eclL] {
			t.Fatalf("маска %d: зворотне читання дало маску %d, рівень %d", m, data&7, data>>3)
		}
	}
}

func TestPlacementOrder(t *testing.T) {
	for v := 1; v <= 40; v++ {
		c := newBlank(v)
		c.drawFunctionPatterns(0)
		order := c.placementOrder()
		if len(order) != numRawDataModules(v) {
			t.Fatalf("v%d: %d модулів у порядку розміщення, очікував %d", v, len(order), numRawDataModules(v))
		}
		s := c.Size
		// починається з правого нижнього кута і йде парами стовпчиків угору
		want := []int32{int32((s-1)*s + s - 1), int32((s-1)*s + s - 2), int32((s-2)*s + s - 1), int32((s-2)*s + s - 2)}
		if !reflect.DeepEqual(order[:4], want) {
			t.Fatalf("v%d: початок порядку %v, очікував %v", v, order[:4], want)
		}
		seen := make(map[int32]bool, len(order))
		for _, idx := range order {
			if c.IsFunc[idx] == 1 || seen[idx] {
				t.Fatalf("v%d: у порядку розміщення службовий або повторний модуль %d", v, idx)
			}
			seen[idx] = true
		}
	}
}

// ---------- кодування ----------

func TestEncodeErrors(t *testing.T) {
	if _, err := Encode([]byte{1}, 0, false); err == nil {
		t.Fatal("версія 0 має бути помилкою")
	}
	if _, err := Encode([]byte{1}, 41, false); err == nil {
		t.Fatal("версія 41 має бути помилкою")
	}
	if _, err := Encode(make([]byte, 18), 1, false); err == nil {
		t.Fatal("18 байт у версію 1 не вміщуються")
	}
	if _, err := Encode(make([]byte, 17), 1, false); err != nil {
		t.Fatalf("17 байт у версію 1 мають уміститись: %v", err)
	}
	if _, err := Encode(make([]byte, 3703), 40, true); err != nil {
		t.Fatalf("3703 байти без корекції мають уміститись: %v", err)
	}
	if _, err := Encode(make([]byte, 3704), 40, true); err == nil {
		t.Fatal("3704 байти без корекції не вміщуються")
	}
}

func TestEncodeStructure(t *testing.T) {
	p := []byte("Слава Україні")
	c, err := Encode(p, 5, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 5 || c.Size != 37 || c.ECL != eclL || c.NoEcc || c.PayloadLen != len(p) {
		t.Fatalf("поля коду: %+v", c)
	}
	if len(c.Modules) != 37*37 || len(c.IsFunc) != 37*37 || len(c.Order) != numRawDataModules(5) || len(c.CwMap) != 134 {
		t.Fatal("розміри масивів коду")
	}
	if c.DataCodewords != 108 || c.TotalCodewords != 134 {
		t.Fatalf("кодових слів %d даних і %d усього, очікував 108 і 134", c.DataCodewords, c.TotalCodewords)
	}
	if c.Mask < 0 || c.Mask > 7 {
		t.Fatalf("маска %d", c.Mask)
	}
	// біти формату відповідають обраній масці
	if data := (readFormat(c) ^ 0x5412) >> 10; data&7 != c.Mask {
		t.Fatalf("у бітах формату маска %d, у коді %d", data&7, c.Mask)
	}
	// детермінованість
	c2, _ := Encode(p, 5, false)
	if !bytes.Equal(c.Modules, c2.Modules) || c.Mask != c2.Mask {
		t.Fatal("повторне кодування дало інший код")
	}
}

func TestEncodeNoEccStructure(t *testing.T) {
	p := make([]byte, 3703)
	rand.New(rand.NewSource(2)).Read(p)
	c, err := Encode(p, 40, true)
	if err != nil {
		t.Fatal(err)
	}
	if !c.NoEcc || c.DataCodewords != 3706 || c.TotalCodewords != 3706 {
		t.Fatalf("без корекції всі 3706 кодових слів мають бути даними: %d/%d", c.DataCodewords, c.TotalCodewords)
	}
	for i, x := range c.CwMap {
		if int(x) != i {
			t.Fatal("без корекції карта деінтерлівінгу має бути тотожною")
		}
	}
	// біти формату й далі стандартні (кажуть «рівень L»)
	if data := (readFormat(c) ^ 0x5412) >> 10; data>>3 != formatBitsFor[eclL] {
		t.Fatal("біти формату мають лишатися стандартними")
	}
	// перші 20 бітів після зняття маски: режим 0100 і довжина 3703
	bits := 0
	for k := 0; k < 20; k++ {
		idx := int(c.Order[k])
		b := int(c.Modules[idx])
		if maskBit(c.Mask, idx%c.Size, idx/c.Size) {
			b ^= 1
		}
		bits = bits<<1 | b
	}
	if bits>>16 != 4 || bits&0xFFFF != 3703 {
		t.Fatalf("заголовок сегмента: режим %d, довжина %d", bits>>16, bits&0xFFFF)
	}
}

func TestApplyMaskInvolution(t *testing.T) {
	c, _ := Encode([]byte("abc"), 2, false)
	before := append([]uint8(nil), c.Modules...)
	for m := 0; m < 8; m++ {
		c.applyMask(m)
		if bytes.Equal(before, c.Modules) {
			t.Fatalf("маска %d нічого не змінила", m)
		}
		c.applyMask(m)
		if !bytes.Equal(before, c.Modules) {
			t.Fatalf("маска %d, застосована двічі, має повертати код", m)
		}
	}
}

func TestPenaltyBlank(t *testing.T) {
	// повністю світла матриця 21×21:
	// N1: 42 ліній × (3 + 16) = 798; N2: 20×20 блоків × 3 = 1200; N3: 0; N4: 0 % темних → k=9 → 90
	c := newBlank(1)
	if got := c.penalty(); got != 798+1200+90 {
		t.Fatalf("штраф порожньої матриці %d, очікував %d", got, 798+1200+90)
	}
	// ідеальна шахівниця: N1 = 0, N2 = 0, N3 = 0, N4: 221 темних з 441 → k = 0
	for y := 0; y < 21; y++ {
		for x := 0; x < 21; x++ {
			c.Modules[y*21+x] = uint8((x + y) % 2)
		}
	}
	if got := c.penalty(); got != 0 {
		t.Fatalf("штраф шахівниці %d, очікував 0", got)
	}
	// шаблон 1011101 з чотирма світлими поруч додає 40 за кожну лінію, де трапляється
	d := newBlank(1)
	row := []uint8{0, 0, 0, 0, 1, 0, 1, 1, 1, 0, 1}
	copy(d.Modules[0:], row)
	blank := newBlank(1).penalty()
	got := d.penalty()
	if got <= blank-100 || got >= blank+100 {
		// точне значення залежить від зміни прогонів і блоків; перевіряємо, що N3 спрацював
		t.Logf("штраф із пошуковим шаблоном: %d (порожня: %d)", got, blank)
	}
	if n3 := d.penalty() - penaltyWithoutN3(d); n3 != 40 {
		t.Fatalf("N3 має додати рівно 40 за один шаблон у рядку, додав %d", n3)
	}
}

// penaltyWithoutN3 — той самий штраф без члена N3, щоб ізолювати його в тесті.
func penaltyWithoutN3(c *Code) int {
	size := c.Size
	get := func(x, y int) uint8 { return c.Modules[y*size+x] }
	res := 0
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
	res += ((absi(dark*20-tot*10)+tot-1)/tot - 1) * 10
	return res
}

func TestHelpers(t *testing.T) {
	if absi(-3) != 3 || absi(3) != 3 || maxi(2, 5) != 5 || maxi(7, 5) != 7 {
		t.Fatal("absi/maxi")
	}
}
