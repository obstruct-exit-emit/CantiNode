package tagwriter

import (
	"encoding/binary"
	"os"
	"unicode/utf8"
)

// repairInvalidUTF8VorbisComment fixes a real, reproduced bug in
// go.senan.xyz/taglib's WASM-compiled TagLib: reading or writing a FLAC
// file whose existing VORBIS_COMMENT block contains a byte sequence that
// isn't valid UTF-8 throws inside TagLib's own parser
// (__cxa_allocate_exception, uncaught by this package) before Write ever
// gets a chance to overwrite that field with CantiNode's own correct
// value. Found live: an EAC-ripped FLAC with an "Artist=Tobias Sammet´s
// Avantasia" comment (a raw Windows-1252 0xB4 byte where a UTF-8 apostrophe
// belongs — a common mistake from older ripping tools not configured to
// force UTF-8 output for Vorbis comments, which the format has always
// required) failed every single track in the album, identically, on both
// read and write — confirmed by reproducing against the actual file, and
// confirmed fixed by patching just that one byte in place.
//
// The comment's actual (garbled) content doesn't matter here — Write is
// about to overwrite Title/Artist/Album/etc. with CantiNode's own known-
// correct values anyway. This just needs TagLib to be able to open the
// file at all: every invalid byte inside the vendor string or any comment
// value is replaced with '?', one byte at a time (never merging or
// dropping bytes), so every block's declared length stays exactly the
// same and nothing else in the file needs to move — confirmed live this
// alone is enough for TagLib to open the file successfully afterward.
//
// A no-op, not an error, for anything that isn't a well-formed FLAC file
// with a VORBIS_COMMENT block already valid, or with none at all — Write
// proceeds exactly as it would have without this call. Malformed enough
// that a block's declared length doesn't fit the file also leaves it
// untouched rather than guessing; TagLib's own call reports that failure
// the same as it always would.
func repairInvalidUTF8VorbisComment(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) < 4 || string(data[:4]) != "fLaC" {
		return nil
	}

	changed := false
	offset := 4
	for offset+4 <= len(data) {
		header := data[offset]
		length := int(data[offset+1])<<16 | int(data[offset+2])<<8 | int(data[offset+3])
		blockStart := offset + 4
		blockEnd := blockStart + length
		if blockEnd > len(data) {
			break
		}
		if header&0x7f == 4 { // VORBIS_COMMENT
			if sanitizeVorbisCommentBlock(data[blockStart:blockEnd]) {
				changed = true
			}
		}
		if header&0x80 != 0 { // last metadata block
			break
		}
		offset = blockEnd
	}

	if !changed {
		return nil
	}
	return os.WriteFile(path, data, 0o644)
}

// sanitizeVorbisCommentBlock replaces every byte of an invalid UTF-8
// sequence within block's vendor string and comment values with '?', in
// place — block's own length never changes, so no caller needs to touch
// any length field anywhere else in the file. Returns whether anything
// was actually changed. A block malformed enough that its own declared
// lengths don't fit within it is left untouched rather than guessed at.
func sanitizeVorbisCommentBlock(block []byte) bool {
	changed := false
	sanitize := func(b []byte) {
		for i := 0; i < len(b); {
			r, size := utf8.DecodeRune(b[i:])
			if r == utf8.RuneError && size <= 1 {
				b[i] = '?'
				changed = true
				i++
				continue
			}
			i += size
		}
	}

	pos := 0
	if pos+4 > len(block) {
		return false
	}
	vendorLen := int(binary.LittleEndian.Uint32(block[pos:]))
	pos += 4
	if vendorLen < 0 || pos+vendorLen > len(block) {
		return false
	}
	sanitize(block[pos : pos+vendorLen])
	pos += vendorLen

	if pos+4 > len(block) {
		return changed
	}
	count := int(binary.LittleEndian.Uint32(block[pos:]))
	pos += 4
	for i := 0; i < count; i++ {
		if pos+4 > len(block) {
			return changed
		}
		clen := int(binary.LittleEndian.Uint32(block[pos:]))
		pos += 4
		if clen < 0 || pos+clen > len(block) {
			return changed
		}
		sanitize(block[pos : pos+clen])
		pos += clen
	}
	return changed
}
