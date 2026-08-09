package tagwriter

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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
// frames), encoding (ISO-8859-1) chosen for simplicity — every field
// CantiNode writes (metadata pulled from MusicBrainz) is plain ASCII in
// practice for the overwhelming majority of releases; genuinely
// non-Latin metadata is a known gap (UTF-8/UTF-16 frame encoding) rather
// than silently mangled, since decodeText elsewhere already tolerates
// whatever's present when reading.
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
	content := append([]byte{0x00}, []byte(value)...) // encoding=ISO-8859-1
	writeFrameHeader(buf, name, len(content))
	buf.Write(content)
}

func writeTXXXFrame(buf *bytes.Buffer, description, value string) {
	if value == "" {
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
