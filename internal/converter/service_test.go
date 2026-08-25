package converter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/klippa-app/go-pdfium/responses"
)

func TestValidateEPUBUsesASCIITemporaryPath(t *testing.T) {
	validatorDir := t.TempDir()
	validatorPath := filepath.Join(validatorDir, "epubcheck-test")
	validator := `#!/bin/sh
LC_ALL=C
case "$1" in
  *[!\ -~]*)
    echo "validator received a non-ASCII path" >&2
    exit 9
    ;;
esac
cmp "$1" "$EXPECTED_EPUB"
`
	if err := os.WriteFile(validatorPath, []byte(validator), 0o755); err != nil {
		t.Fatalf("write validator: %v", err)
	}

	bookDir := t.TempDir()
	bookPath := filepath.Join(bookDir, "中文书名.epub")
	if err := os.WriteFile(bookPath, []byte("valid epub fixture"), 0o600); err != nil {
		t.Fatalf("write EPUB: %v", err)
	}
	t.Setenv("EXPECTED_EPUB", bookPath)

	service := &Service{config: Config{EPUBCheckCommand: validatorPath, RequireEPUBCheck: true}}
	if err := service.validateEPUB(context.Background(), bookPath); err != nil {
		t.Fatalf("validate EPUB with Unicode source path: %v", err)
	}

	entries, err := os.ReadDir(bookDir)
	if err != nil {
		t.Fatalf("read book directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(bookPath) {
		t.Fatalf("temporary validation files were not cleaned: %#v", entries)
	}
}

func TestPlainTextToLinesDoesNotUseFragmentedRectText(t *testing.T) {
	size := 11.0
	lines := plainTextToLines("Generative AI\r\nturns ideas into products.", []*responses.GetPageTextStructuredRect{
		{Text: "G", FontInformation: &responses.FontInformation{Size: size}},
		{Text: "e", FontInformation: &responses.FontInformation{Size: size}},
	})
	if len(lines) != 2 || lines[0].Text != "Generative AI" || lines[1].Text != "turns ideas into products." {
		t.Fatalf("unexpected lines: %#v", lines)
	}
}

func TestStructuredLinesGroupsFragmentsOnSameVisualLine(t *testing.T) {
	font := &responses.FontInformation{Size: 10}
	rects := []*responses.GetPageTextStructuredRect{
		{Text: "Hello", PointPosition: responses.CharPosition{Left: 10, Right: 35, Top: 100, Bottom: 90}, FontInformation: font},
		{Text: "world", PointPosition: responses.CharPosition{Left: 39, Right: 65, Top: 100, Bottom: 90}, FontInformation: font},
		{Text: "第二行", PointPosition: responses.CharPosition{Left: 10, Right: 40, Top: 80, Bottom: 70}, FontInformation: font},
	}
	lines := structuredLines(rects)
	if len(lines) != 2 || lines[0].Text != "Hello world" || lines[1].Text != "第二行" {
		t.Fatalf("unexpected lines: %#v", lines)
	}
}
