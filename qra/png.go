package main

// Рендер QR-кодів у PNG і зворотне зчитування матриці модулів із таких PNG.
// Зчитувач розрахований на чисті, не повернуті зображення з рівною тихою зоною
// (саме такі робить render): масштаб визначається за шириною пошукового шаблону.

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
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

// ReadMatrixPNG зчитує матрицю модулів з PNG, зробленого RenderPNG (або будь-якого
// чистого растрового зображення коду без нахилу).
func ReadMatrixPNG(path string) ([]uint8, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	img, err := png.Decode(f)
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
