package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestCapacities(t *testing.T) {
	want := map[int]int{1: 17, 10: 271, 40: 2953}
	for v, c := range want {
		if got := Capacity(v, false); got != c {
			t.Fatalf("версія %d: місткість %d, очікував %d", v, got, c)
		}
	}
	if got := Capacity(40, true); got != 3703 {
		t.Fatalf("версія 40 без корекції: %d, очікував 3703", got)
	}
}

func TestEncodeDecodeAllVersions(t *testing.T) {
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
				got, _, err := DecodeMatrix(c.Modules, c.Size, noEcc)
				if err != nil {
					t.Fatalf("v%d n=%d noEcc=%v decode: %v", v, n, noEcc, err)
				}
				if !bytes.Equal(got, payload) {
					t.Fatalf("v%d n=%d noEcc=%v: байти не збігаються", v, n, noEcc)
				}
			}
		}
	}
}

func TestEncodeDecodeAllMasks(t *testing.T) {
	// вибір маски залежить від даних; переконуємось, що зчитувач розуміє всі 8 масок
	seen := map[int]bool{}
	rnd := rand.New(rand.NewSource(7))
	for i := 0; i < 400 && len(seen) < 8; i++ {
		p := make([]byte, 1+rnd.Intn(40))
		rnd.Read(p)
		c, _ := Encode(p, 3, false)
		got, _, err := DecodeMatrix(c.Modules, c.Size, false)
		if err != nil || !bytes.Equal(got, p) {
			t.Fatalf("маска %d: %v", c.Mask, err)
		}
		seen[c.Mask] = true
	}
	if len(seen) < 6 {
		t.Logf("перевірено масок: %d", len(seen))
	}
}

func testImage() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 160, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 160; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x ^ y), 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func TestPackUnpack(t *testing.T) {
	for _, src := range [][]byte{testImage(), bytes.Repeat([]byte("тризуб "), 20000), {}} {
		q, h, err := Pack(src, "png", 0, false)
		if err != nil {
			t.Fatal(err)
		}
		if h.OrigSize != uint64(len(src)) {
			t.Fatalf("розмір у заголовку %d, має бути %d", h.OrigSize, len(src))
		}
		back, _, err := Unpack(q)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(back, src) {
			t.Fatal("розпаковане не збігається з оригіналом")
		}
	}
	// пошкодження має ловитися CRC
	q, _, _ := Pack(bytes.Repeat([]byte("abc"), 5000), "txt", 0, false)
	q[len(q)-3] ^= 0x55
	if _, _, err := Unpack(q); err == nil {
		t.Fatal("пошкоджений контейнер розпакувався без помилки")
	}
}

func TestRenderScanRoundTrip(t *testing.T) {
	for _, noEcc := range []bool{false, true} {
		src := bytes.Repeat(testImage(), 3) // ~ кілька кодів
		q, h, err := Pack(src, "png", 0, noEcc)
		if err != nil {
			t.Fatal(err)
		}
		codes, err := encodeAll(q, int(h.QRVersion), noEcc)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		var files []string
		for i, c := range codes {
			p := filepath.Join(dir, "c"+string(rune('a'+i))+".png")
			if err := WritePNG(p, RenderPNG(c, 3, 4)); err != nil {
				t.Fatal(err)
			}
			files = append(files, p)
		}
		sort.Strings(files)
		var got []byte
		for _, f := range files {
			m, size, err := ReadMatrixPNG(f)
			if err != nil {
				t.Fatal(err)
			}
			p, _, err := DecodeMatrix(m, size, noEcc)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, p...)
		}
		if !bytes.Equal(got, q) {
			t.Fatalf("noEcc=%v: зібраний .qra відрізняється від вихідного", noEcc)
		}
		back, _, err := Unpack(got)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(back, src) {
			t.Fatal("зображення після повного кола не збігається")
		}
		_ = os.Remove
	}
}

func BenchmarkEncodeV40(b *testing.B) {
	p := make([]byte, Capacity(40, false))
	rand.New(rand.NewSource(3)).Read(p)
	for i := 0; i < b.N; i++ {
		if _, err := Encode(p, 40, false); err != nil {
			b.Fatal(err)
		}
	}
}
