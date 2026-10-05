package triage

import (
	"regexp"
	"strings"

	"github.com/sbresin/gh-dep-triage/internal/model"
	"github.com/sbresin/gh-dep-triage/internal/parse"
)

var (
	dependabotSystems = map[string]string{"npm_and_yarn": "NPM", "pip": "PYPI", "uv": "PYPI", "go_modules": "GO",
		"bundler": "RUBYGEMS", "cargo": "CARGO", "maven": "MAVEN", "gradle": "MAVEN", "nuget": "NUGET"}
	renovateSystems = map[string]string{"npm": "NPM", "pypi": "PYPI", "go": "GO", "rubygems": "RUBYGEMS",
		"crate": "CARGO", "maven": "MAVEN", "nuget": "NUGET"}
	renovateProjects = map[string]bool{"github-tags": true, "github-releases": true, "github-actions": true}
	plainVersionRe   = regexp.MustCompile(`^v?\d[0-9A-Za-z.+-]*$`)
	pypiSepRe        = regexp.MustCompile(`[-_.]+`)
)

// DepKey maps pr to its deps.dev lookup key: the ecosystem from the
// Dependabot branch name or the Renovate badge. ok is false when the
// ecosystem is unknown or deps.dev doesn't cover it.
func DepKey(pr *model.PR) (model.DepKey, bool) {
	if rest, ok := strings.CutPrefix(pr.HeadRefName, "dependabot/"); ok {
		eco, _, _ := strings.Cut(rest, "/")
		if eco == "github_actions" {
			return projectKey(pr.Package)
		}
		return versionKey(dependabotSystems[eco], pr.Package, pr.TargetVersion)
	}
	if b, ok := parse.RenovateBadge(pr.Body); ok && parse.BadgeMatches(b, pr.TargetVersion) {
		if renovateProjects[b.Datasource] {
			return projectKey(b.Package)
		}
		return versionKey(renovateSystems[b.Datasource], b.Package, b.To)
	}
	return model.DepKey{}, false
}

func projectKey(pkg string) (model.DepKey, bool) {
	parts := strings.Split(pkg, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return model.DepKey{}, false
	}
	return model.DepKey{Name: "github.com/" + parts[0] + "/" + parts[1]}, true
}

func versionKey(system, name, version string) (model.DepKey, bool) {
	if !plainVersionRe.MatchString(version) {
		version = parse.HighestVersion(version)
	}
	if system == "" || name == "" || version == "" {
		return model.DepKey{}, false
	}
	version = strings.TrimPrefix(version, "v")
	switch system {
	case "GO":
		version = "v" + version
	case "PYPI":
		name = pypiSepRe.ReplaceAllString(strings.ToLower(name), "-")
	case "NUGET":
		name = strings.ToLower(name)
	}
	return model.DepKey{System: system, Name: name, Version: version}, true
}
