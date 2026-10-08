package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	innodb "innodb-go-reader"
	"io"
	"os"
	"unicode/utf8"
)

func readQuery(path string, secondary bool) (innodb.SecondaryQuery, error) {
	var q innodb.SecondaryQuery
	f, err := os.Open(path)
	if err != nil {
		return q, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil {
		return q, err
	}
	if len(b) > 1<<20 {
		return q, fmt.Errorf("query exceeds 1 MiB")
	}
	if !utf8.Valid(b) {
		return q, fmt.Errorf("query must be UTF-8")
	}
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' {
		return q, fmt.Errorf("query must be an object")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if secondary {
		err = dec.Decode(&q)
	} else {
		err = dec.Decode(&q.Range)
	}
	if err != nil {
		return q, err
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return q, fmt.Errorf("query must contain one object")
	}
	keys := []innodb.Key{q.Range.Prefix}
	if q.Range.Lower != nil {
		keys = append(keys, q.Range.Lower.Key)
	}
	if q.Range.Upper != nil {
		keys = append(keys, q.Range.Upper.Key)
	}
	for _, key := range keys {
		for i, v := range key {
			if obj, ok := v.(map[string]any); ok {
				s, ok := obj["base64"].(string)
				if !ok || len(obj) != 1 {
					return q, fmt.Errorf("binary key needs only base64")
				}
				key[i], err = base64.StdEncoding.Strict().DecodeString(s)
				if err != nil {
					return q, err
				}
			}
		}
	}
	return q, nil
}
