package main

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestDecodeMatrixAllVersions(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	for _, noEcc := range []bool{false, true} {
		for v := 1; v <= 40; v++ {
			cap := Capacity(v, noEcc)
			for _, n := range []int{0, 1, cap / 2, cap} {
				payload := make([]byte, n)
				rnd.Read(payload)
				c, err := Encode(payload, v, noEcc)
				if err != nil {
					t.Fatalf("v%d n=%d noEcc=%v: %v", v, n, noEcc, err)
				}
				got, meta, err := DecodeMatrix(c.Modules, c.Size, noEcc)
				if err != nil {
					t.Fatalf("v%d n=%d noEcc=%v decode: %v", v, n, noEcc, err)
				}
				if !bytes.Equal(got, payload) {
					t.Fatalf("v%d n=%d noEcc=%v: байти не збігаються", v, n, noEcc)
				}
				if meta.Version != v || meta.Mask != c.Mask || meta.ECL != eclL || meta.PayloadLen != n || meta.DataCodewords != c.DataCodewords {
					t.Fatalf("v%d: метадані зчитування %+v не збігаються з кодом", v, meta)
				}
			}
		}
	}
}

func TestDecodeMatrixAllMasks(t *testing.T) {
	// змушуємо кожну з 8 масок: беремо код, перемасковуємо вручну і переписуємо біти формату
	p := []byte("mask test payload")
	base, _ := Encode(p, 4, false)
	for m := 0; m < 8; m++ {
		c := &Code{Version: 4, Size: base.Size, ECL: eclL, Modules: append([]uint8(nil), base.Modules...), IsFunc: base.IsFunc, Order: base.Order}
		c.applyMask(base.Mask) // зняти поточну
		c.applyMask(m)         // накласти потрібну
		c.drawFormatBits(m)
		got, meta, err := DecodeMatrix(c.Modules, c.Size, false)
		if err != nil || !bytes.Equal(got, p) || meta.Mask != m {
			t.Fatalf("маска %d: err=%v, маска прочитана як %d", m, err, meta.Mask)
		}
	}
}

func TestDecodeMatrixOtherLevels(t *testing.T) {
	// зчитувач читає рівень з бітів формату: кодуємо вручну з M/Q/H через addEcc і перевіряємо
	for _, ecl := range []int{eclM, eclQ, eclH} {
		v := 3
		dataLen := numDataCodewords(v, ecl, false)
		payload := bytes.Repeat([]byte{0xA5}, dataLen-3)
		// будуємо сегмент вручну
		bb := []uint8{}
		push := func(val, n int) {
			for i := n - 1; i >= 0; i-- {
				bb = append(bb, uint8((val>>i)&1))
			}
		}
		push(4, 4)
		push(len(payload), 8)
		for _, b := range payload {
			push(int(b), 8)
		}
		push(0, 4)
		for len(bb) < dataLen*8 {
			push(0xEC, 8)
			if len(bb) < dataLen*8 {
				push(0x11, 8)
			}
		}
		bb = bb[:dataLen*8]
		data := make([]byte, dataLen)
		for i, b := range bb {
			data[i>>3] |= b << (7 - uint(i&7))
		}
		all := addEcc(data, v, ecl)
		c := &Code{Version: v, Size: v*4 + 17, ECL: ecl, Modules: make([]uint8, (v*4+17)*(v*4+17)), IsFunc: make([]uint8, (v*4+17)*(v*4+17))}
		c.drawFunctionPatterns(0)
		c.Order = c.placementOrder()
		for k, idx := range c.Order {
			if k < len(all)*8 {
				c.Modules[idx] = (all[k>>3] >> (7 - uint(k&7))) & 1
			}
		}
		c.applyMask(3)
		c.drawFormatBits(3)
		got, meta, err := DecodeMatrix(c.Modules, c.Size, false)
		if err != nil {
			t.Fatalf("рівень %d: %v", ecl, err)
		}
		if meta.ECL != ecl || !bytes.Equal(got, payload) {
			t.Fatalf("рівень %d прочитано як %d, байти збігаються: %v", ecl, meta.ECL, bytes.Equal(got, payload))
		}
	}
}

func TestDecodeMatrixErrors(t *testing.T) {
	if _, _, err := DecodeMatrix(make([]uint8, 20*20), 20, false); err == nil {
		t.Fatal("розмір 20 не є розміром QR")
	}
	if _, _, err := DecodeMatrix(make([]uint8, 10), 21, false); err == nil {
		t.Fatal("довжина масиву не відповідає розміру")
	}
	if _, _, err := DecodeMatrix(make([]uint8, 181*181), 181, false); err == nil {
		t.Fatal("версія 41 поза діапазоном")
	}
	// зіпсовані біти формату: невідомий рівень не виникає (усі 4 комбінації валідні), але
	// зіпсований режим сегмента має давати помилку
	c, _ := Encode([]byte("x"), 1, false)
	m := append([]uint8(nil), c.Modules...)
	idx := c.Order[0] // старший біт режиму (0 у 0100)
	m[idx] ^= 1
	if _, _, err := DecodeMatrix(m, c.Size, false); err == nil {
		t.Fatal("зіпсований індикатор режиму має давати помилку")
	}
	// завелика довжина: ставимо біти довжини в 255 для версії 1 (місткість 17)
	m = append([]uint8(nil), c.Modules...)
	for k := 4; k < 12; k++ {
		idx := int(c.Order[k])
		cur := m[idx]
		if maskBit(c.Mask, idx%c.Size, idx/c.Size) {
			cur ^= 1
		}
		if cur == 0 {
			m[idx] ^= 1
		}
	}
	if _, _, err := DecodeMatrix(m, c.Size, false); err == nil {
		t.Fatal("довжина 255 у версії 1 має давати помилку")
	}
}

func TestDecodeMatrixNoErrorCorrection(t *testing.T) {
	// свідоме обмеження: один перевернутий модуль даних змінює байти, бо корекція не зчитується
	p := []byte("0123456789")
	c, _ := Encode(p, 2, false)
	m := append([]uint8(nil), c.Modules...)
	m[c.Order[20]] ^= 1 // усередині першого байта даних
	got, _, err := DecodeMatrix(m, c.Size, false)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(got, p) {
		t.Fatal("зчитувач не виправляє помилок, тож результат мав би відрізнятися")
	}
	// а перевернутий модуль у зоні корекції не впливає зовсім
	m = append([]uint8(nil), c.Modules...)
	m[c.Order[c.DataCodewords*8+5]] ^= 1
	got, _, _ = DecodeMatrix(m, c.Size, false)
	if !bytes.Equal(got, p) {
		t.Fatal("модулі корекції не зчитуються, зміна там не має впливати")
	}
}

func TestBitReader(t *testing.T) {
	r := bitReader{data: []byte{0b10110011, 0b01000000}}
	if r.remaining() != 16 {
		t.Fatal("remaining")
	}
	if r.read(3) != 0b101 || r.read(5) != 0b10011 || r.read(2) != 0b01 {
		t.Fatal("read")
	}
	if r.remaining() != 6 {
		t.Fatalf("remaining після 10 бітів = %d", r.remaining())
	}
	if r.read(6) != 0 || r.read(1) != -1 {
		t.Fatal("читання за кінцем має давати -1")
	}
	z := bitReader{}
	if z.read(0) != 0 || z.read(1) != -1 {
		t.Fatal("порожній читач")
	}
}
