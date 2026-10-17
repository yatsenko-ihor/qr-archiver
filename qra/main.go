package main

// qra — пакує будь-який файл у контейнер .qra (deflate + заголовок),
// проганяє його через QR-коди в пам'яті, за бажанням записує коди
// компактно або як картинки, читає коди назад і відновлює файл.
//
//	qra pack   [-v N] [-no-ecc] [-no-qr-check] <файл> [out.qra]
//	qra unpack <in.qra> [вихідний-файл]
//	qra render [-format bits|text|png] [-scale 4] [-quiet 4] [-v N] [-no-ecc] <in.qra> [вихід]
//	qra scan   [-no-ecc] <out.qra> <codes.qrb | codes.qrt | тека-з-картинками | файли…>
//	qra info   <in.qra>

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	fmt.Fprintln(os.Stderr, `qra — будь-який файл ↔ .qra ↔ QR-коди

  qra pack   [-v N] [-no-ecc] [-no-qr-check] <файл> [out.qra]
             стиснути файл, зібрати .qra і перевірити його через QR-коди в пам'яті (без файлів кодів)
  qra unpack <in.qra> [вихідний-файл]
             відновити файл з .qra (перевірка розміру і CRC32)
  qra render [-format bits|text|png] [-scale 4] [-quiet 4] [-v N] [-no-ecc] <in.qra> [вихід]
             записати QR-коди: bits — один компактний .qrb (типово), text — .qrt з base64, png — тека картинок
  qra scan   [-no-ecc] <out.qra> <codes.qrb | codes.qrt | тека | картинки…>
             зчитати коди й зібрати .qra
  qra info   <in.qra>
             показати заголовок`)
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

// ---- паралельне кодування ----

// encodeAll кодує всі шматки одразу (зручно для малих даних і тестів).
func encodeAll(data []byte, v int, noEcc bool) ([]*Code, error) {
	var out []*Code
	err := forEachBatch(data, v, noEcc, func(_ int, codes []*Code) error {
		out = append(out, codes...)
		return nil
	})
	return out, err
}

// forEachBatch ріже data на шматки, кодує їх пакетами паралельно (по горутині на ядро)
// і віддає кожен пакет у fn у правильному порядку. Пам'ять обмежена розміром пакета,
// тому так можна обробити файл будь-якого розміру, не тримаючи всі матриці одночасно.
func forEachBatch(data []byte, v int, noEcc bool, fn func(start int, codes []*Code) error) error {
	cap := Capacity(v, noEcc)
	n := (len(data) + cap - 1) / cap
	if n < 1 {
		n = 1
	}
	workers := runtime.GOMAXPROCS(0)
	batch := workers * 8
	for start := 0; start < n; start += batch {
		end := start + batch
		if end > n {
			end = n
		}
		codes := make([]*Code, end-start)
		errs := make([]error, end-start)
		jobs := make(chan int)
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					lo, hi := i*cap, (i+1)*cap
					if hi > len(data) {
						hi = len(data)
					}
					codes[i-start], errs[i-start] = Encode(data[lo:hi], v, noEcc)
				}
			}()
		}
		for i := start; i < end; i++ {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		for _, e := range errs {
			if e != nil {
				return e
			}
		}
		if err := fn(start, codes); err != nil {
			return err
		}
	}
	return nil
}

// ---- pack ----

func cmdPack(args []string) error {
	fs := flag.NewFlagSet("pack", flag.ExitOnError)
	ver := fs.Int("v", 0, "версія QR 1..40 (0 — авто: найменша, що вміщує тією ж кількістю кодів, що й 40)")
	noEcc := fs.Bool("no-ecc", false, "нестандартні коди без корекції (3 703 Б на код версії 40 замість 2 953)")
	noCheck := fs.Bool("no-qr-check", false, "не проганяти .qra через QR-коди в пам'яті")
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("вкажіть файл для пакування")
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
	v := int(h.QRVersion)
	n := codesFor(len(qra), v, *noEcc)
	orig, stored := len(data), len(qra)
	printPackSummary(src, out, h, orig, stored, n, v, *noEcc, time.Since(t0))
	if *noCheck {
		return nil
	}
	// Перевірка через QR у пам'яті: кодуємо пакетами, одразу зчитуємо лише корисні байти,
	// порівнюємо зі шматком і викидаємо матриці. Жодних файлів кодів.
	t1 := time.Now()
	modules, skipped, ecc := 0, 0, 0
	err = forEachBatch(qra, v, *noEcc, func(start int, codes []*Code) error {
		cap := Capacity(v, *noEcc)
		for i, c := range codes {
			got, _, err := DecodeMatrix(c.Modules, c.Size, *noEcc)
			if err != nil {
				return fmt.Errorf("код %d: %w", start+i+1, err)
			}
			lo, hi := (start+i)*cap, (start+i+1)*cap
			if hi > len(qra) {
				hi = len(qra)
			}
			if string(got) != string(qra[lo:hi]) {
				return fmt.Errorf("код %d: зчитані байти не збігаються з покладеними", start+i+1)
			}
			modules += c.Size * c.Size
			skipped += c.Size*c.Size - len(got)*8
			ecc += c.TotalCodewords - c.DataCodewords
		}
		return nil
	})
	if err != nil {
		return err
	}
	kind := "стандартні, рівень L"
	if *noEcc {
		kind = "нестандартні, без корекції"
	}
	fmt.Printf("QR у пам'яті:   %d кодів %d×%d (%s), %s модулів, у %d потоках за %v\n", n, v*4+17, v*4+17, kind, human(modules), runtime.GOMAXPROCS(0), time.Since(t1).Round(time.Millisecond))
	fmt.Printf("Після QR:       зчитано назад %s = 100,0 %% покладених; QR нічого не додав і не забрав ✓\n", human(stored))
	fmt.Printf("                пропущено %s модулів: службові шаблони, корекція (%s), заголовки, заповнювачі\n", human(skipped), human(ecc))
	return nil
}

// printPackSummary друкує показники стиснення так, щоб було видно, що відбувається до QR, а що після.
func printPackSummary(src, out string, h Header, orig, stored, n, v int, noEcc bool, took time.Duration) {
	codec := map[uint8]string{codecStore: "deflate не допоміг, байти як є", codecDeflate: "deflate"}[h.Codec]
	saved := orig - stored
	fmt.Printf("Файл:           %s  %s\n", src, human(orig))
	fmt.Printf("До QR:          %s → %s у .qra (разом із заголовком %d Б)\n", codec, human(stored), h.size())
	label := "економія"
	if saved < 0 {
		label = "перевитрата (заголовок)"
	}
	fmt.Printf("Ефективність:   лишилось %s від файлу, %s %s (%s), коефіцієнт стиснення %s\n",
		pctStr(stored, orig), label, human(absi(saved)), pctStr(absi(saved), orig), ratioStr(orig, stored))
	if h.Codec == codecStore || float64(stored) > 0.97*float64(orig) {
		fmt.Printf("                файл уже стиснений усередині (відео, JPEG, архів тощо): без втрат менше не буде\n")
	}
	cap := Capacity(v, noEcc)
	perCode := 0
	if n > 0 {
		perCode = orig / n
	}
	fmt.Printf("QR-кодів:       %d, версія %d по %d Б корисних; без стиснення було б %d; один код несе ~%s вихідного файлу\n",
		n, v, cap, codesFor(orig+h.size(), v, noEcc), human(perCode))
	fmt.Printf("Контейнер:      %s, CRC32 %08x, %v\n", out, h.CRC, took.Round(time.Millisecond))
}

// pctStr — a у відсотках від b, з комою як десятковим роздільником.
func pctStr(a, b int) string {
	if b == 0 {
		return "—"
	}
	return strings.Replace(fmt.Sprintf("%.1f %%", 100*float64(a)/float64(b)), ".", ",", 1)
}

// ratioStr — класичний коефіцієнт стиснення before/after, наприклад ×13,7.
func ratioStr(before, after int) string {
	if after == 0 {
		return "—"
	}
	return strings.Replace(fmt.Sprintf("×%.3g", float64(before)/float64(after)), ".", ",", 1)
}

// ---- unpack ----

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

// ---- render ----

func cmdRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	format := fs.String("format", "bits", "bits — один .qrb з упакованими матрицями; text — .qrt з base64; png — тека картинок")
	scale := fs.Int("scale", 4, "png: пікселів на модуль")
	quiet := fs.Int("quiet", 4, "png: тиха зона в модулях")
	ver := fs.Int("v", 0, "версія QR (0 — з заголовка .qra)")
	noEccFlag := fs.Bool("no-ecc", false, "коди без корекції (інакше — за прапорцем у заголовку)")
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("вкажіть <in.qra> [вихід]")
	}
	in := fs.Arg(0)
	data, err := os.ReadFile(in)
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
	n := codesFor(len(data), v, noEcc)
	if n < 1 {
		n = 1
	}
	out := fs.Arg(1)
	t0 := time.Now()
	var where string
	switch *format {
	case "bits", "text":
		ext := map[string]string{"bits": ".qrb", "text": ".qrt"}[*format]
		if out == "" {
			out = strings.TrimSuffix(in, filepath.Ext(in)) + ext
		}
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		defer f.Close()
		var write func(*Code) error
		var flush func() error
		if *format == "bits" {
			bw, err := NewBitsWriter(f, n, noEcc)
			if err != nil {
				return err
			}
			write, flush = bw.WriteCode, func() error { return nil }
		} else {
			tw, err := NewTextWriter(f, n, noEcc)
			if err != nil {
				return err
			}
			write, flush = tw.WriteCode, tw.Flush
		}
		err = forEachBatch(data, v, noEcc, func(_ int, codes []*Code) error {
			for _, c := range codes {
				if err := write(c); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if err := flush(); err != nil {
			return err
		}
		st, _ := f.Stat()
		where = fmt.Sprintf("%s, %s (%s на код)", out, human(int(st.Size())), human(int(st.Size())/n))
	case "png":
		if out == "" {
			out = strings.TrimSuffix(in, filepath.Ext(in)) + "-codes"
		}
		if err := os.MkdirAll(out, 0o755); err != nil {
			return err
		}
		err = forEachBatch(data, v, noEcc, func(start int, codes []*Code) error {
			errs := make([]error, len(codes))
			var wg sync.WaitGroup
			for i, c := range codes {
				wg.Add(1)
				go func(i int, c *Code) {
					defer wg.Done()
					errs[i] = WritePNG(filepath.Join(out, codeFileName(start+i+1)), RenderPNG(c, *scale, *quiet))
				}(i, c)
			}
			wg.Wait()
			for _, e := range errs {
				if e != nil {
					return e
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		where = fmt.Sprintf("%s/%s … %s, %d px на модуль", out, codeFileName(1), codeFileName(n), *scale)
	default:
		return fmt.Errorf("невідомий формат %q: bits, text або png", *format)
	}
	kind := "стандартні, рівень L"
	if noEcc {
		kind = "НЕСТАНДАРТНІ, без корекції: звичайний сканер їх не прочитає"
	}
	fmt.Printf("QR-кодів:    %d, версія %d (%d×%d модулів), %s\n", n, v, v*4+17, v*4+17, kind)
	fmt.Printf("Записано:    %s\n", where)
	fmt.Printf("Час:         %v у %d потоках\n", time.Since(t0).Round(time.Millisecond), runtime.GOMAXPROCS(0))
	return nil
}

// ---- scan ----

func cmdScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	noEccFlag := fs.Bool("no-ecc", false, "коди згенеровано без корекції (для картинок; .qrb/.qrt несуть цей прапорець самі)")
	fs.Parse(args)
	if fs.NArg() < 2 {
		return fmt.Errorf("вкажіть <out.qra> і файл кодів (.qrb/.qrt), теку з картинками або картинки")
	}
	out := fs.Arg(0)
	t0 := time.Now()
	// джерела: контейнери кодів або картинки
	type item struct {
		modules []uint8
		size    int
		noEcc   bool
		name    string
	}
	var items []item
	var imageArgs []string
	for _, a := range fs.Args()[1:] {
		st, err := os.Stat(a)
		if err != nil {
			return err
		}
		if !st.IsDir() {
			if codes, noEcc, err := ReadCodesFile(a); err == nil {
				for i, c := range codes {
					items = append(items, item{c.Modules, c.Size, noEcc, fmt.Sprintf("%s#%d", a, i+1)})
				}
				continue
			}
		}
		imageArgs = append(imageArgs, a)
	}
	files, err := ExpandCodeFiles(imageArgs)
	if err != nil {
		return err
	}
	if len(items) == 0 && len(files) == 0 {
		return fmt.Errorf("не знайдено жодного коду")
	}
	// картинки читаємо паралельно
	imgItems := make([]item, len(files))
	errs := make([]error, len(files))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for i, f := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, f string) {
			defer wg.Done()
			defer func() { <-sem }()
			m, size, err := ReadMatrixImage(f)
			if err != nil {
				errs[i] = err
				return
			}
			imgItems[i] = item{m, size, *noEccFlag, f}
		}(i, f)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	items = append(items, imgItems...)
	// зчитуємо корисні байти паралельно
	payloads := make([][]byte, len(items))
	errs = make([]error, len(items))
	var skipped int64
	var mu sync.Mutex
	for i, it := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, it item) {
			defer wg.Done()
			defer func() { <-sem }()
			p, _, err := DecodeMatrix(it.modules, it.size, it.noEcc)
			if err != nil {
				errs[i] = fmt.Errorf("%s: %w", it.name, err)
				return
			}
			payloads[i] = p
			mu.Lock()
			skipped += int64(it.size*it.size - len(p)*8)
			mu.Unlock()
		}(i, it)
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
	fmt.Printf("Зчитано:     %d кодів, %s корисних байтів; пропущено %s модулів (службові, маска, корекція, заголовки, заповнювачі)\n", len(items), human(len(data)), human(int(skipped)))
	fmt.Printf("Контейнер:   %s, вихідний файл %s (.%s), CRC32 %08x, %v\n", out, human(int(h.OrigSize)), h.Ext, h.CRC, time.Since(t0).Round(time.Millisecond))
	return nil
}

// ---- info ----

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
	v := int(h.QRVersion)
	n := codesFor(len(data), v, noEcc)
	fmt.Printf("Сигнатура:   QRA1, версія формату 1\n")
	fmt.Printf("Кодек:       %s, потік %s\n", codec, human(len(payload)))
	fmt.Printf("Оригінал:    .%s, %s, CRC32 %08x\n", h.Ext, human(int(h.OrigSize)), h.CRC)
	fmt.Printf("QR:          версія %d, без корекції: %v, %d байт на код, кодів: %d, модулів разом: %s\n", v, noEcc, Capacity(v, noEcc), n, human(n*(v*4+17)*(v*4+17)))
	fmt.Printf("Заголовок:   %d Б, увесь контейнер %s\n", h.size(), human(len(data)))
	return nil
}
