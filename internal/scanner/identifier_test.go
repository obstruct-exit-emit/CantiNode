package scanner

import "testing"

func TestNormalizeISBN(t *testing.T) {
	cases := map[string]string{
		"9780553380163":     "9780553380163", // already ISBN-13
		"978-0-553-38016-3": "9780553380163", // hyphenated
		"0553380168":        "9780553380163", // ISBN-10 → 13 (same book)
		"0-553-38016-8":     "9780553380163", // hyphenated ISBN-10
		"080442957X":        "9780804429573", // ISBN-10 with X check digit
		"9780553380164":     "",              // bad ISBN-13 checksum
		"0553380169":        "",              // bad ISBN-10 checksum
		"1234567890123":     "",              // 13 digits, not a valid ISBN
		"5551234567":        "",              // 10-digit phone, not an ISBN
		"":                  "",
		"978055338016":      "", // 12 digits — wrong length
	}
	for in, want := range cases {
		if got := NormalizeISBN(in); got != want {
			t.Errorf("NormalizeISBN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestISBNFromName(t *testing.T) {
	cases := map[string]string{
		"A Game of Thrones 9780553380163":       "9780553380163",
		"A Game of Thrones (978-0-553-38016-3)": "9780553380163",
		"Dune 0553380168 retail":                "9780553380163",
		"The Martian (2011)":                    "", // a year is not an ISBN
		"Some Book":                             "",
		"Call me at 555-123-4567":               "", // phone number rejected by checksum
	}
	for in, want := range cases {
		if got := ISBNFromName(in); got != want {
			t.Errorf("ISBNFromName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestASINFromName(t *testing.T) {
	cases := map[string]string{
		"Book [B0072XL8BC]":     "B0072XL8BC",
		"Title B00X57B4AG here": "B00X57B4AG",
		"BUREAUCRAT novel":      "", // 10-letter caps word is not an ASIN (needs B0)
		"nothing here":          "",
	}
	for in, want := range cases {
		if got := ASINFromName(in); got != want {
			t.Errorf("ASINFromName(%q) = %q, want %q", in, got, want)
		}
	}
}
