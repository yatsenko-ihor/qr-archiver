package main

// qra — пакує файл зображення в контейнер .qra (deflate + заголовок),
// рендерить його у QR-коди, читає QR-коди назад і відновлює зображення.
//
//	qra pack   [-v N] [-no-ecc] <зображення> [out.qra]
//	qra unpack <in.qra> [вихідний-файл]
//	qra render [-scale 4] [-quiet 4] [-v N] [-no-ecc] <in.qra> <тека>
//	qra scan   [-no-ecc] <out.qra> <code-0001.png> [code-0002.png …]
//	qra info   <in.qra>

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "pack":
		err = cmdPack(os.Args[2:])
	case "unpack":
		err = cmdUnpack(os.Args[2:])
	case "render":
		err = cmdRender(os.Args[2:])
	case "scan":
		err = cmdScan(os.Args[2:])
	case "info":
		err = cmdInfo(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "помилка:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `qra — зображення ↔ .qra ↔ QR-коди

  qra pack   [-v N] [-no-ecc] <зображення> [out.qra]      стиснути файл і зібрати .qra
  qra unpack <in.qra> [вихідний-файл]                     відновити файл з .qra (перевірка CRC32)
  qra render [-scale 4] [-quiet 4] [-v N] [-no-ecc] <in.qra> <тека>   нарізати .qra на QR-коди й записати PNG
  qra scan   [-no-ecc] <out.qra> <code-*.png …>           зчитати корисні байти з PNG і зібрати .qra
  qra info   <in.qra>                                     показати заголовок`)
}

func human(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%d Б (%.2f МіБ)", n, float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d Б (%.2f КіБ)", n, float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d Б", n)
}

func codesFor(total, v int, noEcc bool) int {
	c := Capacity(v, noEcc)
	return (total + c - 1) / c
}

func cmdPack(args []string) error {
	fs := flag.NewFlagSet("pack", flag.ExitOnError)
	ver := fs.Int("v", 0, "версія QR 1..40 (0 — авто: найменша, що вміщує тією ж кількістю кодів, що й 40)")
	noEcc := fs.Bool("no-ecc", false, "нестандартні коди без корекції (3 703 Б на код версії 40 замість 2 953)")
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("вкажіть файл зображення")
	}
	src := fs.Arg(0)
	out := fs.Arg(1)
	if out == "" {
		out = strings.TrimSuffix(src, filepath.Ext(src)) + ".qra"
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	t0 := time.Now()
	qra, h, err := Pack(data, strings.TrimPrefix(filepath.Ext(src), "."), *ver, *noEcc)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, qra, 0o644); err != nil {
		return err
	}
	codec := map[uint8]string{codecStore: "без стиснення (файл не зменшився)", codecDeflate: "deflate"}[h.Codec]
	n := codesFor(len(qra), int(h.QRVersion), *noEcc)
	fmt.Printf("Файл:        %s  %s\n", src, human(len(data)))
	fmt.Printf("Стиснення:   %s → %s (%.1f %%)\n", codec, human(int(h.PayloadSize)), 100*float64(h.PayloadSize)/float64(len(data)))
	fmt.Printf("Контейнер:   %s  %s (заголовок %d Б)\n", out, human(len(qra)), h.size())
	fmt.Printf("QR-кодів:    %d, версія %d, по %d байт, замість %d без стиснення\n", n, h.QRVersion, Capacity(int(h.QRVersion), *noEcc), codesFor(len(data)+h.size(), int(h.QRVersion), *noEcc))
	fmt.Printf("CRC32:       %08x,  %v\n", h.CRC, time.Since(t0).Round(time.Millisecond))
	return nil
}

func cmdUnpack(args []string) error {
	fs := flag.NewFlagSet("unpack", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("вкажіть файл .qra")
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	t0 := time.Now()
	orig, h, err := Unpack(data)
	if err != nil {
		return err
	}
	out := fs.Arg(1)
	if out == "" {
		out = strings.TrimSuffix(fs.Arg(0), filepath.Ext(fs.Arg(0))) + ".restored"
		if h.Ext != "" {
			out += "." + h.Ext
		}
	}
	if err := os.WriteFile(out, orig, 0o644); err != nil {
		return err
	}
	fmt.Printf("Відновлено:  %s  %s, CRC32 %08x збігається, %v\n", out, human(len(orig)), h.CRC, time.Since(t0).Round(time.Millisecond))
	return nil
}

// encodeAll кодує шматки паралельно: по горутині-робітнику на ядро.
func encodeAll(data []byte, v int, noEcc bool) ([]*Code, error) {
	cap := Capacity(v, noEcc)
	n := (len(data) + cap - 1) / cap
	if n < 1 {
		n = 1
	}
	codes := make([]*Code, n)
	errs := make([]error, n)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < runtime.GOMAXPROCS(0); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				lo, hi := i*cap, (i+1)*cap
				if hi > len(data) {
					hi = len(data)
				}
				codes[i], errs[i] = Encode(data[lo:hi], v, noEcc)
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	return codes, nil
}

func cmdRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	scale := fs.Int("scale", 4, "пікселів на модуль")
	quiet := fs.Int("quiet", 4, "тиха зона в модулях")
	ver := fs.Int("v", 0, "версія QR (0 — з заголовка .qra)")
	noEccFlag := fs.Bool("no-ecc", false, "коди без корекції (інакше — за прапорцем у заголовку)")
	fs.Parse(args)
	if fs.NArg() < 2 {
		return fmt.Errorf("вкажіть <in.qra> і <теку>")
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	h, _, err := ParseQRA(data)
	if err != nil {
		return err
	}
	v := *ver
	if v == 0 {
		v = int(h.QRVersion)
	}
	noEcc := *noEccFlag || h.Flags&flagNoEcc != 0
	dir := fs.Arg(1)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	t0 := time.Now()
	codes, err := encodeAll(data, v, noEcc)
	if err != nil {
		return err
	}
	tEnc := time.Since(t0)
	// PNG теж пишемо паралельно
	errs := make([]error, len(codes))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for i, c := range codes {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, c *Code) {
			defer wg.Done()
			defer func() { <-sem }()
			errs[i] = WritePNG(filepath.Join(dir, fmt.Sprintf("code-%04d.png", i+1)), RenderPNG(c, *scale, *quiet))
		}(i, c)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	ecc := 0
	for _, c := range codes {
		ecc += c.TotalCodewords - c.DataCodewords
	}
	kind := "стандартні, рівень L"
	if noEcc {
		kind = "НЕСТАНДАРТНІ, без корекції: звичайний сканер їх не прочитає"
	}
	fmt.Printf("QR-кодів:    %d, версія %d (%d×%d модулів), %s\n", len(codes), v, codes[0].Size, codes[0].Size, kind)
	fmt.Printf("Корисних:    %s у кодах, %s кодів корекції поза потоком\n", human(len(data)), human(ecc))
	fmt.Printf("Записано:    %s/code-0001.png … code-%04d.png, %d px на модуль\n", dir, len(codes), *scale)
	fmt.Printf("Час:         кодування %v на %d потоках, разом із PNG %v\n", tEnc.Round(time.Millisecond), runtime.GOMAXPROCS(0), time.Since(t0).Round(time.Millisecond))
	return nil
}

func cmdScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	noEcc := fs.Bool("no-ecc", false, "коди згенеровано без корекції")
	fs.Parse(args)
	if fs.NArg() < 2 {
		return fmt.Errorf("вкажіть <out.qra> і хоча б один PNG")
	}
	out := fs.Arg(0)
	files := append([]string(nil), fs.Args()[1:]...)
	sort.Strings(files)
	t0 := time.Now()
	payloads := make([][]byte, len(files))
	errs := make([]error, len(files))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	skipped := 0
	var mu sync.Mutex
	for i, f := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, f string) {
			defer wg.Done()
			defer func() { <-sem }()
			m, size, err := ReadMatrixPNG(f)
			if err != nil {
				errs[i] = err
				return
			}
			p, c, err := DecodeMatrix(m, size, *noEcc)
			if err != nil {
				errs[i] = fmt.Errorf("%s: %w", f, err)
				return
			}
			payloads[i] = p
			mu.Lock()
			skipped += size*size - len(p)*8
			mu.Unlock()
			_ = c
		}(i, f)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	var data []byte
	for _, p := range payloads {
		data = append(data, p...)
	}
	h, _, err := ParseQRA(data)
	if err != nil {
		return fmt.Errorf("зібрані байти не утворюють .qra (порядок або набір кодів неповний): %w", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("Зчитано:     %d кодів, %s корисних байтів; пропущено %s модулів (службові, маска, корекція, заголовки, заповнювачі)\n", len(files), human(len(data)), human(skipped))
	fmt.Printf("Контейнер:   %s, вихідний файл %s (.%s), CRC32 %08x, %v\n", out, human(int(h.OrigSize)), h.Ext, h.CRC, time.Since(t0).Round(time.Millisecond))
	return nil
}

func cmdInfo(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("вкажіть файл .qra")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	h, payload, err := ParseQRA(data)
	if err != nil {
		return err
	}
	noEcc := h.Flags&flagNoEcc != 0
	codec := map[uint8]string{codecStore: "store", codecDeflate: "deflate"}[h.Codec]
	fmt.Printf("Сигнатура:   QRA1, версія формату 1\n")
	fmt.Printf("Кодек:       %s, потік %s\n", codec, human(len(payload)))
	fmt.Printf("Оригінал:    .%s, %s, CRC32 %08x\n", h.Ext, human(int(h.OrigSize)), h.CRC)
	fmt.Printf("QR:          версія %d, без корекції: %v, %d байт на код, кодів: %d\n", h.QRVersion, noEcc, Capacity(int(h.QRVersion), noEcc), codesFor(len(data), int(h.QRVersion), noEcc))
	fmt.Printf("Заголовок:   %d Б, увесь контейнер %s\n", h.size(), human(len(data)))
	return nil
}
