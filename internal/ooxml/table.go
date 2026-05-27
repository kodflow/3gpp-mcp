package ooxml

import (
	"encoding/xml"
	"strconv"
	"strings"
)

// wTbl mirrors the OOXML table elements we read. Namespace is ignored (Local
// names are unambiguous in document.xml); encoding/xml fills these by element
// name. Only direct-child paths are used so nested tables aren't slurped.
type wTbl struct {
	Grid []wGridCol `xml:"tblGrid>gridCol"`
	Rows []wTr      `xml:"tr"`
}

type wGridCol struct {
	W string `xml:"w,attr"`
}

type wTr struct {
	Cells []wTc `xml:"tc"`
}

type wTc struct {
	GridSpan struct {
		Val string `xml:"val,attr"`
	} `xml:"tcPr>gridSpan"`
	VMerge *wVMerge `xml:"tcPr>vMerge"`
	// Cell text: all run texts of the cell's direct paragraphs, joined.
	Paras []wCellPara `xml:"p"`
}

type wVMerge struct {
	Val string `xml:"val,attr"`
}

type wCellPara struct {
	Texts []string `xml:"r>t"`
}

func (c wTc) text() string {
	var parts []string
	for _, p := range c.Paras {
		if s := strings.TrimSpace(strings.Join(p.Texts, "")); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

func (c wTc) span() int {
	if c.GridSpan.Val == "" {
		return 1
	}
	if n, err := strconv.Atoi(c.GridSpan.Val); err == nil && n > 0 {
		return n
	}
	return 1
}

// reconstruct expands gridSpan (horizontal) and fills vMerge (vertical) into a
// rectangular matrix where every logical (row, col) cell is populated — the
// geometry the HTML round-trip dropped (ECMA-376 §17.4).
func (t wTbl) reconstruct() [][]string {
	grid := len(t.Grid)
	// Fallback: if no tblGrid, infer width from the widest row's spans.
	if grid == 0 {
		for _, r := range t.Rows {
			w := 0
			for _, c := range r.Cells {
				w += c.span()
			}
			if w > grid {
				grid = w
			}
		}
	}
	if grid == 0 {
		return nil
	}
	carry := make([]string, grid) // last "restart"/plain text per column (vMerge fill)
	var out [][]string
	for _, r := range t.Rows {
		row := make([]string, grid)
		col := 0
		for _, c := range r.Cells {
			if col >= grid {
				break
			}
			span := c.span()
			var text string
			if c.VMerge != nil && c.VMerge.Val != "restart" {
				text = carry[col] // bare/continue vMerge inherits the cell above
			} else {
				text = c.text()
				carry[col] = text
			}
			for k := 0; k < span && col+k < grid; k++ {
				row[col+k] = text
				if c.VMerge == nil || c.VMerge.Val == "restart" {
					carry[col+k] = text
				}
			}
			col += span
		}
		out = append(out, row)
	}
	return out
}

// readParagraph consumes tokens until the matching </w:p>, returning the
// paragraph's w:pStyle value and its text with <w:tab/> rendered as a tab and
// <w:br>/<w:cr> as newline — so heading "number<TAB>title" splits reliably.
func readParagraph(dec *xml.Decoder) (style, text string) {
	var sb strings.Builder
	depth := 1
	inText := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "pStyle":
				style = attrVal(t.Attr, "val")
			case "t":
				inText = true
			case "tab":
				sb.WriteByte('\t')
			case "br", "cr":
				sb.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				sb.Write(t)
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inText = false
			}
			depth--
			if depth == 0 {
				return style, sb.String()
			}
		}
	}
	return style, sb.String()
}

func attrVal(attrs []xml.Attr, local string) string {
	for _, a := range attrs {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}
