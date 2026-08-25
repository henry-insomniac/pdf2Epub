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

func TestWriterProducesFixedLayoutTwoPageSpread(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixed.epub")
	book := conversion.Book{
		Title: "固定版式", Language: "zh", Layout: conversion.LayoutFixed,
		Cover: conversion.Image{Name: "cover.jpg", MediaType: "image/jpeg", Data: []byte("cover")},
		Images: []conversion.Image{
			{Name: "page-0001.jpg", MediaType: "image/jpeg", Data: []byte("page1"), Width: 1200, Height: 1680},
			{Name: "page-0002.jpg", MediaType: "image/jpeg", Data: []byte("page2"), Width: 1200, Height: 1680},
		},
		Sections: []conversion.Section{
			{Page: 1, ViewportWidth: 1200, ViewportHeight: 1680, Blocks: []conversion.Block{{Kind: conversion.BlockImage, ImageName: "page-0001.jpg", SourcePage: 1}}},
			{Page: 2, ViewportWidth: 1200, ViewportHeight: 1680, Blocks: []conversion.Block{{Kind: conversion.BlockImage, ImageName: "page-0002.jpg", SourcePage: 2}}},
		},
	}
	if err := (Writer{Now: func() time.Time { return time.Date(2026, 8, 25, 1, 2, 3, 0, time.UTC) }}).Write(path, book); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	opf := readEntry(t, archive, "EPUB/package.opf")
	for _, expected := range []string{
		`<meta property="rendition:layout">pre-paginated</meta>`,
		`<meta property="rendition:spread">both</meta>`,
		`page-progression-direction="ltr"`,
		`properties="page-spread-right"`,
		`properties="page-spread-left"`,
	} {
		if !strings.Contains(opf, expected) {
			t.Fatalf("package.opf missing %q: %s", expected, opf)
		}
	}
	page := readEntry(t, archive, "EPUB/sections/section-0001.xhtml")
	if !strings.Contains(page, `meta name="viewport" content="width=1200, height=1680"`) || !strings.Contains(page, `class="fixed-page"`) {
		t.Fatalf("fixed page metadata missing: %s", page)
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
