package epub

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pdf2epub/internal/conversion"
)

func TestWriterProducesValidEPUBStructure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "book.epub")
	book := conversion.Book{
		Title: "测试 & Book", Author: "作者", Language: "zh", Cover: conversion.Image{Name: "cover.jpg", MediaType: "image/jpeg", Data: []byte("image")},
		Sections: []conversion.Section{{Page: 1, Blocks: []conversion.Block{{Kind: conversion.BlockHeading, Text: "第一章", Level: 1}, {Kind: conversion.BlockParagraph, Text: "正文 <ok>"}}}},
		Outline:  []conversion.OutlineItem{{Title: "第一章", Page: 1, Level: 1}},
	}
	writer := Writer{Now: func() time.Time { return time.Date(2026, 8, 25, 1, 2, 3, 0, time.UTC) }}
	if err := writer.Write(path, book); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStructure(path); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if archive.File[0].Name != "mimetype" || archive.File[0].Method != zip.Store {
		t.Fatal("mimetype is not first and stored")
	}
	data := readEntry(t, archive, "EPUB/sections/section-0001.xhtml")
	if !strings.Contains(data, "正文 &lt;ok&gt;") {
		t.Fatalf("section was not XML escaped: %s", data)
	}
}

func TestWriterRemovesPartialFileOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.epub")
	err := (Writer{}).Write(path, conversion.Book{})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("partial artifact exists: %v", statErr)
	}
}

func readEntry(t *testing.T, archive *zip.ReadCloser, name string) string {
	t.Helper()
	for _, file := range archive.File {
		if file.Name != name {
			continue
		}
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		data, err := io.ReadAll(stream)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	t.Fatalf("entry %s not found", name)
	return ""
}
