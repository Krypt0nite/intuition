package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Severity of a validation issue. Errors block deployment; warnings are advice.
type Severity string

const (
	SevError   Severity = "error"
	SevWarning Severity = "warning"
)

// Issue is a single validation finding.
type Issue struct {
	Severity Severity `json:"severity"`
	File     string   `json:"file,omitempty"`
	ID       string   `json:"id,omitempty"`
	Field    string   `json:"field,omitempty"`
	Message  string   `json:"message"`
}

func (i Issue) String() string {
	loc := i.File
	if i.ID != "" {
		loc += " [" + i.ID + "]"
	}
	if i.Field != "" {
		loc += " ." + i.Field
	}
	return fmt.Sprintf("%-7s %s: %s", i.Severity, loc, i.Message)
}

// Report collects validation results.
type Report struct {
	Files     int     `json:"files"`
	Questions int     `json:"questions"`
	Issues    []Issue `json:"issues"`
}

func (r *Report) add(sev Severity, file, id, field, format string, args ...any) {
	r.Issues = append(r.Issues, Issue{Severity: sev, File: file, ID: id, Field: field, Message: fmt.Sprintf(format, args...)})
}

func (r *Report) filter(s Severity) []Issue {
	var out []Issue
	for _, i := range r.Issues {
		if i.Severity == s {
			out = append(out, i)
		}
	}
	return out
}

// Errors returns blocking issues.
func (r *Report) Errors() []Issue { return r.filter(SevError) }

// Warnings returns non-blocking issues.
func (r *Report) Warnings() []Issue { return r.filter(SevWarning) }

// HasErrors reports whether any blocking issue was found.
func (r *Report) HasErrors() bool { return len(r.Errors()) > 0 }

// packFile is the on-disk format: shared defaults plus a list of questions.
// A file may also be a bare JSON array of questions.
type packFile struct {
	Pack        string                     `json:"pack"`
	Description string                     `json:"description"`
	Defaults    map[string]json.RawMessage `json:"defaults"`
	Questions   []json.RawMessage          `json:"questions"`
}

type scheduleFile struct {
	Days map[string][]string `json:"days"`
}

// LoadDir loads taxonomy.json, every question pack under dir (except schema/
// and daily/), and daily/schedule.json, then runs full validation.
// The returned error is only for unrecoverable problems (e.g. no taxonomy);
// content problems are reported in the Report.
func LoadDir(dir string) (*Library, *Report, error) {
	rep := &Report{}
	tax, err := loadTaxonomy(filepath.Join(dir, "taxonomy.json"))
	if err != nil {
		return nil, rep, err
	}
	lib := &Library{
		Taxonomy: tax,
		ByID:     map[string]*Question{},
		Chains:   map[string][]*Question{},
		Schedule: map[string][]string{},
		Dir:      dir,
	}
	var files []string
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "schema" || rel == "daily" || (rel != "." && strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".json" && rel != "taxonomy.json" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, rep, fmt.Errorf("walk content dir: %w", err)
	}
	sort.Strings(files)
	for _, f := range files {
		loadPack(dir, f, lib, rep)
	}
	loadSchedule(filepath.Join(dir, "daily", "schedule.json"), lib, rep)
	Validate(lib, rep)

	h := sha256.New()
	ids := make([]string, 0, len(lib.Questions))
	for _, q := range lib.Questions {
		ids = append(ids, q.ID+":"+q.Hash)
	}
	sort.Strings(ids)
	for _, s := range ids {
		h.Write([]byte(s))
	}
	lib.Version = hex.EncodeToString(h.Sum(nil))[:12]
	return lib, rep, nil
}

func loadTaxonomy(path string) (*Taxonomy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read taxonomy: %w", err)
	}
	var t Taxonomy
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, fmt.Errorf("parse taxonomy: %w", err)
	}
	prefixes := map[string]bool{}
	for _, c := range t.Categories {
		if c.ID == "" || c.Name == "" || c.Prefix == "" {
			return nil, fmt.Errorf("taxonomy: every category needs id, name and prefix")
		}
		if prefixes[c.Prefix] {
			return nil, fmt.Errorf("taxonomy: duplicate prefix %q", c.Prefix)
		}
		prefixes[c.Prefix] = true
	}
	t.index()
	return &t, nil
}

func loadPack(root, path string, lib *Library, rep *Report) {
	rel, _ := filepath.Rel(root, path)
	rel = filepath.ToSlash(rel)
	data, err := os.ReadFile(path)
	if err != nil {
		rep.add(SevError, rel, "", "", "cannot read file: %v", err)
		return
	}
	rep.Files++
	var pf packFile
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &pf.Questions); err != nil {
			rep.add(SevError, rel, "", "", "invalid JSON array: %v", err)
			return
		}
	} else {
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&pf); err != nil {
			rep.add(SevError, rel, "", "", "invalid pack JSON: %v", err)
			return
		}
	}
	if pf.Pack == "" {
		pf.Pack = strings.TrimSuffix(filepath.Base(rel), ".json")
	}
	for i, raw := range pf.Questions {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			rep.add(SevError, rel, fmt.Sprintf("#%d", i+1), "", "question is not a JSON object: %v", err)
			continue
		}
		for k, v := range pf.Defaults {
			if _, ok := fields[k]; !ok {
				fields[k] = v
			}
		}
		merged, _ := json.Marshal(fields)
		var q Question
		dec := json.NewDecoder(bytes.NewReader(merged))
		dec.DisallowUnknownFields() // catches typos like "explaination"
		if err := dec.Decode(&q); err != nil {
			id := fmt.Sprintf("#%d", i+1)
			var probe struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(merged, &probe) == nil && probe.ID != "" {
				id = probe.ID
			}
			rep.add(SevError, rel, id, "", "cannot decode question: %v", err)
			continue
		}
		q.File, q.Pack = rel, pf.Pack
		canon, _ := json.Marshal(&q)
		sum := sha256.Sum256(canon)
		q.Hash = hex.EncodeToString(sum[:])[:12]
		lib.Questions = append(lib.Questions, &q)
		rep.Questions++
	}
}

func loadSchedule(path string, lib *Library, rep *Report) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // optional
	}
	var sf scheduleFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sf); err != nil {
		rep.add(SevError, "daily/schedule.json", "", "", "invalid schedule: %v", err)
		return
	}
	for day, ids := range sf.Days {
		lib.Schedule[day] = ids
	}
}
