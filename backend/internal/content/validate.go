package content

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

var (
	slugRe        = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	placeholderRe = regexp.MustCompile(`(?i)\b(todo|tbd|tbc|lorem|ipsum|placeholder|fixme|xxx+)\b`)
)

// Explanation length bounds (words). The soft range is the style target.
const (
	MinExplanationWords     = 25
	SoftMinExplanationWords = 45
	SoftMaxExplanationWords = 130
	MaxExplanationWords     = 220
)

// Validate runs every check against a loaded library and fills lib.ByID and
// lib.Chains. It is called by LoadDir; tests may call it directly.
func Validate(lib *Library, rep *Report) {
	lib.ByID = map[string]*Question{}
	lib.Chains = map[string][]*Question{}
	kept := make([]*Question, 0, len(lib.Questions))
	for _, q := range lib.Questions {
		if strings.TrimSpace(q.ID) == "" {
			rep.add(SevError, q.File, "", "id", "missing id")
			continue
		}
		if prev, dup := lib.ByID[q.ID]; dup {
			rep.add(SevError, q.File, q.ID, "id", "duplicate id (first defined in %s)", prev.File)
			continue
		}
		lib.ByID[q.ID] = q
		kept = append(kept, q)
		validateQuestion(lib.Taxonomy, q, rep)
	}
	lib.Questions = kept
	validateChains(lib, rep)
	validateSchedule(lib, rep)
	CheckDuplicates(lib.Questions, rep)
}

func validateQuestion(tax *Taxonomy, q *Question, rep *Report) {
	e := func(field, f string, a ...any) { rep.add(SevError, q.File, q.ID, field, f, a...) }
	w := func(field, f string, a ...any) { rep.add(SevWarning, q.File, q.ID, field, f, a...) }

	if !slugRe.MatchString(q.ID) {
		e("id", "must be lowercase words/digits joined by single hyphens")
	}
	if cat := tax.Category(q.Category); cat == nil {
		e("category", "unknown category %q (declare it in taxonomy.json)", q.Category)
	} else {
		if !strings.HasPrefix(q.ID, cat.Prefix+"-") {
			e("id", "must start with the category prefix %q", cat.Prefix+"-")
		}
		if q.Subcategory == "" {
			e("subcategory", "required")
		} else if !tax.HasSubcategory(q.Category, q.Subcategory) {
			e("subcategory", "%q is not listed for %s in taxonomy.json", q.Subcategory, q.Category)
		}
	}
	if q.Difficulty.Level() == 0 {
		e("difficulty", "must be easy, medium, hard or expert (got %q)", q.Difficulty)
	}
	if !slices.Contains(tax.QuestionTypes, q.QuestionType) {
		e("questionType", "unknown question type %q", q.QuestionType)
	}
	if !slices.Contains(tax.AnswerTypes, q.AnswerType) {
		e("answerType", "unknown answer type %q", q.AnswerType)
	}

	required := map[string]string{
		"scenario": q.Scenario, "question": q.Question, "reveal": q.Reveal,
		"explanation": q.Explanation, "intuitionTrap": q.IntuitionTrap,
		"principle": q.Principle, "variantGroup": q.VariantGroup,
	}
	keys := make([]string, 0, len(required))
	for k := range required {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.TrimSpace(required[k]) == "" {
			e(k, "required")
		}
	}

	// Answers.
	if n := len(q.Options); n < 2 || n > 4 {
		e("options", "need 2 to 4 options, found %d", n)
	}
	seen := map[string]bool{}
	for i, o := range q.Options {
		if strings.TrimSpace(o) == "" {
			e("options", "option %d is empty", i)
			continue
		}
		k := normalizeLoose(o)
		if seen[k] {
			e("options", "option %d duplicates another option", i)
		}
		seen[k] = true
		if l := len([]rune(o)); l > 90 {
			w("options", "option %d is %d characters; keep options scannable", i, l)
		}
		lo := strings.ToLower(o)
		if strings.Contains(lo, "all of the above") || strings.Contains(lo, "none of the above") {
			w("options", "avoid 'all/none of the above'; options are shuffled")
		}
	}
	validIndex := q.CorrectIndex >= 0 && q.CorrectIndex < len(q.Options)
	if !validIndex {
		e("correctIndex", "out of range (0..%d)", len(q.Options)-1)
	}

	// Explanation quality.
	words := len(strings.Fields(q.Explanation))
	switch {
	case words > 0 && words < MinExplanationWords:
		e("explanation", "only %d words; explain what happened and why (aim for 50-120)", words)
	case words > MaxExplanationWords:
		e("explanation", "%d words; move detail into goDeeper (aim for 50-120)", words)
	case words > 0 && (words < SoftMinExplanationWords || words > SoftMaxExplanationWords):
		w("explanation", "%d words; aim for roughly 50-120", words)
	}
	if len([]rune(q.Reveal)) > 100 {
		w("reveal", "headline is %d characters; aim for under 100", len([]rune(q.Reveal)))
	}
	if q.Question != "" && !strings.HasSuffix(strings.TrimSpace(q.Question), "?") {
		w("question", "should end with a question mark")
	}
	if strings.TrimSpace(q.FunFact) == "" {
		w("funFact", "missing; the 'Wait, really?' fact is part of the reveal")
	}

	// Metadata.
	if len(q.Tags) == 0 {
		e("tags", "at least one tag is required")
	}
	if len(q.Concepts) == 0 {
		e("concepts", "at least one concept id is required")
	}
	for _, t := range q.Tags {
		if !slugRe.MatchString(t) {
			e("tags", "%q must be kebab-case", t)
		}
	}
	for _, c := range append(slices.Clone(q.Concepts), q.Prerequisites...) {
		if !slugRe.MatchString(c) {
			e("concepts", "%q must be kebab-case", c)
		}
	}
	if q.VariantGroup != "" && !slugRe.MatchString(q.VariantGroup) {
		e("variantGroup", "must be kebab-case")
	}
	switch q.ReviewStatus {
	case StatusDraft, StatusReviewed, StatusVerified:
	default:
		e("reviewStatus", "must be draft, reviewed or verified (got %q)", q.ReviewStatus)
	}
	if q.SourceURL != "" && !strings.HasPrefix(q.SourceURL, "https://") && !strings.HasPrefix(q.SourceURL, "http://") {
		e("sourceUrl", "must be an http(s) URL")
	}
	if q.GoDeeper != nil && strings.TrimSpace(q.GoDeeper.Text) == "" {
		e("goDeeper.text", "required when goDeeper is present")
	}
	if q.EstimatedTime < 0 || q.EstimatedTime > 120 {
		e("estimatedTime", "must be 0-120 seconds")
	}
	switch q.VisualType {
	case "", "none", "emoji", "diagram", "image":
	default:
		e("visualType", "unknown visual type %q", q.VisualType)
	}
	for field, text := range textFields(q) {
		if placeholderRe.MatchString(text) {
			e(field, "contains placeholder text")
		}
	}

	// Item-writing heuristics.
	if validIndex && len(q.Options) >= 2 {
		correct := float64(len([]rune(q.Options[q.CorrectIndex])))
		var sum float64
		for i, o := range q.Options {
			if i != q.CorrectIndex {
				sum += float64(len([]rune(o)))
			}
		}
		avg := sum / float64(len(q.Options)-1)
		if correct > avg*1.8 && correct-avg > 18 {
			w("options", "the correct option is much longer than the distractors; players may guess from length")
		}
		co := normalizeLoose(q.Options[q.CorrectIndex])
		if len(strings.Fields(co)) >= 3 && strings.Contains(normalizeLoose(q.Scenario+" "+q.Question), co) {
			w("options", "the correct option's wording appears in the scenario or question")
		}
	}
}

func textFields(q *Question) map[string]string {
	m := map[string]string{
		"scenario": q.Scenario, "question": q.Question, "reveal": q.Reveal,
		"explanation": q.Explanation, "intuitionTrap": q.IntuitionTrap,
		"principle": q.Principle, "funFact": q.FunFact, "caveat": q.Caveat,
		"options": strings.Join(q.Options, " | "),
	}
	if q.GoDeeper != nil {
		m["goDeeper"] = q.GoDeeper.Equation + " " + q.GoDeeper.Text + " " + q.GoDeeper.Example
	}
	return m
}

func validateChains(lib *Library, rep *Report) {
	groups := map[string][]*Question{}
	for _, q := range lib.Questions {
		if q.Chain != nil {
			groups[q.Chain.ID] = append(groups[q.Chain.ID], q)
		}
	}
	ids := make([]string, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		qs := groups[id]
		sort.Slice(qs, func(i, j int) bool { return qs[i].Chain.Step < qs[j].Chain.Step })
		total := qs[0].Chain.Total
		ok := slugRe.MatchString(id) && total >= 2 && len(qs) == total
		for i, q := range qs {
			if q.Chain.Total != total || q.Chain.Step != i+1 || q.Category != qs[0].Category || q.ReviewStatus != qs[0].ReviewStatus {
				ok = false
			}
		}
		if !ok {
			rep.add(SevError, qs[0].File, qs[0].ID, "chain",
				"chain %q must have steps 1..total exactly once, with the same total, category and reviewStatus", id)
			continue
		}
		lib.Chains[id] = qs
	}
}

func validateSchedule(lib *Library, rep *Report) {
	days := make([]string, 0, len(lib.Schedule))
	for d := range lib.Schedule {
		days = append(days, d)
	}
	sort.Strings(days)
	for _, day := range days {
		ids := lib.Schedule[day]
		if _, err := time.Parse("2006-01-02", day); err != nil {
			rep.add(SevError, "daily/schedule.json", "", day, "date must be YYYY-MM-DD")
			continue
		}
		if len(ids) < 3 || len(ids) > 10 {
			rep.add(SevError, "daily/schedule.json", "", day, "a daily set needs 3-10 questions, found %d", len(ids))
		}
		for _, id := range ids {
			q := lib.ByID[id]
			switch {
			case q == nil:
				rep.add(SevError, "daily/schedule.json", id, day, "unknown question id")
			case q.Chain != nil:
				rep.add(SevError, "daily/schedule.json", id, day, "chain questions cannot be scheduled individually")
			case !q.Servable(false):
				rep.add(SevError, "daily/schedule.json", id, day, "question is not reviewed")
			}
		}
	}
}

// CheckDuplicates reports likely duplicate questions. Pairs that share a
// variantGroup are intentional variants and only produce a warning.
func CheckDuplicates(qs []*Question, rep *Report) {
	for _, p := range FindSimilar(qs) {
		if p.A.Chain != nil && p.B.Chain != nil && p.A.Chain.ID == p.B.Chain.ID {
			continue
		}
		if p.A.VariantGroup != "" && p.A.VariantGroup == p.B.VariantGroup {
			rep.add(SevWarning, p.B.File, p.B.ID, "variantGroup",
				"very similar to %s (text %.2f, concepts %.2f); make sure the variants test different intuitions",
				p.A.ID, p.TextSim, p.ConceptSim)
			continue
		}
		rep.add(SevError, p.B.File, p.B.ID, "",
			"likely duplicate of %s (text %.2f, concepts %.2f). Reword or merge them, or give both the same variantGroup if they test genuinely different intuitions",
			p.A.ID, p.TextSim, p.ConceptSim)
	}
}

// Describe summarises a library for CLI output.
func Describe(lib *Library) string {
	byCat := map[string]int{}
	byDiff := map[Difficulty]int{}
	for _, q := range lib.Questions {
		byCat[q.Category]++
		byDiff[q.Difficulty]++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d questions, version %s\n", len(lib.Questions), lib.Version)
	for _, c := range lib.Taxonomy.Categories {
		fmt.Fprintf(&b, "  %-18s %4d\n", c.ID, byCat[c.ID])
	}
	for _, d := range []Difficulty{Easy, Medium, Hard, Expert} {
		fmt.Fprintf(&b, "  %-18s %4d\n", d, byDiff[d])
	}
	return b.String()
}
