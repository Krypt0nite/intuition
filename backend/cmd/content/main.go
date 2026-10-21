// Command content validates, summarises, exports and imports the question bank.
//
//	go run ./cmd/content validate [-dir ../content] [-strict] [-json]
//	go run ./cmd/content stats
//	go run ./cmd/content export-demo -out ../frontend/src/demo/pack.json
//	go run ./cmd/content import -database-url postgres://...
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"intuition/internal/content"
	"intuition/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "content directory")
	strict := fs.Bool("strict", false, "treat warnings as errors")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	out := fs.String("out", "../frontend/src/demo/pack.json", "export-demo output path")
	max := fs.Int("max", 400, "export-demo: maximum questions")
	dbURL := fs.String("database-url", os.Getenv("DATABASE_URL"), "import: Postgres URL")
	_ = fs.Parse(args)

	lib, rep, err := content.LoadDir(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(2)
	}
	switch cmd {
	case "validate":
		if *asJSON {
			_ = json.NewEncoder(os.Stdout).Encode(rep)
		} else {
			for _, i := range rep.Issues {
				fmt.Println(i)
			}
			fmt.Printf("\n%d files, %d questions: %d errors, %d warnings\n", rep.Files, rep.Questions, len(rep.Errors()), len(rep.Warnings()))
		}
		if rep.HasErrors() || (*strict && len(rep.Warnings()) > 0) {
			os.Exit(1)
		}
		fmt.Println("content OK")
	case "stats":
		mustClean(rep)
		fmt.Print(content.Describe(lib))
		concepts, groups := map[string]int{}, map[string]int{}
		subs := map[string]int{}
		for _, q := range lib.Questions {
			for _, c := range q.Concepts {
				concepts[c]++
			}
			groups[q.VariantGroup]++
			subs[q.Category+"/"+q.Subcategory]++
		}
		fmt.Printf("  %d distinct concepts, %d variant groups, %d subcategories in use, %d chains\n", len(concepts), len(groups), len(subs), len(lib.Chains))
	case "export-demo":
		mustClean(rep)
		qs := lib.Servable(false)
		sort.Slice(qs, func(i, j int) bool { return qs[i].ID < qs[j].ID })
		if len(qs) > *max {
			qs = qs[:*max]
		}
		pack := map[string]any{"version": lib.Version, "generatedAt": time.Now().UTC().Format(time.RFC3339), "taxonomy": lib.Taxonomy, "questions": qs}
		b, _ := json.Marshal(pack)
		if err := os.WriteFile(*out, b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %d questions to %s (%d KB)\n", len(qs), *out, len(b)/1024)
	case "import":
		mustClean(rep)
		if *dbURL == "" {
			fmt.Fprintln(os.Stderr, "set -database-url or DATABASE_URL")
			os.Exit(2)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		pg, err := store.OpenPostgres(ctx, *dbURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer pg.Close()
		n, err := pg.ImportQuestions(ctx, lib.Questions)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("imported %d questions (version %s)\n", n, lib.Version)
	default:
		usage()
	}
}

func mustClean(rep *content.Report) {
	if rep.HasErrors() {
		for _, i := range rep.Errors() {
			fmt.Fprintln(os.Stderr, i)
		}
		fmt.Fprintln(os.Stderr, "content has errors; run `validate`")
		os.Exit(1)
	}
}

func defaultDir() string {
	for _, d := range []string{"../content", "./content", "/app/content"} {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	return "../content"
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: content <"+strings.Join([]string{"validate", "stats", "export-demo", "import"}, "|")+"> [flags]")
	os.Exit(2)
}
