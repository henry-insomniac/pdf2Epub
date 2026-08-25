package conversion

import "testing"

func TestJoinVisualLines(t *testing.T) {
	tests := []struct{ left, right, want string }{
		{"这是第一", "行正文。", "这是第一行正文。"},
		{"hello", "world", "hello world"},
		{"conver-", "sion", "conversion"},
	}
	for _, test := range tests {
		if got := joinVisualLines(test.left, test.right); got != test.want {
			t.Fatalf("joinVisualLines(%q, %q) = %q, want %q", test.left, test.right, got, test.want)
		}
	}
}

func TestRebuildRemovesRepeatedMarginsAndInfersHeading(t *testing.T) {
	doc := Document{Title: "Test", Pages: []Page{
		{Number: 1, Lines: []Line{{Text: "Sample Book", FontSize: 8}, {Text: "第一章 开始", FontSize: 20}, {Text: "这是第一", FontSize: 10}, {Text: "段正文。", FontSize: 10}, {Text: "1", FontSize: 8}}},
		{Number: 2, Lines: []Line{{Text: "Sample Book", FontSize: 8}, {Text: "第二页正文", FontSize: 10}, {Text: "2", FontSize: 8}}},
		{Number: 3, Lines: []Line{{Text: "Sample Book", FontSize: 8}, {Text: "第三页正文", FontSize: 10}, {Text: "3", FontSize: 8}}},
	}}
	book := Rebuild(doc)
	if len(book.Outline) == 0 || book.Outline[0].Title != "第一章 开始" {
		t.Fatalf("unexpected outline: %#v", book.Outline)
	}
	for _, section := range book.Sections {
		for _, block := range section.Blocks {
			if block.Text == "Sample Book" || block.Text == "1" || block.Text == "2" || block.Text == "3" {
				t.Fatalf("margin leaked into content: %#v", block)
			}
		}
	}
}

func TestRebuildMergesParagraphAcrossPageBoundary(t *testing.T) {
	book := Rebuild(Document{Title: "Test", Pages: []Page{
		{Number: 1, Lines: []Line{{Text: "A paragraph continues"}}},
		{Number: 2, Lines: []Line{{Text: "on the next page."}}},
	}})
	if got := book.Sections[0].Blocks[0].Text; got != "A paragraph continues on the next page." {
		t.Fatalf("cross-page paragraph = %q", got)
	}
	if len(book.Sections[1].Blocks) != 0 {
		t.Fatalf("second page retained duplicate paragraph: %#v", book.Sections[1].Blocks)
	}
}
