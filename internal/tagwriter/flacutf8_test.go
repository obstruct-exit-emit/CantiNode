package tagwriter

import (
	"os"
	"testing"

	taglibpkg "go.senan.xyz/taglib"
)

// corruptFirstVorbisCommentByte overwrites the first byte of path's first
// VORBIS_COMMENT value with an invalid UTF-8 byte (0xB4, a lone
// continuation byte — never valid on its own) — the same shape of damage
// an old ripping tool left behind: a real, reproduced upstream TagLib
// bug where a FLAC's own existing non-UTF-8 tag data crashes any read or
// write attempt (see repairInvalidUTF8VorbisComment's own doc comment).
// Same length in, same length out — nothing else about the file changes.
func corruptFirstVorbisCommentByte(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 4 || string(data[:4]) != "fLaC" {
		t.Fatal("fixture is not a FLAC file")
	}
	offset := 4
	for offset+4 <= len(data) {
		header := data[offset]
		length := int(data[offset+1])<<16 | int(data[offset+2])<<8 | int(data[offset+3])
		blockStart := offset + 4
		blockEnd := blockStart + length
		if blockEnd > len(data) {
			t.Fatal("malformed fixture: block overruns file")
		}
		if header&0x7f == 4 { // VORBIS_COMMENT
			block := data[blockStart:blockEnd]
			vendorLen := int(block[0]) | int(block[1])<<8 | int(block[2])<<16 | int(block[3])<<24
			pos := 4 + vendorLen + 4 // vendor_length field + vendor string + comment_count field
			if pos >= len(block) {
				t.Fatal("fixture's VORBIS_COMMENT has no comments to corrupt")
			}
			// pos now points at the first comment's own 4-byte length
			// prefix; the byte right after that is the first byte of its
			// value.
			valueStart := pos + 4
			if valueStart >= len(block) {
				t.Fatal("fixture's first comment is empty, nothing to corrupt")
			}
			block[valueStart] = 0xb4
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		if header&0x80 != 0 {
			break
		}
		offset = blockEnd
	}
	t.Fatal("fixture has no VORBIS_COMMENT block to corrupt")
}

// TestWriteRepairsInvalidUTF8InExistingFLACComment is the regression test
// for a real, live-found bug: go.senan.xyz/taglib's WASM-compiled TagLib
// throws on ANY read or write of a FLAC whose existing VORBIS_COMMENT
// block contains a byte sequence that isn't valid UTF-8 (an EAC-ripped
// file with "Artist=Tobias Sammet´s Avantasia" — a raw Windows-1252 0xB4
// byte — failed every track in the album, both read and write,
// identically). Write must still succeed despite that corruption; the
// corrupted comment's own content doesn't matter since Write is about to
// overwrite this file's known fields with CantiNode's own values anyway.
func TestWriteRepairsInvalidUTF8InExistingFLACComment(t *testing.T) {
	path := copyFixture(t, "sample_tagged.flac")
	corruptFirstVorbisCommentByte(t, path)

	// Confirms the fixture is genuinely corrupted the way the live bug
	// was — TagLib can't even read it back yet.
	if _, err := taglibpkg.ReadTags(path); err == nil {
		t.Fatal("test setup didn't actually corrupt the fixture — ReadTags succeeded before Write ran")
	}

	if err := Write(path, Tags{Title: "New Title", Artist: "New Artist"}, false, AllEnabled); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := taglibpkg.ReadTags(path)
	if err != nil {
		t.Fatalf("ReadTags after Write: %v", err)
	}
	if len(got[taglibpkg.Title]) == 0 || got[taglibpkg.Title][0] != "New Title" {
		t.Errorf("Title after Write = %v, want [New Title]", got[taglibpkg.Title])
	}
	if len(got[taglibpkg.Artist]) == 0 || got[taglibpkg.Artist][0] != "New Artist" {
		t.Errorf("Artist after Write = %v, want [New Artist]", got[taglibpkg.Artist])
	}
}
