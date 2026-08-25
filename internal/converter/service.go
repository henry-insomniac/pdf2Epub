package converter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/enums"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/responses"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"

	"pdf2epub/internal/app"
	"pdf2epub/internal/conversion"
	"pdf2epub/internal/domain"
	"pdf2epub/internal/epub"
)

type Config struct {
	MaxPages             int
	EPUBCheckCommand     string
	RequireEPUBCheck     bool
	CoverDPI             int
	IllustrationDPI      int
	MaxIllustrationsPage int
}

type Service struct {
	config Config
}

func New(config Config) (*Service, error) {
	if config.MaxPages <= 0 {
		return nil, errors.New("maximum page count must be positive")
	}
	if config.CoverDPI == 0 {
		config.CoverDPI = 120
	}
	if config.IllustrationDPI == 0 {
		config.IllustrationDPI = 110
	}
	if config.MaxIllustrationsPage == 0 {
		config.MaxIllustrationsPage = 12
	}
	if config.RequireEPUBCheck && strings.TrimSpace(config.EPUBCheckCommand) == "" {
		return nil, errors.New("EPUBCheck command is required")
	}
	return &Service{config: config}, nil
}

func (s *Service) Convert(ctx context.Context, request app.ConversionRequest, reporter app.Reporter) (app.ConversionResult, error) {
	reporter.Progress(domain.StagePreflight, 0)
	document, warnings, err := s.extract(ctx, request.SourcePath, request.SourceName, reporter)
	if err != nil {
		return app.ConversionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return app.ConversionResult{}, err
	}
	reporter.Progress(domain.StageRebuilding, len(document.Pages))
	book := conversion.Rebuild(document)
	for _, warning := range warnings {
		book.Warnings = append(book.Warnings, warning)
	}

	reporter.Progress(domain.StagePackaging, len(document.Pages))
	outputName := epub.SafeOutputName(request.SourceName)
	outputPath := filepath.Join(request.JobDir, outputName)
	if err := (epub.Writer{}).Write(outputPath, book); err != nil {
		return app.ConversionResult{}, failure("output.package_failed", "无法完整生成 EPUB 文件。", 0, err)
	}
	if err := epub.ValidateStructure(outputPath); err != nil {
		_ = os.Remove(outputPath)
		return app.ConversionResult{}, failure("output.invalid_epub", "生成的 EPUB 结构无效，已停止提供下载。", 0, err)
	}

	reporter.Progress(domain.StageValidating, len(document.Pages))
	if err := s.validateEPUB(ctx, outputPath); err != nil {
		_ = os.Remove(outputPath)
		return app.ConversionResult{}, failure("output.epubcheck_failed", "EPUB 未通过规范校验，已停止提供下载。", 0, err)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		return app.ConversionResult{}, failure("output.unreadable", "生成的 EPUB 无法读取。", 0, err)
	}
	result := app.ConversionResult{Artifact: domain.Artifact{Name: outputName, Path: outputPath, Size: info.Size()}}
	for _, warning := range book.Warnings {
		result.Warnings = append(result.Warnings, domain.Warning{Code: warning.Code, Message: warning.Message, Page: warning.Page})
	}
	return result, nil
}

func (s *Service) extract(ctx context.Context, sourcePath, sourceName string, reporter app.Reporter) (conversion.Document, []conversion.Warning, error) {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return conversion.Document{}, nil, failure("input.unreadable", "无法读取上传的 PDF。", 0, err)
	}
	pool, err := webassembly.Init(webassembly.Config{
		Context: ctx, MinIdle: 0, MaxIdle: 1, MaxTotal: 1,
		FSConfig:      wazero.NewFSConfig(),
		RuntimeConfig: wazero.NewRuntimeConfig().WithCloseOnContextDone(true),
	})
	if err != nil {
		return conversion.Document{}, nil, failure("engine.unavailable", "PDF 解析引擎启动失败。", 0, err)
	}
	defer pool.Close()
	instance, err := pool.GetInstanceWithContext(ctx)
	if err != nil {
		return conversion.Document{}, nil, err
	}
	defer instance.Close()

	opened, err := instance.OpenDocument(&requests.OpenDocument{File: &data})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "password") {
			return conversion.Document{}, nil, failure("input.protected_pdf", "不支持加密或需要密码的 PDF。", 0, err)
		}
		return conversion.Document{}, nil, failure("input.invalid_pdf", "PDF 已损坏或无法解析。", 0, err)
	}
	defer instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: opened.Document})

	security, err := instance.FPDF_GetSecurityHandlerRevision(&requests.FPDF_GetSecurityHandlerRevision{Document: opened.Document})
	if err != nil {
		return conversion.Document{}, nil, failure("input.security_unknown", "无法确认 PDF 的安全限制。", 0, err)
	}
	permissions, err := instance.FPDF_GetDocPermissions(&requests.FPDF_GetDocPermissions{Document: opened.Document})
	if err != nil {
		return conversion.Document{}, nil, failure("input.security_unknown", "无法确认 PDF 的复制和提取权限。", 0, err)
	}
	if security.SecurityHandlerRevision != -1 || (!permissions.CopyOrExtractText && !permissions.ExtractTextAndGraphics) {
		return conversion.Document{}, nil, failure("input.protected_pdf", "不支持加密或禁止内容提取的 PDF。", 0, nil)
	}
	pageCount, err := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: opened.Document})
	if err != nil || pageCount.PageCount <= 0 {
		return conversion.Document{}, nil, failure("input.no_pages", "PDF 没有可处理的页面。", 0, err)
	}
	if pageCount.PageCount > s.config.MaxPages {
		return conversion.Document{}, nil, failure("input.too_many_pages", fmt.Sprintf("PDF 不能超过 %d 页。", s.config.MaxPages), 0, nil)
	}
	reporter.SetTotalPages(pageCount.PageCount)

	title := meta(instance, opened.Document, "Title")
	if title == "" {
		title = strings.TrimSpace(strings.TrimSuffix(filepath.Base(sourceName), filepath.Ext(sourceName)))
	}
	document := conversion.Document{Title: title, Author: meta(instance, opened.Document, "Author")}
	document.Outline = extractOutline(instance, opened.Document, pageCount.PageCount)
	document.Cover, err = renderCover(instance, opened.Document, s.config.CoverDPI)
	if err != nil {
		return conversion.Document{}, nil, failure("input.cover_failed", "无法从 PDF 第一页生成封面。", 1, err)
	}

	var warnings []conversion.Warning
	for pageIndex := 0; pageIndex < pageCount.PageCount; pageIndex++ {
		if err := ctx.Err(); err != nil {
			return conversion.Document{}, nil, err
		}
		reporter.Progress(domain.StageExtracting, pageIndex)
		page, pageWarnings, err := s.extractPage(instance, opened.Document, pageIndex)
		if err != nil {
			return conversion.Document{}, nil, err
		}
		document.Pages = append(document.Pages, page)
		warnings = append(warnings, pageWarnings...)
		reporter.Progress(domain.StageExtracting, pageIndex+1)
	}
	return document, warnings, nil
}

func (s *Service) extractPage(instance pdfium.Pdfium, documentRef references.FPDF_DOCUMENT, pageIndex int) (conversion.Page, []conversion.Warning, error) {
	pageRequest := requests.Page{ByIndex: &requests.PageByIndex{Document: documentRef, Index: pageIndex}}
	structured, err := instance.GetPageTextStructured(&requests.GetPageTextStructured{Page: pageRequest, Mode: requests.GetPageTextStructuredModeRects, CollectFontInformation: true})
	if err != nil {
		return conversion.Page{}, nil, failure("input.text_extraction_failed", "无法提取页面文字。", pageIndex+1, err)
	}
	plain, err := instance.GetPageText(&requests.GetPageText{Page: pageRequest})
	if err != nil {
		return conversion.Page{}, nil, failure("input.text_extraction_failed", "无法提取页面连续文字。", pageIndex+1, err)
	}
	page := conversion.Page{Number: pageIndex + 1, Lines: plainTextToLines(plain.Text, structured.Rects)}
	size, err := instance.GetPageSize(&requests.GetPageSize{Page: pageRequest})
	if err != nil {
		return conversion.Page{}, nil, failure("input.page_size_failed", "无法读取页面尺寸。", pageIndex+1, err)
	}
	regions, err := imageRegions(instance, pageRequest, size.Width, size.Height, s.config.MaxIllustrationsPage)
	if err != nil {
		return conversion.Page{}, nil, failure("input.object_inspection_failed", "无法确认页面内容是否完整。", pageIndex+1, err)
	}
	visibleText := strings.TrimSpace(joinLineText(page.Lines))
	if visibleText == "" && pageIndex > 0 {
		largest := largestCoverage(regions)
		if largest >= 0.55 || len(regions) == 0 {
			return conversion.Page{}, nil, failure("input.missing_text_page", "该页没有可提取文字，可能是扫描页或整页图片；V1 不提供 OCR。", pageIndex+1, nil)
		}
	}
	if len(regions) == 0 {
		return page, nil, nil
	}
	images, err := renderRegions(instance, pageRequest, pageIndex, size.Width, size.Height, regions, s.config.IllustrationDPI)
	if err != nil {
		return conversion.Page{}, nil, failure("input.image_extraction_failed", "无法完整保留页面插图。", pageIndex+1, err)
	}
	page.Images = images
	return page, []conversion.Warning{{Code: "content.image_position_approximated", Message: "页面插图已保留，但在 EPUB 中的位置按源页末尾近似安排。", Page: pageIndex + 1}}, nil
}

func (s *Service) validateEPUB(ctx context.Context, path string) error {
	command := strings.TrimSpace(s.config.EPUBCheckCommand)
	if command == "" {
		if s.config.RequireEPUBCheck {
			return errors.New("EPUBCheck command is not configured")
		}
		return nil
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		if !s.config.RequireEPUBCheck {
			return nil
		}
		return err
	}
	cmd := exec.CommandContext(ctx, resolved, path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("epubcheck: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func failure(code, message string, page int, cause error) error {
	conversionFailure := app.ConversionFailure{Failure: domain.Failure{Code: code, Message: message, Page: page}}
	if cause == nil {
		return conversionFailure
	}
	return fmt.Errorf("%w: %v", conversionFailure, cause)
}

func meta(instance pdfium.Pdfium, document references.FPDF_DOCUMENT, tag string) string {
	response, err := instance.FPDF_GetMetaText(&requests.FPDF_GetMetaText{Document: document, Tag: tag})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(response.Value)
}

func extractOutline(instance pdfium.Pdfium, document references.FPDF_DOCUMENT, pages int) []conversion.OutlineItem {
	response, err := instance.GetBookmarks(&requests.GetBookmarks{Document: document})
	if err != nil {
		return nil
	}
	var result []conversion.OutlineItem
	var walk func([]responses.GetBookmarksBookmark, int)
	walk = func(items []responses.GetBookmarksBookmark, level int) {
		for _, item := range items {
			page := bookmarkPage(item)
			if strings.TrimSpace(item.Title) != "" && page >= 0 && page < pages {
				result = append(result, conversion.OutlineItem{Title: strings.TrimSpace(item.Title), Page: page + 1, Level: level})
			}
			walk(item.Children, level+1)
		}
	}
	walk(response.Bookmarks, 1)
	return result
}

func bookmarkPage(bookmark responses.GetBookmarksBookmark) int {
	if bookmark.DestInfo != nil {
		return bookmark.DestInfo.PageIndex
	}
	if bookmark.ActionInfo != nil && bookmark.ActionInfo.DestInfo != nil {
		return bookmark.ActionInfo.DestInfo.PageIndex
	}
	return -1
}

func renderCover(instance pdfium.Pdfium, document references.FPDF_DOCUMENT, dpi int) (conversion.Image, error) {
	response, err := instance.RenderPageInDPI(&requests.RenderPageInDPI{Page: requests.Page{ByIndex: &requests.PageByIndex{Document: document, Index: 0}}, DPI: dpi})
	if err != nil {
		return conversion.Image{}, err
	}
	defer response.Cleanup()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, response.Result.RenderedImage, &jpeg.Options{Quality: 86}); err != nil {
		return conversion.Image{}, err
	}
	return conversion.Image{Name: "cover.jpg", MediaType: "image/jpeg", Data: buffer.Bytes()}, nil
}

type imageRegion struct {
	left, bottom, right, top float64
	coverage                 float64
}

func imageRegions(instance pdfium.Pdfium, page requests.Page, width, height float64, limit int) ([]imageRegion, error) {
	count, err := instance.FPDFPage_CountObjects(&requests.FPDFPage_CountObjects{Page: page})
	if err != nil {
		return nil, err
	}
	var regions []imageRegion
	for index := 0; index < count.Count && len(regions) < limit; index++ {
		object, err := instance.FPDFPage_GetObject(&requests.FPDFPage_GetObject{Page: page, Index: index})
		if err != nil {
			return nil, err
		}
		objectType, err := instance.FPDFPageObj_GetType(&requests.FPDFPageObj_GetType{PageObject: object.PageObject})
		if err != nil {
			return nil, err
		}
		if objectType.Type != enums.FPDF_PAGEOBJ_IMAGE {
			continue
		}
		bounds, err := instance.FPDFPageObj_GetBounds(&requests.FPDFPageObj_GetBounds{PageObject: object.PageObject})
		if err != nil {
			return nil, err
		}
		coverage := float64((bounds.Right-bounds.Left)*(bounds.Top-bounds.Bottom)) / (width * height)
		if coverage < 0.025 {
			continue
		}
		regions = append(regions, imageRegion{left: float64(bounds.Left), bottom: float64(bounds.Bottom), right: float64(bounds.Right), top: float64(bounds.Top), coverage: coverage})
	}
	return regions, nil
}

func renderRegions(instance pdfium.Pdfium, page requests.Page, pageIndex int, width, height float64, regions []imageRegion, dpi int) ([]conversion.Image, error) {
	response, err := instance.RenderPageInDPI(&requests.RenderPageInDPI{Page: page, DPI: dpi})
	if err != nil {
		return nil, err
	}
	defer response.Cleanup()
	imageBounds := response.Result.RenderedImage.Bounds()
	ratioX := float64(imageBounds.Dx()) / width
	ratioY := float64(imageBounds.Dy()) / height
	var images []conversion.Image
	for index, region := range regions {
		rectangle := image.Rect(
			clamp(int(region.left*ratioX), 0, imageBounds.Dx()),
			clamp(int((height-region.top)*ratioY), 0, imageBounds.Dy()),
			clamp(int(region.right*ratioX), 0, imageBounds.Dx()),
			clamp(int((height-region.bottom)*ratioY), 0, imageBounds.Dy()),
		)
		if rectangle.Dx() < 8 || rectangle.Dy() < 8 {
			continue
		}
		cropped := image.NewRGBA(image.Rect(0, 0, rectangle.Dx(), rectangle.Dy()))
		for y := 0; y < rectangle.Dy(); y++ {
			for x := 0; x < rectangle.Dx(); x++ {
				cropped.Set(x, y, response.Result.RenderedImage.At(rectangle.Min.X+x, rectangle.Min.Y+y))
			}
		}
		var buffer bytes.Buffer
		if err := png.Encode(&buffer, cropped); err != nil {
			return nil, err
		}
		images = append(images, conversion.Image{Name: fmt.Sprintf("page-%04d-image-%02d.png", pageIndex+1, index+1), MediaType: "image/png", Data: buffer.Bytes()})
	}
	return images, nil
}

func rectsToLines(rects []*responses.GetPageTextStructuredRect) []conversion.Line {
	var lines []conversion.Line
	for _, rect := range rects {
		if rect == nil {
			continue
		}
		fontSize := 0.0
		bold := false
		if rect.FontInformation != nil {
			fontSize = rect.FontInformation.RenderedSize
			if fontSize <= 0 {
				fontSize = rect.FontInformation.Size
			}
			bold = rect.FontInformation.Weight >= 600 || strings.Contains(strings.ToLower(rect.FontInformation.Name), "bold")
		}
		for _, text := range strings.FieldsFunc(rect.Text, func(r rune) bool { return r == '\r' || r == '\n' }) {
			text = strings.TrimSpace(text)
			if text != "" {
				lines = append(lines, conversion.Line{Text: text, FontSize: fontSize, Bold: bold})
			}
		}
	}
	return lines
}

func plainTextToLines(text string, rects []*responses.GetPageTextStructuredRect) []conversion.Line {
	defaultSize := medianRectFontSize(rects)
	structured := structuredLines(rects)
	matched := make(map[string]conversion.Line)
	for _, line := range structured {
		key := comparableLine(line.Text)
		if key != "" {
			matched[key] = line
		}
	}
	var lines []conversion.Line
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	pendingBreak := false
	for _, textLine := range strings.Split(text, "\n") {
		textLine = strings.TrimSpace(textLine)
		if textLine == "" {
			pendingBreak = len(lines) > 0
			continue
		}
		line := conversion.Line{Text: textLine, FontSize: defaultSize, BreakBefore: pendingBreak}
		if metadata, ok := matched[comparableLine(textLine)]; ok {
			line.FontSize = metadata.FontSize
			line.Bold = metadata.Bold
			line.BreakBefore = line.BreakBefore || metadata.BreakBefore
		}
		lines = append(lines, line)
		pendingBreak = false
	}
	return lines
}

func comparableLine(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

type positionedFragment struct {
	text             string
	left, right, y   float64
	height, fontSize float64
	bold             bool
}

func structuredLines(rects []*responses.GetPageTextStructuredRect) []conversion.Line {
	type group struct {
		fragments []positionedFragment
		y, height float64
	}
	var groups []group
	for _, rect := range rects {
		if rect == nil || strings.TrimSpace(rect.Text) == "" {
			continue
		}
		position := rect.PointPosition
		height := absFloat(position.Top - position.Bottom)
		fontSize, bold := rectFont(rect)
		fragment := positionedFragment{text: rect.Text, left: position.Left, right: position.Right, y: (position.Top + position.Bottom) / 2, height: height, fontSize: fontSize, bold: bold}
		matched := -1
		for index := len(groups) - 1; index >= 0 && index >= len(groups)-4; index-- {
			tolerance := maxFloat(1.5, maxFloat(height, groups[index].height)*0.32)
			if absFloat(groups[index].y-fragment.y) <= tolerance {
				matched = index
				break
			}
		}
		if matched < 0 {
			groups = append(groups, group{fragments: []positionedFragment{fragment}, y: fragment.y, height: height})
		} else {
			groups[matched].fragments = append(groups[matched].fragments, fragment)
		}
	}
	var lines []conversion.Line
	var previousY, previousHeight float64
	for groupIndex, current := range groups {
		sortFragments(current.fragments)
		var builder strings.Builder
		var size float64
		var bold bool
		for index, fragment := range current.fragments {
			if index > 0 && needsFragmentSpace(current.fragments[index-1], fragment) {
				builder.WriteByte(' ')
			}
			builder.WriteString(strings.TrimSpace(fragment.text))
			if fragment.fontSize > size {
				size = fragment.fontSize
			}
			bold = bold || fragment.bold
		}
		text := strings.TrimSpace(builder.String())
		if text == "" {
			continue
		}
		breakBefore := groupIndex > 0 && absFloat(current.y-previousY) > maxFloat(current.height, previousHeight)*1.65
		lines = append(lines, conversion.Line{Text: text, FontSize: size, Bold: bold, BreakBefore: breakBefore})
		previousY, previousHeight = current.y, current.height
	}
	return lines
}

func credibleStructuredLines(lines []conversion.Line, plain string) bool {
	plainRunes := textRuneCount(plain)
	if plainRunes == 0 {
		return len(lines) == 0
	}
	structuredRunes := 0
	for _, line := range lines {
		structuredRunes += textRuneCount(line.Text)
	}
	ratio := float64(structuredRunes) / float64(plainRunes)
	return ratio >= 0.82 && ratio <= 1.18 && len(lines) <= 400
}

func rectFont(rect *responses.GetPageTextStructuredRect) (float64, bool) {
	if rect.FontInformation == nil {
		return 0, false
	}
	size := rect.FontInformation.RenderedSize
	if size <= 0 {
		size = rect.FontInformation.Size
	}
	name := strings.ToLower(rect.FontInformation.Name)
	return size, rect.FontInformation.Weight >= 600 || strings.Contains(name, "bold")
}

func sortFragments(fragments []positionedFragment) {
	for i := 1; i < len(fragments); i++ {
		for j := i; j > 0 && fragments[j].left < fragments[j-1].left; j-- {
			fragments[j], fragments[j-1] = fragments[j-1], fragments[j]
		}
	}
}

func needsFragmentSpace(left, right positionedFragment) bool {
	if strings.HasSuffix(left.text, " ") || strings.HasPrefix(right.text, " ") {
		return true
	}
	leftText, rightText := strings.TrimSpace(left.text), strings.TrimSpace(right.text)
	if leftText == "" || rightText == "" || endsCJKText(leftText) || startsCJKText(rightText) {
		return false
	}
	fontSize := maxFloat(left.fontSize, right.fontSize)
	return fontSize > 0 && right.left-left.right > fontSize*0.12
}

func textRuneCount(text string) int {
	count := 0
	for _, r := range text {
		if !unicode.IsSpace(r) {
			count++
		}
	}
	return count
}

func startsCJKText(text string) bool {
	for _, r := range text {
		return unicode.In(r, unicode.Han)
	}
	return false
}

func endsCJKText(text string) bool {
	runes := []rune(text)
	return len(runes) > 0 && unicode.In(runes[len(runes)-1], unicode.Han)
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}

func medianRectFontSize(rects []*responses.GetPageTextStructuredRect) float64 {
	var values []float64
	for _, rect := range rects {
		if rect == nil || rect.FontInformation == nil {
			continue
		}
		size := rect.FontInformation.RenderedSize
		if size <= 0 {
			size = rect.FontInformation.Size
		}
		if size > 0 {
			values = append(values, size)
		}
	}
	if len(values) == 0 {
		return 0
	}
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
	return values[len(values)/2]
}

func joinLineText(lines []conversion.Line) string {
	var builder strings.Builder
	for _, line := range lines {
		builder.WriteString(line.Text)
	}
	return builder.String()
}

func largestCoverage(regions []imageRegion) float64 {
	var largest float64
	for _, region := range regions {
		if region.coverage > largest {
			largest = region.coverage
		}
	}
	return largest
}

func clamp(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
