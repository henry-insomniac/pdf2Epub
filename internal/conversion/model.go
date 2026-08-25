package conversion

type Document struct {
	Title    string
	Author   string
	Language string
	Pages    []Page
	Outline  []OutlineItem
	Cover    Image
}

type Page struct {
	Number int
	Lines  []Line
	Images []Image
}

type Line struct {
	Text        string
	FontSize    float64
	Bold        bool
	BreakBefore bool
}

type Image struct {
	Name      string
	MediaType string
	Data      []byte
}

type OutlineItem struct {
	Title string
	Page  int
	Level int
}

type Book struct {
	Title    string
	Author   string
	Language string
	Sections []Section
	Outline  []OutlineItem
	Cover    Image
	Images   []Image
	Warnings []Warning
}

type Section struct {
	Page   int
	Blocks []Block
}

type BlockKind string

const (
	BlockParagraph BlockKind = "paragraph"
	BlockHeading   BlockKind = "heading"
	BlockListItem  BlockKind = "list_item"
	BlockCode      BlockKind = "code"
	BlockImage     BlockKind = "image"
)

type Block struct {
	Kind       BlockKind
	Text       string
	Level      int
	ImageName  string
	SourcePage int
}

type Warning struct {
	Code    string
	Message string
	Page    int
}
