package converter

import (
	"testing"

	"github.com/klippa-app/go-pdfium/responses"
)

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
