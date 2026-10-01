package download

import (
	"crypto/sha1"
	"encoding/hex"
	"testing"
)

// buildTestTorrent assembles a minimal but realistic bencoded .torrent
// around the given raw info-dict bytes, returning both the full file and
// the info-dict span alone — so a test can compute the expected SHA-1
// directly from the exact bytes torrentInfoHash is supposed to hash,
// without hand-calculating a hex digest.
func buildTestTorrent(infoDict string) (full []byte, info string) {
	full = []byte("d8:announce4:test4:info" + infoDict + "e")
	return full, infoDict
}

func TestTorrentInfoHashMatchesRawInfoDictBytes(t *testing.T) {
	infoDict := "d6:lengthi12345e4:name8:test.mp312:piece lengthi16384e6:pieces20:01234567890123456789e"
	full, info := buildTestTorrent(infoDict)

	want := sha1.Sum([]byte(info))
	wantHex := hex.EncodeToString(want[:])

	got, err := torrentInfoHash(full)
	if err != nil {
		t.Fatalf("torrentInfoHash: %v", err)
	}
	if got != wantHex {
		t.Errorf("torrentInfoHash = %q, want %q", got, wantHex)
	}
}

// TestTorrentInfoHashIgnoresSurroundingKeys proves the hash is computed
// only from the info dict's own bytes, independent of whatever other keys
// (and their order) surround it — real .torrent files carry announce,
// announce-list, comment, created by, creation date, etc., none of which
// may affect the info-hash.
func TestTorrentInfoHashIgnoresSurroundingKeys(t *testing.T) {
	infoDict := "d6:lengthi999e4:name4:song12:piece lengthi32768e6:pieces20:AAAAAAAAAAAAAAAAAAAAe"
	minimal, _ := buildTestTorrent(infoDict)
	withExtras := []byte("d7:comment4:test10:created by4:test13:creation datei1e4:info" + infoDict + "e")

	h1, err := torrentInfoHash(minimal)
	if err != nil {
		t.Fatalf("torrentInfoHash(minimal): %v", err)
	}
	h2, err := torrentInfoHash(withExtras)
	if err != nil {
		t.Fatalf("torrentInfoHash(withExtras): %v", err)
	}
	if h1 != h2 {
		t.Errorf("hash changed with surrounding keys: %q vs %q", h1, h2)
	}
}

func TestTorrentInfoHashMalformedInput(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"not a dict", []byte("i123e")},
		{"missing info key", []byte("d8:announce4:teste")},
		{"truncated string length", []byte("d4:info")},
		{"truncated dict", []byte("d4:infod4:name")},
		{"string runs past end", []byte("d4:info99:short")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := torrentInfoHash(c.data); err == nil {
				t.Errorf("torrentInfoHash(%q) = nil error, want one", c.data)
			}
		})
	}
}

// TestTorrentInfoHashSkipsListsAndNestedDicts proves bencodeSkipValue can
// walk past keys whose values are lists or nested dicts (multi-file
// torrents nest a "files" list of dicts inside info) to find a later key
// — not just flat strings/integers.
func TestTorrentInfoHashSkipsListsAndNestedDicts(t *testing.T) {
	full := []byte("d5:filesld4:name3:foo6:lengthi1eee4:info4:reale")

	got, err := torrentInfoHash(full)
	if err != nil {
		t.Fatalf("torrentInfoHash: %v", err)
	}
	want := sha1.Sum([]byte("4:real"))
	wantHex := hex.EncodeToString(want[:])
	if got != wantHex {
		t.Errorf("torrentInfoHash = %q, want %q", got, wantHex)
	}
}

func TestBencodeReadStringAndSkipValue(t *testing.T) {
	s, next, err := bencodeReadString([]byte("4:spamrest"), 0)
	if err != nil || s != "spam" || next != 6 {
		t.Errorf("bencodeReadString = (%q, %d, %v), want (\"spam\", 6, nil)", s, next, err)
	}

	if _, _, err := bencodeReadString([]byte("4spam"), 0); err == nil {
		t.Error("expected error for a string length missing its colon")
	}
	if _, _, err := bencodeReadString([]byte("99:short"), 0); err == nil {
		t.Error("expected error when the declared length runs past the input")
	}

	next, err = bencodeSkipValue([]byte("i42erest"), 0)
	if err != nil || next != 4 {
		t.Errorf("bencodeSkipValue(integer) = (%d, %v), want (4, nil)", next, err)
	}
	if _, err := bencodeSkipValue([]byte("xnope"), 0); err == nil {
		t.Error("expected error for an invalid bencode type byte")
	}
	if _, err := bencodeSkipValue(nil, 0); err == nil {
		t.Error("expected error on empty input")
	}
}
