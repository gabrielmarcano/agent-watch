package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// decodePlist decodes an XML property list: <dict> becomes map[string]any,
// <array> []any, <string>/<data>/<date> string, <integer> int64, <real>
// float64, <true/>/<false/> bool. It is enough to read back the LaunchAgent
// this binary writes (and any hand-edited variant of it).
func decodePlist(data []byte) (any, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("plist: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local == "plist" {
			continue
		}
		return decodePlistValue(dec, se)
	}
}

func decodePlistValue(dec *xml.Decoder, se xml.StartElement) (any, error) {
	switch se.Name.Local {
	case "dict":
		m := map[string]any{}
		key, haveKey := "", false
		for {
			tok, err := dec.Token()
			if err != nil {
				return nil, fmt.Errorf("plist dict: %w", err)
			}
			switch t := tok.(type) {
			case xml.StartElement:
				if t.Name.Local == "key" {
					if err := dec.DecodeElement(&key, &t); err != nil {
						return nil, fmt.Errorf("plist key: %w", err)
					}
					haveKey = true
					continue
				}
				if !haveKey {
					return nil, errors.New("plist dict: value without a key")
				}
				v, err := decodePlistValue(dec, t)
				if err != nil {
					return nil, err
				}
				m[key] = v
				haveKey = false
			case xml.EndElement:
				return m, nil
			}
		}
	case "array":
		var out []any
		for {
			tok, err := dec.Token()
			if err != nil {
				return nil, fmt.Errorf("plist array: %w", err)
			}
			switch t := tok.(type) {
			case xml.StartElement:
				v, err := decodePlistValue(dec, t)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			case xml.EndElement:
				return out, nil
			}
		}
	case "string", "data", "date":
		var s string
		if err := dec.DecodeElement(&s, &se); err != nil {
			return nil, fmt.Errorf("plist %s: %w", se.Name.Local, err)
		}
		return s, nil
	case "integer":
		var s string
		if err := dec.DecodeElement(&s, &se); err != nil {
			return nil, fmt.Errorf("plist integer: %w", err)
		}
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("plist integer: %w", err)
		}
		return n, nil
	case "real":
		var s string
		if err := dec.DecodeElement(&s, &se); err != nil {
			return nil, fmt.Errorf("plist real: %w", err)
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return nil, fmt.Errorf("plist real: %w", err)
		}
		return f, nil
	case "true", "false":
		if err := dec.Skip(); err != nil {
			return nil, fmt.Errorf("plist bool: %w", err)
		}
		return se.Name.Local == "true", nil
	default:
		return nil, fmt.Errorf("plist: unsupported element <%s>", se.Name.Local)
	}
}

// xmlEscape escapes s for XML character data (&, <, >, quotes, and
// newlines/tabs as character references).
func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
