package main

// Контейнер .qra: заголовок + стиснений потік. Саме ці байти (разом із
// заголовком) ріжуться на шматки і стають корисним вмістом QR-кодів, тому
// склеївши корисні байти всіх кодів, отримуємо .qra без жодних втрат.

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

const (
	magic        = "QRA1"
	codecStore   = 0
	codecDeflate = 1
	flagNoEcc    = 1
)

type Header struct {
	Flags       uint8
	Codec       uint8
	QRVersion   uint8
	Ext         string
	OrigSize    uint64
	CRC         uint32
	PayloadSize uint64
}

func (h Header) size() int { return 4 + 1 + 1 + 1 + 1 + 1 + len(h.Ext) + 8 + 4 + 8 }

func (h Header) marshal() []byte {
	b := make([]byte, 0, h.size())
	b = append(b, magic...)
	b = append(b, 1, h.Flags, h.Codec, h.QRVersion, uint8(len(h.Ext)))
	b = append(b, h.Ext...)
	b = binary.LittleEndian.AppendUint64(b, h.OrigSize)
	b = binary.LittleEndian.AppendUint32(b, h.CRC)
	b = binary.LittleEndian.AppendUint64(b, h.PayloadSize)
	return b
}

// ParseQRA розбирає файл .qra і повертає заголовок та стиснений потік.
func ParseQRA(data []byte) (Header, []byte, error) {
	var h Header
	if len(data) < 9 || string(data[:4]) != magic {
		return h, nil, errors.New("це не файл .qra: немає сигнатури QRA1")
	}
	if data[4] != 1 {
		return h, nil, fmt.Errorf("непідтримувана версія формату %d", data[4])
	}
	h.Flags, h.Codec, h.QRVersion = data[5], data[6], data[7]
	n := int(data[8])
	p := 9
	if len(data) < p+n+20 {
		return h, nil, errors.New("обірваний заголовок .qra")
	}
	h.Ext = string(data[p : p+n])
	p += n
	h.OrigSize = binary.LittleEndian.Uint64(data[p:])
	h.CRC = binary.LittleEndian.Uint32(data[p+8:])
	h.PayloadSize = binary.LittleEndian.Uint64(data[p+12:])
	p += 20
	if uint64(len(data)-p) < h.PayloadSize {
		return h, nil, fmt.Errorf("потік обірваний: є %d байт, має бути %d", len(data)-p, h.PayloadSize)
	}
	return h, data[p : p+int(h.PayloadSize)], nil
}

// Pack стискає байти файлу й формує .qra. ext — розширення без крапки.
func Pack(orig []byte, ext string, qrVersion int, noEcc bool) ([]byte, Header, error) {
	if len(ext) > 255 {
		ext = ext[:255]
	}
	// Паралельно: CRC32 (hash/crc32 використовує SIMD-інструкції CLMUL/SSE4.2) і deflate.
	crcCh := make(chan uint32, 1)
	go func() { crcCh <- crc32.ChecksumIEEE(orig) }()
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return nil, Header{}, err
	}
	if _, err := w.Write(orig); err != nil {
		return nil, Header{}, err
	}
	if err := w.Close(); err != nil {
		return nil, Header{}, err
	}
	payload, codec := buf.Bytes(), uint8(codecDeflate)
	if len(payload) >= len(orig) {
		payload, codec = orig, codecStore
	}
	h := Header{Codec: codec, Ext: ext, OrigSize: uint64(len(orig)), CRC: <-crcCh, PayloadSize: uint64(len(payload))}
	if noEcc {
		h.Flags |= flagNoEcc
	}
	total := h.size() + len(payload)
	if qrVersion == 0 {
		qrVersion = ChooseVersion(total, noEcc)
	}
	h.QRVersion = uint8(qrVersion)
	out := append(h.marshal(), payload...)
	return out, h, nil
}

// Unpack відновлює вихідний файл з .qra і перевіряє розмір та CRC32.
func Unpack(data []byte) ([]byte, Header, error) {
	h, payload, err := ParseQRA(data)
	if err != nil {
		return nil, h, err
	}
	var orig []byte
	switch h.Codec {
	case codecStore:
		orig = payload
	case codecDeflate:
		orig, err = io.ReadAll(flate.NewReader(bytes.NewReader(payload)))
		if err != nil {
			return nil, h, fmt.Errorf("deflate: %w", err)
		}
	default:
		return nil, h, fmt.Errorf("невідомий кодек %d", h.Codec)
	}
	if uint64(len(orig)) != h.OrigSize {
		return nil, h, fmt.Errorf("розмір після розпакування %d, у заголовку %d", len(orig), h.OrigSize)
	}
	if crc32.ChecksumIEEE(orig) != h.CRC {
		return nil, h, errors.New("CRC32 не збігається: файл пошкоджений")
	}
	return orig, h, nil
}
