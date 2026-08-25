package epub

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"pdf2epub/internal/conversion"
)

const mimetype = "application/epub+zip"

type Writer struct {
	Now func() time.Time
}

func (w Writer) Write(path string, book conversion.Book) error {
	if strings.TrimSpace(book.Title) == "" || len(book.Sections) == 0 {
		return errors.New("book title and at least one section are required")
	}
	if w.Now == nil {
		w.Now = time.Now
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create epub: %w", err)
	}
	archive := zip.NewWriter(file)
	fail := func(err error) error {
		_ = archive.Close()
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}

	if err := writeStored(archive, "mimetype", []byte(mimetype)); err != nil {
		return fail(err)
	}
	entries := map[string][]byte{
		"META-INF/container.xml": []byte(containerXML),
		"EPUB/styles.css":        []byte(stylesCSS),
		"EPUB/nav.xhtml":         []byte(navXHTML(book)),
		"EPUB/package.opf":       []byte(packageOPF(book, w.Now().UTC())),
	}
	for index, section := range book.Sections {
		entries[sectionPath(index)] = []byte(sectionXHTML(book, section, index))
	}
	if len(book.Cover.Data) > 0 {
		entries["EPUB/images/"+book.Cover.Name] = book.Cover.Data
	}
	for _, image := range book.Images {
		entries["EPUB/images/"+image.Name] = image.Data
	}
	for name, data := range entries {
		if err := writeDeflated(archive, name, data); err != nil {
			return fail(err)
		}
	}
	if err := archive.Close(); err != nil {
		return fail(fmt.Errorf("finish epub archive: %w", err))
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close epub: %w", err)
	}
	return nil
}

func ValidateStructure(path string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open epub: %w", err)
	}
	defer reader.Close()
	if len(reader.File) == 0 || reader.File[0].Name != "mimetype" || reader.File[0].Method != zip.Store {
		return errors.New("mimetype must be the first uncompressed entry")
	}
	required := map[string]bool{"META-INF/container.xml": false, "EPUB/package.opf": false, "EPUB/nav.xhtml": false}
	for _, file := range reader.File {
		if _, ok := required[file.Name]; ok {
			required[file.Name] = true
			stream, err := file.Open()
			if err != nil {
				return err
			}
			if err := validateXML(stream); err != nil {
				_ = stream.Close()
				return fmt.Errorf("parse %s: %w", file.Name, err)
			}
			_ = stream.Close()
		}
	}
	for name, found := range required {
		if !found {
			return fmt.Errorf("required entry %s is missing", name)
		}
	}
	return nil
}

func validateXML(reader io.Reader) error {
	decoder := xml.NewDecoder(reader)
	foundRoot := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if !foundRoot {
				return errors.New("XML document has no root element")
			}
			return nil
		}
		if err != nil {
			return err
		}
		if _, ok := token.(xml.StartElement); ok {
			foundRoot = true
		}
	}
}

func writeStored(archive *zip.Writer, name string, data []byte) error {
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	header.SetMode(0o600)
	entry, err := archive.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}

func writeDeflated(archive *zip.Writer, name string, data []byte) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0o600)
	entry, err := archive.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}

func packageOPF(book conversion.Book, modified time.Time) string {
	identifier := bookIdentifier(book)
	var manifest, spine strings.Builder
	manifest.WriteString(`<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="css" href="styles.css" media-type="text/css"/>`)
	if len(book.Cover.Data) > 0 {
		fmt.Fprintf(&manifest, `<item id="cover" href="images/%s" media-type="%s" properties="cover-image"/>`, escape(book.Cover.Name), escape(book.Cover.MediaType))
	}
	for index := range book.Sections {
		fmt.Fprintf(&manifest, `<item id="section-%d" href="sections/section-%04d.xhtml" media-type="application/xhtml+xml"/>`, index+1, index+1)
		fmt.Fprintf(&spine, `<itemref idref="section-%d"/>`, index+1)
	}
	for index, image := range book.Images {
		fmt.Fprintf(&manifest, `<item id="image-%d" href="images/%s" media-type="%s"/>`, index+1, escape(image.Name), escape(image.MediaType))
	}
	creator := ""
	if book.Author != "" {
		creator = `<dc:creator>` + escape(book.Author) + `</dc:creator>`
	}
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="book-id" version="3.0" xml:lang="` + escape(book.Language) + `">` +
		`<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="book-id">urn:sha256:` + identifier + `</dc:identifier><dc:title>` + escape(book.Title) + `</dc:title>` + creator + `<dc:language>` + escape(book.Language) + `</dc:language><meta property="dcterms:modified">` + modified.Format("2006-01-02T15:04:05Z") + `</meta></metadata>` +
		`<manifest>` + manifest.String() + `</manifest><spine>` + spine.String() + `</spine></package>`
}

func navXHTML(book conversion.Book) string {
	var items strings.Builder
	for _, item := range book.Outline {
		index := sectionIndexForPage(book.Sections, item.Page)
		fmt.Fprintf(&items, `<li><a href="sections/section-%04d.xhtml">%s</a></li>`, index+1, escape(item.Title))
	}
	if items.Len() == 0 {
		items.WriteString(`<li><a href="sections/section-0001.xhtml">正文</a></li>`)
	}
	return `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="` + escape(book.Language) + `"><head><title>目录</title><link rel="stylesheet" type="text/css" href="styles.css"/></head><body><nav epub:type="toc" id="toc"><h1>目录</h1><ol>` + items.String() + `</ol></nav></body></html>`
}

func sectionXHTML(book conversion.Book, section conversion.Section, index int) string {
	var body strings.Builder
	for _, block := range section.Blocks {
		switch block.Kind {
		case conversion.BlockHeading:
			level := block.Level
			if level < 1 || level > 6 {
				level = 2
			}
			fmt.Fprintf(&body, "<h%d>%s</h%d>", level, escape(block.Text), level)
		case conversion.BlockListItem:
			body.WriteString(`<ul><li>` + escape(block.Text) + `</li></ul>`)
		case conversion.BlockCode:
			body.WriteString(`<pre><code>` + escape(block.Text) + `</code></pre>`)
		case conversion.BlockImage:
			body.WriteString(`<figure><img src="../images/` + escape(block.ImageName) + `" alt="第 ` + fmt.Sprint(block.SourcePage) + ` 页插图"/></figure>`)
		default:
			body.WriteString(`<p>` + escape(block.Text) + `</p>`)
		}
	}
	title := fmt.Sprintf("第 %d 页", section.Page)
	return `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" lang="` + escape(book.Language) + `"><head><title>` + escape(title) + `</title><link rel="stylesheet" type="text/css" href="../styles.css"/></head><body><section id="page-` + fmt.Sprint(section.Page) + `" data-source-page="` + fmt.Sprint(section.Page) + `">` + body.String() + `</section></body></html>`
}

func sectionIndexForPage(sections []conversion.Section, page int) int {
	for index, section := range sections {
		if section.Page >= page {
			return index
		}
	}
	return max(0, len(sections)-1)
}

func sectionPath(index int) string {
	return fmt.Sprintf("EPUB/sections/section-%04d.xhtml", index+1)
}

func bookIdentifier(book conversion.Book) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, book.Title+"\x00"+book.Author+"\x00"+fmt.Sprint(len(book.Sections)))
	return hex.EncodeToString(hash.Sum(nil))
}

func escape(value string) string {
	var builder strings.Builder
	_ = xml.EscapeText(&builder, []byte(value))
	return builder.String()
}

func SafeOutputName(sourceName string) string {
	base := strings.TrimSuffix(filepath.Base(sourceName), filepath.Ext(sourceName))
	base = strings.TrimSpace(base)
	if base == "" || base == "." {
		base = "converted"
	}
	return base + ".epub"
}

const containerXML = `<?xml version="1.0" encoding="UTF-8"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="EPUB/package.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`

const stylesCSS = `:root{color-scheme:light dark}body{font-family:system-ui,-apple-system,"Noto Sans CJK SC",sans-serif;line-height:1.75;max-width:42rem;margin:0 auto;padding:5%;overflow-wrap:anywhere}p{margin:.8em 0;text-align:start}h1,h2,h3,h4,h5,h6{line-height:1.3;margin:1.6em 0 .65em;break-after:avoid}img{display:block;max-width:100%;height:auto;margin:auto}figure{margin:1.5em 0}pre{white-space:pre-wrap;overflow-wrap:anywhere;padding:1em;background:rgba(127,127,127,.12)}a{color:inherit;text-decoration-thickness:.08em}`
