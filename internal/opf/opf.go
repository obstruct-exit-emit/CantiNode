// Package opf renders OPF 2.0 metadata sidecars — the format Calibre and
// Audiobookshelf read (metadata.opf / <book>.opf next to the files).
package opf

import (
	"bytes"
	"encoding/xml"
	"fmt"

	"github.com/librinode/librinode/internal/library"
)

type opfMeta struct {
	Name    string `xml:"name,attr"`
	Content string `xml:"content,attr"`
}

type opfCreator struct {
	Role string `xml:"opf:role,attr"`
	Name string `xml:",chardata"`
}

type opfIdentifier struct {
	Scheme string `xml:"opf:scheme,attr"`
	Value  string `xml:",chardata"`
}

type opfMetadata struct {
	XMLNSDC     string          `xml:"xmlns:dc,attr"`
	XMLNSOPF    string          `xml:"xmlns:opf,attr"`
	Title       string          `xml:"dc:title"`
	Creators    []opfCreator    `xml:"dc:creator,omitempty"`
	Description string          `xml:"dc:description,omitempty"`
	Date        string          `xml:"dc:date,omitempty"`
	Language    string          `xml:"dc:language,omitempty"`
	Identifiers []opfIdentifier `xml:"dc:identifier,omitempty"`
	Metas       []opfMeta       `xml:"meta,omitempty"`
}

type opfPackage struct {
	XMLName  xml.Name    `xml:"package"`
	Version  string      `xml:"version,attr"`
	XMLNS    string      `xml:"xmlns,attr"`
	Metadata opfMetadata `xml:"metadata"`
}

// Render builds the sidecar XML for a book. Series and identifiers are
// optional; whatever is missing simply drops out.
func Render(book *library.Book, authorName string, series []library.SeriesLink) ([]byte, error) {
	md := opfMetadata{
		XMLNSDC:     "http://purl.org/dc/elements/1.1/",
		XMLNSOPF:    "http://www.idpf.org/2007/opf",
		Title:       book.Title,
		Description: book.Description,
		Date:        book.ReleaseDate,
	}
	if authorName != "" {
		md.Creators = []opfCreator{{Role: "aut", Name: authorName}}
	}
	for _, e := range book.Editions {
		if e.ISBN13 != "" {
			md.Identifiers = append(md.Identifiers, opfIdentifier{Scheme: "ISBN", Value: e.ISBN13})
			if md.Language == "" {
				md.Language = e.Language
			}
			break
		}
	}
	if len(series) > 0 {
		md.Metas = append(md.Metas,
			opfMeta{Name: "calibre:series", Content: series[0].Title},
			opfMeta{Name: "calibre:series_index", Content: fmt.Sprintf("%g", series[0].Position)},
		)
	}

	out, err := xml.MarshalIndent(opfPackage{
		Version:  "2.0",
		XMLNS:    "http://www.idpf.org/2007/opf",
		Metadata: md,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	buf.Write(out)
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}
