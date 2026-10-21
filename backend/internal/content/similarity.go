package content

import (
	"sort"
	"strings"
	"unicode"
)

// Thresholds for flagging near-duplicates.
const (
	TextSimDuplicate    = 0.75 // near-identical wording
	TextSimWithConcepts = 0.45 // similar wording AND the same concepts
	ConceptSimThreshold = 0.5
)

var stopwords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`a an the you your yours is are was were be been being it its of to in on
		and or but what which who whom does do did doe with for at by as that this these those if into onto from
		while when how then than there their they them so such can could would should will just very also about
		after before over under up out now some any each other another both happen happens happened next
		we our us i me my he she his her him has have had get gets got let suppose imagine`) {
		m[w] = true
	}
	return m
}()

// Words that express the same quantitative idea normalise to one token, so
// "double the radius" and "the radius doubles" compare as equal.
var numberWords = map[string]string{
	"double": "x2", "doubles": "x2", "doubled": "x2", "doubling": "x2", "twice": "x2", "two": "x2", "2": "x2",
	"half": "x05", "halves": "x05", "halved": "x05", "halving": "x05",
	"triple": "x3", "triples": "x3", "tripled": "x3", "three": "x3", "3": "x3",
	"quadruple": "x4", "quadrupled": "x4", "four": "x4", "4": "x4",
}

// Tokens normalises text for similarity: lowercase, punctuation stripped,
// stopwords removed, number words unified, light stemming.
func Tokens(text string) []string {
	text = strings.ToLower(strings.ReplaceAll(text, "’", "'"))
	var b strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	var out []string
	for _, w := range strings.Fields(b.String()) {
		w = strings.Trim(strings.TrimSuffix(w, "'s"), "'")
		if w == "" || stopwords[w] {
			continue
		}
		if n, ok := numberWords[w]; ok {
			out = append(out, n)
			continue
		}
		w = stem(w)
		if len(w) < 2 || stopwords[w] {
			continue
		}
		out = append(out, w)
	}
	return out
}

func stem(w string) string {
	switch {
	case len(w) > 5 && strings.HasSuffix(w, "ing"):
		return w[:len(w)-3]
	case len(w) > 4 && strings.HasSuffix(w, "ied"):
		return w[:len(w)-3] + "y"
	case len(w) > 4 && strings.HasSuffix(w, "ies"):
		return w[:len(w)-3] + "y"
	case len(w) > 4 && strings.HasSuffix(w, "ed"):
		return w[:len(w)-2]
	case len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") &&
		!strings.HasSuffix(w, "us") && !strings.HasSuffix(w, "is"):
		return w[:len(w)-1]
	}
	return w
}

func normalizeLoose(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(stripPunct(s))), " ")
}

func stripPunct(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			return r
		}
		return ' '
	}, s)
}

func setOf(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

// Jaccard similarity of two sets.
func Jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// TextSimilarity compares two questions' scenario+question text.
func TextSimilarity(a, b *Question) float64 {
	return Jaccard(setOf(Tokens(a.Scenario+" "+a.Question)), setOf(Tokens(b.Scenario+" "+b.Question)))
}

// SimilarPair is a likely duplicate.
type SimilarPair struct {
	A, B       *Question
	TextSim    float64
	ConceptSim float64
}

// FindSimilar returns likely-duplicate pairs. Candidate pairs come from an
// inverted index (shared concept or one of each question's rarest tokens), so
// the check stays fast at tens of thousands of questions instead of O(n²).
func FindSimilar(qs []*Question) []SimilarPair {
	n := len(qs)
	toks := make([]map[string]bool, n)
	cons := make([]map[string]bool, n)
	df := map[string]int{}
	for i, q := range qs {
		toks[i] = setOf(Tokens(q.Scenario + " " + q.Question))
		cons[i] = setOf(q.Concepts)
		for t := range toks[i] {
			df[t]++
		}
	}
	index := map[string][]int{}
	for i := range qs {
		for _, t := range rarest(toks[i], df, 5) {
			index["t:"+t] = append(index["t:"+t], i)
		}
		for c := range cons[i] {
			index["c:"+c] = append(index["c:"+c], i)
		}
	}
	checked := map[[2]int]bool{}
	var out []SimilarPair
	for _, list := range index {
		if len(list) > 500 {
			continue // too common to be discriminating
		}
		for x := 0; x < len(list); x++ {
			for y := x + 1; y < len(list); y++ {
				i, j := list[x], list[y]
				if i > j {
					i, j = j, i
				}
				key := [2]int{i, j}
				if i == j || checked[key] {
					continue
				}
				checked[key] = true
				ts := Jaccard(toks[i], toks[j])
				cs := Jaccard(cons[i], cons[j])
				if ts >= TextSimDuplicate || (ts >= TextSimWithConcepts && cs >= ConceptSimThreshold) {
					out = append(out, SimilarPair{A: qs[i], B: qs[j], TextSim: ts, ConceptSim: cs})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].A.ID != out[j].A.ID {
			return out[i].A.ID < out[j].A.ID
		}
		return out[i].B.ID < out[j].B.ID
	})
	return out
}

func rarest(set map[string]bool, df map[string]int, k int) []string {
	list := make([]string, 0, len(set))
	for t := range set {
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool {
		if df[list[i]] != df[list[j]] {
			return df[list[i]] < df[list[j]]
		}
		return list[i] < list[j]
	})
	if len(list) > k {
		list = list[:k]
	}
	return list
}
