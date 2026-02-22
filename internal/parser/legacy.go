// Package parser — legacy format support for .xls, .doc, and .ppt files.
// Uses shakinm/xlsReader for .xls and richardlehane/mscfb for .doc/.ppt (OLE2).
package parser

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"strings"
	"unicode/utf16"

	"github.com/richardlehane/mscfb"
	"github.com/shakinm/xlsReader/xls"
)

// minImageSize is the minimum image data size (1KB) for extracted images.
// Images smaller than this threshold are filtered out as likely icons/bullets.
const minImageSize = 1024

// parseXLSLegacy extracts text from legacy .xls (BIFF) files using xlsReader.
func (dp *DocumentParser) parseXLSLegacy(data []byte) (result *ParseResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("xls解析错误: %v", r)
		}
	}()

	wb, err := xls.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("xls解析错误: %w", err)
	}

	var sb strings.Builder
	numSheets := wb.GetNumberSheets()
	for i := 0; i < numSheets; i++ {
		sheet, err := wb.GetSheet(i)
		if err != nil {
			continue
		}
		sheetName := sheet.GetName()
		numRows := sheet.GetNumberRows()
		for rowIdx := 0; rowIdx < numRows; rowIdx++ {
			row, err := sheet.GetRow(rowIdx)
			if err != nil || row == nil {
				continue
			}
			cols := row.GetCols()
			for colIdx, cell := range cols {
				val := strings.TrimSpace(cell.GetString())
				if val == "" {
					continue
				}
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(fmt.Sprintf("%s-%d,%d: %s", sheetName, rowIdx+1, colIdx+1, val))
			}
		}
	}

	text := CleanText(sb.String())
	if text == "" {
		return nil, fmt.Errorf("xls文件内容为空")
	}

	return &ParseResult{
		Text: text,
		Metadata: map[string]string{
			"type":        "excel",
			"format":      "xls_legacy",
			"sheet_count": fmt.Sprintf("%d", numSheets),
		},
	}, nil
}


// parseWordLegacy extracts text from legacy .doc files using mscfb (OLE2).
// It reads the "WordDocument" stream and extracts text from the binary content.
func (dp *DocumentParser) parseWordLegacy(data []byte) (result *ParseResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("doc解析错误: %v", r)
		}
	}()

	doc, err := mscfb.New(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("doc解析错误: %w", err)
	}

	// Look for the WordDocument and 0Table/1Table streams
	var wordDocData []byte
	var tableData []byte
	var tableName string
	var docDataStream []byte

	for {
		entry, nextErr := doc.Next()
		if nextErr != nil {
			break
		}
		switch entry.Name {
		case "WordDocument":
			wordDocData, _ = io.ReadAll(entry)
		case "0Table":
			if tableData == nil {
				tableData, _ = io.ReadAll(entry)
				tableName = entry.Name
			}
		case "1Table":
			tableData, _ = io.ReadAll(entry)
			tableName = entry.Name
		case "Data":
			docDataStream, _ = io.ReadAll(entry)
		}
	}

	if len(wordDocData) == 0 {
		return nil, fmt.Errorf("doc解析错误: 未找到WordDocument流")
	}

	text := extractWordText(wordDocData, tableData, tableName)
	text = filterWordFieldCodes(text)
	text = CleanText(text)
	if text == "" {
		return nil, fmt.Errorf("doc文件内容为空")
	}

	// 图片提取（独立 recover）
	var images []ImageRef
	if len(docDataStream) > 0 {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("Warning: DOC图片提取panic: %v", r)
					images = nil
				}
			}()
			images = extractDocImages(docDataStream)
		}()
	}

	return &ParseResult{
		Text:   text,
		Images: images,
		Metadata: map[string]string{
			"type":        "word",
			"format":      "doc_legacy",
			"image_count": fmt.Sprintf("%d", len(images)),
		},
	}, nil
}


// extractWordText extracts text from a Word binary document.
// It reads the FIB (File Information Block) to locate the text in the
// WordDocument stream or the piece table in the Table stream.
func extractWordText(wordDoc []byte, tableData []byte, tableName string) string {
	if len(wordDoc) < 12 {
		return ""
	}

	// Read FIB fields
	// Offset 0x000A: flags (bit 9 = fWhichTblStm: 0=0Table, 1=1Table)
	flags := binary.LittleEndian.Uint16(wordDoc[0x0A:0x0C])
	whichTable := (flags >> 9) & 1

	// Verify table stream matches
	if tableName != "" {
		expectedTable := "0Table"
		if whichTable == 1 {
			expectedTable = "1Table"
		}
		if tableName != expectedTable && tableData != nil {
			// Wrong table stream, try to use it anyway
			_ = expectedTable
		}
	}

	// Try piece table approach first (more reliable for complex documents)
	if len(tableData) > 0 {
		if text := extractFromPieceTable(wordDoc, tableData); text != "" {
			return text
		}
	}

	// Fallback: extract text directly from WordDocument stream
	return extractDirectText(wordDoc)
}

// extractFromPieceTable reads the CLX (piece table) from the Table stream
// to extract text from the WordDocument stream.
func extractFromPieceTable(wordDoc []byte, tableData []byte) string {
	if len(wordDoc) < 0x01A2+4 {
		return ""
	}

	// FIB offset 0x01A2: fcClx (offset of CLX in table stream)
	// FIB offset 0x01A6: lcbClx (size of CLX)
	fcClx := binary.LittleEndian.Uint32(wordDoc[0x01A2:0x01A6])
	lcbClx := binary.LittleEndian.Uint32(wordDoc[0x01A6:0x01AA])

	if fcClx == 0 || lcbClx == 0 || int(fcClx+lcbClx) > len(tableData) {
		return ""
	}

	clx := tableData[fcClx : fcClx+lcbClx]

	// Find the Pcdt (piece table descriptor) in the CLX
	// Skip any Prc (property) entries (type 0x01)
	pos := 0
	for pos < len(clx) {
		if clx[pos] == 0x01 {
			// Prc: skip
			if pos+3 > len(clx) {
				break
			}
			cbGrpprl := int(binary.LittleEndian.Uint16(clx[pos+1 : pos+3]))
			pos += 3 + cbGrpprl
		} else if clx[pos] == 0x02 {
			// Pcdt found
			pos++
			break
		} else {
			break
		}
	}

	if pos >= len(clx) || pos+4 > len(clx) {
		return ""
	}

	// Read lcb (size of PlcPcd)
	lcb := int(binary.LittleEndian.Uint32(clx[pos : pos+4]))
	pos += 4

	if pos+lcb > len(clx) || lcb < 12 {
		return ""
	}

	plcPcd := clx[pos : pos+lcb]

	// PlcPcd structure: array of CPs (n+1 uint32s) followed by array of PCDs (n * 8 bytes)
	// Each PCD is 8 bytes
	pcdSize := 8
	// n = (lcb - 4) / (4 + pcdSize)
	n := (lcb - 4) / (4 + pcdSize)
	if n <= 0 {
		return ""
	}

	// Verify: (n+1)*4 + n*8 should equal lcb
	cpArraySize := (n + 1) * 4
	if cpArraySize+n*pcdSize > lcb {
		return ""
	}

	var sb strings.Builder
	for i := 0; i < n; i++ {
		cpStart := binary.LittleEndian.Uint32(plcPcd[i*4 : i*4+4])
		cpEnd := binary.LittleEndian.Uint32(plcPcd[(i+1)*4 : (i+1)*4+4])

		pcdOffset := cpArraySize + i*pcdSize
		if pcdOffset+8 > len(plcPcd) {
			break
		}

		// PCD structure: 2 bytes (flags) + 4 bytes (fc) + 2 bytes (prm)
		fcCompressed := binary.LittleEndian.Uint32(plcPcd[pcdOffset+2 : pcdOffset+6])

		isUnicode := (fcCompressed & 0x40000000) == 0
		fc := fcCompressed & 0x3FFFFFFF

		charCount := cpEnd - cpStart
		if charCount == 0 || charCount > 1000000 {
			continue
		}

		if isUnicode {
			// Unicode: each character is 2 bytes
			byteOffset := fc
			byteLen := charCount * 2
			if int(byteOffset+byteLen) > len(wordDoc) {
				continue
			}
			chunk := wordDoc[byteOffset : byteOffset+byteLen]
			u16s := make([]uint16, charCount)
			for j := uint32(0); j < charCount; j++ {
				u16s[j] = binary.LittleEndian.Uint16(chunk[j*2 : j*2+2])
			}
			runes := utf16.Decode(u16s)
			for _, r := range runes {
				if r == 0x0D || r == 0x0B {
					sb.WriteByte('\n')
				} else if r == 0x07 {
					sb.WriteByte('\t') // table cell marker
				} else if r >= 0x20 || r == 0x09 {
					sb.WriteRune(r)
				}
			}
		} else {
			// ANSI: each character is 1 byte, fc is divided by 2
			byteOffset := fc / 2
			if int(byteOffset+charCount) > len(wordDoc) {
				continue
			}
			chunk := wordDoc[byteOffset : byteOffset+charCount]
			for _, b := range chunk {
				if b == 0x0D || b == 0x0B {
					sb.WriteByte('\n')
				} else if b == 0x07 {
					sb.WriteByte('\t')
				} else if b >= 0x20 || b == 0x09 {
					sb.WriteByte(b)
				}
			}
		}
	}

	return sb.String()
}

// extractDirectText is a fallback that scans the WordDocument stream for
// readable text sequences. Less accurate but works when piece table parsing fails.
func extractDirectText(wordDoc []byte) string {
	var sb strings.Builder
	// Try to find text by scanning for printable character sequences
	// This is a best-effort fallback
	inText := false
	for i := 0; i < len(wordDoc); i++ {
		b := wordDoc[i]
		if (b >= 0x20 && b < 0x7F) || b == 0x0A || b == 0x0D || b == 0x09 {
			if b == 0x0D {
				sb.WriteByte('\n')
			} else {
				sb.WriteByte(b)
			}
			inText = true
		} else {
			if inText && sb.Len() > 0 {
				// Add separator between text blocks
				last := sb.String()
				if len(last) > 0 && last[len(last)-1] != '\n' {
					sb.WriteByte('\n')
				}
			}
			inText = false
		}
	}
	return sb.String()
}

// wordFieldCodePatterns contains Word field code markers that should be filtered.
var wordFieldCodePatterns = []string{
	"HYPERLINK",
	"PAGEREF",
	"MERGEFORMAT",
	"TOC \\o",
	"TOC \\h",
	"\\l \"",
	" \\h",
}

// filterWordFieldCodes removes lines containing Word field codes from extracted text.
// Field codes like HYPERLINK, PAGEREF, TOC, MERGEFORMAT are internal Word markers
// that leak through the piece table extraction and add noise to the content.
func filterWordFieldCodes(text string) string {
	lines := strings.Split(text, "\n")
	var filtered []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			filtered = append(filtered, line)
			continue
		}
		isFieldCode := false
		for _, pat := range wordFieldCodePatterns {
			if strings.Contains(trimmed, pat) {
				isFieldCode = true
				break
			}
		}
		if !isFieldCode {
			filtered = append(filtered, line)
		}
	}
	return strings.Join(filtered, "\n")
}

// parsePPTLegacy extracts text from legacy .ppt files using mscfb (OLE2).
// It reads the "PowerPoint Document" stream and extracts text records.
func (dp *DocumentParser) parsePPTLegacy(data []byte) (result *ParseResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("ppt解析错误: %v", r)
		}
	}()

	doc, err := mscfb.New(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("ppt解析错误: %w", err)
	}

	var pptData []byte
	var picturesData []byte
	for {
		entry, nextErr := doc.Next()
		if nextErr != nil {
			break
		}
		if entry.Name == "PowerPoint Document" {
			pptData, _ = io.ReadAll(entry)
		} else if entry.Name == "Pictures" {
			picturesData, _ = io.ReadAll(entry)
		}
	}

	if len(pptData) == 0 {
		return nil, fmt.Errorf("ppt解析错误: 未找到PowerPoint Document流")
	}

	text := extractPPTText(pptData)
	text = CleanText(text)
	if text == "" {
		return nil, fmt.Errorf("ppt文件内容为空")
	}

	// 图片提取（独立 recover）
	var images []ImageRef
	if len(picturesData) > 0 {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("Warning: PPT图片提取panic: %v", r)
					images = nil
				}
			}()
			images = extractPPTImages(picturesData)
		}()
	}

	return &ParseResult{
		Text:   text,
		Images: images,
		Metadata: map[string]string{
			"type":        "ppt",
			"format":      "ppt_legacy",
			"image_count": fmt.Sprintf("%d", len(images)),
		},
	}, nil
}

// pptNoisePatterns contains master slide template placeholders and other noise
// commonly found in legacy PPT files that should be filtered out.
var pptNoisePatterns = []string{
	"单击此处编辑母版",
	"单击此处编辑母版标题样式",
	"单击此处编辑母版文本样式",
	"单击此处编辑母版副标题样式",
	"Click to edit Master title style",
	"Click to edit Master text styles",
	"Click to edit Master subtitle style",
}

// pptNoiseExact contains short strings that are exact-match noise from master slides.
var pptNoiseExact = map[string]bool{
	"*":   true,
	"二级": true,
	"三级": true,
	"四级": true,
	"五级": true,
	"Second level": true,
	"Third level":  true,
	"Fourth level": true,
	"Fifth level":  true,
}

// isPPTNoise returns true if the text line is master slide template noise.
func isPPTNoise(text string) bool {
	if pptNoiseExact[text] {
		return true
	}
	for _, pat := range pptNoisePatterns {
		if strings.Contains(text, pat) {
			return true
		}
	}
	return false
}

// extractPPTText parses the PowerPoint Document binary stream to extract text.
// PPT binary format uses record headers: recVer(4bits) + recInstance(12bits) + recType(16bits) + recLen(32bits)
// Text is stored in TextBytesAtom (type 0x0FA8) and TextCharsAtom (type 0x0FA0).
// Master slide template placeholders are filtered out.
func extractPPTText(data []byte) string {
	var sb strings.Builder
	pos := 0

	for pos+8 <= len(data) {
		// Record header: 8 bytes
		recVerInstance := binary.LittleEndian.Uint16(data[pos : pos+2])
		recType := binary.LittleEndian.Uint16(data[pos+2 : pos+4])
		recLen := binary.LittleEndian.Uint32(data[pos+4 : pos+8])

		recVer := recVerInstance & 0x0F
		_ = recVer

		pos += 8

		if recLen > uint32(len(data)-pos) {
			break
		}

		switch recType {
		case 0x0FA0: // TextCharsAtom — UTF-16LE text
			if recLen >= 2 {
				charCount := recLen / 2
				u16s := make([]uint16, charCount)
				for i := uint32(0); i < charCount; i++ {
					u16s[i] = binary.LittleEndian.Uint16(data[pos+int(i*2) : pos+int(i*2+2)])
				}
				runes := utf16.Decode(u16s)
				text := string(runes)
				text = strings.TrimSpace(text)
				if text != "" && !isPPTNoise(text) {
					if sb.Len() > 0 {
						sb.WriteString("\n")
					}
					sb.WriteString(text)
				}
			}
			pos += int(recLen)

		case 0x0FA8: // TextBytesAtom — ANSI text
			if recLen > 0 {
				text := strings.TrimSpace(string(data[pos : pos+int(recLen)]))
				if text != "" && !isPPTNoise(text) {
					if sb.Len() > 0 {
						sb.WriteString("\n")
					}
					sb.WriteString(text)
				}
			}
			pos += int(recLen)

		default:
			// Container records (recVer == 0x0F) contain sub-records, so we
			// descend into them by not skipping recLen.
			if recVer == 0x0F {
				// Don't skip — sub-records will be parsed in the next iteration
			} else {
				pos += int(recLen)
			}
		}
	}

	return sb.String()
}

// extractPPTImages parses the raw bytes of the Pictures stream from a PPT OLE2
// container and returns an ImageRef for each embedded image that is ≥ 1KB.
// The Pictures stream contains consecutive BLIP Records. Each record has an
// 8-byte header (recVerInstance, recType, recLen) followed by a variable-length
// BLIP header and then the raw image data.
//
// Supported BLIP types:
//   0xF01A – EMF, 0xF01B – WMF, 0xF01D – JPEG, 0xF01E – PNG
//
// For EMF/WMF metafiles, the function decompresses the data (if zlib-compressed)
// and attempts to extract embedded raster images. If no embedded raster is found,
// the metafile is skipped since Go cannot natively render WMF/EMF.
//
// Any individual record that cannot be parsed is silently skipped.
func extractPPTImages(picturesData []byte) []ImageRef {
	var images []ImageRef
	pos := 0
	imageIndex := 1

	for pos+8 <= len(picturesData) {
		// --- Record Header (8 bytes) ---
		recVerInstance := binary.LittleEndian.Uint16(picturesData[pos : pos+2])
		recType := binary.LittleEndian.Uint16(picturesData[pos+2 : pos+4])
		recLen := binary.LittleEndian.Uint32(picturesData[pos+4 : pos+8])

		recInstance := recVerInstance >> 4

		// Sanity check: recLen must not exceed remaining data
		if int(recLen) > len(picturesData)-(pos+8) {
			break
		}

		recordDataStart := pos + 8
		pos += 8 + int(recLen) // advance to next record regardless of outcome

		isMetafile := false

		// Determine BLIP header size based on recType and whether it's dual-UID.
		// Dual-UID is indicated by recInstance having bit 4 set (recInstance & 0x10 != 0).
		var blipHeaderSize int
		var uidSize int
		switch recType {
		case 0xF01A, 0xF01B: // EMF, WMF
			isMetafile = true
			if recInstance&0x10 != 0 {
				uidSize = 32
				blipHeaderSize = 66 // 32 (2×UID) + 34 (metafile header)
			} else {
				uidSize = 16
				blipHeaderSize = 50 // 16 (UID) + 34 (metafile header)
			}
		case 0xF01D, 0xF01E: // JPEG, PNG
			// Single UID: 16 (UID) + 1 (tag) = 17
			// Dual UID:   32 (2×UID) + 1 (tag) = 33
			if recInstance&0x10 != 0 {
				blipHeaderSize = 33
			} else {
				blipHeaderSize = 17
			}
		default:
			// Unknown BLIP type – skip this record
			continue
		}

		// Validate that the BLIP header fits within recLen
		if int(recLen) < blipHeaderSize {
			continue
		}

		imageData := append([]byte(nil), picturesData[recordDataStart+blipHeaderSize:recordDataStart+int(recLen)]...)

		// For metafiles (EMF/WMF), decompress and try to extract embedded raster images
		if isMetafile {
			// OfficeArtMetafileHeader is 34 bytes, located after the UID(s).
			// Layout: cbSize(4) + rcBounds(16) + ptSize(8) + cbSave(4) + compression(1) + filter(1)
			// compression byte is at offset uidSize + 32 from recordDataStart
			metaHeaderStart := recordDataStart + uidSize
			if metaHeaderStart+34 > recordDataStart+int(recLen) {
				continue
			}
			compression := picturesData[metaHeaderStart+32]

			var rawMetafile []byte
			if compression == 0x00 {
				// DEFLATE compressed — decompress with zlib
				r, err := zlib.NewReader(bytes.NewReader(imageData))
				if err != nil {
					log.Printf("Warning: PPT metafile zlib decompress failed: %v", err)
					continue
				}
				rawMetafile, err = io.ReadAll(r)
				r.Close()
				if err != nil {
					log.Printf("Warning: PPT metafile zlib read failed: %v", err)
					continue
				}
			} else {
				// No compression (0xFE)
				rawMetafile = imageData
			}

			// Try to extract embedded raster images from the metafile
			raster := extractRasterFromMetafile(rawMetafile, recType)
			if raster == nil || len(raster) < minImageSize {
				log.Printf("Warning: PPT metafile image %d has no extractable raster data, skipping", imageIndex)
				continue
			}
			imageData = raster
		}

		// Apply minimum size filter
		if len(imageData) < minImageSize {
			continue
		}

		images = append(images, ImageRef{
			Alt:  fmt.Sprintf("PPT图片%d", imageIndex),
			Data: imageData,
		})
		imageIndex++
	}

	return images
}

// extractRasterFromMetafile attempts to extract embedded JPEG/PNG/BMP raster
// images from raw WMF or EMF metafile data. Many PPT metafiles are simple
// wrappers around a single raster image.
//
// For EMF: scans for EMR_STRETCHDIBITS (0x51) and EMR_SETDIBITSTODEVICE (0x49)
// records which contain Device Independent Bitmap (DIB) data.
//
// For WMF: scans for embedded JPEG/PNG by magic byte detection, and also looks
// for META_STRETCHDIB (0x0F43) records containing DIB data.
//
// Returns the largest found raster image as JPEG/PNG bytes, or nil if none found.
func extractRasterFromMetafile(data []byte, recType uint16) []byte {
	// Strategy 1: Scan for embedded JPEG/PNG by magic bytes (works for both WMF and EMF)
	if img := findEmbeddedRaster(data); img != nil {
		return img
	}

	// Strategy 2: Parse EMF records for DIB data
	if recType == 0xF01A { // EMF
		if img := extractDIBFromEMF(data); img != nil {
			return img
		}
	}

	// Strategy 3: Parse WMF records for DIB data
	if recType == 0xF01B { // WMF
		if img := extractDIBFromWMF(data); img != nil {
			return img
		}
	}

	return nil
}

// findEmbeddedRaster scans binary data for JPEG or PNG magic bytes and returns
// the largest contiguous image found. This handles the common case where a
// metafile is just a wrapper around a raster image.
func findEmbeddedRaster(data []byte) []byte {
	var best []byte

	// Look for JPEG (FF D8 FF)
	for i := 0; i+3 <= len(data); i++ {
		if data[i] == 0xFF && data[i+1] == 0xD8 && data[i+2] == 0xFF {
			// Find JPEG end marker (FF D9)
			end := findJPEGEnd(data[i:])
			if end > 0 && end > len(best) {
				best = data[i : i+end]
			}
		}
	}

	// Look for PNG (89 50 4E 47)
	for i := 0; i+8 <= len(data); i++ {
		if data[i] == 0x89 && data[i+1] == 0x50 && data[i+2] == 0x4E && data[i+3] == 0x47 {
			// Find PNG end (IEND chunk)
			end := findPNGEnd(data[i:])
			if end > 0 && end > len(best) {
				best = data[i : i+end]
			}
		}
	}

	if len(best) >= minImageSize {
		return append([]byte(nil), best...)
	}
	return nil
}

// findJPEGEnd returns the length of a JPEG image starting at data[0],
// by scanning for the EOI marker (FF D9). Returns 0 if not found.
func findJPEGEnd(data []byte) int {
	for i := 2; i+1 < len(data); i++ {
		if data[i] == 0xFF && data[i+1] == 0xD9 {
			return i + 2
		}
	}
	return 0
}

// findPNGEnd returns the length of a PNG image starting at data[0],
// by scanning for the IEND chunk. Returns 0 if not found.
func findPNGEnd(data []byte) int {
	// IEND chunk: length(4) + "IEND" + CRC(4) = 12 bytes
	iend := []byte("IEND")
	for i := 8; i+8 <= len(data); i++ {
		if bytes.Equal(data[i:i+4], iend) {
			// IEND chunk ends 4 bytes after "IEND" (CRC)
			end := i + 4 + 4
			if end <= len(data) {
				return end
			}
		}
	}
	return 0
}

// extractDIBFromEMF parses EMF records looking for bitmap records and extracts
// the DIB data, converting it to PNG.
func extractDIBFromEMF(data []byte) []byte {
	// EMF header: must start with record type 1 (EMR_HEADER)
	if len(data) < 8 {
		return nil
	}

	var bestDIB []byte
	pos := 0

	for pos+8 <= len(data) {
		recType := binary.LittleEndian.Uint32(data[pos : pos+4])
		recSize := binary.LittleEndian.Uint32(data[pos+4 : pos+8])

		if recSize < 8 || int(recSize) > len(data)-pos {
			break
		}

		recData := data[pos : pos+int(recSize)]

		switch recType {
		case 0x51: // EMR_STRETCHDIBITS
			if dib := parseDIBFromStretchDIBits(recData); len(dib) > len(bestDIB) {
				bestDIB = dib
			}
		case 0x49: // EMR_SETDIBITSTODEVICE
			if dib := parseDIBFromSetDIBits(recData); len(dib) > len(bestDIB) {
				bestDIB = dib
			}
		}

		pos += int(recSize)
	}

	if len(bestDIB) < minImageSize {
		return nil
	}
	return convertDIBToPNG(bestDIB)
}

// parseDIBFromStretchDIBits extracts the DIB (header + pixel data) from an
// EMR_STRETCHDIBITS record.
// Record layout (after Type and Size):
//   Bounds(16) + xDest(4) + yDest(4) + xSrc(4) + ySrc(4) + cxSrc(4) + cySrc(4)
//   + offBmiSrc(4) + cbBmiSrc(4) + offBitsSrc(4) + cbBitsSrc(4) + UsageSrc(4)
//   + BitBltRasterOperation(4) + cxDest(4) + cyDest(4)
//   + BitmapBuffer(variable)
func parseDIBFromStretchDIBits(rec []byte) []byte {
	if len(rec) < 80 {
		return nil
	}
	offBmi := binary.LittleEndian.Uint32(rec[48:52])
	cbBmi := binary.LittleEndian.Uint32(rec[52:56])
	offBits := binary.LittleEndian.Uint32(rec[56:60])
	cbBits := binary.LittleEndian.Uint32(rec[60:64])

	if cbBmi == 0 || cbBits == 0 {
		return nil
	}
	if int(offBmi+cbBmi) > len(rec) || int(offBits+cbBits) > len(rec) {
		return nil
	}

	// Combine BITMAPINFO header + pixel data into a single DIB
	dib := make([]byte, cbBmi+cbBits)
	copy(dib, rec[offBmi:offBmi+cbBmi])
	copy(dib[cbBmi:], rec[offBits:offBits+cbBits])
	return dib
}

// parseDIBFromSetDIBits extracts the DIB from an EMR_SETDIBITSTODEVICE record.
// Record layout (after Type and Size):
//   Bounds(16) + xDest(4) + yDest(4) + xSrc(4) + ySrc(4) + cxSrc(4) + cySrc(4)
//   + offBmiSrc(4) + cbBmiSrc(4) + offBitsSrc(4) + cbBitsSrc(4) + UsageSrc(4)
//   + iStartScan(4) + cScans(4)
func parseDIBFromSetDIBits(rec []byte) []byte {
	if len(rec) < 76 {
		return nil
	}
	offBmi := binary.LittleEndian.Uint32(rec[48:52])
	cbBmi := binary.LittleEndian.Uint32(rec[52:56])
	offBits := binary.LittleEndian.Uint32(rec[56:60])
	cbBits := binary.LittleEndian.Uint32(rec[60:64])

	if cbBmi == 0 || cbBits == 0 {
		return nil
	}
	if int(offBmi+cbBmi) > len(rec) || int(offBits+cbBits) > len(rec) {
		return nil
	}

	dib := make([]byte, cbBmi+cbBits)
	copy(dib, rec[offBmi:offBmi+cbBmi])
	copy(dib[cbBmi:], rec[offBits:offBits+cbBits])
	return dib
}

// extractDIBFromWMF parses WMF records looking for META_STRETCHDIB (0x0F43)
// and META_DIBSTRETCHBLT (0x0B41) records containing DIB data.
func extractDIBFromWMF(data []byte) []byte {
	// WMF files may start with a placeable header (magic 0x9AC6CDD7) or
	// directly with the standard header.
	pos := 0
	if len(data) >= 4 && binary.LittleEndian.Uint32(data[0:4]) == 0x9AC6CDD7 {
		pos = 22 // skip placeable header
	}

	// Standard WMF header is 18 bytes minimum
	if pos+18 > len(data) {
		return nil
	}
	// Skip standard header: Type(2) + HeaderSize(2) + Version(2) + FileSize(4) + ...
	headerSize := binary.LittleEndian.Uint16(data[pos+2 : pos+4])
	pos += int(headerSize) * 2 // headerSize is in 16-bit words

	var bestDIB []byte

	for pos+6 <= len(data) {
		// WMF record: Size(4 bytes, in 16-bit words) + Function(2 bytes)
		recSizeWords := binary.LittleEndian.Uint32(data[pos : pos+4])
		recFunc := binary.LittleEndian.Uint16(data[pos+4 : pos+6])
		recSizeBytes := int(recSizeWords) * 2

		if recSizeBytes < 6 || pos+recSizeBytes > len(data) {
			break
		}

		// End of records marker
		if recFunc == 0x0000 {
			break
		}

		recData := data[pos : pos+recSizeBytes]

		switch recFunc {
		case 0x0F43: // META_STRETCHDIB
			// Parameters: RasterOp(4) + SrcHeight(2) + SrcWidth(2) + YSrc(2) + XSrc(2)
			//           + DestHeight(2) + DestWidth(2) + YDest(2) + XDest(2) + DIB(variable)
			if len(recData) > 6+22 {
				dib := recData[6+22:] // skip record header (6) + parameters (22)
				if len(dib) > len(bestDIB) {
					bestDIB = append([]byte(nil), dib...)
				}
			}
		case 0x0B41: // META_DIBSTRETCHBLT
			// Parameters: RasterOp(4) + SrcHeight(2) + SrcWidth(2) + YSrc(2) + XSrc(2)
			//           + DestHeight(2) + DestWidth(2) + YDest(2) + XDest(2) + DIB(variable)
			if len(recData) > 6+22 {
				dib := recData[6+22:]
				if len(dib) > len(bestDIB) {
					bestDIB = append([]byte(nil), dib...)
				}
			}
		}

		pos += recSizeBytes
	}

	if len(bestDIB) < minImageSize {
		return nil
	}
	return convertDIBToPNG(bestDIB)
}

// convertDIBToPNG converts a raw Device Independent Bitmap (BITMAPINFO header +
// pixel data) to PNG format. Supports 24-bit and 32-bit uncompressed DIBs,
// as well as DIBs where the pixel data is actually embedded JPEG or PNG.
func convertDIBToPNG(dib []byte) []byte {
	if len(dib) < 40 {
		return nil
	}

	// BITMAPINFOHEADER
	biSize := binary.LittleEndian.Uint32(dib[0:4])
	biWidth := int32(binary.LittleEndian.Uint32(dib[4:8]))
	biHeight := int32(binary.LittleEndian.Uint32(dib[8:12]))
	biBitCount := binary.LittleEndian.Uint16(dib[14:16])
	biCompression := binary.LittleEndian.Uint32(dib[16:20])

	// BI_JPEG (4) or BI_PNG (5) — pixel data is embedded JPEG/PNG
	if biCompression == 4 || biCompression == 5 {
		pixelData := dib[biSize:]
		if len(pixelData) >= minImageSize {
			return append([]byte(nil), pixelData...)
		}
		return nil
	}

	// Only handle uncompressed (BI_RGB = 0) 24-bit or 32-bit
	if biCompression != 0 || (biBitCount != 24 && biBitCount != 32) {
		return nil
	}

	w := int(biWidth)
	if w < 0 {
		w = -w
	}
	h := int(biHeight)
	topDown := biHeight < 0
	if h < 0 {
		h = -h
	}

	if w == 0 || h == 0 || w > 20000 || h > 20000 {
		return nil
	}

	bytesPerPixel := int(biBitCount) / 8
	stride := (w*bytesPerPixel + 3) & ^3 // rows are padded to 4-byte boundary
	pixelData := dib[biSize:]

	if len(pixelData) < stride*h {
		return nil
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		srcY := y
		if !topDown {
			srcY = h - 1 - y // DIB is bottom-up by default
		}
		rowStart := srcY * stride
		for x := 0; x < w; x++ {
			off := rowStart + x*bytesPerPixel
			b := pixelData[off]
			g := pixelData[off+1]
			r := pixelData[off+2]
			a := uint8(255)
			if bytesPerPixel == 4 {
				a = pixelData[off+3]
				if a == 0 && (r != 0 || g != 0 || b != 0) {
					a = 255 // fix pre-multiplied alpha with non-zero color
				}
			}
			img.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: a})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

// extractDocImages scans the raw bytes of a DOC Data stream for embedded
// JPEG and PNG images using magic-number detection. Images smaller than 1KB
// are filtered out. Each valid image is returned as an ImageRef with Alt
// set to "DOC图片N" (N starting from 1). Extraction failures for individual
// images are silently skipped.
func extractDocImages(dataStream []byte) []ImageRef {
	if len(dataStream) == 0 {
		return nil
	}

	var images []ImageRef
	imageIndex := 1
	pos := 0

	jpegMagic := []byte{0xFF, 0xD8, 0xFF}
	jpegEOI := []byte{0xFF, 0xD9}
	pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	pngIEND := []byte{0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82}

	for pos < len(dataStream) {
		// Check for JPEG magic
		if pos+3 <= len(dataStream) && bytes.Equal(dataStream[pos:pos+3], jpegMagic) {
			// Find the boundary: next image magic or end of stream
			boundary := len(dataStream)
			for scan := pos + 3; scan < len(dataStream); scan++ {
				if scan+3 <= len(dataStream) && bytes.Equal(dataStream[scan:scan+3], jpegMagic) {
					boundary = scan
					break
				}
				if scan+8 <= len(dataStream) && bytes.Equal(dataStream[scan:scan+8], pngMagic) {
					boundary = scan
					break
				}
			}
			// Find the LAST FF D9 within the boundary
			searchRegion := dataStream[pos+3 : boundary]
			lastEOI := bytes.LastIndex(searchRegion, jpegEOI)
			if lastEOI >= 0 {
				endPos := pos + 3 + lastEOI + 2 // include the EOI marker
				imgData := dataStream[pos:endPos]
				if len(imgData) >= minImageSize {
					images = append(images, ImageRef{
						Alt:  fmt.Sprintf("DOC图片%d", imageIndex),
						Data: append([]byte(nil), imgData...), // copy to avoid holding entire stream
					})
					imageIndex++
				}
				pos = endPos
				continue
			}
			// No EOI found — skip this JPEG start and continue scanning
			pos++
			continue
		}

		// Check for PNG magic
		if pos+8 <= len(dataStream) && bytes.Equal(dataStream[pos:pos+8], pngMagic) {
			iendIdx := bytes.Index(dataStream[pos+8:], pngIEND)
			if iendIdx >= 0 {
				endPos := pos + 8 + iendIdx + len(pngIEND)
				imgData := dataStream[pos:endPos]
				if len(imgData) >= minImageSize {
					images = append(images, ImageRef{
						Alt:  fmt.Sprintf("DOC图片%d", imageIndex),
						Data: append([]byte(nil), imgData...), // copy to avoid holding entire stream
					})
					imageIndex++
				}
				pos = endPos
				continue
			}
			// No IEND found — skip this PNG start and continue scanning
			pos++
			continue
		}

		pos++
	}

	return images
}
