package tagwriter

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"unicode/utf16"
)

// musicBrainzUFIDProvider matches internal/tagreader's constant of the
// same name — the well-known UFID owner Picard (and most other taggers)
// use for the ID3v2 frame carrying a recording's MusicBrainz ID.
const musicBrainzUFIDProvider = "http://musicbrainz.org"

// writeID3v2 replaces path's existing ID3v2 tag (if any) with a fresh
// ID3v2.3 tag built from tags, leaving the audio data after it untouched.
// Written to a temp file in the same directory and renamed over the
// original — the original's file handle is closed before the rename, not
// just before this function returns, since a rename over a still-open
// file fails on Windows (this project develops on Windows day to day;
// see docs/development.md).
func writeID3v2(path string, tags Tags) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}

	skip, err := existingID3v2Size(f)
	if err != nil {
		f.Close()
		return fmt.Errorf("read existing id3v2 header of %s: %w", path, err)
	}
	if _, err := f.Seek(skip, io.SeekStart); err != nil {
		f.Close()
		return fmt.Errorf("seek past existing tag in %s: %w", path, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".cantinode-tagwrite-*")
	if err != nil {
		f.Close()
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	// Any early return after this point must remove the temp file — only
	// the final, successful os.Rename is allowed to leave it behind
	// (under its new name).
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(buildID3v2Tag(tags)); err != nil {
		tmp.Close()
		f.Close()
		return fmt.Errorf("write tag to temp file: %w", err)
	}
	if _, err := io.Copy(tmp, f); err != nil {
		tmp.Close()
		f.Close()
		return fmt.Errorf("copy audio data to temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		f.Close()
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	cleanup = false
	return nil
}

// existingID3v2Size returns the total byte size (header + frames) of an
// existing ID3v2 tag at the start of f, or 0 if there isn't one — so
// writeID3v2 knows how many leading bytes to discard before appending the
// original audio data after the new tag.
func existingID3v2Size(f *os.File) (int64, error) {
	header := make([]byte, 10)
	n, err := io.ReadFull(f, header)
	if err != nil {
		if err == io.ErrUnexpectedEOF || err == io.EOF {
			return 0, nil // file shorter than a tag header — nothing to skip
		}
		return 0, err
	}
	if n != 10 || string(header[0:3]) != "ID3" {
		return 0, nil
	}
	size := synchsafeDecode(header[6:10])
	return 10 + int64(size), nil
}

func synchsafeDecode(b []byte) uint32 {
	return uint32(b[0])<<21 | uint32(b[1])<<14 | uint32(b[2])<<7 | uint32(b[3])
}

func synchsafeEncode(n int) [4]byte {
	return [4]byte{
		byte((n >> 21) & 0x7F),
		byte((n >> 14) & 0x7F),
		byte((n >> 7) & 0x7F),
		byte(n & 0x7F),
	}
}

// buildID3v2Tag renders tags as a complete ID3v2.3 tag (10-byte header +
// frames). Every text frame is ISO-8859-1 when the value is pure ASCII —
// the overwhelming majority of releases — and UTF-16 (with a BOM,
// ID3v2.3's other valid text encoding) otherwise, per encodeID3v2Text.
func buildID3v2Tag(tags Tags) []byte {
	var frames bytes.Buffer

	writeTextFrame(&frames, "TIT2", tags.Title)
	writeTextFrame(&frames, "TPE1", tags.Artist)
	writeTextFrame(&frames, "TALB", tags.Album)
	writeTextFrame(&frames, "TPE2", tags.AlbumArtist)
	if tags.TrackNumber > 0 {
		writeTextFrame(&frames, "TRCK", strconv.Itoa(tags.TrackNumber))
	}
	if tags.DiscNumber > 0 {
		writeTextFrame(&frames, "TPOS", strconv.Itoa(tags.DiscNumber))
	}
	writeTextFrame(&frames, "TYER", tags.Year)

	writeTXXXFrame(&frames, "MusicBrainz Artist Id", tags.MusicBrainzArtistID)
	writeTXXXFrame(&frames, "MusicBrainz Album Id", tags.MusicBrainzAlbumID)
	writeTXXXFrame(&frames, "MusicBrainz Release Group Id", tags.MusicBrainzReleaseGroupID)
	writeUFIDFrame(&frames, tags.MusicBrainzRecordingID)

	var out bytes.Buffer
	out.WriteString("ID3")
	out.Write([]byte{3, 0}) // version 2.3, revision 0
	out.WriteByte(0)        // flags
	size := synchsafeEncode(frames.Len())
	out.Write(size[:])
	out.Write(frames.Bytes())
	return out.Bytes()
}

func writeTextFrame(buf *bytes.Buffer, name, value string) {
	if value == "" {
		return
	}
	content := encodeID3v2Text(value)
	writeFrameHeader(buf, name, len(content))
	buf.Write(content)
}

// writeTXXXFrame's description is always one of this file's own hardcoded
// ASCII literals ("MusicBrainz Artist Id", ...) and value is always an
// MBID (also always ASCII) at every call site today, so this never
// actually hits the UTF-16 branch in practice — encodeID3v2Text is still
// used here rather than hardcoding ISO-8859-1 so that stays true even if
// a future caller ever passes something else, instead of quietly
// depending on an assumption this file doesn't enforce.
func writeTXXXFrame(buf *bytes.Buffer, description, value string) {
	if value == "" {
		return
	}
	if !isASCIIText(description) || !isASCIIText(value) {
		var content bytes.Buffer
		content.WriteByte(0x01) // encoding=UTF-16 (BOM)
		content.Write(utf16LEWithBOM(description))
		content.Write([]byte{0x00, 0x00}) // UTF-16 null terminator between the two parts
		content.Write(utf16LEWithBOM(value))
		writeFrameHeader(buf, "TXXX", content.Len())
		buf.Write(content.Bytes())
		return
	}
	var content bytes.Buffer
	content.WriteByte(0x00) // encoding=ISO-8859-1
	content.WriteString(description)
	content.WriteByte(0x00)
	content.WriteString(value)
	writeFrameHeader(buf, "TXXX", content.Len())
	buf.Write(content.Bytes())
}

// encodeID3v2Text renders value as a complete ID3v2.3 text-frame body
// (leading encoding byte + encoded text): plain ISO-8859-1 when value is
// pure ASCII (every byte < 0x80, so its raw UTF-8 bytes are already
// identical to Latin-1 — the common case, and the most widely compatible
// encoding for it), UTF-16LE with a byte-order-mark otherwise — the fix
// for a real bug found live: a guest vocalist's name with a diacritic
// ("Jørn Lande", "Hansi Kürsch") came back as mojibake ("JÃ¸rn Lande")
// after a round trip. The encoding byte was already correctly read back
// as ISO-8859-1 by every reader (this file always wrote 0x00), but the
// bytes underneath were still raw UTF-8, not actually transcoded — a real
// ID3v2 reader decoding those bytes AS Latin-1 (exactly what the encoding
// byte told it to do) turned every multi-byte UTF-8 sequence into two
// separate Latin-1 characters. ISO-8859-1 can't represent non-Latin
// scripts at all (Cyrillic, CJK, ...), so this doesn't attempt a lossy
// Latin-1 transcode for the non-ASCII case — UTF-16 is ID3v2.3's other
// valid text encoding and losslessly covers everything.
func encodeID3v2Text(value string) []byte {
	if isASCIIText(value) {
		return append([]byte{0x00}, []byte(value)...)
	}
	content := []byte{0x01} // encoding=UTF-16 (BOM)
	return append(content, utf16LEWithBOM(value)...)
}

// utf16LEWithBOM renders s as UTF-16LE code units prefixed with a
// byte-order-mark — the actual encoded bytes encodeID3v2Text (and
// writeTXXXFrame's own UTF-16 branch) write after the leading encoding
// byte.
func utf16LEWithBOM(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 2+2*len(units))
	out[0], out[1] = 0xFF, 0xFE // BOM, little-endian
	for i, u := range units {
		binary.LittleEndian.PutUint16(out[2+2*i:], u)
	}
	return out
}

func isASCIIText(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func writeUFIDFrame(buf *bytes.Buffer, recordingMBID string) {
	if recordingMBID == "" {
		return
	}
	var content bytes.Buffer
	content.WriteString(musicBrainzUFIDProvider)
	content.WriteByte(0x00)
	content.WriteString(recordingMBID)
	writeFrameHeader(buf, "UFID", content.Len())
	buf.Write(content.Bytes())
}

func writeFrameHeader(buf *bytes.Buffer, name string, contentLen int) {
	buf.WriteString(name)
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(contentLen))
	buf.Write(size[:])
	buf.Write([]byte{0, 0}) // flags
}
