package conversion

import "testing"

func TestFixedLayoutBookKeepsOneImagePerSourcePage(t *testing.T) {
	book := FixedLayoutBook(Document{Title: "中文图书", Author: "作者"}, []Image{
		{Name: "page-0001.jpg", Width: 1200, Height: 1680},
		{Name: "page-0002.jpg", Width: 1200, Height: 1680},
	})
	if book.Layout != LayoutFixed || book.Language != "zh" {
		t.Fatalf("book layout/language = %q/%q", book.Layout, book.Language)
	}
	if len(book.Sections) != 2 || book.Sections[1].Page != 2 {
		t.Fatalf("sections = %#v", book.Sections)
	}
	if got := book.Sections[0].Blocks[0].ImageName; got != "page-0001.jpg" {
		t.Fatalf("first page image = %q", got)
	}
	if len(book.Outline) != 1 || book.Outline[0].Title != "正文" {
		t.Fatalf("outline = %#v", book.Outline)
	}
}
