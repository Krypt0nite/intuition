package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShippedContentIsValid(t *testing.T) {
	lib, rep, err := LoadDir("../../../content")
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range rep.Issues {
		t.Log(i)
	}
	if rep.HasErrors() {
		t.Fatalf("shipped content has %d errors", len(rep.Errors()))
	}
	if len(lib.Questions) < 140 {
		t.Fatalf("expected a substantial library, got %d", len(lib.Questions))
	}
	per := map[string]int{}
	diff := map[Difficulty]int{}
	for _, q := range lib.Questions {
		per[q.Category]++
		diff[q.Difficulty]++
	}
	for _, c := range lib.Taxonomy.Categories {
		if per[c.ID] < 12 {
			t.Errorf("category %s has only %d questions", c.ID, per[c.ID])
		}
	}
	for _, d := range []Difficulty{Easy, Medium, Hard, Expert} {
		if diff[d] < 10 {
			t.Errorf("difficulty %s has only %d questions", d, diff[d])
		}
	}
	if len(lib.Chains) == 0 {
		t.Error("expected at least one chain round")
	}
}

const taxonomy = `{"categories":[{"id":"physics","name":"Physics","emoji":"x","prefix":"phy","color":"#fff","subcategories":["Gravity"]}],
"questionTypes":["prediction"],"answerTypes":["change"]}`

func good(id, scenario string, concepts string, group string) string {
	return `{"id":"` + id + `","subcategory":"Gravity","difficulty":"medium","questionType":"prediction","answerType":"change",
	"scenario":"` + scenario + `","question":"What happens to gravity?","options":["Increases","Decreases","Stays the same"],"correctIndex":1,
	"reveal":"It decreases.","explanation":"` + strings.Repeat("Gravity weakens with the square of distance from the centre. ", 6) + `",
	"intuitionTrap":"trap","principle":"g = GM/r^2","funFact":"fact","tags":["gravity"],"concepts":[` + concepts + `],"variantGroup":"` + group + `"}`
}

func writeLib(t *testing.T, questions ...string) (*Library, *Report) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "taxonomy.json"), []byte(taxonomy), 0o644)
	os.MkdirAll(filepath.Join(dir, "physics"), 0o755)
	pack := `{"pack":"t","defaults":{"category":"physics","reviewStatus":"reviewed"},"questions":[` + strings.Join(questions, ",") + `]}`
	os.WriteFile(filepath.Join(dir, "physics", "t.json"), []byte(pack), 0o644)
	lib, rep, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return lib, rep
}

func hasIssue(rep *Report, sev Severity, id, substr string) bool {
	for _, i := range rep.Issues {
		if i.Severity == sev && (id == "" || i.ID == id) && strings.Contains(i.Message+i.Field, substr) {
			return true
		}
	}
	return false
}

func TestValidatorCatchesMalformedQuestions(t *testing.T) {
	missing := strings.Replace(good("phy-a", "You drop a ball.", `"c-a"`, "g-a"), `"explanation":`, `"explanationX":`, 1)
	badIndex := strings.Replace(good("phy-b", "A rocket launches.", `"c-b"`, "g-b"), `"correctIndex":1`, `"correctIndex":7`, 1)
	badCat := strings.Replace(good("phy-c", "A comet passes.", `"c-c"`, "g-c"), `"subcategory":"Gravity"`, `"category":"alchemy","subcategory":"Gravity"`, 1)
	placeholder := good("phy-d", "TODO write this scenario.", `"c-d"`, "g-d")
	dupID1 := good("phy-e", "A lift accelerates upward.", `"c-e"`, "g-e")
	dupID2 := good("phy-e", "A train brakes hard.", `"c-f"`, "g-f")
	short := strings.Replace(good("phy-g", "A pendulum swings.", `"c-g"`, "g-g"), strings.Repeat("Gravity weakens with the square of distance from the centre. ", 6), "Too short.", 1)
	_, rep := writeLib(t, missing, badIndex, badCat, placeholder, dupID1, dupID2, short)
	cases := []struct{ id, sub string }{
		{"phy-a", "unknown field"},
		{"phy-b", "out of range"},
		{"phy-c", "unknown category"},
		{"phy-d", "placeholder"},
		{"phy-e", "duplicate id"},
		{"phy-g", "only 2 words"},
	}
	for _, c := range cases {
		if !hasIssue(rep, SevError, c.id, c.sub) {
			t.Errorf("expected error for %s containing %q; issues: %v", c.id, c.sub, rep.Issues)
		}
	}
}

func TestNearDuplicateDetection(t *testing.T) {
	a := good("phy-radius-a", "You double Earth's radius.", `"inverse-square-gravity","surface-gravity"`, "group-a")
	b := good("phy-radius-b", "Earth's radius doubles while its mass remains constant.", `"inverse-square-gravity","surface-gravity"`, "group-b")
	c := good("phy-unrelated", "A spinning skater pulls her arms in toward her body.", `"angular-momentum"`, "group-c")
	_, rep := writeLib(t, a, b, c)
	if !hasIssue(rep, SevError, "", "likely duplicate") {
		t.Fatalf("near-duplicate pair not detected: %v", rep.Issues)
	}
	for _, i := range rep.Issues {
		if strings.Contains(i.Message, "phy-unrelated") || i.ID == "phy-unrelated" && strings.Contains(i.Message, "duplicate") {
			t.Fatalf("unrelated question flagged: %v", i)
		}
	}
	// Same variant group downgrades to a warning.
	b2 := strings.Replace(b, `"group-b"`, `"group-a"`, 1)
	_, rep2 := writeLib(t, a, b2)
	if rep2.HasErrors() || !hasIssue(rep2, SevWarning, "", "very similar") {
		t.Fatalf("expected warning only: %v", rep2.Issues)
	}
}

func TestTokensNormalise(t *testing.T) {
	got := strings.Join(Tokens("Earth's radius DOUBLES, while it remains constant!"), " ")
	if got != "earth radius x2 remain constant" {
		t.Fatalf("unexpected tokens: %q", got)
	}
}
