package triage

import (
	"path"
	"regexp"
	"strings"

	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/parse"
)

const (
	TierAuto   = "auto"
	TierReview = "review"
	TierManual = "manual"
)

// ponytail: keyword list, flags harmless notes and misses unworded breaks; an LLM or diff check can replace it.
var riskyNotesRe = regexp.MustCompile(`(?i)breaking|removed|deprecat|migrat|security|CVE-|drop(ped)? support`)

var manifestGlobs = []string{
	"package.json", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb",
	"go.mod", "go.sum", "go.work", "go.work.sum",
	"pyproject.toml", "poetry.lock", "uv.lock", "Pipfile", "Pipfile.lock", "requirements*.txt", "requirements*.in", "setup.cfg",
	"Cargo.toml", "Cargo.lock", "Gemfile", "Gemfile.lock", "*.gemspec",
	"pom.xml", "build.gradle", "build.gradle.kts", "gradle.lockfile", "libs.versions.toml",
	"*.csproj", "packages.lock.json", "Directory.Packages.props", "composer.json", "composer.lock",
	".terraform.lock.hcl", "*.tf", "Dockerfile*", "docker-compose*.yml", "docker-compose*.yaml", "compose*.yml", "compose*.yaml",
	"action.yml", "action.yaml", ".pre-commit-config.yaml", "mise.toml", ".tool-versions",
}

// Tier classifies how much review a PR needs before merging. Call it after
// MergeDenied is set; reasons say what kept the PR out of auto.
func Tier(pr *model.PR) (string, []string) {
	if pr.MergeDenied != "" {
		return TierManual, []string{pr.MergeDenied}
	}
	var reasons []string
	add := func(r string) { reasons = append(reasons, r) }
	if pr.Bump != model.BumpPatch && pr.Bump != model.BumpMinor {
		add("bump_" + pr.Bump)
	}
	if r := pr.Risk; r == nil {
		add("no_depsdev_data")
	} else {
		if r.PublishedAt.IsZero() {
			add("depsdev_unknown_version")
		}
		if r.SourceRepo == "" || r.Stars == 0 {
			add("no_source_repo")
		}
		for _, f := range r.Findings {
			add("finding_" + f)
		}
		if len(r.Advisories) > 0 {
			add("advisory")
		}
		if r.Deprecated {
			add("deprecated")
		}
	}
	if pr.Checks.Pending > 0 {
		add("checks_pending")
	}
	if !tested(pr.CheckRuns) {
		add("untested")
	}
	if !onlyManifests(pr) {
		add("non_manifest_files")
	}
	if notes := parse.ReleaseNotes(pr.Body); notes == "" {
		add("no_release_notes")
	} else if riskyNotesRe.MatchString(notes) {
		add("changelog_keyword")
	}
	if len(reasons) > 0 {
		return TierReview, reasons
	}
	return TierAuto, nil
}

// tested: every required check passed, or with none required, at least one passed.
func tested(checks []model.Check) bool {
	required, passed := 0, 0
	for _, c := range checks {
		if c.Required {
			required++
			if c.State != model.CheckStatePassed {
				return false
			}
		}
		if c.State == model.CheckStatePassed {
			passed++
		}
	}
	return required > 0 || passed > 0
}

func onlyManifests(pr *model.PR) bool {
	if len(pr.Files) == 0 || pr.FileCount > len(pr.Files) {
		return false
	}
	for _, f := range pr.Files {
		if !isManifest(f) {
			return false
		}
	}
	return true
}

func isManifest(p string) bool {
	if strings.HasPrefix(p, ".github/workflows/") {
		return true
	}
	base := path.Base(p)
	for _, g := range manifestGlobs {
		if ok, _ := path.Match(g, base); ok {
			return true
		}
	}
	return false
}
