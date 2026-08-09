package scanner

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"strings"
)

// EpubIdentifiers reads an epub's embedded metadata and returns any ISBN
// (normalized to ISBN-13) and ASIN it declares. An epub is a zip; its OPF
// package document lists Dublin Core <dc:identifier> entries, which is where a
// well-produced ebook records its ISBN. Anything unreadable — a truncated file,
// a non-epub with the wrong extension, no identifiers — yields empty strings, so
// the caller simply falls through to title matching.
//
// Only epub is read: it's the one ebook format with a standard, openable
// metadata container. mobi/azw3/pdf carry identifiers too, but in proprietary
// or unstructured ways not worth the fragility — a filename ISBN still covers
// them.
func EpubIdentifiers(path string) (isbn13, asin string) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", ""
	}
	defer zr.Close()

	opf := findOPF(zr)
	if opf == nil {
		return "", ""
	}
	rc, err := opf.Open()
	if err != nil {
		return "", ""
	}
	defer rc.Close()
	return identifiersFromOPF(rc)
}

// findOPF locates the package document: META-INF/container.xml names it, but a
// direct scan for a *.opf entry is the fallback for containers that omit it.
func findOPF(zr *zip.ReadCloser) *zip.File {
	var firstOPF *zip.File
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, "META-INF/container.xml") {
			if p := opfPathFromContainer(f); p != "" {
				for _, g := range zr.File {
					if g.Name == p {
						return g
					}
				}
			}
		}
		if firstOPF == nil && strings.HasSuffix(strings.ToLower(f.Name), ".opf") {
			firstOPF = f
		}
	}
	return firstOPF
}

func opfPathFromContainer(f *zip.File) string {
	rc, err := f.Open()
	if err != nil {
		return ""
	}
	defer rc.Close()
	var doc struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if xml.NewDecoder(rc).Decode(&doc) != nil || len(doc.Rootfiles) == 0 {
		return ""
	}
	return doc.Rootfiles[0].FullPath
}

// identifiersFromOPF pulls ISBN/ASIN out of the OPF's <dc:identifier> elements.
// Namespaces vary between producers, so the decoder matches on the local name
// "identifier" and reads both the element text and its opf:scheme hint. A value
// is tried as an ISBN first (via the strict, checksum-validating normalizer),
// then as an ASIN.
func identifiersFromOPF(r io.Reader) (isbn13, asin string) {
	dec := xml.NewDecoder(r)
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "identifier" {
			continue
		}
		var scheme string
		for _, a := range start.Attr {
			if a.Name.Local == "scheme" {
				scheme = strings.ToUpper(a.Value)
			}
		}
		var text string
		if dec.DecodeElement(&text, &start) != nil {
			continue
		}
		text = strings.TrimSpace(text)
		// "urn:isbn:9780..." and "isbn:0553..." are common prefixings.
		if i := strings.LastIndex(strings.ToLower(text), "isbn:"); i >= 0 {
			text = text[i+len("isbn:"):]
		}
		if isbn13 == "" {
			if v := NormalizeISBN(text); v != "" {
				isbn13 = v
			}
		}
		if asin == "" {
			if strings.Contains(scheme, "ASIN") || strings.Contains(scheme, "AMAZON") || strings.Contains(scheme, "MOBI") {
				if v := ASINFromName(text); v != "" {
					asin = v
				}
			}
		}
		if isbn13 != "" && asin != "" {
			break
		}
	}
	return isbn13, asin
}
