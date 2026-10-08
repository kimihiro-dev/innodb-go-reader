package visual

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	innodb "innodb-go-reader"
	"io"
	"net/url"
	"path/filepath"
)

//go:embed report.html
var document string
var pageTemplate = template.Must(template.New("report").Parse(document))

// exactJSON encodes every JSON number as its exact decimal string. JavaScript
// explicitly converts bounded page/offset counts, and uses BigInt for LSN/IDs.
func exactJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var value any
	if err = d.Decode(&value); err != nil {
		return nil, err
	}
	var convert func(any) any
	convert = func(v any) any {
		switch x := v.(type) {
		case json.Number:
			return x.String()
		case []any:
			for i := range x {
				x[i] = convert(x[i])
			}
		case map[string]any:
			for k, v := range x {
				x[k] = convert(v)
			}
		}
		return v
	}
	return json.Marshal(convert(value))
}
func (r *Report) payload(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", innodb.ErrUnsupported)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || r.Space == nil || r.Version != 1 {
		return nil, fmt.Errorf("%w: missing visualization report", innodb.ErrUnsupported)
	}
	b, err := exactJSON(r)
	if err != nil {
		return nil, err
	}
	limit := r.maxBytes
	if limit == 0 {
		limit = 64 << 20
	}
	if uint64(len(b)) > limit {
		return nil, fmt.Errorf("%w: embedded report JSON bytes", innodb.ErrLimit)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return b, nil
}

type manualLink struct {
	Title string
	URL   template.URL
	Path  string
}

// WriteHTML writes a complete offline document; callers needing atomic file
// publication should use the CLI's --output. A writer failure may leave a prefix.
func WriteHTML(ctx context.Context, w io.Writer, r *Report) error {
	if w == nil {
		return fmt.Errorf("%w: nil output", innodb.ErrUnsupported)
	}
	b, err := r.payload(ctx)
	if err != nil {
		return err
	}
	links := []manualLink{{Title: "页与记录", Path: "03-page-to-record.md"}, {Title: "LOB引用与数据页", Path: "13-lob-reference-and-pages.md"}, {Title: "空间指标与分配", Path: "75-space-allocation-layout.md"}, {Title: "离线报告层级", Path: "81-visual-report-model.md"}, {Title: "导航与验证", Path: "82-visual-report-validation.md"}}
	if r.ManualDir != "" {
		if !filepath.IsAbs(r.ManualDir) {
			return fmt.Errorf("%w: manual directory must be absolute", innodb.ErrUnsupported)
		}
		for i := range links {
			u := url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(r.ManualDir, links[i].Path))}
			links[i].URL = template.URL(u.String())
		}
	}
	// json.Marshal escapes <, >, &, U+2028 and U+2029, so even hostile names
	// cannot terminate the application/json script element. Never trust raw input JS.
	err = pageTemplate.Execute(checkedWriter{ctx, w}, struct {
		Title  string
		Data   template.JS
		Manual []manualLink
	}{r.Title, template.JS(b), links})
	if err != nil {
		return err
	}
	return ctx.Err()
}

type checkedWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w checkedWriter) Write(b []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.w.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return n, err
}
