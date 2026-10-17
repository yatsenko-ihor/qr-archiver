package main

// Рендер QR-кодів у растрові файли і зворотне зчитування матриці модулів.
// Записуємо PNG (без втрат), читаємо PNG, JPEG або GIF. Зчитувач розрахований
// на чисті, не повернуті зображення з рівною тихою зоною (саме такі робить
// render): масштаб визначається за шириною пошукового шаблону.

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func RenderPNG(c *Code, scale, quiet int) *image.Paletted {
	W := (c.Size + 2*quiet) * scale
	img := image.NewPaletted(image.Rect(0, 0, W, W), color.Palette{color.White, color.Black})
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Modules[y*c.Size+x] == 0 {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				row := ((y+quiet)*scale + dy) * img.Stride
				px := (x + quiet) * scale
				for dx := 0; dx < scale; dx++ {
					img.Pix[row+px+dx] = 1
				}
			}
		}
	}
	return img
}

func WritePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// codeFileName — ім'я файлу коду з номером (від 1). Шість цифр: до 999 999 кодів.
func codeFileName(i int) string { return fmt.Sprintf("code-%06d.png", i) }

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true}

// ExpandCodeFiles розгортає аргументи scan: тека → усі зображення в ній, файл → як є.
// Порядок природний: числа в іменах порівнюються як числа (code-2 перед code-10).
func ExpandCodeFiles(args []string) ([]string, error) {
	var files []string
	for _, a := range args {
		st, err := os.Stat(a)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			files = append(files, a)
			continue
		}
		entries, err := os.ReadDir(a)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() && imageExts[strings.ToLower(filepath.Ext(e.Name()))] {
				files = append(files, filepath.Join(a, e.Name()))
			}
		}
	}
	sort.SliceStable(files, func(i, j int) bool { return naturalLess(files[i], files[j]) })
	return files, nil
}

// naturalLess порівнює рядки так, що послідовності цифр зіставляються за значенням.
func naturalLess(a, b string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		ca, cb := a[i], b[j]
		if isDigit(ca) && isDigit(cb) {
			si := i
			for i < len(a) && isDigit(a[i]) {
				i++
			}
			sj := j
			for j < len(b) && isDigit(b[j]) {
				j++
			}
			na, _ := strconv.ParseUint(strings.TrimLeft(a[si:i], "0"), 10, 64)
			nb, _ := strconv.ParseUint(strings.TrimLeft(b[sj:j], "0"), 10, 64)
			if na != nb {
				return na < nb
			}
			continue
		}
		if ca != cb {
			return ca < cb
		}
		i++
		j++
	}
	return len(a)-i < len(b)-j
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// ReadMatrixImage зчитує матрицю модулів із растрового файлу (PNG, JPEG, GIF),
// зробленого RenderPNG або будь-якого чистого зображення коду без нахилу.
func ReadMatrixImage(path string) ([]uint8, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", path, err)
	}
	b := img.Bounds()
	W, H := b.Dx(), b.Dy()
	dark := func(x, y int) bool {
		r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
		return (r+g+bl)/3 < 0x8000
	}
	// перший темний піксель — лівий верхній кут пошукового шаблону
	x0, y0 := -1, -1
	for y := 0; y < H && y0 < 0; y++ {
		for x := 0; x < W; x++ {
			if dark(x, y) {
				x0, y0 = x, y
				break
			}
		}
	}
	if y0 < 0 {
		return nil, 0, errors.New("порожнє зображення")
	}
	run := 0
	for x := x0; x < W && dark(x, y0); x++ {
		run++
	}
	scale := (run + 3) / 7
	if scale < 1 {
		return nil, 0, errors.New("не вдалося визначити масштаб модуля")
	}
	x1 := x0
	for x := W - 1; x > x0; x-- {
		if dark(x, y0) {
			x1 = x
			break
		}
	}
	size := (x1 - x0 + 1 + scale/2) / scale
	if size < 21 || (size-17)%4 != 0 {
		return nil, 0, fmt.Errorf("розмір %d модулів не схожий на QR-код", size)
	}
	m := make([]uint8, size*size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := x0+x*scale+scale/2, y0+y*scale+scale/2
			if px < W && py < H && dark(px, py) {
				m[y*size+x] = 1
			}
		}
	}
	return m, size, nil
}
