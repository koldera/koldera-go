package koldera

import (
	"encoding/json"
	"fmt"
)

// Document is the structured document of POST /v1/documents/jobs: semantic blocks rendered to a tagged PDF.
// Structure is declared, never inferred. Checks a schema cannot express (heading order, empty table headers, missing
// image descriptions, language tags, font names) are made by the server and come back as a *ValidationError with
// Errors.
type Document struct {
	// Title is written to the PDF title metadata (at least one non-blank character, at most 500).
	Title string
	// Language is the primary language as a BCP 47 tag, for example en or uk.
	Language string
	Page     *Page
	Theme    *Theme
	// Header and Footer repeat on every page. A Footer may contain a PageNumber.
	Header []Block
	Footer []Block
	// Body is the content in reading order.
	Body []Block
}

// PageSize is a paper size.
type PageSize string

// Paper sizes.
const (
	PageSizeA4     PageSize = "A4"
	PageSizeA5     PageSize = "A5"
	PageSizeLetter PageSize = "Letter"
	PageSizeLegal  PageSize = "Legal"
)

// Orientation is a page orientation.
type Orientation string

// Page orientations.
const (
	OrientationPortrait  Orientation = "portrait"
	OrientationLandscape Orientation = "landscape"
)

// Page is the page setup; every field is optional.
type Page struct {
	Size        PageSize    `json:"size,omitempty"`
	Orientation Orientation `json:"orientation,omitempty"`
	// Margins is a length with a unit (mm, cm, in or pt) for all four sides, for example 20mm.
	Margins string `json:"margins,omitempty"`
}

// Theme is typography and colour; every field is optional.
type Theme struct {
	// Font is a bundled font name such as Noto Sans.
	Font string `json:"font,omitempty"`
	// FontSize is in points, 6-24.
	FontSize float64 `json:"fontSize,omitempty"`
	// Accent is #rrggbb.
	Accent string `json:"accent,omitempty"`
}

// Block is a block of a document. The kinds are Heading, Paragraph, List, Table, Image, Link, PageBreak, Spacer,
// KeyValue and, in a footer, PageNumber.
type Block interface{ isBlock() }

// Inline is a piece of running text: Text, Strong, Emphasis or InlineLink.
type Inline interface{ isInline() }

// ListPart is a piece of a list item: any Inline, or a NestedList as the last element.
type ListPart interface{ isListPart() }

// ListItem is one list item: inline pieces, optionally followed by one NestedList.
type ListItem []ListPart

// Text is plain running text.
type Text string

// Strong is strong text.
type Strong string

// Emphasis is emphasised text.
type Emphasis string

// InlineLink is a hyperlink inside running text. The text must describe the target and must not be blank; the URL is
// http, https or mailto.
type InlineLink struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

// NestedList is a list inside a list item.
type NestedList struct {
	Ordered bool       `json:"ordered,omitempty"`
	Items   []ListItem `json:"items"`
}

func (Text) isInline()       {}
func (Strong) isInline()     {}
func (Emphasis) isInline()   {}
func (InlineLink) isInline() {}

func (Text) isListPart()       {}
func (Strong) isListPart()     {}
func (Emphasis) isListPart()   {}
func (InlineLink) isListPart() {}
func (NestedList) isListPart() {}

// Heading is a heading. The first heading in the body must be level 1 or 2 and each later one at most one level deeper
// than the previous.
type Heading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
}

// Paragraph is a paragraph of running text.
type Paragraph struct {
	Content []Inline `json:"content"`
	// Lang is a BCP 47 tag when it differs from the document language.
	Lang string `json:"lang,omitempty"`
	// Font is a bundled font name when it differs from the theme font.
	Font string `json:"font,omitempty"`
}

// List is a bulleted or, with Ordered, numbered list.
type List struct {
	Ordered bool       `json:"ordered,omitempty"`
	Items   []ListItem `json:"items"`
}

// ColumnAlign is the horizontal alignment of a table column.
type ColumnAlign string

// Column alignments.
const (
	AlignLeft   ColumnAlign = "left"
	AlignCenter ColumnAlign = "center"
	AlignRight  ColumnAlign = "right"
)

// Table is a data table with a caption and at least one header row. Header cells are tagged as headers.
type Table struct {
	// Caption says what the table shows; it is read before the table.
	Caption string `json:"caption"`
	// Header holds at least one row with a non-blank cell; its first row sets the column count.
	Header [][]string `json:"header"`
	Rows   [][]string `json:"rows"`
	// RowHeaders marks the first cell of every body row as a row header.
	RowHeaders bool `json:"rowHeaders,omitempty"`
	// ColumnAlign has one entry per column when present.
	ColumnAlign []ColumnAlign `json:"columnAlign,omitempty"`
}

// Image is an uploaded image (see Documents.UploadImage), described by Alt or marked Decorative. An image needs one of
// the two; a missing description is reported by the server with code IMAGE_ALT_MISSING.
type Image struct {
	UploadID   string `json:"uploadId"`
	Alt        string `json:"alt,omitempty"`
	Decorative bool   `json:"decorative,omitempty"`
}

// Link is a paragraph that consists of one link.
type Link struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

// PageBreak starts a new page.
type PageBreak struct{}

// Spacer is vertical space, not announced by assistive technology.
type Spacer struct {
	// Height is a length with a unit (mm, cm, in or pt), for example 8mm.
	Height string `json:"height"`
}

// KeyValue is label and value pairs.
type KeyValue struct {
	Rows [][2]string `json:"rows"`
}

// PageNumber is the current page number, filled in on every page. It belongs in a footer.
type PageNumber struct{}

func (Heading) isBlock()    {}
func (Paragraph) isBlock()  {}
func (List) isBlock()       {}
func (Table) isBlock()      {}
func (Image) isBlock()      {}
func (Link) isBlock()       {}
func (PageBreak) isBlock()  {}
func (Spacer) isBlock()     {}
func (KeyValue) isBlock()   {}
func (PageNumber) isBlock() {}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// marshalBlock encodes v with "type" first and the fields of v after it.
func marshalBlock(kind string, v any) ([]byte, error) {
	fields, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	head := fmt.Sprintf(`{"type":%q`, kind)
	if string(fields) == "{}" {
		return []byte(head + "}"), nil
	}
	return append([]byte(head+","), fields[1:]...), nil
}

// MarshalJSON encodes the heading with its type.
func (b Heading) MarshalJSON() ([]byte, error) {
	type plain Heading
	return marshalBlock("heading", plain(b))
}

// MarshalJSON encodes the paragraph with its type.
func (b Paragraph) MarshalJSON() ([]byte, error) {
	type plain Paragraph
	b.Content = orEmpty(b.Content)
	return marshalBlock("paragraph", plain(b))
}

// MarshalJSON encodes the list with its type.
func (b List) MarshalJSON() ([]byte, error) {
	type plain List
	b.Items = orEmpty(b.Items)
	return marshalBlock("list", plain(b))
}

// MarshalJSON encodes the table with its type.
func (b Table) MarshalJSON() ([]byte, error) {
	type plain Table
	b.Header = orEmpty(b.Header)
	b.Rows = orEmpty(b.Rows)
	return marshalBlock("table", plain(b))
}

// MarshalJSON encodes the image with its type: a decorative image sends no description, any other sends Alt even
// when it is empty so the server can report it.
func (b Image) MarshalJSON() ([]byte, error) {
	if b.Decorative {
		return marshalBlock("image", struct {
			UploadID   string `json:"uploadId"`
			Decorative bool   `json:"decorative"`
		}{b.UploadID, true})
	}
	return marshalBlock("image", struct {
		UploadID string `json:"uploadId"`
		Alt      string `json:"alt"`
	}{b.UploadID, b.Alt})
}

// MarshalJSON encodes the link block with its type.
func (b Link) MarshalJSON() ([]byte, error) { type plain Link; return marshalBlock("link", plain(b)) }

// MarshalJSON encodes the page break.
func (PageBreak) MarshalJSON() ([]byte, error) { return marshalBlock("pageBreak", struct{}{}) }

// MarshalJSON encodes the spacer with its type.
func (b Spacer) MarshalJSON() ([]byte, error) {
	type plain Spacer
	return marshalBlock("spacer", plain(b))
}

// MarshalJSON encodes the key-value block with its type.
func (b KeyValue) MarshalJSON() ([]byte, error) {
	type plain KeyValue
	b.Rows = orEmpty(b.Rows)
	return marshalBlock("keyValue", plain(b))
}

// MarshalJSON encodes the page number.
func (PageNumber) MarshalJSON() ([]byte, error) { return marshalBlock("pageNumber", struct{}{}) }

// MarshalJSON encodes a strong text as {"strong": text}.
func (s Strong) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Strong string `json:"strong"`
	}{string(s)})
}

// MarshalJSON encodes an emphasised text as {"emphasis": text}.
func (e Emphasis) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Emphasis string `json:"emphasis"`
	}{string(e)})
}

// MarshalJSON encodes a link as {"link": {...}}.
func (l InlineLink) MarshalJSON() ([]byte, error) {
	type plain InlineLink
	return json.Marshal(struct {
		Link plain `json:"link"`
	}{plain(l)})
}

// MarshalJSON encodes a nested list as {"list": {...}}.
func (n NestedList) MarshalJSON() ([]byte, error) {
	type plain NestedList
	n.Items = orEmpty(n.Items)
	return json.Marshal(struct {
		List plain `json:"list"`
	}{plain(n)})
}

// MarshalJSON encodes the document; the arrays a document needs are never null.
func (d Document) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Title    string  `json:"title"`
		Language string  `json:"language"`
		Page     *Page   `json:"page,omitempty"`
		Theme    *Theme  `json:"theme,omitempty"`
		Header   []Block `json:"header,omitempty"`
		Footer   []Block `json:"footer,omitempty"`
		Body     []Block `json:"body"`
	}{d.Title, d.Language, d.Page, d.Theme, d.Header, d.Footer, orEmpty(d.Body)})
}

// ---- decoding ---------------------------------------------------------------------------------------

// UnmarshalJSON decodes a document written in the JSON format of the schema, so documents kept as files can be sent.
// Only what the SDK model can express is accepted: an unknown block or inline kind, or an image without an uploadId
// (the file form is for the command-line tool), is an error.
func (d *Document) UnmarshalJSON(data []byte) error {
	var raw struct {
		Title    string            `json:"title"`
		Language string            `json:"language"`
		Page     *Page             `json:"page"`
		Theme    *Theme            `json:"theme"`
		Header   []json.RawMessage `json:"header"`
		Footer   []json.RawMessage `json:"footer"`
		Body     []json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := Document{Title: raw.Title, Language: raw.Language, Page: raw.Page, Theme: raw.Theme}
	var err error
	if out.Header, err = decodeBlocks(raw.Header); err != nil {
		return fmt.Errorf("header: %w", err)
	}
	if out.Footer, err = decodeBlocks(raw.Footer); err != nil {
		return fmt.Errorf("footer: %w", err)
	}
	if out.Body, err = decodeBlocks(raw.Body); err != nil {
		return fmt.Errorf("body: %w", err)
	}
	*d = out
	return nil
}

func decodeBlocks(raws []json.RawMessage) ([]Block, error) {
	if raws == nil {
		return nil, nil
	}
	blocks := make([]Block, 0, len(raws))
	for i, raw := range raws {
		block, err := decodeBlock(raw)
		if err != nil {
			return nil, fmt.Errorf("block %d: %w", i, err)
		}
		blocks = append(blocks, block)
	}
	return blocks, nil
}

func into[T Block](raw json.RawMessage) (Block, error) {
	var b T
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, err
	}
	return b, nil
}

func decodeBlock(raw json.RawMessage) (Block, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	switch head.Type {
	case "heading":
		return into[Heading](raw)
	case "paragraph":
		var shadow struct {
			Content []json.RawMessage `json:"content"`
			Lang    string            `json:"lang"`
			Font    string            `json:"font"`
		}
		if err := json.Unmarshal(raw, &shadow); err != nil {
			return nil, err
		}
		content := make([]Inline, 0, len(shadow.Content))
		for _, piece := range shadow.Content {
			part, err := decodePart(piece)
			if err != nil {
				return nil, err
			}
			inline, ok := part.(Inline)
			if !ok {
				return nil, fmt.Errorf("a paragraph cannot contain a list")
			}
			content = append(content, inline)
		}
		return Paragraph{Content: content, Lang: shadow.Lang, Font: shadow.Font}, nil
	case "list":
		return into[List](raw)
	case "table":
		return into[Table](raw)
	case "image":
		var b Image
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, err
		}
		if b.UploadID == "" {
			return nil, fmt.Errorf("an image needs an uploadId (see Documents.UploadImage)")
		}
		return b, nil
	case "link":
		return into[Link](raw)
	case "pageBreak":
		return PageBreak{}, nil
	case "spacer":
		return into[Spacer](raw)
	case "keyValue":
		return into[KeyValue](raw)
	case "pageNumber":
		return PageNumber{}, nil
	case "":
		return nil, fmt.Errorf("a block needs a type")
	}
	return nil, fmt.Errorf("unknown block type %q", head.Type)
}

// UnmarshalJSON decodes the pieces of one list item.
func (li *ListItem) UnmarshalJSON(data []byte) error {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return err
	}
	item := make(ListItem, 0, len(raws))
	for _, raw := range raws {
		part, err := decodePart(raw)
		if err != nil {
			return err
		}
		item = append(item, part)
	}
	*li = item
	return nil
}

func decodePart(raw json.RawMessage) (ListPart, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return Text(text), nil
	}
	var shape struct {
		Strong   *string `json:"strong"`
		Emphasis *string `json:"emphasis"`
		Link     *struct {
			Text string `json:"text"`
			URL  string `json:"url"`
		} `json:"link"`
		List *NestedList `json:"list"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		return nil, fmt.Errorf("an inline piece must be a string or an object: %w", err)
	}
	switch {
	case shape.Strong != nil:
		return Strong(*shape.Strong), nil
	case shape.Emphasis != nil:
		return Emphasis(*shape.Emphasis), nil
	case shape.Link != nil:
		return InlineLink{Text: shape.Link.Text, URL: shape.Link.URL}, nil
	case shape.List != nil:
		return *shape.List, nil
	}
	return nil, fmt.Errorf("unknown inline piece %s", raw)
}
