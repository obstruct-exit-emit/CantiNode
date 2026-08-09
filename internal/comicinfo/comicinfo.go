// Package comicinfo writes ComicInfo.xml sidecars into CBZ archives — the
// metadata format Kavita, Komga, and comic readers use. CBR (RAR) archives
// can't be written by pure Go, so they're left untouched.
package comicinfo

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Info is the subset of the ComicInfo schema LibriNode fills.
type Info struct {
	XMLName xml.Name `xml:"ComicInfo"`
	Series  string   `xml:"Series,omitempty"`
	Number  string   `xml:"Number,omitempty"`
	Title   string   `xml:"Title,omitempty"`
	Writer  string   `xml:"Writer,omitempty"`
	Summary string   `xml:"Summary,omitempty"`
	Year    int      `xml:"Year,omitempty"`
}

// Inject rewrites a .cbz adding (or replacing) ComicInfo.xml at the archive
// root. Non-cbz paths are ignored without error.
func Inject(cbzPath string, info Info) error {
	if strings.ToLower(filepath.Ext(cbzPath)) != ".cbz" {
		return nil
	}
	reader, err := zip.OpenReader(cbzPath)
	if err != nil {
		return fmt.Errorf("comicinfo: opening %s: %w", cbzPath, err)
	}
	defer reader.Close()

	tmp, err := os.CreateTemp(filepath.Dir(cbzPath), ".comicinfo-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	writer := zip.NewWriter(tmp)
	for _, entry := range reader.File {
		if strings.EqualFold(filepath.Base(entry.Name), "ComicInfo.xml") {
			continue // replaced below
		}
		if err := copyZipEntry(writer, entry); err != nil {
			writer.Close()
			tmp.Close()
			return err
		}
	}

	w, err := writer.Create("ComicInfo.xml")
	if err != nil {
		writer.Close()
		tmp.Close()
		return err
	}
	payload, err := xml.MarshalIndent(info, "", "  ")
	if err != nil {
		writer.Close()
		tmp.Close()
		return err
	}
	if _, err := w.Write(append([]byte(xml.Header), payload...)); err != nil {
		writer.Close()
		tmp.Close()
		return err
	}

	if err := writer.Close(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	reader.Close()
	return os.Rename(tmpPath, cbzPath)
}

func copyZipEntry(writer *zip.Writer, entry *zip.File) error {
	w, err := writer.CreateHeader(&zip.FileHeader{
		Name:   entry.Name,
		Method: entry.Method,
	})
	if err != nil {
		return err
	}
	r, err := entry.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	_, err = io.Copy(w, r)
	return err
}
