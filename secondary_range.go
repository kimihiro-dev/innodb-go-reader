package innodb

import (
	"fmt"
	"unicode/utf8"
)

type secondaryBound struct {
	key       indexKey
	inclusive bool
}
type secondarySelection struct {
	lower, upper           *secondaryBound
	exactLower, exactUpper *secondaryBound
	columns                []Column
	reverse, empty         bool
}

func encodeSecondaryKey(key Key, fields []SecondaryField) (indexKey, error) {
	if len(key) == 0 || len(key) > len(fields) {
		return nil, fmt.Errorf("%w: secondary query member count", ErrUnsupported)
	}
	result := make(indexKey, len(key))
	for i, value := range key {
		c := fields[i].Definition
		if value == nil {
			if !c.Nullable {
				return nil, fmt.Errorf("%w: NULL nonnullable query member", ErrUnsupported)
			}
			continue
		}
		b, err := encodeQueryValue(value, c)
		if err != nil {
			return nil, fmt.Errorf("%w: secondary query %s: %v", ErrUnsupported, c.Name, err)
		}
		result[i] = b
	}
	return result, nil
}

// Truncate encoded characters, not arbitrary UTF-8 bytes. For single-byte
// charsets PrefixBytes is also the number of characters.
func secondaryPrefix(raw []byte, f SecondaryField) []byte {
	if raw == nil || f.PrefixBytes == 0 {
		return raw
	}
	n := f.PrefixBytes
	if f.Definition.isText() && f.Definition.charsetWidth() > 1 {
		count := n / f.Definition.charsetWidth()
		n = 0
		for count > 0 && n < len(raw) {
			_, width := utf8.DecodeRune(raw[n:])
			n += width
			count--
		}
	}
	if len(raw) < n {
		n = len(raw)
	}
	return raw[:n]
}
func prepareSecondaryRange(q KeyRange, s SecondarySchema) (*secondarySelection, error) {
	x := &secondarySelection{reverse: q.Reverse}
	for _, f := range s.Fields[:s.UserFields] {
		x.columns = append(x.columns, f.Definition)
	}
	if q.Prefix != nil && (q.Lower != nil || q.Upper != nil) {
		return nil, fmt.Errorf("%w: prefix and bounds conflict", ErrUnsupported)
	}
	if q.Prefix != nil {
		key, err := encodeSecondaryKey(q.Prefix, s.Fields[:s.UserFields])
		if err != nil {
			return nil, err
		}
		x.exactLower = &secondaryBound{key, true}
		x.exactUpper = &secondaryBound{key, true}
	} else {
		for _, v := range []struct {
			in  *KeyBound
			out **secondaryBound
		}{{q.Lower, &x.exactLower}, {q.Upper, &x.exactUpper}} {
			if v.in == nil {
				continue
			}
			if len(v.in.Key) != s.UserFields {
				return nil, fmt.Errorf("%w: complete declared secondary key required", ErrUnsupported)
			}
			key, err := encodeSecondaryKey(v.in.Key, s.Fields[:s.UserFields])
			if err != nil {
				return nil, err
			}
			*v.out = &secondaryBound{key, v.in.Inclusive}
		}
	}
	if x.exactLower != nil && x.exactUpper != nil {
		cmp := x.compare(x.exactLower.key, x.exactUpper.key)
		x.empty = cmp > 0 || cmp == 0 && (!x.exactLower.inclusive || !x.exactUpper.inclusive)
	}
	coarse := func(b *secondaryBound) *secondaryBound {
		if b == nil {
			return nil
		}
		out := &secondaryBound{key: append(indexKey{}, b.key...), inclusive: b.inclusive}
		for i, raw := range out.key {
			if s.Fields[i].PrefixBytes > 0 {
				out.key = out.key[:i+1]
				out.key[i] = secondaryPrefix(raw, s.Fields[i])
				out.inclusive = true
				break
			}
		}
		return out
	}
	x.lower = coarse(x.exactLower)
	x.upper = coarse(x.exactUpper)
	return x, nil
}
func (x *secondarySelection) compare(key, bound indexKey) int {
	return compareSecondary(key, bound, x.columns[:len(bound)])
}
func (x *secondarySelection) below(key indexKey, b *secondaryBound) bool {
	if b == nil {
		return false
	}
	c := x.compare(key, b.key)
	return c < 0 || c == 0 && !b.inclusive
}
func (x *secondarySelection) above(key indexKey, b *secondaryBound) bool {
	if b == nil {
		return false
	}
	c := x.compare(key, b.key)
	return c > 0 || c == 0 && !b.inclusive
}
func (x *secondarySelection) matches(key indexKey) bool {
	return !x.below(key, x.exactLower) && !x.above(key, x.exactUpper)
}
func (x *secondarySelection) entryRange(entries []pageEntry, p Page, low, high indexKey) (int, int) {
	if p.Level == 0 {
		return directorySearch(entries, p, func(i int) bool { return !x.below(entries[i].order, x.lower) }), directorySearch(entries, p, func(i int) bool { return x.above(entries[i].order, x.upper) })
	}
	first := directorySearch(entries, p, func(i int) bool {
		hi := high
		if i+1 < len(entries) {
			hi = entries[i+1].order
		}
		if hi == nil || x.lower == nil {
			return true
		}
		cmp := x.compare(hi, x.lower.key)
		return cmp > 0 || cmp == 0 && x.lower.inclusive
	})
	end := directorySearch(entries, p, func(i int) bool {
		lo := low
		if !entries[i].minimum {
			lo = entries[i].order
		}
		return lo != nil && x.above(lo, x.upper)
	})
	return first, end
}
