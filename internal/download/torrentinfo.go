package download

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strconv"
)

// torrentInfoHash computes a .torrent file's BitTorrent v1 info-hash — the
// hex-encoded SHA-1 of its bencoded "info" dictionary's exact raw bytes,
// per BEP 3 — directly from the file's own bytes, the same real,
// unambiguous identifier qBittorrent itself derives and reports back in
// its own torrent list. Computed and used up front (addFile), the same
// way a magnet's own hash already is (see magnetHash) — Sonarr/Radarr do
// the same: never infer a grab's identity from whatever title a client or
// tracker ends up reporting back, which can legitimately differ from what
// was asked for (a debrid bridge in particular routinely echoes the
// uploader's own name instead of honoring a rename request, silently
// defeating a title-based match — findHash exists only as the
// last-resort fallback for a .torrent this can't parse).
func torrentInfoHash(torrent []byte) (string, error) {
	start, end, err := bencodeDictValueSpan(torrent, "info")
	if err != nil {
		return "", fmt.Errorf("compute torrent info-hash: %w", err)
	}
	sum := sha1.Sum(torrent[start:end])
	return hex.EncodeToString(sum[:]), nil
}

// bencodeDictValueSpan scans the bencoded dictionary at the start of b for
// key, returning the byte offsets of its value's exact raw encoding
// (start inclusive, end exclusive). Bencoding's dictionary key order is
// significant to BEP 3's own info-hash definition, so the original bytes
// are hashed directly rather than re-serialized — a re-encode could only
// ever be trusted to round-trip byte-for-byte by re-implementing every
// bencoding convention real-world torrent files (and the tools that wrote
// them) actually use, which is exactly the risk locating the original
// span sidesteps.
func bencodeDictValueSpan(b []byte, key string) (start, end int, err error) {
	if len(b) == 0 || b[0] != 'd' {
		return 0, 0, fmt.Errorf("not a bencoded dictionary")
	}
	i := 1
	for i < len(b) && b[i] != 'e' {
		k, next, err := bencodeReadString(b, i)
		if err != nil {
			return 0, 0, err
		}
		valStart := next
		valEnd, err := bencodeSkipValue(b, next)
		if err != nil {
			return 0, 0, err
		}
		if k == key {
			return valStart, valEnd, nil
		}
		i = valEnd
	}
	return 0, 0, fmt.Errorf("key %q not found", key)
}

// bencodeReadString reads a bencoded byte-string ("<len>:<bytes>") at
// offset i, returning its decoded value and the offset just past it.
func bencodeReadString(b []byte, i int) (string, int, error) {
	start := i
	for i < len(b) && b[i] != ':' {
		i++
	}
	if i >= len(b) {
		return "", 0, fmt.Errorf("malformed bencoded string length")
	}
	n, err := strconv.Atoi(string(b[start:i]))
	if err != nil || n < 0 {
		return "", 0, fmt.Errorf("malformed bencoded string length")
	}
	i++ // skip ':'
	if i+n > len(b) {
		return "", 0, fmt.Errorf("bencoded string runs past end of input")
	}
	return string(b[i : i+n]), i + n, nil
}

// bencodeSkipValue returns the offset just past the bencoded value
// (string, integer, list, or dict) starting at i, without decoding it —
// all bencodeDictValueSpan needs from anything but the one key it wants.
func bencodeSkipValue(b []byte, i int) (int, error) {
	if i >= len(b) {
		return 0, fmt.Errorf("unexpected end of input")
	}
	switch {
	case b[i] == 'i':
		j := i + 1
		for j < len(b) && b[j] != 'e' {
			j++
		}
		if j >= len(b) {
			return 0, fmt.Errorf("malformed bencoded integer")
		}
		return j + 1, nil
	case b[i] == 'l' || b[i] == 'd':
		isDict := b[i] == 'd'
		j := i + 1
		for j < len(b) && b[j] != 'e' {
			if isDict {
				_, next, err := bencodeReadString(b, j)
				if err != nil {
					return 0, err
				}
				j = next
			}
			next, err := bencodeSkipValue(b, j)
			if err != nil {
				return 0, err
			}
			j = next
		}
		if j >= len(b) {
			return 0, fmt.Errorf("malformed bencoded list/dict")
		}
		return j + 1, nil
	case b[i] >= '0' && b[i] <= '9':
		_, next, err := bencodeReadString(b, i)
		return next, err
	default:
		return 0, fmt.Errorf("invalid bencode type byte %q", b[i])
	}
}
