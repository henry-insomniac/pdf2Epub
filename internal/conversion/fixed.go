package conversion

import (
	"strings"
	"unicode"
)

// FixedLayoutBook keeps each source page as one viewport-sized image. EPUB
// readers can pair the declared left/right pages into a two-page spread.
func FixedLayoutBook(document Document, pageImages []Image) Book {
	language := strings.TrimSpace(document.Language)
	if language == "" {
		language = titleLanguage(document.Title)
	}
	book := Book{
		Title:    strings.TrimSpace(document.Title),
		Author:   strings.TrimSpace(document.Author),
		Language: language,
		Outline:  append([]OutlineItem(nil), document.Outline...),
		Cover:    document.Cover,
		Layout:   LayoutFixed,
	}
	for index, pageImage := range pageImages {
		page := index + 1
		book.Images = append(book.Images, pageImage)
		book.Sections = append(book.Sections, Section{
			Page:           page,
			ViewportWidth:  pageImage.Width,
			ViewportHeight: pageImage.Height,
			Blocks: []Block{{
				Kind:       BlockImage,
				ImageName:  pageImage.Name,
				SourcePage: page,
			}},
		})
	}
	if len(book.Outline) == 0 && len(book.Sections) > 0 {
		book.Outline = []OutlineItem{{Title: "正文", Page: 1, Level: 1}}
	}
	return book
}

func titleLanguage(title string) string {
	for _, r := range title {
		if unicode.In(r, unicode.Han) {
			return "zh"
		}
	}
	return "und"
}
