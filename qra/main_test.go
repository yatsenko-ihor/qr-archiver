package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

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

func TestHumanAndCodesFor(t *testing.T) {
	if human(999) != "999 Б" || !strings.HasPrefix(human(2048), "2048 Б (2.00 КіБ)") || !strings.HasPrefix(human(3<<20), "3145728 Б (3.00 МіБ)") {
		t.Fatalf("human: %q %q %q", human(999), human(2048), human(3<<20))
	}
	if codesFor(0, 40, false) != 0 || codesFor(1, 40, false) != 1 || codesFor(2953, 40, false) != 1 || codesFor(2954, 40, false) != 2 || codesFor(3703, 40, true) != 1 {
		t.Fatal("codesFor")
	}
}

func TestEncodeAllParallel(t *testing.T) {
	data := bytes.Repeat(testImage(), 4)
	v := 40
	codes, err := encodeAll(data, v, false)
	if err != nil {
		t.Fatal(err)
	}
	cap := Capacity(v, false)
	if want := (len(data) + cap - 1) / cap; len(codes) != want {
		t.Fatalf("кодів %d, очікував %d", len(codes), want)
	}
	// кожен код містить свій шматок у правильному порядку
	var got []byte
	for i, c := range codes {
		p, _, err := DecodeMatrix(c.Modules, c.Size, false)
		if err != nil {
			t.Fatalf("код %d: %v", i, err)
		}
		got = append(got, p...)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("склеєні шматки не дорівнюють вихідним даним")
	}
	// порожні дані → один код
	codes, err = encodeAll(nil, 1, false)
	if err != nil || len(codes) != 1 || codes[0].PayloadLen != 0 {
		t.Fatalf("порожні дані: %d кодів, %v", len(codes), err)
	}
	// помилка кодування прокидається нагору (версія 0)
	if _, err := encodeAll([]byte{1}, 0, false); err == nil {
		t.Fatal("версія 0 має давати помилку")
	}
}

// Повне коло через команди CLI у тимчасовій теці.
func TestCommandsRoundTrip(t *testing.T) {
	for _, noEcc := range []bool{false, true} {
		dir := t.TempDir()
		src := filepath.Join(dir, "картинка.png")
		img := bytes.Repeat(testImage(), 2)
		os.WriteFile(src, img, 0o644)
		qra := filepath.Join(dir, "a.qra")
		packArgs := []string{src, qra}
		if noEcc {
			packArgs = append([]string{"-no-ecc"}, packArgs...)
		}
		if err := cmdPack(packArgs); err != nil {
			t.Fatal(err)
		}
		if err := cmdInfo([]string{qra}); err != nil {
			t.Fatal(err)
		}
		codes := filepath.Join(dir, "codes")
		if err := cmdRender([]string{"-format", "png", "-scale", "2", "-quiet", "3", qra, codes}); err != nil {
			t.Fatal(err)
		}
		files, _ := filepath.Glob(filepath.Join(codes, "code-*.png"))
		if len(files) == 0 {
			t.Fatal("render не створив PNG")
		}
		// типовий формат bits і text: один файл кожен
		qrb := filepath.Join(dir, "codes.qrb")
		if err := cmdRender([]string{qra, qrb}); err != nil {
			t.Fatal(err)
		}
		qrt := filepath.Join(dir, "codes.qrt")
		if err := cmdRender([]string{"-format", "text", qra, qrt}); err != nil {
			t.Fatal(err)
		}
		for _, src := range []string{qrb, qrt} {
			out := filepath.Join(dir, filepath.Base(src)+".scanned.qra")
			if err := cmdScan([]string{out, src}); err != nil {
				t.Fatalf("scan %s: %v", src, err)
			}
			a, _ := os.ReadFile(qra)
			b, _ := os.ReadFile(out)
			if !bytes.Equal(a, b) {
				t.Fatalf("%s: відсканований .qra відрізняється", src)
			}
		}
		// без імені виходу render кладе .qrb поруч із .qra
		if err := cmdRender([]string{qra}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "a.qrb")); err != nil {
			t.Fatal("render без імені не створив a.qrb")
		}
		if err := cmdRender([]string{"-format", "nope", qra}); err == nil {
			t.Fatal("невідомий формат має давати помилку")
		}
		scanned := filepath.Join(dir, "scanned.qra")
		scanArgs := []string{scanned, codes} // тека, а не перелік файлів
		if noEcc {
			scanArgs = append([]string{"-no-ecc"}, scanArgs...)
		}
		if err := cmdScan(scanArgs); err != nil {
			t.Fatal(err)
		}
		a, _ := os.ReadFile(qra)
		b, _ := os.ReadFile(scanned)
		if !bytes.Equal(a, b) {
			t.Fatal("відсканований .qra відрізняється від записаного")
		}
		out := filepath.Join(dir, "restored.png")
		if err := cmdUnpack([]string{scanned, out}); err != nil {
			t.Fatal(err)
		}
		back, _ := os.ReadFile(out)
		if !bytes.Equal(back, img) {
			t.Fatal("відновлене зображення не збігається з оригіналом")
		}
		// unpack без імені: береться <ім'я>.restored.<ext>
		if err := cmdUnpack([]string{scanned}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "scanned.restored.png")); err != nil {
			t.Fatal("unpack не створив файл з типовим ім'ям")
		}
		// pack без імені вихідного файлу: <ім'я без розширення>.qra
		if err := cmdPack([]string{src}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "картинка.qra")); err != nil {
			t.Fatal("pack не створив файл з типовим ім'ям")
		}
	}
}

// Будь-який файл, не лише зображення: pack не розбирає формат, лише байти.
func TestAnyFileNotJustImages(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]byte{
		"notes.txt":  []byte(strings.Repeat("Будь-який файл: текст, архів, аудіо. ", 300)),
		"data.bin":   bytes.Repeat([]byte{0, 1, 2, 3, 255, 254}, 5000),
		"noext":      []byte("файл без розширення"),
		"tiny.json":  []byte("{}"),
		"zeros.dat":  make([]byte, 100000),
		"empty.file": {},
	}
	for name, content := range cases {
		src := filepath.Join(dir, name)
		os.WriteFile(src, content, 0o644)
		qra := filepath.Join(dir, name+".qra")
		if err := cmdPack([]string{src, qra}); err != nil {
			t.Fatalf("%s: pack: %v", name, err)
		}
		codes := filepath.Join(dir, name+".qrb")
		if err := cmdRender([]string{qra, codes}); err != nil {
			t.Fatalf("%s: render: %v", name, err)
		}
		scanned := filepath.Join(dir, name+".scanned.qra")
		if err := cmdScan([]string{scanned, codes}); err != nil {
			t.Fatalf("%s: scan: %v", name, err)
		}
		out := filepath.Join(dir, name+".restored")
		if err := cmdUnpack([]string{scanned, out}); err != nil {
			t.Fatalf("%s: unpack: %v", name, err)
		}
		back, _ := os.ReadFile(out)
		if !bytes.Equal(back, content) {
			t.Fatalf("%s: відновлений вміст відрізняється", name)
		}
		h, _, _ := ParseQRA(mustRead(t, qra))
		wantExt := strings.TrimPrefix(filepath.Ext(name), ".")
		if h.Ext != wantExt {
			t.Fatalf("%s: розширення в заголовку %q, очікував %q", name, h.Ext, wantExt)
		}
	}
}

func mustRead(t *testing.T, p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPackQRCheck(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "f.bin")
	os.WriteFile(src, bytes.Repeat([]byte("qr check "), 2000), 0o644)
	if err := cmdPack([]string{src, filepath.Join(dir, "a.qra")}); err != nil {
		t.Fatalf("pack з перевіркою через QR: %v", err)
	}
	if err := cmdPack([]string{"-no-qr-check", "-no-ecc", "-v", "12", src, filepath.Join(dir, "b.qra")}); err != nil {
		t.Fatalf("pack без перевірки: %v", err)
	}
	a, _ := os.ReadFile(filepath.Join(dir, "a.qra"))
	b, _ := os.ReadFile(filepath.Join(dir, "b.qra"))
	if len(a) != len(b) || a[7] == b[7] {
		t.Fatal("обидва .qra мають однакову довжину, але різну версію QR у заголовку")
	}
}

func TestForEachBatchOrderAndBatching(t *testing.T) {
	// 100 шматків версії 1 → кілька пакетів; порядок і межі пакетів мають бути правильні
	data := make([]byte, 17*100)
	for i := range data {
		data[i] = byte(i)
	}
	var starts []int
	var all []*Code
	err := forEachBatch(data, 1, false, "", func(start int, codes []*Code) error {
		starts = append(starts, start)
		all = append(all, codes...)
		return nil
	})
	if err != nil || len(all) != 100 || starts[0] != 0 {
		t.Fatalf("пакети: %v, кодів %d, %v", starts, len(all), err)
	}
	if len(starts) < 2 {
		t.Fatal("очікував кілька пакетів")
	}
	for i, c := range all {
		p, _, _ := DecodeMatrix(c.Modules, c.Size, false)
		if !bytes.Equal(p, data[i*17:(i+1)*17]) {
			t.Fatalf("код %d містить не свій шматок", i)
		}
	}
	// помилка з fn зупиняє обхід
	calls := 0
	err = forEachBatch(data, 1, false, "", func(int, []*Code) error { calls++; return os.ErrInvalid })
	if err == nil || calls != 1 {
		t.Fatal("помилка з fn має повертатися одразу")
	}
}

func TestNaturalLessAndExpand(t *testing.T) {
	if !naturalLess("code-2.png", "code-10.png") || naturalLess("code-10.png", "code-2.png") || naturalLess("a", "a") || !naturalLess("a", "ab") || !naturalLess("code-000001.png", "code-000002.png") {
		t.Fatal("naturalLess")
	}
	dir := t.TempDir()
	for _, n := range []string{"code-10.png", "code-2.png", "code-1.png", "readme.txt", "x.JPG"} {
		os.WriteFile(filepath.Join(dir, n), []byte{0}, 0o644)
	}
	files, err := ExpandCodeFiles([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	if strings.Join(names, ",") != "code-1.png,code-2.png,code-10.png,x.JPG" {
		t.Fatalf("розгортання теки: %v", names)
	}
	// файли передаються як є, тека і файл можна змішувати
	files, _ = ExpandCodeFiles([]string{filepath.Join(dir, "readme.txt"), dir})
	if len(files) != 5 || filepath.Base(files[0]) != "code-1.png" {
		t.Fatalf("змішані аргументи: %v", files)
	}
	if _, err := ExpandCodeFiles([]string{filepath.Join(dir, "нема")}); err == nil {
		t.Fatal("неіснуючий шлях")
	}
	if codeFileName(7) != "code-000007.png" || codeFileName(123456) != "code-123456.png" {
		t.Fatal("codeFileName")
	}
}

func TestCommandsErrors(t *testing.T) {
	dir := t.TempDir()
	if err := cmdPack(nil); err == nil {
		t.Error("pack без аргументів")
	}
	if err := cmdPack([]string{filepath.Join(dir, "нема.png")}); err == nil {
		t.Error("pack неіснуючого файлу")
	}
	if err := cmdUnpack(nil); err == nil {
		t.Error("unpack без аргументів")
	}
	junk := filepath.Join(dir, "junk.qra")
	os.WriteFile(junk, []byte("junk"), 0o644)
	if err := cmdUnpack([]string{junk}); err == nil {
		t.Error("unpack сміття")
	}
	if err := cmdInfo(nil); err == nil || cmdInfo([]string{junk}) == nil {
		t.Error("info без аргументів або зі сміттям")
	}
	if err := cmdRender(nil); err == nil {
		t.Error("render без аргументів")
	}
	if err := cmdRender([]string{junk}); err == nil {
		t.Error("render сміття")
	}
	if err := cmdScan([]string{filepath.Join(dir, "o.qra")}); err == nil {
		t.Error("scan без кодів")
	}
	empty := filepath.Join(dir, "empty")
	os.Mkdir(empty, 0o755)
	if err := cmdScan([]string{filepath.Join(dir, "o.qra"), empty}); err == nil {
		t.Error("scan порожньої теки")
	}
	if err := cmdScan([]string{filepath.Join(dir, "o.qra"), filepath.Join(dir, "нема.png")}); err == nil {
		t.Error("scan неіснуючого PNG")
	}
	// scan коду, який не є частиною .qra → помилка збірки
	c, err := Encode([]byte("просто текст, не .qra"), 3, false)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "text.png")
	WritePNG(p, RenderPNG(c, 3, 4))
	if err := cmdScan([]string{filepath.Join(dir, "o.qra"), p}); err == nil {
		t.Error("scan коду без сигнатури QRA1 має давати помилку")
	}
	// scan лише частини кодів → обірваний потік
	src := filepath.Join(dir, "img.png")
	os.WriteFile(src, bytes.Repeat(testImage(), 3), 0o644)
	qra := filepath.Join(dir, "img.qra")
	cmdPack([]string{"-v", "10", src, qra})
	codes := filepath.Join(dir, "codes")
	cmdRender([]string{"-format", "png", qra, codes})
	files, _ := filepath.Glob(filepath.Join(codes, "code-*.png"))
	if len(files) < 2 {
		t.Fatal("очікував кілька кодів")
	}
	sort.Strings(files)
	if err := cmdScan([]string{filepath.Join(dir, "part.qra"), files[0]}); err == nil {
		t.Error("scan лише першого коду має давати помилку обірваного потоку")
	}
}

func TestDataModuleBitsAndCountWriter(t *testing.T) {
	c, _ := Encode([]byte("bits"), 1, false)
	b := dataModuleBits(c)
	if len(b) != (len(c.Order)+7)/8 {
		t.Fatalf("довжина %d", len(b))
	}
	// перші 4 біти — індикатор байтового режиму 0100
	if b[0]>>4 != 0b0100 {
		t.Fatalf("перші біти %04b, очікував 0100", b[0]>>4)
	}
	var cw countWriter
	cw.Write([]byte("abc"))
	cw.Write(nil)
	if cw.n != 3 {
		t.Fatal("countWriter")
	}
	// прогрес без термінала мовчить і не панікує
	p := newProgress("x", 10)
	p.add(5)
	p.add(5)
	p.finish()
	p2 := newProgress("", 0)
	p2.add(0)
	p2.finish()
}

func TestPctAndRatio(t *testing.T) {
	if pctStr(50, 200) != "25,0 %" || pctStr(1, 0) != "—" || pctStr(3590, 49206) != "7,3 %" {
		t.Fatalf("pctStr: %q %q %q", pctStr(50, 200), pctStr(1, 0), pctStr(3590, 49206))
	}
	if ratioStr(49206, 3590) != "×13,7" || ratioStr(100, 100) != "×1" || ratioStr(1, 0) != "—" || ratioStr(1000, 993) != "×1,01" {
		t.Fatalf("ratioStr: %q %q %q %q", ratioStr(49206, 3590), ratioStr(100, 100), ratioStr(1, 0), ratioStr(1000, 993))
	}
}
