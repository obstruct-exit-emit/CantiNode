package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/librinode/librinode/internal/library"
	"github.com/librinode/librinode/internal/scanner"
)

// Existing-file import: the white-glove path for files a scan found but could
// not confidently match. Each unmatched prose file gets its author's
// bibliography as candidates (excluding books already owned in that format);
// when the filename singles out exactly one candidate the match is confident —
// importable in one click individually, or all at once via import-matched.
// Matching a file enrolls the book in the format's library and monitors it,
// so an adopted book behaves like one added by hand.

// unmatchedCandidate is one of the author's books offered for manual selection.
type unmatchedCandidate struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Year  string `json:"year,omitempty"`
}

// unmatchedOption is an unmatched file plus everything the import flow needs.
// Prose files carry author fields and book candidates; manga/comic files carry
// series fields, the parsed volume number, and volume candidates; magazine
// files carry series fields and the parsed issue identifier (issues are
// materialized on import, so a confident magazine match may have no Suggested
// book yet).
type unmatchedOption struct {
	File       library.BookFile     `json:"file"`
	AuthorName string               `json:"authorName,omitempty"`
	AuthorID   int64                `json:"authorId,omitempty"`
	SeriesName string               `json:"seriesName,omitempty"`
	SeriesID   int64                `json:"seriesId,omitempty"`
	Volume     float64              `json:"volume,omitempty"`    // manga/comic: parsed volume number
	Issue      string               `json:"issue,omitempty"`     // magazine: parsed issue identifier
	Suggested  int64                `json:"suggested,omitempty"` // candidate id; confident when Confident
	Confident  bool                 `json:"confident"`
	Confidence int                  `json:"confidence"` // 0–100, how sure the suggestion is
	Candidates []unmatchedCandidate `json:"candidates"`
	// Duplicate is set when the file confidently matches a book already OWNED
	// in this format — the user resolves it: replace the library's copy with
	// this file, or delete this file from disk.
	Duplicate *duplicateInfo `json:"duplicate,omitempty"`
}

// duplicateInfo names the owned book an unmatched file duplicates, and the
// file currently holding that spot in the library.
type duplicateInfo struct {
	BookID     int64            `json:"bookId"`
	Title      string           `json:"title"`
	Year       string           `json:"year,omitempty"`
	File       library.BookFile `json:"file"` // the book's current file in this format
	Confidence int              `json:"confidence"`
}

// relFile pairs an unmatched file with its path relative to its root folder —
// what every per-type parser works from.
type relFile struct {
	file library.BookFile
	rel  string
}

// unmatchedOptions builds the import options for every unmatched file of one
// media type — each library gets its own matching, same rich shape.
func (s *server) unmatchedOptions(mediaType string) ([]unmatchedOption, error) {
	files, err := s.store.ListUnmatchedBookFiles()
	if err != nil {
		return nil, err
	}
	roots, err := s.store.ListRootFolders()
	if err != nil {
		return nil, err
	}
	rootByID := map[int64]string{}
	for _, r := range roots {
		rootByID[r.ID] = r.Path
	}

	mine := []relFile{}
	for i := range files {
		f := files[i]
		if f.MediaType != mediaType {
			continue
		}
		rel := f.Path
		if root, ok := rootByID[f.RootFolderID]; ok {
			if r, err := filepath.Rel(root, f.Path); err == nil {
				rel = r
			}
		}
		mine = append(mine, relFile{file: f, rel: rel})
	}

	switch mediaType {
	case "manga", "comic":
		return s.volumeOptions(mediaType, mine)
	case "magazine":
		return s.magazineOptions(mine)
	default:
		return s.proseOptions(mediaType, mine)
	}
}

// proseOptions matches ebook/audiobook files against their author-folder's
// bibliography.
func (s *server) proseOptions(mediaType string, files []relFile) ([]unmatchedOption, error) {
	authors, err := s.store.ListAuthors()
	if err != nil {
		return nil, err
	}
	authorByName := map[string]*library.Author{}
	for i := range authors {
		authorByName[scanner.Normalize(authors[i].Name)] = &authors[i]
	}

	out := []unmatchedOption{}
	for i := range files {
		f := files[i].file
		rel := files[i].rel
		opt := unmatchedOption{File: f, Candidates: []unmatchedCandidate{}}

		parsed := scanner.ParsePath(rel)
		opt.AuthorName = parsed.Author

		author := authorByName[scanner.Normalize(parsed.Author)]
		if parsed.Author == "" || author == nil {
			out = append(out, opt) // author unknown — nothing to offer
			continue
		}
		opt.AuthorID = author.ID
		opt.AuthorName = author.Name // canonical casing over the folder's

		books, err := s.store.ListBooks(author.ID)
		if err != nil {
			return nil, err
		}
		// The filename's normalized text; a candidate whose title appears in
		// it is a hit. The candidate matching the LONGEST title wins — "Dune
		// Messiah retail" contains both "dune messiah" and "dune", and the
		// longer match is the real one. Confident only when exactly one
		// candidate attains the longest match.
		normTitle := scanner.Normalize(parsed.Title)
		normAlt := scanner.Normalize(parsed.AltTitle) // "" when no alt
		// hitIn reports whether a key appears in the parsed title (or its
		// after-the-dash alt) and how much of that text the key explains —
		// the coverage half of the confidence rating.
		hitIn := func(key string) (bool, float64) {
			if normAlt != "" && strings.Contains(normAlt, key) {
				return true, float64(len(key)) / float64(len(normAlt))
			}
			if normTitle != "" && strings.Contains(normTitle, key) {
				return true, float64(len(key)) / float64(len(normTitle))
			}
			return false, 0
		}
		// Unowned books become candidates; owned books are tracked separately —
		// a confident match against one means this file is a DUPLICATE of a
		// book the library already has.
		var want, dup matchTally
		var dupBook *library.Book
		// Fuzzy fallback (used only when nothing matched by containment): the
		// closest unowned title by character-bigram similarity, with the
		// runner-up so an ambiguous near-tie can be rejected.
		var fuzzyBest, fuzzySecond float64
		var fuzzyBook *library.Book
		for j := range books {
			b := &books[j]
			if b.MediaType != "book" {
				continue // volumes/issues import through their series
			}
			owned := b.HasEbookFile
			if mediaType == "audiobook" {
				owned = b.HasAudiobookFile
			}
			// This book's longest matching key and its coverage.
			hit := 0
			cov := 0.0
			isExact := false
			for _, key := range scanner.TitleKeys(b.Title) {
				if key == "" || len(key) <= hit {
					continue
				}
				if ok, c := hitIn(key); ok {
					hit, cov = len(key), c
					isExact = key == normTitle || key == normAlt
				}
			}
			if owned {
				if dup.consider(hit, cov, isExact) {
					dupBook = b
				}
				continue
			}
			cand := unmatchedCandidate{ID: b.ID, Title: b.Title}
			if len(b.ReleaseDate) >= 4 {
				cand.Year = b.ReleaseDate[:4]
			}
			opt.Candidates = append(opt.Candidates, cand)
			if want.consider(hit, cov, isExact) {
				opt.Suggested = b.ID
			}
			// Track the closest title by fuzzy similarity for the rescue path.
			sim := 0.0
			for _, key := range scanner.TitleKeys(b.Title) {
				if key == "" {
					continue
				}
				if v := scanner.Similarity(key, normTitle); v > sim {
					sim = v
				}
				if normAlt != "" {
					if v := scanner.Similarity(key, normAlt); v > sim {
						sim = v
					}
				}
			}
			if sim > fuzzyBest {
				fuzzyBest, fuzzySecond, fuzzyBook = sim, fuzzyBest, b
			} else if sim > fuzzySecond {
				fuzzySecond = sim
			}
		}
		opt.Confident = want.unique()
		opt.Confidence = want.confidence()
		if !opt.Confident {
			opt.Suggested = 0 // ambiguous (or nothing) — the user picks
		}
		// Fuzzy rescue: nothing contained the title, but one candidate is close
		// and clearly ahead of the runner-up. Pre-select it so the file lands
		// in the picker with a best guess — never confident, so it's never
		// auto-imported; the user confirms.
		const fuzzyFloor, fuzzyGap = 0.72, 0.08
		if want.bestLen == 0 && opt.Suggested == 0 && fuzzyBook != nil &&
			fuzzyBest >= fuzzyFloor && fuzzyBest-fuzzySecond >= fuzzyGap {
			opt.Suggested = fuzzyBook.ID
		}
		// Duplicate flag: a unique confident match against an owned book, with
		// the file currently holding that spot so the user can compare.
		if dup.unique() && dupBook != nil {
			if bookFiles, err := s.store.ListBookFiles(dupBook.ID); err == nil {
				for k := range bookFiles {
					if bookFiles[k].MediaType == mediaType {
						info := &duplicateInfo{
							BookID: dupBook.ID, Title: dupBook.Title,
							File: bookFiles[k], Confidence: dup.confidence(),
						}
						if len(dupBook.ReleaseDate) >= 4 {
							info.Year = dupBook.ReleaseDate[:4]
						}
						opt.Duplicate = info
						break
					}
				}
			}
		}
		out = append(out, opt)
	}
	return out, nil
}

// volumeOptions matches manga/comic archives against library series: the
// series comes from the folder (or filename prefix), the volume from the
// number in the name. Ownership is variant-aware for manga — a colorized file
// only duplicates the volume's colorized copy.
func (s *server) volumeOptions(mediaType string, files []relFile) ([]unmatchedOption, error) {
	seriesList, err := s.store.ListSeries(mediaType)
	if err != nil {
		return nil, err
	}
	byNorm := map[string]*library.Series{}
	for i := range seriesList {
		byNorm[scanner.Normalize(seriesList[i].Title)] = &seriesList[i]
	}
	// Per-series volume data, fetched once per series actually referenced.
	type seriesData struct {
		volumes   []library.Book
		positions map[int64]float64
	}
	cache := map[int64]*seriesData{}
	load := func(id int64) (*seriesData, error) {
		if d, ok := cache[id]; ok {
			return d, nil
		}
		volumes, err := s.store.ListVolumes(id)
		if err != nil {
			return nil, err
		}
		positions, err := s.store.SeriesBookPositions(id)
		if err != nil {
			return nil, err
		}
		d := &seriesData{volumes: volumes, positions: positions}
		cache[id] = d
		return d, nil
	}
	ownedFor := func(v *library.Book, variant string) bool {
		if mediaType == "manga" {
			if variant == "color" {
				return v.HasColorFile
			}
			return v.HasMonoFile
		}
		return v.HasFile
	}

	out := []unmatchedOption{}
	for i := range files {
		f := files[i].file
		guess, number := scanner.ComicGuess(files[i].rel)
		opt := unmatchedOption{File: f, SeriesName: guess, Volume: number,
			Candidates: []unmatchedCandidate{}}

		// Exact series name first; else a unique fuzzy hit (the guess contains
		// the series title or vice versa).
		sr := byNorm[scanner.Normalize(guess)]
		exactSeries := sr != nil
		if sr == nil {
			normGuess := scanner.Normalize(guess)
			var only *library.Series
			hits := 0
			for j := range seriesList {
				st := scanner.Normalize(seriesList[j].Title)
				if st != "" && normGuess != "" &&
					(strings.Contains(normGuess, st) || strings.Contains(st, normGuess)) {
					hits++
					only = &seriesList[j]
				}
			}
			if hits == 1 {
				sr = only
			}
		}
		if sr == nil {
			out = append(out, opt) // series unknown — UI offers adding it
			continue
		}
		opt.SeriesID = sr.ID
		opt.SeriesName = sr.Title

		data, err := load(sr.ID)
		if err != nil {
			return nil, err
		}
		var match *library.Book
		for j := range data.volumes {
			v := &data.volumes[j]
			if !ownedFor(v, f.Variant) {
				cand := unmatchedCandidate{ID: v.ID, Title: v.Title}
				if len(v.ReleaseDate) >= 4 {
					cand.Year = v.ReleaseDate[:4]
				}
				opt.Candidates = append(opt.Candidates, cand)
			}
			if number > 0 && data.positions[v.ID] == number {
				match = v
			}
		}
		switch {
		case number == 0:
			// No volume number in the name — manual pick only.
		case match == nil:
			opt.Confidence = 30 // series matched, but no such volume position
		case ownedFor(match, f.Variant):
			// The volume (this variant, for manga) is already owned: duplicate.
			conf := 85
			if exactSeries {
				conf = 95
			}
			if bookFiles, err := s.store.ListBookFiles(match.ID); err == nil {
				for k := range bookFiles {
					bf := &bookFiles[k]
					if bf.MediaType != mediaType {
						continue
					}
					if mediaType == "manga" && bf.Variant != f.Variant {
						continue
					}
					info := &duplicateInfo{BookID: match.ID, Title: match.Title,
						File: *bf, Confidence: conf}
					if len(match.ReleaseDate) >= 4 {
						info.Year = match.ReleaseDate[:4]
					}
					opt.Duplicate = info
					break
				}
			}
			opt.Confidence = conf
		default:
			opt.Suggested = match.ID
			opt.Confident = true
			opt.Confidence = 85
			if exactSeries {
				opt.Confidence = 95
			}
		}
		out = append(out, opt)
	}
	return out, nil
}

// magazineOptions matches magazine files against magazine series by title and
// issue identifier (date or number). Import materializes the issue, so a
// confident option may carry no Suggested book yet — series + issue is enough.
func (s *server) magazineOptions(files []relFile) ([]unmatchedOption, error) {
	mags, err := s.store.ListSeries("magazine")
	if err != nil {
		return nil, err
	}
	byNorm := map[string]*library.Series{}
	for i := range mags {
		byNorm[scanner.Normalize(mags[i].Title)] = &mags[i]
	}

	out := []unmatchedOption{}
	for i := range files {
		f := files[i].file
		title, identifier := scanner.MagazineGuess(files[i].rel)
		opt := unmatchedOption{File: f, SeriesName: title, Issue: identifier,
			Candidates: []unmatchedCandidate{}}

		sr := byNorm[scanner.Normalize(title)]
		exact := sr != nil
		if sr == nil {
			normTitle := scanner.Normalize(title)
			var only *library.Series
			hits := 0
			for j := range mags {
				st := scanner.Normalize(mags[j].Title)
				if st != "" && normTitle != "" &&
					(strings.Contains(normTitle, st) || strings.Contains(st, normTitle)) {
					hits++
					only = &mags[j]
				}
			}
			if hits == 1 {
				sr = only
			}
		}
		if sr == nil {
			out = append(out, opt) // magazine unknown — UI offers adding it by name
			continue
		}
		opt.SeriesID = sr.ID
		opt.SeriesName = sr.Title
		if identifier == "" {
			out = append(out, opt) // no date/number in the name — dismiss or rename
			continue
		}
		conf := 80
		if exact {
			conf = 95
		}
		if existing, err := s.store.GetBookByForeignID(sr.Source, sr.ForeignID+":"+identifier); err == nil {
			if existing.HasFile {
				// Issue already owned: duplicate.
				if bookFiles, err := s.store.ListBookFiles(existing.ID); err == nil && len(bookFiles) > 0 {
					opt.Duplicate = &duplicateInfo{BookID: existing.ID, Title: existing.Title,
						File: bookFiles[0], Confidence: conf}
				}
				opt.Confidence = conf
				out = append(out, opt)
				continue
			}
			opt.Suggested = existing.ID // issue exists, unowned — plain adopt
		}
		opt.Confident = true
		opt.Confidence = conf
		out = append(out, opt)
	}
	return out, nil
}

// matchTally accumulates one match race: the longest hit wins, ties are
// remembered (they kill confidence), and the runner-up length feeds the gap
// half of the rating.
type matchTally struct {
	bestLen, secondLen, bestCount int
	bestCov                       float64
	exact                         bool
}

// consider feeds one book's longest hit; true when it takes the lead.
func (t *matchTally) consider(hit int, cov float64, isExact bool) bool {
	switch {
	case hit == 0:
	case hit > t.bestLen:
		t.secondLen = t.bestLen
		t.bestLen, t.bestCov, t.bestCount, t.exact = hit, cov, 1, isExact
		return true
	case hit == t.bestLen:
		t.bestCount++
	default:
		if hit > t.secondLen {
			t.secondLen = hit
		}
	}
	return false
}

// unique reports a single undisputed winner.
func (t *matchTally) unique() bool { return t.bestLen > 0 && t.bestCount == 1 }

// confidence turns the tally into a 0–100 rating: an exact title is certain;
// a unique longest match scores by how much of the filename the title
// explains (coverage) plus how far ahead of the runner-up it is (gap); a tie
// can't be trusted regardless of length.
func (t *matchTally) confidence() int {
	switch {
	case t.bestLen == 0:
		return 0
	case t.bestCount > 1:
		return 40 // something matched, but it's a coin toss
	case t.exact:
		return 100
	default:
		gap := float64(t.bestLen-t.secondLen) / float64(t.bestLen)
		return 55 + int(25*t.bestCov+0.5) + int(20*gap+0.5)
	}
}

// importableMediaType reports whether the existing-file import flow serves a
// media type — all five libraries (magazines too: importing an on-disk issue
// is organizational, not acquisition).
func importableMediaType(mediaType string) bool {
	switch mediaType {
	case "ebook", "audiobook", "manga", "comic", "magazine":
		return true
	}
	return false
}

// handleUnmatchedOptions serves the existing-file import view for a library:
// GET /api/v1/bookfile/unmatched/options?mediaType=….
func (s *server) handleUnmatchedOptions(w http.ResponseWriter, r *http.Request) {
	mediaType := r.URL.Query().Get("mediaType")
	if !importableMediaType(mediaType) {
		writeError(w, http.StatusBadRequest, "mediaType must be ebook, audiobook, manga, comic, or magazine")
		return
	}
	options, err := s.unmatchedOptions(mediaType)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, options)
}

// importTarget resolves the book a confident option imports into. Magazine
// issues that don't exist yet are materialized here (unmonitored — the file
// in hand IS the issue).
func (s *server) importTarget(opt *unmatchedOption) (int64, error) {
	if opt.Suggested > 0 {
		return opt.Suggested, nil
	}
	if opt.File.MediaType == "magazine" && opt.SeriesID > 0 && opt.Issue != "" {
		series, err := s.store.GetSeries(opt.SeriesID)
		if err != nil {
			return 0, err
		}
		book, err := s.store.CreateMagazineIssue(series, opt.Issue, false)
		if err != nil {
			return 0, err
		}
		return book.ID, nil
	}
	return 0, nil
}

// handleImportMatched imports every confident match in one go:
// POST /api/v1/bookfile/import-matched {"mediaType":"…"}. Files without a
// confident match are left for per-file review.
func (s *server) handleImportMatched(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MediaType string `json:"mediaType"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !importableMediaType(req.MediaType) {
		writeError(w, http.StatusBadRequest, "mediaType must be ebook, audiobook, manga, comic, or magazine")
		return
	}
	options, err := s.unmatchedOptions(req.MediaType)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	imported := 0
	review := 0
	messages := []string{}
	for i := range options {
		opt := &options[i]
		if !opt.Confident {
			review++
			continue
		}
		bookID, err := s.importTarget(opt)
		if err != nil || bookID == 0 {
			if err != nil {
				messages = append(messages, filepath.Base(opt.File.Path)+": "+err.Error())
			}
			review++
			continue
		}
		skips, err := s.adoptFile(opt.File.ID, bookID)
		if err != nil {
			messages = append(messages, filepath.Base(opt.File.Path)+": "+err.Error())
			review++
			continue
		}
		messages = append(messages, skips...)
		imported++
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"imported": imported, "needsReview": review, "messages": messages,
	})
}
