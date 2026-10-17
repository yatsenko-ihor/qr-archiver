package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackUnpackModules(t *testing.T) {
	c, _ := Encode([]byte("modules"), 3, false)
	p := packModules(c.Modules, c.Size)
	if len(p) != (29*29+7)/8 {
		t.Fatalf("упаковано %d байт, очікував %d", len(p), (29*29+7)/8)
	}
	// перший байт — перші 8 модулів верхнього рядка (пошуковий шаблон: 1111111 0)
	if p[0] != 0b11111110 {
		t.Fatalf("перший байт %08b, очікував 11111110", p[0])
	}
	m, err := unpackModules(p, c.Size)
	if err != nil || !bytes.Equal(m, c.Modules) {
		t.Fatalf("розпакування: %v", err)
	}
	if _, err := unpackModules(p[:10], c.Size); err == nil {
		t.Fatal("замало байтів має давати помилку")
	}
}

func TestBitsWriteRead(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewBitsWriter(&buf, 3, true)
	if err != nil {
		t.Fatal(err)
	}
	var codes []*Code
	for i, v := range []int{1, 10, 40} {
		c, _ := Encode(bytes.Repeat([]byte{byte(i + 1)}, Capacity(v, true)), v, true)
		codes = append(codes, c)
		if err := w.WriteCode(c); err != nil {
			t.Fatal(err)
		}
	}
	data := buf.Bytes()
	if string(data[:4]) != "QRB1" || data[4] != 1 || data[5]&flagNoEcc == 0 || binary.LittleEndian.Uint32(data[6:]) != 3 {
		t.Fatalf("заголовок .qrb: % x", data[:10])
	}
	// розмір: заголовок 10 + Σ(4 + ceil(size²/8))
	want := 10
	for _, c := range codes {
		want += 4 + (c.Size*c.Size+7)/8
	}
	if len(data) != want {
		t.Fatalf("довжина %d, очікував %d", len(data), want)
	}
	got, noEcc, err := ReadBits(data)
	if err != nil || !noEcc || len(got) != 3 {
		t.Fatalf("ReadBits: %v, noEcc=%v, n=%d", err, noEcc, len(got))
	}
	for i, c := range codes {
		if got[i].Version != c.Version || got[i].Mask != c.Mask || got[i].Size != c.Size || !bytes.Equal(got[i].Modules, c.Modules) {
			t.Fatalf("код %d після .qrb не збігається", i)
		}
		p, _, err := DecodeMatrix(got[i].Modules, got[i].Size, true)
		if err != nil || len(p) != Capacity(c.Version, true) {
			t.Fatalf("код %d не зчитується з .qrb: %v", i, err)
		}
	}
	// компактність: код версії 40 у .qrb займає 4 + 3917 байт
	if sz := 4 + (177*177+7)/8; sz != 3921 {
		t.Fatalf("розмір запису версії 40 = %d", sz)
	}
}

func TestReadBitsErrors(t *testing.T) {
	var buf bytes.Buffer
	w, _ := NewBitsWriter(&buf, 1, false)
	c, _ := Encode([]byte("x"), 2, false)
	w.WriteCode(c)
	good := buf.Bytes()
	cases := map[string][]byte{
		"порожній":         {},
		"чужа сигнатура":   append([]byte("QRA1"), good[4:]...),
		"версія формату":   append(append([]byte{}, good[:4]...), append([]byte{9}, good[5:]...)...),
		"обірваний запис":  good[:12],
		"обірвана матриця": good[:len(good)-1],
		"версія коду поза": append(append([]byte{}, good[:10]...), append([]byte{99}, good[11:]...)...),
	}
	for name, data := range cases {
		if _, _, err := ReadBits(data); err == nil {
			t.Errorf("%s: очікував помилку", name)
		}
	}
}

func TestTextWriteRead(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewTextWriter(&buf, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	c1, _ := Encode([]byte("перший"), 2, false)
	c2, _ := Encode(bytes.Repeat([]byte{7}, 2953), 40, false)
	w.WriteCode(c1)
	w.WriteCode(c2)
	w.Flush()
	txt := buf.String()
	lines := strings.Split(strings.TrimSpace(txt), "\n")
	if len(lines) != 3 || lines[0] != "QRT1 count=2 noecc=0" || !strings.HasPrefix(lines[1], "v2 m") || !strings.HasPrefix(lines[2], "v40 m") {
		t.Fatalf("текстовий формат: %q…", lines[0])
	}
	got, noEcc, err := ReadText([]byte(txt))
	if err != nil || noEcc || len(got) != 2 {
		t.Fatalf("ReadText: %v", err)
	}
	if !bytes.Equal(got[0].Modules, c1.Modules) || !bytes.Equal(got[1].Modules, c2.Modules) || got[1].Mask != c2.Mask {
		t.Fatal("матриці після тексту не збігаються")
	}
	// порожні рядки ігноруються, зіпсований рядок — помилка
	if _, _, err := ReadText([]byte("QRT1 count=0 noecc=1\n\n")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "QRX1 x\n", "QRT1 count=1 noecc=0\nv2 oops\n", "QRT1 count=1 noecc=0\nv2 m0 !!!\n", "QRT1 count=1 noecc=0\nv2 m0 AAAA\n", "QRT1 count=1 noecc=0\nv77 m0 AAAA\n"} {
		if _, _, err := ReadText([]byte(bad)); err == nil {
			t.Errorf("очікував помилку для %q", bad)
		}
	}
}

func TestReadCodesFile(t *testing.T) {
	dir := t.TempDir()
	c, _ := Encode([]byte("file"), 1, false)
	var b bytes.Buffer
	bw, _ := NewBitsWriter(&b, 1, false)
	bw.WriteCode(c)
	pb := filepath.Join(dir, "codes.qrb")
	os.WriteFile(pb, b.Bytes(), 0o644)
	var tb bytes.Buffer
	tw, _ := NewTextWriter(&tb, 1, false)
	tw.WriteCode(c)
	tw.Flush()
	pt := filepath.Join(dir, "codes.qrt")
	os.WriteFile(pt, tb.Bytes(), 0o644)
	other := filepath.Join(dir, "other.bin")
	os.WriteFile(other, []byte("hello"), 0o644)
	for _, p := range []string{pb, pt} {
		codes, _, err := ReadCodesFile(p)
		if err != nil || len(codes) != 1 || !bytes.Equal(codes[0].Modules, c.Modules) {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if _, _, err := ReadCodesFile(other); err == nil {
		t.Fatal("чужий файл має давати помилку")
	}
	if _, _, err := ReadCodesFile(filepath.Join(dir, "нема")); err == nil {
		t.Fatal("відсутній файл")
	}
}
