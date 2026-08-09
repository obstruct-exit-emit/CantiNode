package api

import (
	"net/http"
	"testing"
)

// TestRootFolderVariants: manga roots carry a colorized/monochrome variant
// (defaulting to monochrome); other media types never do, and an invalid
// manga variant is rejected.
func TestRootFolderVariants(t *testing.T) {
	a := newTestAPI(t, fakeProvider{})

	type rf struct {
		ID        int64  `json:"id"`
		MediaType string `json:"mediaType"`
		Variant   string `json:"variant"`
		Path      string `json:"path"`
	}

	// Manga root without an explicit variant defaults to monochrome.
	mono := t.TempDir()
	var got rf
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "manga", "path": mono}, &got), http.StatusCreated)
	if got.Variant != "mono" {
		t.Fatalf("manga root default variant = %q, want mono", got.Variant)
	}

	// A colorized manga root is accepted as its own root.
	color := t.TempDir()
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "manga", "variant": "color", "path": color}, &got), http.StatusCreated)
	if got.Variant != "color" {
		t.Fatalf("manga root variant = %q, want color", got.Variant)
	}

	// A bogus manga variant is rejected.
	bad := t.TempDir()
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "manga", "variant": "sepia", "path": bad}, nil), http.StatusBadRequest)

	// Non-manga roots never carry a variant, even if one is sent. (Fresh
	// struct — an empty variant is omitted from the response JSON, so a
	// reused struct would keep its prior value.)
	ebook := t.TempDir()
	var ebookRoot rf
	a.want(a.call("POST", "/api/v1/rootfolder",
		map[string]string{"mediaType": "ebook", "variant": "color", "path": ebook}, &ebookRoot), http.StatusCreated)
	if ebookRoot.Variant != "" {
		t.Fatalf("ebook root variant = %q, want empty", ebookRoot.Variant)
	}

	// The list reflects the stored variants.
	var folders []rf
	a.want(a.call("GET", "/api/v1/rootfolder", nil, &folders), http.StatusOK)
	byType := map[string]string{}
	for _, f := range folders {
		byType[f.MediaType+":"+f.Path] = f.Variant
	}
	if byType["manga:"+mono] != "mono" || byType["manga:"+color] != "color" || byType["ebook:"+ebook] != "" {
		t.Fatalf("listed variants wrong: %+v", byType)
	}
}
