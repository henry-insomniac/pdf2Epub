package conversion

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var (
	listPrefix    = regexp.MustCompile(`^\s*(?:[-*•]|\d+[.)]|[（(]?[一二三四五六七八九十]+[）)、.])\s+`)
	pageNumber    = regexp.MustCompile(`^\s*(?:第\s*)?\d{1,4}(?:\s*页)?\s*$`)
	headingPrefix = regexp.MustCompile(`^\s*(?:第[一二三四五六七八九十百]+[章节篇部]|\d+(?:\.\d+){0,3}\s+)`)
)

func Rebuild(document Document) Book {
	book := Book{
		Title: strings.TrimSpace(document.Title), Author: strings.TrimSpace(document.Author),
		Language: document.Language, Outline: document.Outline, Cover: document.Cover, Layout: LayoutReflowable,
	}
	if book.Language == "" {
		book.Language = detectLanguage(document)
	}
	repeated := repeatedMargins(document.Pages)
	fontSizes := collectFontSizes(document.Pages)
	median := median(fontSizes)

	for _, page := range document.Pages {
		section := Section{Page: page.Number}
		lines := cleanLines(page.Lines, repeated)
		for i := 0; i < len(lines); {
			line := lines[i]
			if isHeading(line, median) {
				section.Blocks = append(section.Blocks, Block{Kind: BlockHeading, Text: line.Text, Level: headingLevel(line, median), SourcePage: page.Number})
				i++
				continue
			}
			if listPrefix.MatchString(line.Text) {
				section.Blocks = append(section.Blocks, Block{Kind: BlockListItem, Text: strings.TrimSpace(listPrefix.ReplaceAllString(line.Text, "")), SourcePage: page.Number})
				i++
				continue
			}
			paragraph := line.Text
			i++
			for i < len(lines) && !lines[i].BreakBefore && !isHeading(lines[i], median) && !listPrefix.MatchString(lines[i].Text) {
				paragraph = joinVisualLines(paragraph, lines[i].Text)
				i++
			}
			if strings.TrimSpace(paragraph) != "" {
				section.Blocks = append(section.Blocks, Block{Kind: BlockParagraph, Text: strings.TrimSpace(paragraph), SourcePage: page.Number})
			}
		}
		for _, image := range page.Images {
			book.Images = append(book.Images, image)
			section.Blocks = append(section.Blocks, Block{Kind: BlockImage, ImageName: image.Name, SourcePage: page.Number})
		}
		mergeAcrossPage(book.Sections, &section)
		book.Sections = append(book.Sections, section)
	}
	if len(document.Outline) == 0 {
		book.Outline = inferOutline(book.Sections)
	}
	return book
}

func mergeAcrossPage(previous []Section, current *Section) {
	if len(previous) == 0 || len(current.Blocks) == 0 || current.Blocks[0].Kind != BlockParagraph {
		return
	}
	lastSection := &previous[len(previous)-1]
	if len(lastSection.Blocks) == 0 {
		return
	}
	last := &lastSection.Blocks[len(lastSection.Blocks)-1]
	if last.Kind != BlockParagraph {
		return
	}
	last.Text = joinVisualLines(last.Text, current.Blocks[0].Text)
	current.Blocks = current.Blocks[1:]
}

func repeatedMargins(pages []Page) map[string]bool {
	counts := make(map[string]int)
	for _, page := range pages {
		if len(page.Lines) == 0 {
			continue
		}
		seen := make(map[string]bool)
		for _, index := range []int{0, len(page.Lines) - 1} {
			key := normalizeMargin(page.Lines[index].Text)
			if key != "" && !seen[key] {
				counts[key]++
				seen[key] = true
			}
		}
	}
	repeated := make(map[string]bool)
	threshold := maxInt(3, (len(pages)*3+4)/5)
	for text, count := range counts {
		if count >= threshold {
			repeated[text] = true
		}
	}
	return repeated
}

func cleanLines(lines []Line, repeated map[string]bool) []Line {
	result := make([]Line, 0, len(lines))
	for i, line := range lines {
		line.Text = strings.TrimSpace(line.Text)
		if line.Text == "" {
			continue
		}
		atMargin := i == 0 || i == len(lines)-1
		if atMargin && (pageNumber.MatchString(line.Text) || repeated[normalizeMargin(line.Text)]) {
			continue
		}
		result = append(result, line)
	}
	return result
}

func joinVisualLines(left, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	if strings.HasSuffix(left, "-") && startsASCIIWord(right) {
		return strings.TrimSuffix(left, "-") + right
	}
	if endsCJK(left) || startsCJK(right) {
		return left + right
	}
	return left + " " + right
}

func isHeading(line Line, median float64) bool {
	text := strings.TrimSpace(line.Text)
	if len([]rune(text)) > 80 || strings.HasSuffix(text, "。") || strings.HasSuffix(text, ".") {
		return false
	}
	return headingPrefix.MatchString(text) || (median > 0 && line.FontSize >= median*1.28)
}

func headingLevel(line Line, median float64) int {
	if median > 0 && line.FontSize >= median*1.8 {
		return 1
	}
	if median > 0 && line.FontSize >= median*1.45 {
		return 2
	}
	return 3
}

func inferOutline(sections []Section) []OutlineItem {
	var outline []OutlineItem
	for _, section := range sections {
		for _, block := range section.Blocks {
			if block.Kind == BlockHeading {
				outline = append(outline, OutlineItem{Title: block.Text, Page: section.Page, Level: block.Level})
				if len(outline) >= 200 {
					return outline
				}
			}
		}
	}
	if len(outline) == 0 && len(sections) > 0 {
		return []OutlineItem{{Title: "正文", Page: sections[0].Page, Level: 1}}
	}
	return outline
}

func detectLanguage(document Document) string {
	var cjk, latin int
	for _, page := range document.Pages {
		for _, line := range page.Lines {
			for _, r := range line.Text {
				switch {
				case unicode.In(r, unicode.Han):
					cjk++
				case unicode.Is(unicode.Latin, r):
					latin++
				}
			}
		}
	}
	if cjk > latin/4 {
		return "zh"
	}
	return "en"
}

func collectFontSizes(pages []Page) []float64 {
	var sizes []float64
	for _, page := range pages {
		for _, line := range page.Lines {
			if line.FontSize > 0 {
				sizes = append(sizes, line.FontSize)
			}
		}
	}
	return sizes
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	return values[len(values)/2]
}

func normalizeMargin(text string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(text))), " ")
}

func startsASCIIWord(text string) bool {
	runes := []rune(text)
	return len(runes) > 0 && ((runes[0] >= 'A' && runes[0] <= 'Z') || (runes[0] >= 'a' && runes[0] <= 'z'))
}

func endsCJK(text string) bool {
	runes := []rune(text)
	return len(runes) > 0 && unicode.In(runes[len(runes)-1], unicode.Han)
}

func startsCJK(text string) bool {
	runes := []rune(text)
	return len(runes) > 0 && unicode.In(runes[0], unicode.Han)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
