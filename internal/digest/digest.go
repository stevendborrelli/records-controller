// Package digest computes Records digests: SHA-256 over RFC 8785 (JSON
// Canonicalization Scheme) canonical JSON.
package digest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Prefix of every digest.
const Prefix = "sha256:"

// Of returns the digest of the supplied JSON document.
func Of(doc []byte) (string, error) {
	c, err := Canonicalize(doc)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(c)
	return Prefix + hex.EncodeToString(sum[:]), nil
}

// OfValue returns the digest of v marshalled to JSON.
func OfValue(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return Of(b)
}

// Canonicalize returns the RFC 8785 canonical form of a JSON document.
func Canonicalize(doc []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(doc))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, fmt.Errorf("cannot parse JSON: %w", err)
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after JSON document")
	}
	var buf bytes.Buffer
	if err := write(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func write(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		buf.WriteString(strconv.FormatBool(t))
	case json.Number:
		f, err := strconv.ParseFloat(string(t), 64)
		if err != nil {
			return fmt.Errorf("cannot represent number %s as an IEEE 754 double: %w", t, err)
		}
		n, err := formatNumber(f)
		if err != nil {
			return err
		}
		buf.WriteString(n)
	case string:
		writeString(buf, t)
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := write(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		// RFC 8785 sorts properties by their UTF-16 code units.
		slices.SortFunc(keys, func(a, b string) int {
			return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b)))
		})
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeString(buf, k)
			buf.WriteByte(':')
			if err := write(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("unsupported JSON value %T", v)
	}
	return nil
}

// writeString serializes a string as ECMAScript JSON.stringify does.
func writeString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(buf, `\u%04x`, r)
				continue
			}
			var b [utf8.UTFMax]byte
			buf.Write(b[:utf8.EncodeRune(b[:], r)])
		}
	}
	buf.WriteByte('"')
}

// formatNumber serializes a double as ECMAScript Number.prototype.toString
// does, which is what RFC 8785 requires.
func formatNumber(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", fmt.Errorf("NaN and Infinity are not valid JSON numbers")
	}
	if f == 0 {
		return "0", nil // Also normalizes -0.
	}
	sign := ""
	if f < 0 {
		sign = "-"
		f = -f
	}

	// Shortest round-tripping digits, as d.ddde±x.
	e := strconv.FormatFloat(f, 'e', -1, 64)
	mantissa, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	x, err := strconv.Atoi(exp)
	if err != nil {
		return "", err
	}
	k := len(digits)
	n := x + 1 // Position of the decimal point relative to digits.

	var out string
	switch {
	case k <= n && n <= 21:
		out = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		out = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		out = "0." + strings.Repeat("0", -n) + digits
	default:
		out = digits[:1]
		if k > 1 {
			out += "." + digits[1:]
		}
		if n-1 >= 0 {
			out += "e+" + strconv.Itoa(n-1)
		} else {
			out += "e-" + strconv.Itoa(-(n - 1))
		}
	}
	return sign + out, nil
}
