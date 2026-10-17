package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestRenderPNGGeometry(t *testing.T) {
	c, _ := Encode([]byte("render"), 2, false)
	for _, tc := range []struct{ scale, quiet int }{{1, 0}, {3, 4}, {8, 2}} {
		img := RenderPNG(c, tc.scale, tc.quiet)
		W := (c.Size + 2*tc.quiet) * tc.scale
		if img.Bounds().Dx() != W || img.Bounds().Dy() != W {
			t.Fatalf("scale=%d quiet=%d: розмір %v", tc.scale, tc.quiet, img.Bounds())
		}
		// тиха зона світла, кут пошукового шаблону темний, його внутрішнє кільце світле
		if tc.quiet > 0 && img.ColorIndexAt(0, 0) != 0 {
			t.Fatal("тиха зона має бути світлою")
		}
		q := tc.quiet * tc.scale
		if img.ColorIndexAt(q, q) != 1 || img.ColorIndexAt(q+tc.scale, q+tc.scale) != 0 || img.ColorIndexAt(q+2*tc.scale, q+2*tc.scale) != 1 {
			t.Fatalf("scale=%d quiet=%d: пошуковий шаблон намальовано неправильно", tc.scale, tc.quiet)
		}
		// кожен модуль заповнений цілком
		for y := 0; y < c.Size; y++ {
			for x := 0; x < c.Size; x++ {
				want := uint8(c.Modules[y*c.Size+x])
				for dy := 0; dy < tc.scale; dy++ {
					for dx := 0; dx < tc.scale; dx++ {
						if img.ColorIndexAt(q+x*tc.scale+dx, q+y*tc.scale+dy) != want {
							t.Fatalf("модуль (%d,%d) заповнений не цілком", x, y)
						}
					}
				}
			}
		}
	}
}

func TestWriteReadPNGRoundTrip(t *testing.T) {
	dir := t.TempDir()
	for v := range []int{1, 7, 40} {
		v = []int{1, 7, 40}[v]
		p := bytes.Repeat([]byte{byte(v)}, Capacity(v, false))
		c, _ := Encode(p, v, false)
		for _, tc := range []struct{ scale, quiet int }{{1, 0}, {2, 4}, {5, 1}} {
			path := filepath.Join(dir, "c.png")
			if err := WritePNG(path, RenderPNG(c, tc.scale, tc.quiet)); err != nil {
				t.Fatal(err)
			}
			m, size, err := ReadMatrixPNG(path)
			if err != nil {
				t.Fatalf("v%d scale=%d quiet=%d: %v", v, tc.scale, tc.quiet, err)
			}
			if size != c.Size || !bytes.Equal(m, c.Modules) {
				t.Fatalf("v%d scale=%d quiet=%d: матриця після PNG не збігається (розмір %d)", v, tc.scale, tc.quiet, size)
			}
			got, _, err := DecodeMatrix(m, size, false)
			if err != nil || !bytes.Equal(got, p) {
				t.Fatalf("v%d: байти після PNG не збігаються: %v", v, err)
			}
		}
	}
}

func TestReadMatrixPNGErrors(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := ReadMatrixPNG(filepath.Join(dir, "нема.png")); err == nil {
		t.Fatal("відсутній файл")
	}
	notPng := filepath.Join(dir, "x.png")
	os.WriteFile(notPng, []byte("not a png"), 0o644)
	if _, _, err := ReadMatrixPNG(notPng); err == nil {
		t.Fatal("не PNG")
	}
	// повністю біле зображення
	blank := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for i := range blank.Pix {
		blank.Pix[i] = 255
	}
	bp := filepath.Join(dir, "blank.png")
	WritePNG(bp, blank)
	if _, _, err := ReadMatrixPNG(bp); err == nil {
		t.Fatal("порожнє зображення має давати помилку")
	}
	// темний прямокутник без структури QR: розмір не підходить
	box := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			if x >= 10 && x < 80 && y >= 10 && y < 30 {
				box.Set(x, y, color.Black)
			} else {
				box.Set(x, y, color.White)
			}
		}
	}
	bx := filepath.Join(dir, "box.png")
	WritePNG(bx, box)
	if _, _, err := ReadMatrixPNG(bx); err == nil {
		t.Fatal("прямокутник не є QR-кодом")
	}
	// WritePNG у неіснуючу теку
	if err := WritePNG(filepath.Join(dir, "no", "dir", "x.png"), blank); err == nil {
		t.Fatal("запис у неіснуючу теку має давати помилку")
	}
	// PNG з іншим кольором (темно-синій на жовтому) теж читається за порогом яскравості
	c, _ := Encode([]byte("colour"), 1, false)
	pal := RenderPNG(c, 3, 4)
	rgb := image.NewRGBA(pal.Bounds())
	for y := 0; y < rgb.Bounds().Dy(); y++ {
		for x := 0; x < rgb.Bounds().Dx(); x++ {
			if pal.ColorIndexAt(x, y) == 1 {
				rgb.Set(x, y, color.RGBA{0, 0, 90, 255})
			} else {
				rgb.Set(x, y, color.RGBA{255, 230, 120, 255})
			}
		}
	}
	cp := filepath.Join(dir, "colour.png")
	f, _ := os.Create(cp)
	png.Encode(f, rgb)
	f.Close()
	m, size, err := ReadMatrixPNG(cp)
	if err != nil || size != c.Size || !bytes.Equal(m, c.Modules) {
		t.Fatalf("кольоровий код: %v", err)
	}
}
