package koldera

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDocumentFixturesSurviveADecodeAndEncodeRoundTrip(t *testing.T) {
	for _, name := range []string{"document-job-accepted", "document-job-prefer-wait-finished", "document-job-validation-errors", "document-job-image-upload-invalid"} {
		var doc Document
		if err := json.Unmarshal(fixtures[name].Request.Body, &doc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !jsonEqual(out, fixtures[name].Request.Body) {
			t.Errorf("%s: round trip differs\n got  %s\n want %s", name, out, fixtures[name].Request.Body)
		}
	}
}

func TestEveryBlockAndInlineKindEncodesTheShapeOfTheSchema(t *testing.T) {
	doc := Document{
		Title:    "Annual report",
		Language: "en",
		Page:     &Page{Size: PageSizeA4, Orientation: OrientationLandscape, Margins: "15mm"},
		Theme:    &Theme{Font: "Noto Serif", FontSize: 12, Accent: "#1a1a1a"},
		Header:   []Block{Paragraph{Content: []Inline{Text("Acme")}}},
		Footer:   []Block{PageNumber{}},
		Body: []Block{
			Heading{Level: 1, Text: "Summary"},
			Paragraph{Content: []Inline{Text("See "), Emphasis("this"), Text(" and "), InlineLink{Text: "the site", URL: "https://example.com"}}, Lang: "uk", Font: "Noto Sans"},
			List{Ordered: true, Items: []ListItem{
				{Text("first")},
				{Text("second"), NestedList{Items: []ListItem{{Strong("inner")}}}},
			}},
			Table{Caption: "Totals", Header: [][]string{{"Name", "Sum"}}, Rows: [][]string{{"A", "1"}}, RowHeaders: true, ColumnAlign: []ColumnAlign{AlignLeft, AlignRight}},
			Image{UploadID: "33333333-3333-4333-8333-333333333333", Decorative: true},
			Image{UploadID: "33333333-3333-4333-8333-333333333333", Alt: ""},
			Link{Text: "Contact", URL: "mailto:a@example.com"},
			PageBreak{},
			Spacer{Height: "8mm"},
			KeyValue{Rows: [][2]string{{"From", "Acme"}, {"To", "Beta"}}},
		},
	}
	want := `{
	  "title": "Annual report", "language": "en",
	  "page": {"size": "A4", "orientation": "landscape", "margins": "15mm"},
	  "theme": {"font": "Noto Serif", "fontSize": 12, "accent": "#1a1a1a"},
	  "header": [{"type": "paragraph", "content": ["Acme"]}],
	  "footer": [{"type": "pageNumber"}],
	  "body": [
	    {"type": "heading", "level": 1, "text": "Summary"},
	    {"type": "paragraph", "content": ["See ", {"emphasis": "this"}, " and ", {"link": {"text": "the site", "url": "https://example.com"}}], "lang": "uk", "font": "Noto Sans"},
	    {"type": "list", "ordered": true, "items": [["first"], ["second", {"list": {"items": [[{"strong": "inner"}]]}}]]},
	    {"type": "table", "caption": "Totals", "header": [["Name", "Sum"]], "rows": [["A", "1"]], "rowHeaders": true, "columnAlign": ["left", "right"]},
	    {"type": "image", "uploadId": "33333333-3333-4333-8333-333333333333", "decorative": true},
	    {"type": "image", "uploadId": "33333333-3333-4333-8333-333333333333", "alt": ""},
	    {"type": "link", "text": "Contact", "url": "mailto:a@example.com"},
	    {"type": "pageBreak"},
	    {"type": "spacer", "height": "8mm"},
	    {"type": "keyValue", "rows": [["From", "Acme"], ["To", "Beta"]]}
	  ]
	}`
	got, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(got, []byte(want)) {
		t.Errorf("encoded document differs\n got  %s\n want %s", got, want)
	}
	var back Document
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatal(err)
	}
	again, _ := json.Marshal(back)
	if !jsonEqual(again, got) {
		t.Errorf("decode then encode changed the document\n got  %s\n want %s", again, got)
	}
}

func TestOptionalFieldsAreOmittedNotZero(t *testing.T) {
	out, err := json.Marshal(Document{Title: "T", Language: "en", Body: []Block{Paragraph{Content: []Inline{Text("x")}}, List{Items: []ListItem{{Text("y")}}}, Table{Caption: "c", Header: [][]string{{"h"}}, Rows: [][]string{}}}})
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"page", "theme", "header", "footer"} {
		if _, sent := top[absent]; sent {
			t.Errorf("top-level %q was sent: %s", absent, out)
		}
	}
	for _, absent := range []string{`"lang"`, `"font"`, `"ordered"`, `"rowHeaders"`, `"columnAlign"`} {
		if strings.Contains(string(out), absent) {
			t.Errorf("%s was sent: %s", absent, out)
		}
	}
}

func TestDecodingRefusesWhatTheSchemaDoesNotKnow(t *testing.T) {
	for name, text := range map[string]string{
		"unknown block":  `{"title":"T","language":"en","body":[{"type":"video"}]}`,
		"no block type":  `{"title":"T","language":"en","body":[{"level":1}]}`,
		"bad inline":     `{"title":"T","language":"en","body":[{"type":"paragraph","content":[{"bold":"x"}]}]}`,
		"number inline":  `{"title":"T","language":"en","body":[{"type":"paragraph","content":[3]}]}`,
		"file image":     `{"title":"T","language":"en","body":[{"type":"image","file":"a.png","alt":"x"}]}`,
		"pageNumber out": `{"title":"T","language":"en","body":[{"type":"pageNumber"}]}`,
	} {
		var doc Document
		err := json.Unmarshal([]byte(text), &doc)
		if name == "pageNumber out" {
			// a page number is a legal block value; the server decides where it may appear
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: decoded without an error", name)
		}
	}
}
