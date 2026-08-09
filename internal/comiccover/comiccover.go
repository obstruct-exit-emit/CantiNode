// Package comiccover extracts the cover image (first page) from a comic
// archive — CBZ (zip) or CBR (rar, read via the pure-Go rardecode) — so the
// UI can show a real cover for owned manga/comic volumes instead of relying
// on provider metadata.
package comiccover

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/nwaples/rardecode/v2"
)

// imageExt reports whether name has a recognized page extension. The actual
// content type is decided from the bytes' signature, not the extension.
func imageExt(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		return true
	}
	return false
}

// imageSig returns the content type when data begins with a known image
// signature. This is what rejects a non-image masquerading as a page (e.g. a
// placeholder ".jpg" holding text), so callers fall through to the next entry
// or file.
func imageSig(data []byte) (string, bool) {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg", true
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png", true
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif", true
	case len(data) >= 12 && bytes.Equal(data[0:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp", true
	}
	return "", false
}

// ContentType reports the image content type of raw bytes by their
// signature — used to serve a cached cover without re-deriving it from the
// archive. Returns false when the bytes aren't a recognized image.
func ContentType(data []byte) (string, bool) {
	return imageSig(data)
}

// Extract returns the cover page of a .cbz/.zip or .cbr/.rar archive with its
// content type: the first image entry, by name (page order for conventionally
// named comics), whose bytes are a real image.
func Extract(archivePath string) (data []byte, contentType string, err error) {
	switch strings.ToLower(path.Ext(archivePath)) {
	case ".cbz", ".zip":
		return extractZip(archivePath)
	case ".cbr", ".rar":
		return extractRar(archivePath)
	}
	return nil, "", fmt.Errorf("comiccover: unsupported archive %q", path.Base(archivePath))
}

func extractZip(p string) ([]byte, string, error) {
	r, err := zip.OpenReader(p)
	if err != nil {
		return nil, "", err
	}
	defer r.Close()

	imgs := []*zip.File{}
	for _, f := range r.File {
		if !f.FileInfo().IsDir() && imageExt(f.Name) {
			imgs = append(imgs, f)
		}
	}
	sort.Slice(imgs, func(i, j int) bool { return imgs[i].Name < imgs[j].Name })

	for _, f := range imgs {
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		if ct, ok := imageSig(data); ok {
			return data, ct, nil
		}
	}
	return nil, "", fmt.Errorf("comiccover: no readable image in %q", path.Base(p))
}

func extractRar(p string) ([]byte, string, error) {
	files, err := rardecode.List(p)
	if err != nil {
		return nil, "", err
	}
	imgs := []*rardecode.File{}
	for _, f := range files {
		if !f.IsDir && imageExt(f.Name) {
			imgs = append(imgs, f)
		}
	}
	sort.Slice(imgs, func(i, j int) bool { return imgs[i].Name < imgs[j].Name })

	for _, f := range imgs {
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		if ct, ok := imageSig(data); ok {
			return data, ct, nil
		}
	}
	return nil, "", fmt.Errorf("comiccover: no readable image in %q", path.Base(p))
}
