package main

// Компактні форми QR-кодів без графіки.
//
// bits (.qrb): бінарний контейнер, у якому кожен код — це його матриця модулів,
// упакована по 8 модулів у байт, рядок за рядком. Код версії 40 (177×177 = 31 329
// модулів) займає 3 917 байт замість ~10–15 КБ у PNG; службові шаблони теж є,
// тому це повноцінний QR-код, лише не намальований.
//
// text (.qrt): той самий вміст як текст, по рядку на код: версія і base64
// упакованих модулів. Можна вставити в лист чи чат.

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	bitsMagic = "QRB1"
	textMagic = "QRT1"
)

// packModules упаковує матрицю size×size по 8 модулів у байт (старший біт перший).
func packModules(modules []uint8, size int) []byte {
	out := make([]byte, (size*size+7)/8)
	for i, m := range modules {
		if m != 0 {
			out[i>>3] |= 0x80 >> uint(i&7)
		}
	}
	return out
}

func unpackModules(packed []byte, size int) ([]uint8, error) {
	n := size * size
	if len(packed) < (n+7)/8 {
		return nil, fmt.Errorf("замало байтів для матриці %d×%d", size, size)
	}
	m := make([]uint8, n)
	for i := 0; i < n; i++ {
		m[i] = (packed[i>>3] >> (7 - uint(i&7))) & 1
	}
	return m, nil
}

// ---- bits (.qrb) ----

// BitsWriter пише контейнер .qrb послідовно: заголовок одразу, коди по мірі надходження.
type BitsWriter struct {
	w io.Writer
	n int
}

func NewBitsWriter(w io.Writer, count int, noEcc bool) (*BitsWriter, error) {
	h := make([]byte, 0, 10)
	h = append(h, bitsMagic...)
	var flags byte
	if noEcc {
		flags |= flagNoEcc
	}
	h = append(h, 1, flags)
	h = binary.LittleEndian.AppendUint32(h, uint32(count))
	if _, err := w.Write(h); err != nil {
		return nil, err
	}
	return &BitsWriter{w: w}, nil
}

func (b *BitsWriter) WriteCode(c *Code) error {
	rec := []byte{byte(c.Version), byte(c.Mask), 0, 0}
	rec = append(rec, packModules(c.Modules, c.Size)...)
	_, err := b.w.Write(rec)
	b.n++
	return err
}

// BitsCode — один код із контейнера .qrb.
type BitsCode struct {
	Version, Mask int
	Modules       []uint8
	Size          int
}

// ReadBits читає весь контейнер .qrb.
func ReadBits(data []byte) ([]BitsCode, bool, error) {
	if len(data) < 10 || string(data[:4]) != bitsMagic {
		return nil, false, errors.New("це не файл кодів .qrb: немає сигнатури QRB1")
	}
	if data[4] != 1 {
		return nil, false, fmt.Errorf("непідтримувана версія формату .qrb %d", data[4])
	}
	noEcc := data[5]&flagNoEcc != 0
	count := int(binary.LittleEndian.Uint32(data[6:]))
	p := 10
	codes := make([]BitsCode, 0, count)
	for i := 0; i < count; i++ {
		if len(data) < p+4 {
			return nil, noEcc, fmt.Errorf("обірваний запис коду %d", i+1)
		}
		v := int(data[p])
		mask := int(data[p+1])
		if v < 1 || v > 40 {
			return nil, noEcc, fmt.Errorf("код %d: версія %d поза діапазоном", i+1, v)
		}
		size := v*4 + 17
		need := (size*size + 7) / 8
		p += 4
		if len(data) < p+need {
			return nil, noEcc, fmt.Errorf("код %d: обірвана матриця", i+1)
		}
		m, err := unpackModules(data[p:p+need], size)
		if err != nil {
			return nil, noEcc, err
		}
		p += need
		codes = append(codes, BitsCode{Version: v, Mask: mask, Modules: m, Size: size})
	}
	return codes, noEcc, nil
}

// ---- text (.qrt) ----

type TextWriter struct {
	w *bufio.Writer
}

func NewTextWriter(w io.Writer, count int, noEcc bool) (*TextWriter, error) {
	bw := bufio.NewWriter(w)
	ne := 0
	if noEcc {
		ne = 1
	}
	if _, err := fmt.Fprintf(bw, "%s count=%d noecc=%d\n", textMagic, count, ne); err != nil {
		return nil, err
	}
	return &TextWriter{w: bw}, nil
}

func (t *TextWriter) WriteCode(c *Code) error {
	_, err := fmt.Fprintf(t.w, "v%d m%d %s\n", c.Version, c.Mask, base64.StdEncoding.EncodeToString(packModules(c.Modules, c.Size)))
	return err
}

func (t *TextWriter) Flush() error { return t.w.Flush() }

// ReadText читає текстовий контейнер .qrt.
func ReadText(data []byte) ([]BitsCode, bool, error) {
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	if !sc.Scan() || !strings.HasPrefix(sc.Text(), textMagic+" ") {
		return nil, false, errors.New("це не текстовий файл кодів .qrt: немає сигнатури QRT1")
	}
	noEcc := strings.Contains(sc.Text(), "noecc=1")
	var codes []BitsCode
	for line := 1; sc.Scan(); line++ {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) != 3 || !strings.HasPrefix(f[0], "v") || !strings.HasPrefix(f[1], "m") {
			return nil, noEcc, fmt.Errorf("рядок %d: очікував «vN mK base64»", line+1)
		}
		v, err1 := strconv.Atoi(f[0][1:])
		mask, err2 := strconv.Atoi(f[1][1:])
		raw, err3 := base64.StdEncoding.DecodeString(f[2])
		if err1 != nil || err2 != nil || err3 != nil || v < 1 || v > 40 {
			return nil, noEcc, fmt.Errorf("рядок %d: не вдалося розібрати код", line+1)
		}
		size := v*4 + 17
		m, err := unpackModules(raw, size)
		if err != nil {
			return nil, noEcc, fmt.Errorf("рядок %d: %w", line+1, err)
		}
		codes = append(codes, BitsCode{Version: v, Mask: mask, Modules: m, Size: size})
	}
	return codes, noEcc, sc.Err()
}

// ReadCodesFile розпізнає .qrb або .qrt за вмістом.
func ReadCodesFile(path string) ([]BitsCode, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	switch {
	case len(data) >= 4 && string(data[:4]) == bitsMagic:
		return ReadBits(data)
	case len(data) >= 4 && string(data[:4]) == textMagic:
		return ReadText(data)
	}
	return nil, false, fmt.Errorf("%s: не .qrb і не .qrt", path)
}
