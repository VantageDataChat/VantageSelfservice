// Package parser provides document parsing functionality for multiple file formats.
// It uses vantagedatachat libraries (gopdf2, goword, goexcel, goppt) to extract text.
package parser

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	gopdf "github.com/VantageDataChat/GoPDF2"
	goexcel "github.com/VantageDataChat/GoExcel"
	goppt "github.com/VantageDataChat/GoPPT"
	goword "github.com/VantageDataChat/GoWord"
)

// DocumentParser handles parsing of various document formats.
type DocumentParser struct{}

// ParseResult holds the extracted text and metadata from a parsed document.
type ParseResult struct {
	Text     string            `json:"text"`
	Metadata map[string]string `json:"metadata"`
}

// Parse dispatches to the correct parser based on fileType.
// Supported types: "pdf", "word", "excel", "ppt".
func (dp *DocumentParser) Parse(fileData []byte, fileType string) (*ParseResult, error) {
	switch strings.ToLower(fileType) {
	case "pdf":
		return dp.parsePDF(fileData)
	case "word":
		return dp.parseWord(fileData)
	case "excel":
		return dp.parseExcel(fileData)
	case "ppt":
		return dp.parsePPT(fileData)
	default:
		return nil, fmt.Errorf("不支持的文件格式: %s", fileType)
	}
}

// parsePDF extracts text from PDF data using gopdf2, preserving paragraph structure.
func (dp *DocumentParser) parsePDF(data []byte) (result *ParseResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("pdf解析错误: %v", r)
		}
	}()

	pageCount, err := gopdf.GetSourcePDFPageCountFromBytes(data)
	if err != nil {
		return nil, fmt.Errorf("pdf解析错误: %w", err)
	}

	var sb strings.Builder
	for i := 0; i < pageCount; i++ {
		text, err := gopdf.ExtractPageText(data, i)
		if err != nil {
			return nil, fmt.Errorf("pdf解析错误: 第%d页提取失败: %w", i+1, err)
		}
		if text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(text)
		}
	}

	return &ParseResult{
		Text: CleanText(sb.String()),
		Metadata: map[string]string{
			"type":       "pdf",
			"page_count": fmt.Sprintf("%d", pageCount),
		},
	}, nil
}

// parseWord extracts text from Word data using goword, preserving headings and paragraphs.
func (dp *DocumentParser) parseWord(data []byte) (result *ParseResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("word解析错误: %v", r)
		}
	}()

	doc, err := goword.OpenFromBytes(data)
	if err != nil {
		return nil, fmt.Errorf("word解析错误: %w", err)
	}

	text := doc.ExtractText()

	return &ParseResult{
		Text: CleanText(text),
		Metadata: map[string]string{
			"type":  "word",
			"title": doc.Properties.Title,
		},
	}, nil
}

// parseExcel extracts cell content from Excel data using goexcel,
// organized per sheet in "SheetName-Row,Col" format.
func (dp *DocumentParser) parseExcel(data []byte) (result *ParseResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("excel解析错误: %v", r)
		}
	}()

	reader := goexcel.NewXLSXReader()
	wb, err := reader.Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("excel解析错误: %w", err)
	}

	var sb strings.Builder
	sheetNames := wb.GetSheetNames()
	for _, name := range sheetNames {
		sheet, err := wb.GetSheetByName(name)
		if err != nil {
			continue
		}
		rows, err := sheet.RowIterator()
		if err != nil {
			continue
		}
		for rowIdx, row := range rows {
			for _, cell := range row {
				if cell == nil || cell.IsEmpty() {
					continue
				}
				val := cell.GetFormattedValue()
				if val == "" {
					continue
				}
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(fmt.Sprintf("%s-%d,%d: %s", name, rowIdx+1, cell.Col()+1, val))
			}
		}
	}

	return &ParseResult{
		Text: CleanText(sb.String()),
		Metadata: map[string]string{
			"type":        "excel",
			"sheet_count": fmt.Sprintf("%d", len(sheetNames)),
		},
	}, nil
}

// parsePPT extracts slide text from PowerPoint data using goppt, per page.
func (dp *DocumentParser) parsePPT(data []byte) (result *ParseResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("ppt解析错误: %v", r)
		}
	}()

	pres, err := goppt.ReadFrom(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("ppt解析错误: %w", err)
	}

	var sb strings.Builder
	slides := pres.Slides()
	for i, slide := range slides {
		text := slide.ExtractText()
		if text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(fmt.Sprintf("Slide %d:\n%s", i+1, text))
		}
	}

	return &ParseResult{
		Text: CleanText(sb.String()),
		Metadata: map[string]string{
			"type":        "ppt",
			"slide_count": fmt.Sprintf("%d", len(slides)),
		},
	}, nil
}

// CleanText removes excessive whitespace and meaningless special characters from text.
// It trims leading/trailing whitespace, collapses multiple spaces into one,
// and removes control characters (except newlines and tabs).
func CleanText(text string) string {
	// Remove control characters except \n and \t
	controlRe := regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]`)
	text = controlRe.ReplaceAllString(text, "")

	// Collapse multiple spaces/tabs into a single space (per line)
	lines := strings.Split(text, "\n")
	var cleaned []string
	spaceRe := regexp.MustCompile(`[ \t]+`)
	for _, line := range lines {
		line = spaceRe.ReplaceAllString(line, " ")
		line = strings.TrimSpace(line)
		cleaned = append(cleaned, line)
	}
	text = strings.Join(cleaned, "\n")

	// Collapse 3+ consecutive newlines into 2
	nlRe := regexp.MustCompile(`\n{3,}`)
	text = nlRe.ReplaceAllString(text, "\n\n")

	return strings.TrimSpace(text)
}
