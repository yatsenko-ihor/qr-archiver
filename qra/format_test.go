package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"hash/crc32"
	"math/rand"
	"strings"
	"testing"
)

func TestHeaderMarshalParse(t *testing.T) {
	h := Header{Flags: flagNoEcc, Codec: codecDeflate, QRVersion: 33, Ext: "png", OrigSize: 123456789, CRC: 0xDEADBEEF, PayloadSize: 5}
	b := h.marshal()
	if len(b) != h.size() || h.size() != 29+3 {
		t.Fatalf("довжина заголовка %d, size() = %d, очікував 32", len(b), h.size())
	}
	if string(b[:4]) != "QRA1" || b[4] != 1 || b[5] != flagNoEcc || b[6] != codecDeflate || b[7] != 33 || b[8] != 3 || string(b[9:12]) != "png" {
		t.Fatalf("розкладка заголовка: % x", b[:12])
	}
	if binary.LittleEndian.Uint64(b[12:]) != 123456789 || binary.LittleEndian.Uint32(b[20:]) != 0xDEADBEEF || binary.LittleEndian.Uint64(b[24:]) != 5 {
		t.Fatal("числові поля заголовка")
	}
	got, payload, err := ParseQRA(append(b, 1, 2, 3, 4, 5, 99))
	if err != nil {
		t.Fatal(err)
	}
	if got != h || !bytes.Equal(payload, []byte{1, 2, 3, 4, 5}) {
		t.Fatalf("розбір: %+v, потік % x", got, payload)
	}
	// порожнє розширення і розширення в 255 символів
	for _, ext := range []string{"", strings.Repeat("x", 255)} {
		h := Header{Ext: ext}
		g, _, err := ParseQRA(h.marshal())
		if err != nil || g.Ext != ext {
			t.Fatalf("розширення довжини %d: %v", len(ext), err)
		}
	}
}

func TestParseQRAErrors(t *testing.T) {
	good := Header{Ext: "bmp", PayloadSize: 4}.marshal()
	good = append(good, 1, 2, 3, 4)
	cases := map[string][]byte{
		"порожній":            {},
		"чужа сигнатура":      append([]byte("ZIP1"), good[4:]...),
		"версія формату 2":    append(append([]byte{}, good[:4]...), append([]byte{2}, good[5:]...)...),
		"обірваний заголовок": good[:15],
		"обірваний потік":     good[:len(good)-1],
	}
	for name, data := range cases {
		if _, _, err := ParseQRA(data); err == nil {
			t.Errorf("%s: очікував помилку", name)
		}
	}
}

func TestPackChoosesCodec(t *testing.T) {
	compressible := bytes.Repeat([]byte("тризуб на блакитному тлі "), 2000)
	q, h, err := Pack(compressible, "bmp", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if h.Codec != codecDeflate || int(h.PayloadSize) >= len(compressible) || len(q) != h.size()+int(h.PayloadSize) {
		t.Fatalf("стисливий файл: кодек %d, потік %d з %d", h.Codec, h.PayloadSize, len(compressible))
	}
	if h.CRC != crc32.ChecksumIEEE(compressible) || h.OrigSize != uint64(len(compressible)) || h.Ext != "bmp" || h.Flags != 0 {
		t.Fatalf("поля заголовка: %+v", h)
	}
	// потік має бути справжнім deflate-raw
	_, raw, _ := ParseQRA(q)
	var out bytes.Buffer
	if _, err := out.ReadFrom(flate.NewReader(bytes.NewReader(raw))); err != nil || !bytes.Equal(out.Bytes(), compressible) {
		t.Fatal("потік не розпаковується як deflate-raw")
	}

	random := make([]byte, 20000)
	rand.New(rand.NewSource(4)).Read(random)
	q, h, err = Pack(random, "jpg", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if h.Codec != codecStore || int(h.PayloadSize) != len(random) || !bytes.Equal(q[h.size():], random) {
		t.Fatalf("нестисливий файл має зберігатися як є: кодек %d, потік %d", h.Codec, h.PayloadSize)
	}
}

func TestPackVersionAndFlags(t *testing.T) {
	data := bytes.Repeat([]byte{7}, 100)
	q, h, _ := Pack(data, "x", 0, false)
	if h.QRVersion != uint8(ChooseVersion(len(q), false)) {
		t.Fatalf("авто-версія %d, очікував %d", h.QRVersion, ChooseVersion(len(q), false))
	}
	_, h, _ = Pack(data, "x", 40, false)
	if h.QRVersion != 40 {
		t.Fatal("явна версія має зберігатися")
	}
	_, h, _ = Pack(data, "x", 0, true)
	if h.Flags&flagNoEcc == 0 {
		t.Fatal("прапорець без корекції не встановлено")
	}
	_, h, _ = Pack(data, strings.Repeat("e", 300), 0, false)
	if len(h.Ext) != 255 {
		t.Fatal("розширення має обрізатися до 255")
	}
	q, _, _ = Pack(nil, "", 0, false)
	if back, _, err := Unpack(q); err != nil || len(back) != 0 {
		t.Fatalf("порожній файл: %v", err)
	}
}

func TestUnpackErrors(t *testing.T) {
	src := bytes.Repeat([]byte("abc"), 5000)
	q, h, _ := Pack(src, "txt", 0, false)
	// зіпсований байт потоку → CRC або помилка deflate
	bad := append([]byte(nil), q...)
	bad[len(bad)-3] ^= 0x55
	if _, _, err := Unpack(bad); err == nil {
		t.Fatal("пошкоджений потік розпакувався без помилки")
	}
	// підмінений розмір оригіналу
	bad = append([]byte(nil), q...)
	binary.LittleEndian.PutUint64(bad[9+len(h.Ext):], 1)
	if _, _, err := Unpack(bad); err == nil {
		t.Fatal("підмінений розмір має ловитися")
	}
	// підмінений CRC
	bad = append([]byte(nil), q...)
	binary.LittleEndian.PutUint32(bad[17+len(h.Ext):], 0)
	if _, _, err := Unpack(bad); err == nil {
		t.Fatal("підмінений CRC має ловитися")
	}
	// невідомий кодек
	bad = append([]byte(nil), q...)
	bad[6] = 9
	if _, _, err := Unpack(bad); err == nil {
		t.Fatal("невідомий кодек має ловитися")
	}
	// не .qra взагалі
	if _, _, err := Unpack([]byte("hello")); err == nil {
		t.Fatal("сміття має давати помилку")
	}
	// і нормальний шлях
	back, gh, err := Unpack(q)
	if err != nil || !bytes.Equal(back, src) || gh != h {
		t.Fatalf("штатне розпакування: %v", err)
	}
}
