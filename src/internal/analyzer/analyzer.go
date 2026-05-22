// Package analyzer builds the structured project snapshot consumed by every
// later review. Schema version is part of the cached output; downstream
// modules must check it before reading other fields.
package analyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const SchemaVersion = 1

type Snapshot struct {
	SchemaVersion int          `json:"schema_version"`
	Language      Language     `json:"language"`
	Manifests     []Manifest   `json:"manifests"`
	Frameworks    []Framework  `json:"frameworks"`
	EntryPoints   []EntryPoint `json:"entry_points"`
	Fingerprint   string       `json:"fingerprint"`
}

type Language struct {
	Primary string         `json:"primary"`
	Mix     []LanguageStat `json:"mix"`
}

type LanguageStat struct {
	Name string `json:"name"`
	Loc  int    `json:"loc"`
}

type Manifest struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

type Framework struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

type EntryPoint struct {
	Path string `json:"path"`
}

// extension → language name; only languages we actually expect to detect in
// the MVP. Adding a new one is a one-line change.
var extLang = map[string]string{
	".go":   "go",
	".py":   "python",
	".js":   "javascript",
	".jsx":  "javascript",
	".ts":   "typescript",
	".tsx":  "typescript",
	".rs":   "rust",
	".java": "java",
}

type manifestRule struct {
	filename string
	kind     string
	language string
}

var manifestRules = []manifestRule{
	{"go.mod", "go.mod", "go"},
	{"requirements.txt", "requirements.txt", "python"},
	{"pyproject.toml", "pyproject.toml", "python"},
	{"package.json", "package.json", "javascript"},
	{"Cargo.toml", "Cargo.toml", "rust"},
	{"pom.xml", "pom.xml", "java"},
}

// frameworkRule: when this token appears in this manifest, attribute the framework.
type frameworkRule struct {
	manifest string
	token    string
	name     string
}

var frameworkRules = []frameworkRule{
	{"requirements.txt", "flask", "flask"},
	{"requirements.txt", "django", "django"},
	{"package.json", `"react"`, "react"},
	{"package.json", `"vue"`, "vue"},
	{"package.json", `"express"`, "express"},
	{"go.mod", "gin-gonic", "gin"},
	{"go.mod", "labstack/echo", "echo"},
}

// Analyze walks the project rooted at root and returns a Snapshot.
func Analyze(root string) (*Snapshot, error) {
	snap := &Snapshot{SchemaVersion: SchemaVersion}
	locByLang := map[string]int{}
	hasher := sha256.New()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort walk
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" || name == ".review-cache" {
				return filepath.SkipDir
			}
			return nil
		}

		base := filepath.Base(path)
		for _, rule := range manifestRules {
			if base == rule.filename {
				snap.Manifests = append(snap.Manifests, Manifest{Kind: rule.kind, Path: rel})
				// Frameworks rely on manifest contents.
				if b, err := os.ReadFile(path); err == nil {
					lower := strings.ToLower(string(b))
					for _, fr := range frameworkRules {
						if fr.manifest == rule.filename && strings.Contains(lower, strings.ToLower(fr.token)) {
							snap.Frameworks = append(snap.Frameworks, Framework{Name: fr.name, Source: rel})
						}
					}
				}
			}
		}

		// Language detection by extension + a rough LOC count for primary selection.
		ext := strings.ToLower(filepath.Ext(path))
		if lang, ok := extLang[ext]; ok {
			if b, err := os.ReadFile(path); err == nil {
				lines := strings.Count(string(b), "\n") + 1
				locByLang[lang] += lines
				hasher.Write([]byte(rel))
				hasher.Write(b)
			}
		}

		// Heuristic entry points.
		if base == "main.go" || base == "app.py" || base == "manage.py" || base == "index.js" || base == "main.py" {
			snap.EntryPoints = append(snap.EntryPoints, EntryPoint{Path: rel})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	for lang, loc := range locByLang {
		snap.Language.Mix = append(snap.Language.Mix, LanguageStat{Name: lang, Loc: loc})
	}
	sort.Slice(snap.Language.Mix, func(i, j int) bool {
		return snap.Language.Mix[i].Loc > snap.Language.Mix[j].Loc
	})
	if len(snap.Language.Mix) > 0 {
		snap.Language.Primary = snap.Language.Mix[0].Name
	} else {
		snap.Language.Primary = "unknown"
	}
	snap.Fingerprint = hex.EncodeToString(hasher.Sum(nil))[:16]
	return snap, nil
}

func WriteSnapshot(root string, snap *Snapshot) error {
	dir := filepath.Join(root, ".review-cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "project.json"), b, 0o644)
}

func ReadSnapshot(root string) (*Snapshot, error) {
	b, err := os.ReadFile(filepath.Join(root, ".review-cache", "project.json"))
	if err != nil {
		return nil, err
	}
	var snap Snapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}
